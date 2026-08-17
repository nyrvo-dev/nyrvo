// Package cli implements the nyrvo command line.
//
// Commands, flags, and exit codes are a public contract once released: scripts
// and CI jobs depend on them, so they change only deliberately.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/capture"
	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/collector/docker"
	"github.com/nyrvo-dev/nyrvo/internal/collector/env"
	"github.com/nyrvo-dev/nyrvo/internal/collector/git"
	"github.com/nyrvo-dev/nyrvo/internal/collector/requirements"
	"github.com/nyrvo-dev/nyrvo/internal/collector/runtime"
	"github.com/nyrvo-dev/nyrvo/internal/collector/system"
	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/output"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// Exit codes are part of the CLI contract.
const (
	// ExitOK means the command did what was asked. Finding differences is a
	// successful diff: drift is an answer, not an error.
	ExitOK = 0
	// ExitError means the command could not complete (unreadable snapshot,
	// failed collector, filesystem error).
	ExitError = 1
	// ExitUsage means the command line itself was wrong.
	ExitUsage = 2
)

const usage = `nyrvo - see why an environment behaves differently

Usage:
  nyrvo capture <name> [--json]     capture this environment as <name>
  nyrvo diff <a> <b> [--json]       compare two captured environments
  nyrvo ci inspect [--json]         show the environments CI declares
  nyrvo ci capture <job>            save a CI job's declared environment as "ci"
  nyrvo ci import <run> [job]       import a run that happened, as "ci"
  nyrvo ci replay <job> [--json]    print what a job does, without running it
  nyrvo doctor [--json]            diagnose local against ci
  nyrvo doctor <run> [--json]      import a run and diagnose it in one step
  nyrvo doctor <a> <b> [--json]    diagnose two captured environments
                                   add --fail-on=high to exit non-zero on findings
                                   add --ai to print a request for your own agent
  nyrvo config set <key> <value>    set a preference (ai.agent)
  nyrvo config unset <key>          clear a preference
  nyrvo config list                 show preferences and where they live
  nyrvo list                        list captured environments
  nyrvo version                     print version information

Snapshots are stored as .nyrvo/snapshots/<name>.json in the current directory.
Environment variable values are never stored, only their names.

Exit codes: 0 success, 1 error, 2 usage.
`

// Run executes one command line and returns the process exit code. Taking the
// writers as parameters keeps the whole CLI testable without capturing os
// streams.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return ExitUsage
	}

	var err error
	switch cmd := args[0]; cmd {
	case "capture":
		err = runCapture(ctx, args[1:], stdout, stderr)
	case "diff":
		err = runDiff(args[1:], stdout, stderr)
	case "ci":
		err = runCI(ctx, args[1:], stdout)
	case "doctor":
		err = runDoctor(ctx, args[1:], stdout, stderr)
	case "config":
		err = runConfig(args[1:], stdout)
	case "list":
		err = runList(args[1:], stdout, stderr)
	case "version":
		err = runVersion(stdout)
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)
		return ExitOK
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return ExitUsage
	}

	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, errSilent):
		// The command already explained itself; repeating it as "nyrvo: " would
		// only add noise to an exit code the caller asked for.
		return ExitError
	case errors.Is(err, errUsage):
		_, _ = fmt.Fprintf(stderr, "%v\n\n%s", err, usage)
		return ExitUsage
	default:
		_, _ = fmt.Fprintf(stderr, "nyrvo: %v\n", err)
		return ExitError
	}
}

// errUsage marks a caller mistake, which exits 2 and prints usage, as opposed
// to an operational failure, which exits 1.
var errUsage = errors.New("usage")

// splitFlags separates flags from positional arguments so a flag may appear
// anywhere on the command line: `nyrvo diff local ci --json` is the form the
// documentation shows and the one people type, but flag.Parse stops at the
// first positional argument and would silently ignore the flag.
//
// This is safe only because every nyrvo flag is boolean, so no argument can
// belong to a preceding flag. A future flag that takes a value must use the
// --flag=value form, or this split has to learn about it.
func splitFlags(args []string) (flags, operands []string) {
	for i, a := range args {
		// Everything after "--" is positional by convention, which is how a
		// snapshot name that starts with a dash can still be passed.
		if a == "--" {
			operands = append(operands, args[i+1:]...)
			return flags, operands
		}
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			continue
		}
		operands = append(operands, a)
	}
	return flags, operands
}

func usageErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errUsage, fmt.Sprintf(format, a...))
}

// defaultCollectors is the observation set for this milestone. Order decides
// only the progress output; the snapshot itself is normalized afterwards.
//
// The set is assembled here rather than inside capture so the engine stays
// independent of which collectors exist, and so tests can capture with a
// stub set.
func defaultCollectors() []collector.Collector {
	return []collector.Collector{
		system.System{},
		&git.Git{},
		runtime.Go(),
		runtime.Node(),
		runtime.NPM(),
		runtime.Python(),
		runtime.Ruby(),
		runtime.PHP(),
		runtime.Rust(),
		runtime.Java(),
		&docker.Docker{},
		// Requirements reads the checked-out project, not the machine, so it
		// runs from the working directory like the git collector does.
		requirements.Requirements{},
		env.Env{},
	}
}

func runCapture(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "write the snapshot as JSON to stdout")
	flags, operands := splitFlags(args)
	if err := fs.Parse(flags); err != nil {
		return usageErr("capture: %v", err)
	}
	if len(operands) != 1 {
		return usageErr("capture takes exactly one snapshot name")
	}
	name := operands[0]

	res, err := capture.Run(ctx, defaultCollectors(), capture.Options{Name: name})
	if err != nil {
		return err
	}

	store := snapshot.NewStore("")
	// The snapshot is saved even when a collector failed: a partial capture is
	// still evidence, and discarding it would lose the very run the user is
	// trying to explain. The failure is reported and the exit code reflects it.
	if err := store.Save(res.Snapshot); err != nil {
		return err
	}

	if *asJSON {
		// Machine-readable output is the snapshot document itself, so `nyrvo
		// capture --json` and the stored file are interchangeable. Collector
		// status goes to stderr to keep stdout a clean JSON stream.
		if err := output.CaptureText(stderr, res); err != nil {
			return err
		}
		if err := output.SnapshotJSON(stdout, res.Snapshot); err != nil {
			return err
		}
	} else {
		if err := output.CaptureText(stdout, res); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stdout, "\nSnapshot saved: %s\n", name); err != nil {
			return err
		}
	}

	if res.Failed() {
		return fmt.Errorf("capture saved as %q, but these collectors failed: %s", name, strings.Join(res.FailedSections(), ", "))
	}
	return nil
}

func runDiff(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "write the comparison as JSON")
	flags, operands := splitFlags(args)
	if err := fs.Parse(flags); err != nil {
		return usageErr("diff: %v", err)
	}
	if len(operands) != 2 {
		return usageErr("diff takes exactly two snapshot names")
	}

	store := snapshot.NewStore("")
	a, err := store.Load(operands[0])
	if err != nil {
		return err
	}
	b, err := store.Load(operands[1])
	if err != nil {
		return err
	}

	res := diff.Compare(a, b)
	if *asJSON {
		return output.JSON(stdout, res)
	}
	return output.DiffText(stdout, res)
}

func runList(args []string, stdout, _ io.Writer) error {
	if len(args) > 0 {
		return usageErr("list takes no arguments")
	}
	names, err := snapshot.NewStore("").List()
	if err != nil {
		return err
	}
	return output.SnapshotList(stdout, names)
}

// runVersion reports the build's module version. Values come from the build
// info the toolchain embeds, so `go install ...@v1.2.3` self-reports correctly
// without a release process that rewrites source files.
func runVersion(stdout io.Writer) error {
	version, revision := "dev", ""
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				revision = s.Value[:7]
			}
		}
	}
	if revision != "" {
		_, err := fmt.Fprintf(stdout, "nyrvo %s (%s), snapshot schema %d\n", version, revision, snapshot.SchemaVersion)
		return err
	}
	_, err := fmt.Fprintf(stdout, "nyrvo %s, snapshot schema %d\n", version, snapshot.SchemaVersion)
	return err
}
