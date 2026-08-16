// Package runtime collects the installed language runtimes into a snapshot's
// Runtimes section.
//
// Runtimes differ only in which binary to probe and how to spell the version
// flag, so a single collector parameterized by a probe description replaces
// what would otherwise be one near-identical copy per language. Keeping one
// implementation means a fix to version parsing or fallback logic lands in all
// of them at once, and supporting another language is a constructor.
//
// Every runtime is probed on every capture, whatever the project is written in.
// A snapshot describes a machine, not a repository: narrowing the probes to the
// languages a project declares would make two snapshots of the same machine
// differ by which directory they were taken in, and there would be nothing left
// to compare across projects.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// probe describes one way to ask a runtime for its version. A runtime may have
// several probes tried in order: some systems ship "python" without "python3".
type probe struct {
	binary string
	args   []string
}

// runtimeCollector is a single Collector implementation covering every runtime.
// It shadows the standard library runtime package; the stdlib package is
// deliberately never imported here.
type runtimeCollector struct {
	// name is the stable lowercase runtime identifier used as the diff key.
	name string
	// probes are tried in order; the first probe whose binary exists and
	// answers with a parseable version wins.
	probes []probe
}

// newCollector builds a runtimeCollector from its name and probes in preference
// order. It is unexported so tests can drive collectors for binaries that do
// not exist without exposing a public constructor.
func newCollector(name string, probes ...probe) *runtimeCollector {
	return &runtimeCollector{name: name, probes: probes}
}

// Name identifies the runtime in progress output and errors.
func (c *runtimeCollector) Name() string { return c.name }

// Collect appends one snapshot.Runtime to snap.Runtimes.
//
// Existing entries are preserved and kept in order: capture merges many
// collectors into one snapshot, and snapshot.Normalize (not this collector) is
// what guarantees deterministic ordering.
func (c *runtimeCollector) Collect(ctx context.Context, snap *snapshot.Snapshot) error {
	if err := ctx.Err(); err != nil {
		// A cancelled capture must be reported as cancelled even when every
		// probe is missing; labelling a cancellation "unavailable" would hide
		// the caller's own failure to stop.
		return err
	}
	// why remembers the last probe's complaint. A runtime that is installed but
	// refuses to answer has a reason, and that reason is the useful part: it is
	// usually the very drift the user is capturing to find.
	var why error
	for _, p := range c.probes {
		path, err := collector.LookPath(p.binary)
		if err != nil {
			if errors.Is(err, collector.ErrUnavailable) {
				// The binary is not installed; a later probe (python when
				// python3 is absent) may still answer.
				continue
			}
			return err
		}
		out, err := collector.Run(ctx, p.binary, p.args...)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				// A cancelled capture is the caller's own failure and must never
				// be reported as a runtime that happens to be unusable.
				return ctxErr
			}
			// The binary exists and would not answer. A project pinning a
			// toolchain this machine does not have makes rustc, rbenv and pyenv
			// all exit non-zero, and that situation is exactly what Nyrvo is
			// being run to discover — aborting the capture over it would throw
			// away every other observation to report the one the user wanted.
			why = err
			continue
		}
		version, err := NormalizeVersion(out)
		if err != nil {
			why = fmt.Errorf("%s %v: %w", p.binary, p.args, err)
			continue
		}
		snap.Runtimes = append(snap.Runtimes, snapshot.Runtime{
			Name:    c.name,
			Version: version,
			Path:    path,
		})
		return nil
	}
	// No probe produced a version; hand the sentinel back wrapped so capture
	// records the runtime as absent instead of failing. When a probe did run and
	// refused, its complaint travels with the sentinel: "not installed" and
	// "installed but pinned to a toolchain you do not have" are different
	// answers, and the second one is the interesting one.
	if why != nil {
		return fmt.Errorf("%s: %v: %w", c.name, why, collector.ErrUnavailable)
	}
	return fmt.Errorf("%s: %w", c.name, collector.ErrUnavailable)
}

// dottedVersion matches a run of digits separated by single dots ("1.22",
// "3.11.9"). Requiring at least one dot rejects bare numbers that appear inside
// arbitrary text ("Build 12"), so a bogus version is never invented. The match
// stops at the first non-digit-or-dot, so build qualifiers such as "3.11.9rc1"
// yield "3.11.9" without fabricating suffix digits.
var dottedVersion = regexp.MustCompile(`\d+\.\d+(\.\d+)*`)

// NormalizeVersion extracts the first dotted numeric version from tool output,
// stripping tool-specific prefixes and suffixes:
//
//	"go version go1.24.2 darwin/arm64" -> "1.24.2"
//	"v24.4.0"                          -> "24.4.0"
//	"Python 3.13.3"                    -> "3.13.3"
//	"go1.22"                           -> "1.22"
//	"Python 3.11.9rc1"                 -> "3.11.9"
//
// It returns an error when the output contains no recognizable dotted version
// so callers never record a misleading value, and it never panics.
func NormalizeVersion(out string) (string, error) {
	version := dottedVersion.FindString(out)
	if version == "" {
		return "", fmt.Errorf("no dotted numeric version in %q", out)
	}
	return version, nil
}

// Go returns a collector for the Go toolchain.
func Go() collector.Collector {
	return newCollector("go", probe{binary: "go", args: []string{"version"}})
}

// Node returns a collector for the Node.js runtime.
func Node() collector.Collector {
	return newCollector("node", probe{binary: "node", args: []string{"--version"}})
}

// Ruby returns a collector for the Ruby runtime.
func Ruby() collector.Collector {
	return newCollector("ruby", probe{binary: "ruby", args: []string{"--version"}})
}

// PHP returns a collector for the PHP runtime.
func PHP() collector.Collector {
	return newCollector("php", probe{binary: "php", args: []string{"--version"}})
}

// Rust returns a collector for the Rust toolchain. It probes the compiler
// rather than cargo: cargo reports its own version, which tracks rustc but is
// not the version a project's rust-version constraint talks about.
func Rust() collector.Collector {
	return newCollector("rust", probe{binary: "rustc", args: []string{"--version"}})
}

// Java returns a collector for the Java runtime.
//
// It uses --version rather than the older -version because -version writes to
// stderr, which collector.Run does not read: the probe would come back empty
// and be reported as an unparseable version rather than as a missing runtime.
// The consequence is that a JDK older than 9, which has no --version, is seen
// as absent. That is the honest failure of the two.
func Java() collector.Collector {
	return newCollector("java", probe{binary: "java", args: []string{"--version"}})
}

// Python returns a collector for the Python runtime. It probes "python3" first
// because that is the unambiguous name on platforms that ship both; systems
// with only legacy "python" are still covered by the fallback probe.
func Python() collector.Collector {
	return newCollector("python",
		probe{binary: "python3", args: []string{"--version"}},
		probe{binary: "python", args: []string{"--version"}},
	)
}
