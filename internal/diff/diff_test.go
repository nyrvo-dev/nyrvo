package diff

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

func base(name string) *snapshot.Snapshot {
	s := snapshot.New(name, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	s.System = &snapshot.System{OS: "darwin", Arch: "arm64", Kernel: "25.5.0"}
	s.Git = &snapshot.Git{SHA: "abc123", Branch: "main", Dirty: false}
	s.Runtimes = []snapshot.Runtime{
		{Name: "go", Version: "1.25.0"},
		{Name: "node", Version: "24.4.0"},
	}
	s.Environment = &snapshot.Environment{Names: []string{"DATABASE_URL", "PATH", "REDIS_URL"}}
	return s
}

func TestIdenticalSnapshotsHaveNoDifferences(t *testing.T) {
	got := Compare(base("local"), base("staging"))
	if !got.Empty() {
		t.Fatalf("expected no differences, got %+v", got.Differences)
	}
	if got.A != "local" || got.B != "staging" {
		t.Fatalf("snapshot names not carried into result: %+v", got)
	}
}

// Capture time must never register as environment drift.
func TestTimestampsAreNotSemanticDrift(t *testing.T) {
	a := base("local")
	b := base("later")
	b.CreatedAt = a.CreatedAt.Add(72 * time.Hour)
	if got := Compare(a, b); !got.Empty() {
		t.Fatalf("timestamps produced differences: %+v", got.Differences)
	}
}

// Collectors may finish in any order; content, not position, decides equality.
func TestCollectionOrderIsNotSemanticDrift(t *testing.T) {
	a := base("local")
	b := base("changed")
	b.Runtimes = []snapshot.Runtime{{Name: "node", Version: "24.4.0"}, {Name: "go", Version: "1.25.0"}}
	b.Environment = &snapshot.Environment{Names: []string{"REDIS_URL", "PATH", "DATABASE_URL"}}
	if got := Compare(a, b); !got.Empty() {
		t.Fatalf("ordering produced differences: %+v", got.Differences)
	}
}

func TestSemanticDifferences(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*snapshot.Snapshot)
		want   Difference
	}{
		{
			name:   "runtime version mismatch",
			mutate: func(s *snapshot.Snapshot) { s.Runtime("node").Version = "22.18.0" },
			want:   Difference{Component: ComponentRuntime, Key: "node", Kind: KindChanged, A: "24.4.0", B: "22.18.0"},
		},
		{
			name:   "runtime missing in b",
			mutate: func(s *snapshot.Snapshot) { s.Runtimes = s.Runtimes[:1] },
			want:   Difference{Component: ComponentRuntime, Key: "node", Kind: KindOnlyInA, A: "24.4.0"},
		},
		{
			name: "environment variable only in a",
			mutate: func(s *snapshot.Snapshot) {
				s.Environment.Names = []string{"DATABASE_URL", "PATH"}
			},
			want: Difference{Component: ComponentEnvironment, Key: "REDIS_URL", Kind: KindOnlyInA, A: present},
		},
		{
			name: "environment variable only in b",
			mutate: func(s *snapshot.Snapshot) {
				s.Environment.Names = append(s.Environment.Names, "CI")
			},
			want: Difference{Component: ComponentEnvironment, Key: "CI", Kind: KindOnlyInB, B: present},
		},
		{
			name:   "git sha mismatch",
			mutate: func(s *snapshot.Snapshot) { s.Git.SHA = "def456" },
			want:   Difference{Component: ComponentGit, Key: "sha", Kind: KindChanged, A: "abc123", B: "def456"},
		},
		{
			name:   "git dirty state",
			mutate: func(s *snapshot.Snapshot) { s.Git.Dirty = true },
			want:   Difference{Component: ComponentGit, Key: "dirty", Kind: KindChanged, A: "false", B: "true"},
		},
		{
			name:   "os mismatch",
			mutate: func(s *snapshot.Snapshot) { s.System.OS = "linux" },
			want:   Difference{Component: ComponentSystem, Key: "os", Kind: KindChanged, A: "darwin", B: "linux"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, b := base("local"), base("changed")
			tc.mutate(b)
			got := Compare(a, b)
			if len(got.Differences) != 1 {
				t.Fatalf("want exactly 1 difference, got %+v", got.Differences)
			}
			if got.Differences[0] != tc.want {
				t.Fatalf("got %+v, want %+v", got.Differences[0], tc.want)
			}
		})
	}
}

