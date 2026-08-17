// Package capture runs collectors and assembles a snapshot of the current
// environment.
package capture

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// Status is the outcome of one collector.
type Status string

const (
	// StatusOK means the collector observed its section.
	StatusOK Status = "ok"
	// StatusUnavailable means the collector had nothing to observe here (no
	// Git repository, runtime not installed). This is normal, not a failure.
	StatusUnavailable Status = "unavailable"
	// StatusFailed means the collector was expected to work and did not.
	StatusFailed Status = "failed"
)

// SectionResult reports how one collector fared, so the renderer can show the
// user what was and was not observed instead of silently omitting sections.
type SectionResult struct {
	Collector string `json:"collector"`
	Status    Status `json:"status"`
	// Error is the failure message for StatusFailed and the reason for
	// StatusUnavailable. It never contains environment values.
	Error string `json:"error,omitempty"`
}

// Result is a completed capture: the snapshot plus per-collector status.
type Result struct {
	Snapshot *snapshot.Snapshot `json:"snapshot"`
	Sections []SectionResult    `json:"sections"`
}

// Failed reports whether any collector failed outright. Unavailable sections do
// not count: a machine without Node still produces a useful snapshot.
func (r *Result) Failed() bool {
	for _, s := range r.Sections {
		if s.Status == StatusFailed {
			return true
		}
	}
	return false
}

// FailedSections names the collectors that failed outright, in the order
// they ran.
func (r *Result) FailedSections() []string {
	var failed []string
	for _, s := range r.Sections {
		if s.Status == StatusFailed {
			failed = append(failed, s.Collector)
		}
	}
	return failed
}

// Options configures a capture run.
type Options struct {
	// Name identifies the snapshot ("local", "staging").
	Name string
	// Now supplies the capture timestamp; tests inject a fixed clock so golden
	// output stays stable.
	Now func() time.Time
	// OnSectionStart is called immediately before each collector runs, in the
	// same order as the collectors. It mirrors OnSection, which fires after the
	// collector answers, so a caller can show progress while a collector is
	// running: a capture spawns a dozen external tools and takes seconds, and a
	// label that only appeared after the tool answered would still leave the
	// terminal silent for the whole wait.
	//
	// Capture does not print. It hands the event up and lets the caller decide
	// whether anything is rendered at all.
	OnSectionStart func(name string)
	// OnSection is called as each collector finishes, before the next one
	// starts. Run still returns every section in the Result; this exists only so
	// a caller can report progress while the capture is happening. A capture
	// spawns a dozen external tools and takes seconds, and without this the
	// terminal stays silent until all of them have answered.
	//
	// Capture does not print. It hands the section up and lets the caller decide
	// whether anything is rendered at all.
	OnSection func(SectionResult)
}

// Run executes collectors in order and returns the assembled snapshot.
//
// Collectors run sequentially. They are process spawns measured in
// milliseconds, and sequential execution keeps the snapshot deterministic
// without any synchronization around the shared snapshot value.
// ponytail: if the collector set grows to something slow (Docker, databases),
// parallelize per-section with each collector writing only its own field.
//
// A collector reporting ErrUnavailable leaves its section absent and the
// capture continues; any other collector error is recorded and surfaces through
// Result.Failed, because a tool that exists but misbehaves is evidence the user
// needs, not something to hide.
func Run(ctx context.Context, collectors []collector.Collector, opts Options) (*Result, error) {
	if err := snapshot.ValidateName(opts.Name); err != nil {
		return nil, err
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	snap := snapshot.New(opts.Name, now())
	// A capture is the one snapshot produced by watching a machine rather than
	// by reading a file about one, and it has to say so. Leaving the source
	// unset left the strongest evidence Nyrvo has as the only kind that could
	// not state where it came from, next to CI snapshots that do.
	snap.Source = &snapshot.Source{Kind: snapshot.SourceLocal}
	result := &Result{Snapshot: snap, Sections: make([]SectionResult, 0, len(collectors))}

	for _, c := range collectors {
		// A cancelled context aborts the whole capture: a partial snapshot
		// presented as complete would be misleading evidence.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("capture cancelled during %q: %w", c.Name(), err)
		}
		if opts.OnSectionStart != nil {
			opts.OnSectionStart(c.Name())
		}
		section := SectionResult{Collector: c.Name(), Status: StatusOK}
		switch err := c.Collect(ctx, snap); {
		case err == nil:
		case errors.Is(err, collector.ErrUnavailable):
			section.Status = StatusUnavailable
			section.Error = err.Error()
		default:
			section.Status = StatusFailed
			section.Error = err.Error()
		}
		result.Sections = append(result.Sections, section)
		if opts.OnSection != nil {
			opts.OnSection(section)
		}
	}

	snap.Normalize()
	return result, nil
}
