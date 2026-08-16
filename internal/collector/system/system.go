// Package system observes the host operating system and CPU architecture.
//
// OS and Arch are read from the Go runtime rather than from an external tool: a
// capture must always describe the platform it ran on, even where uname is
// missing or broken, so those two fields can never fail to be present. Kernel
// is purely informational and is omitted instead of treated as an error when it
// cannot be read.
package system

import (
	"context"
	"runtime"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// System fills the snapshot's System section.
type System struct{}

// Compile-time check that System satisfies the collector contract.
var _ collector.Collector = System{}

// Name identifies this collector in progress output and errors.
func (System) Name() string { return "system" }

// Collect fills snap.System with the host OS, architecture, and kernel release.
//
// OS and Arch come from runtime.GOOS and runtime.GOARCH, which reflect the
// binary's build target and never depend on tools or the filesystem, so they
// are written unconditionally before any external call: even a cancelled
// context must not lose the one thing this collector guarantees to know. Kernel
// is best-effort; a missing or failing uname degrades the section rather than
// failing the capture, since kernel strings differ too often to drive
// diagnostics anyway.
func (System) Collect(ctx context.Context, snap *snapshot.Snapshot) error {
	sys := &snapshot.System{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}
	// Assigned before the uname probe so that a cancelled context or a missing
	// binary cannot yield a section that is emptier than it must be.
	snap.System = sys

	kernel, err := collector.Run(ctx, "uname", "-r")
	if err != nil {
		// uname may be absent in minimal containers, or fail under a cancelled
		// context; a missing kernel string is not an error, so nothing to report.
		return nil
	}
	sys.Kernel = kernel
	return nil
}
