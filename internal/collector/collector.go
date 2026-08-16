// Package collector defines how Nyrvo observes an environment.
//
// A collector fills one section of a snapshot. Collectors are deliberately
// small and independent so contributors can add one without understanding the
// rest of the pipeline, and so a missing tool degrades one section instead of
// failing the whole capture.
package collector

import (
	"context"
	"errors"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// ErrUnavailable reports that a collector cannot observe anything here: no Git
// repository, no Python on PATH, no Docker daemon. Capture records the section
// as absent and continues.
//
// This is a sentinel rather than a separate Available() method because probing
// twice (once to ask, once to collect) doubles the process spawns and can
// disagree with itself between the two calls.
var ErrUnavailable = errors.New("collector unavailable")

// Collector observes one aspect of an environment and writes it into snap.
//
// Implementations must respect ctx (use exec.CommandContext for external
// tools), must not print anything, and must not persist environment variable
// values. They may assume they are the only writer of their own section.
type Collector interface {
	// Name identifies the collector in progress output and errors, e.g. "git".
	Name() string
	// Collect fills this collector's section of snap. Returning ErrUnavailable
	// (possibly wrapped) marks the section absent without failing the capture;
	// any other error is reported as a collection failure.
	Collect(ctx context.Context, snap *snapshot.Snapshot) error
}