// An environment where a collector was unavailable must diff without panicking,
// and must not invent differences for sections nobody observed.
func TestMissingSectionsDoNotPanic(t *testing.T) {
	full := base("local")
	empty := snapshot.New("bare", time.Now())

	// A section the other side never described is one difference, not one per
	// key: three "missing" lines for git would read as if the other environment
	// had been inspected and found to be on another commit.
	got := Compare(full, empty)
	want := []Difference{
		{Component: ComponentSystem, Kind: KindOnlyInA, A: described},
		{Component: ComponentGit, Kind: KindOnlyInA, A: described},
		{Component: ComponentRuntime, Kind: KindOnlyInA, A: described},
		{Component: ComponentEnvironment, Kind: KindOnlyInA, A: described},
	}
	if len(got.Differences) != len(want) {
		t.Fatalf("got %+v, want one difference per described section: %+v", got.Differences, want)
	}
	for i := range want {
		if got.Differences[i] != want[i] {
			t.Fatalf("difference %d = %+v, want %+v", i, got.Differences[i], want[i])
		}
	}

	// The mirror case reports the same way from the other side.
	if mirrored := Compare(empty, full); len(mirrored.Differences) != len(want) {
		t.Fatalf("mirrored comparison = %+v, want %d whole-section differences", mirrored.Differences, len(want))
	} else if mirrored.Differences[0].Kind != KindOnlyInB {
		t.Fatalf("mirrored kind = %q, want %q", mirrored.Differences[0].Kind, KindOnlyInB)
	}

	if got := Compare(empty, snapshot.New("bare2", time.Now())); !got.Empty() {
		t.Fatalf("two empty snapshots differ: %+v", got.Differences)
	}
	if got := Compare(nil, nil); !got.Empty() {
		t.Fatalf("nil snapshots differ: %+v", got.Differences)
	}
}

// An optional field observed on one side only (no uname, or a detached HEAD in
// CI) is reported as only_in_a — never as a change to the empty string, and
// never silently dropped.
func TestOptionalFieldsAbsentOnOneSide(t *testing.T) {
	a, b := base("local"), base("changed")
	b.System.Kernel = ""
	b.Git.Branch = ""

	got := Compare(a, b)
	want := []Difference{
		{Component: ComponentSystem, Key: "kernel", Kind: KindOnlyInA, A: "25.5.0"},
		{Component: ComponentGit, Key: "branch", Kind: KindOnlyInA, A: "main"},
	}
	if len(got.Differences) != len(want) {
		t.Fatalf("got %+v, want %+v", got.Differences, want)
	}
	for i := range want {
		if got.Differences[i] != want[i] {
			t.Fatalf("got %+v, want %+v", got.Differences, want)
		}
	}

	// Neither side observed it: nothing to report.
	a.System.Kernel = ""
	a.Git.Branch = ""
	if got := Compare(a, b); !got.Empty() {
		t.Fatalf("fields absent on both sides produced differences: %+v", got.Differences)
	}
}

// A CI-derived environment lists only what the workflow sets. Reporting every
// local variable it does not mention as "missing in ci" would bury the real
// findings under one line per shell variable, so those absences are suppressed
// while the variables CI does declare are still compared.
func TestPartialEnvironmentSuppressesAbsences(t *testing.T) {
	local := base("local")
	local.Environment.Names = []string{"DATABASE_URL", "HOME", "PATH", "SHELL", "TERM"}

	ci := base("ci")
	ci.System = nil
	ci.Git = nil
	ci.Runtimes = nil
	ci.Environment = &snapshot.Environment{Names: []string{"CI", "DATABASE_URL"}, Partial: true}

	got := Compare(local, ci)
	if !got.PartialEnvironment {
		t.Error("PartialEnvironment = false, want true so the reader knows the comparison was narrowed")
	}

	var envDiffs []Difference
	for _, d := range got.Differences {
		if d.Component == ComponentEnvironment {
			envDiffs = append(envDiffs, d)
		}
	}
	want := []Difference{{Component: ComponentEnvironment, Key: "CI", Kind: KindOnlyInB, B: present}}
	if len(envDiffs) != len(want) || envDiffs[0] != want[0] {
		t.Fatalf("environment differences = %+v, want %+v", envDiffs, want)
	}

	// Suppression is one-directional: the complete side can still testify to
	// absence, and the partial side's own declarations are still compared.
	reversed := Compare(ci, local)
	var reversedEnv []Difference
	for _, d := range reversed.Differences {
		if d.Component == ComponentEnvironment {
			reversedEnv = append(reversedEnv, d)
		}
	}
	wantReversed := Difference{Component: ComponentEnvironment, Key: "CI", Kind: KindOnlyInA, A: present}
	if len(reversedEnv) != 1 || reversedEnv[0] != wantReversed {
		t.Fatalf("reversed environment differences = %+v, want %+v", reversedEnv, wantReversed)
	}

	// Sections other than environment are unaffected by partiality.
	if len(got.Differences) <= len(envDiffs) {
		t.Error("partial environments must not suppress system, git or runtime differences")
	}
}

