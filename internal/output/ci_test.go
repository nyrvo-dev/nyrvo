package output

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

func TestCIInspectTextEmpty(t *testing.T) {
	want := "No workflow jobs found in .github/workflows.\n"
	if got := render(t, func(w io.Writer) error { return CIInspectText(w, nil) }); got != want {
		t.Errorf("CIInspectText empty\ngot:\n%q\nwant:\n%q", got, want)
	}
}

// TestCIInspectTextLayout locks in the exact rendered bytes for one fully
// populated job so column alignment, the env line, and the placement of the
// notes block cannot drift unnoticed.
func TestCIInspectTextLayout(t *testing.T) {
	snap := snapshot.New("ci", time.Now())
	snap.System = &snapshot.System{OS: "linux", Arch: "amd64"}
	snap.Runtimes = []snapshot.Runtime{
		{Name: "node", Version: "20"},
		{Name: "python", Version: "3.12"},
	}
	snap.Environment = &snapshot.Environment{Names: []string{"NODE_ENV", "REDIS_URL"}}
	snap.Source = &snapshot.Source{
		Kind: snapshot.SourceGitHubActions,
		Notes: []string{
			"on triggers are not modelled",
			`job "test": strategy.matrix.include is not modelled`,
		},
	}
	jobs := []CIJob{{Workflow: "ci.yml", Job: "test", Snapshot: snap}}

	want := `CI environments declared in .github/workflows

ci.yml  job test
  system     linux/amd64
  runtimes   node 20, python 3.12
  env        NODE_ENV, REDIS_URL
  not modelled
    - on triggers are not modelled
    - job "test": strategy.matrix.include is not modelled
`
	if got := render(t, func(w io.Writer) error { return CIInspectText(w, jobs) }); got != want {
		t.Errorf("CIInspectText layout\ngot:\n%q\nwant:\n%q", got, want)
	}
}

// Absent sections must be spelled out, not left blank: "not determined" and
// "none declared" are gaps the user can act on, a blank line is not.
func TestCIInspectTextAbsentFields(t *testing.T) {
	snap := snapshot.New("ci", time.Now())
	snap.Source = &snapshot.Source{Kind: snapshot.SourceGitHubActions}
	jobs := []CIJob{{Workflow: "ci.yml", Job: "test", Snapshot: snap}}

	out := render(t, func(w io.Writer) error { return CIInspectText(w, jobs) })
	if !strings.Contains(out, "  system     not determined\n") {
		t.Errorf("missing not determined line:\n%q", out)
	}
	if !strings.Contains(out, "  runtimes   none declared\n") {
		t.Errorf("missing none declared line:\n%q", out)
	}
}

// Notes are printed under a "not modelled" heading, one per line, and the
// heading appears only when there are notes to print.
func TestCIInspectTextNotesHeading(t *testing.T) {
	withNotes := snapshot.New("ci", time.Now())
	withNotes.System = &snapshot.System{OS: "linux", Arch: "amd64"}
	withNotes.Source = &snapshot.Source{Kind: snapshot.SourceGitHubActions, Notes: []string{"one", "two"}}

	noNotes := snapshot.New("ci", time.Now())
	noNotes.System = &snapshot.System{OS: "linux", Arch: "amd64"}
	noNotes.Source = &snapshot.Source{Kind: snapshot.SourceGitHubActions}

	noSource := snapshot.New("ci", time.Now())
	noSource.System = &snapshot.System{OS: "linux", Arch: "amd64"}

	tests := []struct {
		name string
		snap *snapshot.Snapshot
		want string // expected substring, or "" meaning the heading must be absent
	}{
		{"notes one per line", withNotes, "  not modelled\n    - one\n    - two\n"},
		{"no notes, source present", noNotes, ""},
		{"no source at all", noSource, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := render(t, func(w io.Writer) error {
				return CIInspectText(w, []CIJob{{Workflow: "ci.yml", Job: "test", Snapshot: tt.snap}})
			})
			if tt.want == "" {
				if strings.Contains(out, "not modelled") {
					t.Errorf("unexpected not modelled heading:\n%q", out)
				}
			} else if !strings.Contains(out, tt.want) {
				t.Errorf("missing notes block:\ngot:\n%q\nwant substring:\n%q", out, tt.want)
			}
		})
	}
}

// A broken writer must surface its error from both code paths (the empty
// shortcut and the full layout), never panic.
func TestCIInspectTextWriterError(t *testing.T) {
	sentinel := errors.New("disk full")
	jobs := []CIJob{{Workflow: "ci.yml", Job: "test", Snapshot: snapshot.New("ci", time.Now())}}

	tests := []struct {
		name string
		jobs []CIJob
	}{
		{"empty", nil},
		{"job", jobs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CIInspectText(errWriter{err: sentinel}, tt.jobs)
			if err == nil {
				t.Fatalf("%s: CIInspectText returned nil for a failing writer", tt.name)
			}
			if !errors.Is(err, sentinel) {
				t.Errorf("%s: CIInspectText returned %v, want it to wrap sentinel %v", tt.name, err, sentinel)
			}
		})
	}
}
