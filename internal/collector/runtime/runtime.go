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
	// stderr asks for the tool's stderr as well. Only tools that genuinely
	// answer there set it: `java -version` on a JDK older than 9 writes the
	// version to stderr and nothing to stdout, so without this a machine with
	// Java 8 reports no Java at all. It is per-probe rather than a general
	// fallback because reading stderr whenever stdout is empty would take any
	// tool's warning for a version.
	stderr bool
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
	// timedOut records that a probe ran out of time rather than answering. That
	// is not the same as the runtime being absent, and the difference has to
	// survive to the snapshot: a binary that exists and was too slow once must
	// not be published as a machine that lost a tool.
	timedOut := false
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
		out, errOut, err := collector.RunOutput(ctx, p.binary, p.args...)
		if err == nil && out == "" && p.stderr {
			out = errOut
		}
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
			//
			// Running out of time is the one failure that says nothing about the
			// runtime. The binary was found by LookPath a moment ago, so it is
			// installed; all that is unknown is its version.
			if collector.IsTimeout(err) {
				timedOut = true
			}
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
	// A probe that ran out of time leaves the runtime unknown rather than
	// absent, and the snapshot has to say so. Without this the diff between two
	// captures of one machine reports a runtime as having disappeared, which is
	// drift Nyrvo invented rather than drift it observed.
	if timedOut {
		snap.MarkUnmeasured("runtime", c.name)
	} else if why != nil {
		// A probe that answered by refusing is the deterministic opposite of a
		// timeout: LookPath found the binary a moment ago, so the runtime is
		// installed, and it declined to answer — a global.json, a
		// rust-toolchain.toml or an rbenv version naming a toolchain this
		// machine does not have. Asking again gives the same refusal, so the
		// snapshot must report it, not skip it the way unmeasured is skipped.
		//
		// Only the name is recorded. The probe's own error carries the user's
		// absolute paths, and a snapshot is pasted into bug reports.
		snap.MarkUnusable("runtime", c.name)
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

// NPM returns a collector for the npm package manager.
//
// It is a runtime here because package.json declares engines.npm separately
// from engines.node, and that declaration was being recorded as a requirement
// against a version nothing ever observed — a constraint that could never be
// met or violated, only stored. npm's version is independent of Node's and its
// lockfile format has changed across majors, so the drift is real.
func NPM() collector.Collector {
	return newCollector("npm", probe{binary: "npm", args: []string{"--version"}})
}

// DotNet returns a collector for the .NET SDK.
//
// dotnet --version reports the SDK version — "9.0.100", not the runtime's
// "9.0.1" — and that is exactly what a project's global.json sdk.version
// constrains, so the observation lines up with the requirement read against it.
func DotNet() collector.Collector {
	return newCollector("dotnet", probe{binary: "dotnet", args: []string{"--version"}})
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
// --version is tried first because it answers on stdout like every other
// runtime. A JDK older than 9 does not have it and writes its version to stderr
// under -version, so that spelling is the fallback and asks for stderr
// explicitly.
func Java() collector.Collector {
	return newCollector("java",
		probe{binary: "java", args: []string{"--version"}},
		// A JDK older than 9 has no --version and answers -version on stderr.
		// Java 8 is still widely deployed and setup-java still installs it, so
		// reporting such a machine as having no Java would be a plain untruth.
		probe{binary: "java", args: []string{"-version"}, stderr: true},
	)
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