// Partiality is about the environment list only; nothing else changes when
// both sides are complete.
func TestCompleteEnvironmentsStillReportAbsences(t *testing.T) {
	a := base("local")
	b := base("other")
	b.Environment = &snapshot.Environment{Names: []string{"PATH"}}

	got := Compare(a, b)
	if got.PartialEnvironment {
		t.Error("PartialEnvironment = true for two complete environments")
	}
	found := 0
	for _, d := range got.Differences {
		if d.Component == ComponentEnvironment && d.Kind == KindOnlyInA {
			found++
		}
	}
	if found != 2 {
		t.Errorf("got %d only_in_a environment differences, want 2 (DATABASE_URL, REDIS_URL)", found)
	}
}

func TestResultJSONIsDeterministic(t *testing.T) {
	a, b := base("local"), base("changed")
	b.Runtime("node").Version = "22.18.0"
	b.Environment.Names = []string{"DATABASE_URL", "CI"}

	first, err := json.Marshal(Compare(a, b))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		next, err := json.Marshal(Compare(a, b))
		if err != nil {
			t.Fatal(err)
		}
		if string(next) != string(first) {
			t.Fatalf("diff JSON is unstable:\n%s\n%s", first, next)
		}
	}
}

// Differences are grouped by component in a fixed order so output never
// reshuffles between runs.
func TestDifferenceOrdering(t *testing.T) {
	a, b := base("local"), base("changed")
	b.Environment.Names = []string{"DATABASE_URL"}
	b.System.OS = "linux"
	b.Runtime("go").Version = "1.24.0"

	got := Compare(a, b)
	var order []string
	for _, d := range got.Differences {
		order = append(order, d.Component+"/"+d.Key)
	}
	want := []string{"system/os", "runtime/go", "environment/PATH", "environment/REDIS_URL"}
	if len(order) != len(want) {
		t.Fatalf("got %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("got %v, want %v", order, want)
		}
	}
}

func TestPartialRuntimesSuppressesAbsences(t *testing.T) {
	local := base("local")
	local.Runtimes = []snapshot.Runtime{
		{Name: "go", Version: "1.26.6"},
		{Name: "node", Version: "23.6.0"},
		{Name: "python", Version: "3.13.3"},
	}

	// What a workflow declares: setup-go and nothing else. The runner image
	// still ships node and python, so their absence here is silence, not a fact.
	ci := base("ci")
	ci.System = nil
	ci.Git = nil
	ci.Environment = nil
	ci.Runtimes = []snapshot.Runtime{{Name: "go", Version: "1.25"}}
	ci.PartialRuntimes = true

	got := Compare(local, ci)
	if !got.PartialRuntimes {
		t.Error("PartialRuntimes = false, want true so the reader knows the comparison was narrowed")
	}

	var runtimeDiffs []Difference
	for _, d := range got.Differences {
		if d.Component == ComponentRuntime {
			runtimeDiffs = append(runtimeDiffs, d)
		}
	}
	want := []Difference{{Component: ComponentRuntime, Key: "go", Kind: KindChanged, A: "1.26.6", B: "1.25"}}
	if len(runtimeDiffs) != len(want) || runtimeDiffs[0] != want[0] {
		t.Fatalf("runtime differences = %+v, want only the runtime both sides describe, got %+v", want, runtimeDiffs)
	}
}

