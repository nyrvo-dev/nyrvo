package capture

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// stub is a collector whose behaviour the test dictates, so capture can be
// exercised without any real tool being installed.
type stub struct {
	name    string
	err     error
	collect func(*snapshot.Snapshot)
	calls   *int
}

func (s stub) Name() string { return s.name }

func (s stub) Collect(_ context.Context, snap *snapshot.Snapshot) error {
	if s.calls != nil {
		*s.calls++
	}
	if s.collect != nil {
		s.collect(snap)
	}
	return s.err
}

func fixedClock() func() time.Time {
	t := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func TestRunRecordsSectionStatus(t *testing.T) {
	collectors := []collector.Collector{
		stub{name: "system", collect: func(s *snapshot.Snapshot) {
			s.System = &snapshot.System{OS: "linux", Arch: "amd64"}
		}},
		stub{name: "git", err: fmt.Errorf("no repository: %w", collector.ErrUnavailable)},
		stub{name: "node", err: errors.New("node exited 1")},
	}

	res, err := Run(context.Background(), collectors, Options{Name: "local", Now: fixedClock()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []SectionResult{
		{Collector: "system", Status: StatusOK},
		{Collector: "git", Status: StatusUnavailable, Error: "no repository: collector unavailable"},
		{Collector: "node", Status: StatusFailed, Error: "node exited 1"},
	}
	if len(res.Sections) != len(want) {
		t.Fatalf("sections = %+v, want %+v", res.Sections, want)
	}
	for i := range want {
		if res.Sections[i] != want[i] {
			t.Fatalf("section %d = %+v, want %+v", i, res.Sections[i], want[i])
		}
	}

	// A tool that exists but misbehaves is a failure the user must see; a tool
	// that is simply absent is not.
	if !res.Failed() {
		t.Error("Failed() = false, want true when a collector errored")
	}
	if res.Snapshot.System == nil {
		t.Error("a failing collector must not discard what the others observed")
	}
}

func TestRunUnavailableCollectorsAreNotFailures(t *testing.T) {
	collectors := []collector.Collector{
		stub{name: "node", err: fmt.Errorf("not installed: %w", collector.ErrUnavailable)},
	}
	res, err := Run(context.Background(), collectors, Options{Name: "local", Now: fixedClock()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Failed() {
		t.Error("Failed() = true, want false: a missing optional runtime is normal")
	}
}

func TestFailedSectionsReturnsOnlyFailedCollectors(t *testing.T) {
	res := &Result{Sections: []SectionResult{
		{Collector: "system", Status: StatusOK},
		{Collector: "node", Status: StatusUnavailable},
		{Collector: "docker", Status: StatusFailed},
		{Collector: "git", Status: StatusFailed},
	}}

	got := res.FailedSections()
	want := []string{"docker", "git"}
	if len(got) != len(want) {
		t.Fatalf("FailedSections() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FailedSections() = %v, want %v", got, want)
		}
	}
}

func TestFailedSectionsReturnsNilWhenNothingFailed(t *testing.T) {
	res := &Result{Sections: []SectionResult{
		{Collector: "system", Status: StatusOK},
		{Collector: "node", Status: StatusUnavailable},
	}}

	if got := res.FailedSections(); got != nil {
		t.Errorf("FailedSections() = %v, want nil", got)
	}
}

func TestRunNormalizesSnapshot(t *testing.T) {
	collectors := []collector.Collector{
		stub{name: "runtimes", collect: func(s *snapshot.Snapshot) {
			s.Runtimes = []snapshot.Runtime{{Name: "node", Version: "24"}, {Name: "go", Version: "1.25"}}
			s.Environment = &snapshot.Environment{Names: []string{"Z", "A"}}
		}},
	}
	res, err := Run(context.Background(), collectors, Options{Name: "local", Now: fixedClock()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Snapshot.Runtimes[0].Name != "go" {
		t.Errorf("runtimes not sorted: %+v", res.Snapshot.Runtimes)
	}
	if res.Snapshot.Environment.Names[0] != "A" {
		t.Errorf("environment names not sorted: %+v", res.Snapshot.Environment.Names)
	}
	if res.Snapshot.SchemaVersion != snapshot.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", res.Snapshot.SchemaVersion, snapshot.SchemaVersion)
	}
	if !res.Snapshot.CreatedAt.Equal(fixedClock()()) {
		t.Errorf("CreatedAt = %v, want the injected clock value", res.Snapshot.CreatedAt)
	}
}

// A cancelled capture must not be presented as a complete picture of the
// environment, so it fails instead of returning a half-filled snapshot.
func TestRunCancelledContextStopsEarly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	collectors := []collector.Collector{stub{name: "system", calls: &calls}}

	res, err := Run(ctx, collectors, Options{Name: "local", Now: fixedClock()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if res != nil {
		t.Errorf("Run() = %+v, want nil result on cancellation", res)
	}
	if calls != 0 {
		t.Errorf("collector ran %d times after cancellation, want 0", calls)
	}
}

func TestRunRejectsInvalidName(t *testing.T) {
	for _, name := range []string{"", "../escape", "a/b"} {
		if _, err := Run(context.Background(), nil, Options{Name: name, Now: fixedClock()}); err == nil {
			t.Errorf("Run(name=%q) = nil error, want a validation failure", name)
		}
	}
}

// Without an injected clock the capture still has to be stamped, otherwise
// snapshots would be indistinguishable in time.
func TestRunDefaultsClock(t *testing.T) {
	res, err := Run(context.Background(), nil, Options{Name: "local"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Snapshot.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero, want the current time")
	}
}

func TestRunRecordsThatTheSnapshotWasObservedLocally(t *testing.T) {
	res, err := Run(context.Background(), nil, Options{Name: "local"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Without this, a local capture is the only snapshot Nyrvo produces that
	// cannot say where it came from, while every CI snapshot can.
	if res.Snapshot.Source == nil || res.Snapshot.Source.Kind != snapshot.SourceLocal {
		t.Fatalf("source = %+v, want kind %q", res.Snapshot.Source, snapshot.SourceLocal)
	}
}

// TestRunReportsEachSectionBeforeTheNextCollectorRuns is the whole point of the
// hook. A capture spawns a dozen external tools, and a hook that only delivered
// its sections once every one of them had answered would render exactly the
// silence it exists to remove: each collector asserts that its own predecessor
// was already reported by the time it started.
func TestRunReportsEachSectionBeforeTheNextCollectorRuns(t *testing.T) {
	var delivered []string
	seen := func(name string) collector.Collector {
		return stub{name: name, collect: func(*snapshot.Snapshot) {
			if name == "system" {
				return
			}
			if len(delivered) == 0 {
				t.Errorf("%s started before any section was reported", name)
			}
		}}
	}

	res, err := Run(context.Background(), []collector.Collector{
		seen("system"), seen("git"), seen("node"),
	}, Options{
		Name: "local", Now: fixedClock(),
		OnSection: func(s SectionResult) { delivered = append(delivered, s.Collector) },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{"system", "git", "node"}
	if len(delivered) != len(want) {
		t.Fatalf("delivered %v, want %v", delivered, want)
	}
	for i, name := range want {
		if delivered[i] != name {
			t.Errorf("delivered[%d] = %q, want %q", i, delivered[i], name)
		}
		// The hook reports what the Result records, so a caller that renders
		// only the stream never disagrees with one that renders the Result.
		if res.Sections[i].Collector != name {
			t.Errorf("sections[%d] = %q, want %q", i, res.Sections[i].Collector, name)
		}
	}
}

// A nil hook is the normal case for every caller that does not render progress.
func TestRunWithoutHook(t *testing.T) {
	if _, err := Run(context.Background(), []collector.Collector{stub{name: "system"}}, Options{
		Name: "local", Now: fixedClock(),
	}); err != nil {
		t.Fatalf("Run without a hook: %v", err)
	}
}

// TestRunReportsEachSectionStartBeforeItsCollector is the counterpart of
// TestRunReportsEachSectionBeforeTheNextCollectorRuns for the start hook. Each
// collector asserts that its own name was announced before it ran, and that the
// announcement arrived before the finish report for the same collector.
func TestRunReportsEachSectionStartBeforeItsCollector(t *testing.T) {
	var started []string
	var finished []string
	seen := func(name string) collector.Collector {
		return stub{name: name, collect: func(*snapshot.Snapshot) {
			if name == "system" {
				return
			}
			if len(started) == 0 || started[len(started)-1] != name {
				t.Errorf("%s ran before its own OnSectionStart announced it; started so far: %v", name, started)
			}
		}}
	}

	res, err := Run(context.Background(), []collector.Collector{
		seen("system"), seen("git"), seen("node"),
	}, Options{
		Name: "local", Now: fixedClock(),
		OnSectionStart: func(name string) { started = append(started, name) },
		OnSection:      func(s SectionResult) { finished = append(finished, s.Collector) },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{"system", "git", "node"}
	if len(started) != len(want) {
		t.Fatalf("started %v, want %v", started, want)
	}
	if len(finished) != len(want) {
		t.Fatalf("finished %v, want %v", finished, want)
	}
	for i, name := range want {
		if started[i] != name {
			t.Errorf("started[%d] = %q, want %q", i, started[i], name)
		}
		if finished[i] != name {
			t.Errorf("finished[%d] = %q, want %q", i, finished[i], name)
		}
		// The start hook reports the same name the Result records, so a caller
		// that renders only the stream never disagrees with one that renders
		// the Result.
		if res.Sections[i].Collector != name {
			t.Errorf("sections[%d] = %q, want %q", i, res.Sections[i].Collector, name)
		}
	}
}

// A capture whose collectors hang must stop on its own. Every probe has its own
// deadline, but nothing bounded their sum, so a machine where each tool hangs
// spent the total of all of them with a spinner turning and no way to know it
// would ever end.
func TestRunStopsWhenTheBudgetExpires(t *testing.T) {
	slow := stub{name: "system", collect: func(*snapshot.Snapshot) { time.Sleep(200 * time.Millisecond) }}
	never := 0
	collectors := []collector.Collector{slow, stub{name: "git", calls: &never}}

	res, err := Run(context.Background(), collectors, Options{
		Name:   "local",
		Now:    fixedClock(),
		Budget: 50 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("Run() succeeded, want a budget failure")
	}
	// The message has to name the budget and the collector that was running:
	// "nyrvo hung" is not a report anyone can act on.
	for _, want := range []string{"gave up after", "system"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Run() error = %q, want it to mention %q", err, want)
		}
	}
	// No snapshot, deliberately. Collectors that never ran leave their sections
	// absent, and absence in a snapshot means "looked for and not found" — so
	// saving a partial capture would publish this machine as lacking every tool
	// the capture did not reach. That is the bug class this project keeps
	// fixing, and the budget must not reintroduce it.
	if res != nil {
		t.Errorf("Run() = %+v, want no snapshot when the budget expires", res)
	}
	if never != 0 {
		t.Errorf("collector after the budget ran %d times, want 0", never)
	}
}

// A caller's own cancellation and the capture's budget both cancel the same
// context, and they mean different things: one is the user pressing Ctrl-C, the
// other is a machine that will not answer. Only the cause tells them apart.
func TestRunDistinguishesCancellationFromTheBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Run(ctx, []collector.Collector{stub{name: "system"}}, Options{
		Name:   "local",
		Now:    fixedClock(),
		Budget: time.Minute,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if strings.Contains(err.Error(), "gave up after") {
		t.Errorf("a user's cancellation was reported as a budget failure: %v", err)
	}
}

// Windows allows every probe three times as long, so its capture budget has to
// be larger or a healthy Windows capture would be cut off by the bound meant to
// catch a broken one. Both branches are exercised here rather than only on the
// platform CI happens to be running.
func TestDefaultBudgetIsLargerOnWindows(t *testing.T) {
	win, unix := defaultBudget("windows"), defaultBudget("linux")
	if win <= unix {
		t.Errorf("defaultBudget(windows) = %s, want more than %s", win, unix)
	}
	// The budget only earns its place if it is well clear of a healthy capture
	// and still well under the sum of every probe deadline it is bounding.
	if unix < 30*time.Second {
		t.Errorf("defaultBudget(linux) = %s, too tight for a slow but healthy machine", unix)
	}
}
