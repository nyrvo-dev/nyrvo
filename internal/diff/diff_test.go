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

	got := Compare(full, empty)
	if len(got.Differences) == 0 {
		t.Fatal("expected the fully observed side to report only_in_a differences")
	}
	for _, d := range got.Differences {
		if d.Kind != KindOnlyInA {
			t.Fatalf("unexpected kind %q for %s/%s", d.Kind, d.Component, d.Key)
		}
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