func TestPartialRuntimesStillTestifiesToWhatItDeclares(t *testing.T) {
	// Suppression runs one way only. A runtime the workflow sets up and the
	// laptop does not have is real drift and must survive.
	local := base("local")
	local.Runtimes = []snapshot.Runtime{{Name: "go", Version: "1.26.6"}}

	ci := base("ci")
	ci.System = nil
	ci.Git = nil
	ci.Environment = nil
	ci.Runtimes = []snapshot.Runtime{
		{Name: "go", Version: "1.26.6"},
		{Name: "node", Version: "22.0.0"},
	}
	ci.PartialRuntimes = true

	var runtimeDiffs []Difference
	for _, d := range Compare(local, ci).Differences {
		if d.Component == ComponentRuntime {
			runtimeDiffs = append(runtimeDiffs, d)
		}
	}
	want := Difference{Component: ComponentRuntime, Key: "node", Kind: KindOnlyInB, B: "22.0.0"}
	if len(runtimeDiffs) != 1 || runtimeDiffs[0] != want {
		t.Fatalf("runtime differences = %+v, want %+v", runtimeDiffs, want)
	}
}

// The bug in one test: two captures of one machine, where the first could not
// read npm in time. Without this, the comparison reports that npm appeared
// between them — drift Nyrvo invented rather than drift it observed.
func TestUnmeasuredKeysAreNotReportedAsDrift(t *testing.T) {
	a := snapshot.New("local", time.Time{})
	a.Unmeasured = []string{"runtime.npm"}
	b := snapshot.New("other", time.Time{})
	b.Runtimes = []snapshot.Runtime{{Name: "npm", Version: "10.9.8"}}

	res := Compare(a, b)
	if len(res.Differences) != 0 {
		t.Errorf("an unread probe was reported as a difference: %+v", res.Differences)
	}
	// The reader has to be told the comparison was narrowed, or silence reads as
	// agreement.
	if !res.Unmeasured {
		t.Error("Result.Unmeasured = false, want true so the narrowing is announced")
	}
}

// daemon_running is the case a one-sided rule would miss. It is a bool, so a
// probe that ran out of time leaves a confident "false" that is
// indistinguishable from an observation, and the difference reads as a change
// rather than as an absence.
func TestUnmeasuredBoolIsNotReportedAsAChange(t *testing.T) {
	a := snapshot.New("local", time.Time{})
	a.Docker = &snapshot.Docker{ClientVersion: "29.1.5", DaemonRunning: false}
	a.Unmeasured = []string{"docker.daemon_running", "docker.server_version"}
	b := snapshot.New("other", time.Time{})
	b.Docker = &snapshot.Docker{ClientVersion: "29.1.5", ServerVersion: "29.1.5", DaemonRunning: true}

	res := Compare(a, b)
	for _, d := range res.Differences {
		if d.Component == ComponentDocker {
			t.Errorf("an unread docker probe was reported: %+v", d)
		}
	}
}

// The narrowing must be announced even when nothing was dropped, for the same
// reason a partial environment is: a reader deciding whether the environments
// agree needs to know the comparison could not cover everything.
func TestUnmeasuredIsAnnouncedEvenWhenNothingWasDropped(t *testing.T) {
	a := snapshot.New("local", time.Time{})
	a.Unmeasured = []string{"runtime.npm"}
	b := snapshot.New("other", time.Time{})

	if res := Compare(a, b); !res.Unmeasured {
		t.Error("Result.Unmeasured = false, want true")
	}
}

// Suppression must be exact. A probe that failed to read npm says nothing about
// node, and swallowing a real difference is the mirror-image defect.
func TestUnmeasuredSuppressesOnlyTheKeyItNames(t *testing.T) {
	a := snapshot.New("local", time.Time{})
	a.Runtimes = []snapshot.Runtime{{Name: "node", Version: "20.1.0"}}
	a.Unmeasured = []string{"runtime.npm"}
	b := snapshot.New("other", time.Time{})
	b.Runtimes = []snapshot.Runtime{{Name: "node", Version: "22.4.0"}, {Name: "npm", Version: "10.9.8"}}

	res := Compare(a, b)
	if len(res.Differences) != 1 {
		t.Fatalf("differences = %+v, want only the node change", res.Differences)
	}
	if d := res.Differences[0]; d.Key != "node" || d.Kind != KindChanged {
		t.Errorf("difference = %+v, want the node version change", d)
	}
}
