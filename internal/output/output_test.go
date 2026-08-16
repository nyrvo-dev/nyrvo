package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/capture"
	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// render runs one renderer against a fresh buffer and returns what it wrote,
// failing the test if the renderer reports an error. The buffer keeps golden
// assertions free of error-handling noise.
func render(t *testing.T, fn func(io.Writer) error) string {
	t.Helper()
	var buf bytes.Buffer
	if err := fn(&buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestDiffTextEmpty(t *testing.T) {
	res := &diff.Result{A: "local", B: "ci"}
	want := "No differences between local and ci.\n"
	if got := render(t, func(w io.Writer) error { return DiffText(w, res) }); got != want {
		t.Errorf("DiffText empty result\ngot:\n%q\nwant:\n%q", got, want)
	}
}

// TestDiffTextGroupsAndAligns locks in the exact rendered bytes for a small
// fixed result so column alignment and component grouping cannot drift
// unnoticed. The result carries system before git and the differences appear
// in that order; the renderer must not reorder them.
func TestDiffTextGroupsAndAligns(t *testing.T) {
	res := &diff.Result{
		SchemaVersion: snapshot.SchemaVersion,
		A:             "local",
		B:             "ci",
		Differences: []diff.Difference{
			{Component: diff.ComponentSystem, Key: "os", Kind: diff.KindChanged, A: "darwin", B: "linux"},
			{Component: diff.ComponentSystem, Key: "kernel", Kind: diff.KindChanged, A: "23.2.1", B: "6.8.1"},
			{Component: diff.ComponentGit, Key: "branch", Kind: diff.KindOnlyInB, B: "main"},
		},
	}
	want := `Differences between local and ci

System

  os
    local  darwin
    ci     linux
  kernel
    local  23.2.1
    ci     6.8.1

Git

  branch
    local  missing
    ci     main
`
	if got := render(t, func(w io.Writer) error { return DiffText(w, res) }); got != want {
		t.Errorf("DiffText fixed result\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// TestDiffTextSpellsOutAbsentSide pins down that an observation present on only
// one side reads as "missing" on the other. This matters for environment
// variables, whose stored value is only ever the placeholder "present": the
// opposite must read as absence, never as the empty string.
func TestDiffTextSpellsOutAbsentSide(t *testing.T) {
	tests := []struct {
		name string
		res  *diff.Result
		want string
	}{
		{
			name: "only_in_a",
			res: &diff.Result{
				A: "local",
				B: "ci",
				Differences: []diff.Difference{
					{Component: diff.ComponentSystem, Key: "kernel", Kind: diff.KindOnlyInA, A: "23.2.1"},
				},
			},
			want: "Differences between local and ci\n\nSystem\n\n  kernel\n    local  23.2.1\n    ci     missing\n",
		},
		{
			name: "only_in_b",
			res: &diff.Result{
				A: "local",
				B: "ci",
				Differences: []diff.Difference{
					{Component: diff.ComponentGit, Key: "branch", Kind: diff.KindOnlyInB, B: "main"},
				},
			},
			want: "Differences between local and ci\n\nGit\n\n  branch\n    local  missing\n    ci     main\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := render(t, func(w io.Writer) error { return DiffText(w, tt.res) }); got != tt.want {
				t.Errorf("DiffText %s\ngot:\n%q\nwant:\n%q", tt.name, got, tt.want)
			}
		})
	}
}

// A whole-section difference (empty key) must read as "the other side never
// described this", not as an observation that came back empty.
func TestDiffTextWholeSectionDifference(t *testing.T) {
	res := &diff.Result{
		A: "local",
		B: "ci",
		Differences: []diff.Difference{
			{Component: diff.ComponentGit, Kind: diff.KindOnlyInA, A: "described"},
		},
	}
	want := "Differences between local and ci\n\nGit\n\n  described in local, not described in ci\n"
	if got := render(t, func(w io.Writer) error { return DiffText(w, res) }); got != want {
		t.Errorf("DiffText whole-section\ngot:\n%q\nwant:\n%q", got, want)
	}

	res.Differences[0] = diff.Difference{Component: diff.ComponentGit, Kind: diff.KindOnlyInB, B: "described"}
	want = "Differences between local and ci\n\nGit\n\n  described in ci, not described in local\n"
	if got := render(t, func(w io.Writer) error { return DiffText(w, res) }); got != want {
		t.Errorf("DiffText whole-section mirrored\ngot:\n%q\nwant:\n%q", got, want)
	}
}

// A narrowed environment comparison must announce itself, whether or not any
// difference was found: a report that looks exhaustive while skipping variables
// is worse than a noisy one.
func TestDiffTextPartialEnvironmentNote(t *testing.T) {
	const note = "One side lists only the environment variables it declares"

	empty := &diff.Result{A: "local", B: "ci", PartialEnvironment: true}
	if got := render(t, func(w io.Writer) error { return DiffText(w, empty) }); !strings.Contains(got, note) {
		t.Errorf("no-differences output omits the partial-environment note:\n%q", got)
	}

	withDiff := &diff.Result{
		A: "local", B: "ci", PartialEnvironment: true,
		Differences: []diff.Difference{
			{Component: diff.ComponentEnvironment, Key: "CI", Kind: diff.KindOnlyInB, B: "present"},
		},
	}
	if got := render(t, func(w io.Writer) error { return DiffText(w, withDiff) }); !strings.Contains(got, note) {
		t.Errorf("difference output omits the partial-environment note:\n%q", got)
	}

	complete := &diff.Result{A: "local", B: "other"}
	if got := render(t, func(w io.Writer) error { return DiffText(w, complete) }); strings.Contains(got, note) {
		t.Errorf("complete comparison printed the partial-environment note:\n%q", got)
	}
}

func TestCaptureTextStatusLines(t *testing.T) {
	res := &capture.Result{
		Sections: []capture.SectionResult{
			{Collector: "system", Status: capture.StatusOK},
			{Collector: "git", Status: capture.StatusUnavailable, Error: "not a git repository"},
			{Collector: "python", Status: capture.StatusFailed, Error: "boom: exit status 1"},
		},
	}
	want := "Capturing environment...\n\n" +
		"  ok        system\n" +
		"  skipped   git (not available here)\n" +
		"  FAILED    python: boom: exit status 1\n"
	if got := render(t, func(w io.Writer) error { return CaptureText(w, res) }); got != want {
		t.Errorf("CaptureText status lines\ngot:\n%q\nwant:\n%q", got, want)
	}
}

// TestCaptureTextOmitsErrorForOKSection guards the section body: a successful
// collector's Error field must never leak into output, even if a caller leaves
// it populated.
func TestCaptureTextOmitsErrorForOKSection(t *testing.T) {
	res := &capture.Result{
		Sections: []capture.SectionResult{
			{Collector: "system", Status: capture.StatusOK, Error: "this must never be printed"},
		},
	}
	want := "Capturing environment...\n\n  ok        system\n"
	if got := render(t, func(w io.Writer) error { return CaptureText(w, res) }); got != want {
		t.Errorf("CaptureText OK section\ngot:\n%q\nwant:\n%q", got, want)
	}
	if strings.Contains(render(t, func(w io.Writer) error { return CaptureText(w, res) }), "must never be printed") {
		t.Error("CaptureText leaked an OK section's error text")
	}
}

func TestJSONIndentedAndRoundTrips(t *testing.T) {
	type sample struct {
		Name string `json:"name"`
		Vals []int  `json:"vals"`
	}
	in := sample{Name: "local", Vals: []int{1, 2, 3}}
	var buf bytes.Buffer
	if err := JSON(&buf, in); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("JSON output not newline-terminated: %q", out)
	}
	if !strings.Contains(out, "\n  ") {
		t.Errorf("JSON output not indented: %q", out)
	}
	var got sample
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round-trip mismatch:\ngot:  %+v\nwant: %+v", got, in)
	}
}

func TestSnapshotList(t *testing.T) {
	names := []string{"local", "staging"}
	want := "local\nstaging\n"
	if got := render(t, func(w io.Writer) error { return SnapshotList(w, names) }); got != want {
		t.Errorf("SnapshotList names\ngot:\n%q\nwant:\n%q", got, want)
	}
}

func TestSnapshotListEmptyPrintsGuidance(t *testing.T) {
	want := "No snapshots yet. Run: nyrvo capture local\n"
	if got := render(t, func(w io.Writer) error { return SnapshotList(w, nil) }); got != want {
		t.Errorf("SnapshotList empty\ngot:\n%q\nwant:\n%q", got, want)
	}
}

// TestSnapshotJSONMatchesMarshal asserts that SnapshotJSON emits exactly the
// canonical bytes stored on disk, so piping and reading a snapshot file are
// interchangeable. Marshal normalizes in place, but Normalize is idempotent, so
// reusing the same snapshot is safe.
func TestSnapshotJSONMatchesMarshal(t *testing.T) {
	snap := snapshot.New("local", time.Now())
	snap.System = &snapshot.System{OS: "darwin", Arch: "arm64", Kernel: "23.2.1"}
	want, err := snapshot.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var buf bytes.Buffer
	if err := SnapshotJSON(&buf, snap); err != nil {
		t.Fatalf("SnapshotJSON: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("SnapshotJSON != snapshot.Marshal\ngot:  %q\nwant: %q", buf.Bytes(), want)
	}
}

// errWriter is a stub whose Write always fails, so a renderer's error path can
// be exercised without any real I/O.
type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

// TestRenderersPropagateWriterError covers every renderer: a broken writer must
// surface its error to the caller, never panic. The tabwriter-backed DiffText
// path is included because its error only appears at Flush time, not at Write
// time.
func TestRenderersPropagateWriterError(t *testing.T) {
	sentinel := errors.New("disk full")
	noDiff := &diff.Result{A: "local", B: "ci"}
	withDiff := &diff.Result{
		A: "local",
		B: "ci",
		Differences: []diff.Difference{
			{Component: diff.ComponentSystem, Key: "os", Kind: diff.KindChanged, A: "darwin", B: "linux"},
		},
	}
	snap := snapshot.New("local", time.Now())

	tests := []struct {
		name string
		run  func(w io.Writer) error
	}{
		{"JSON", func(w io.Writer) error { return JSON(w, map[string]string{"a": "b"}) }},
		{"CaptureText", func(w io.Writer) error {
			return CaptureText(w, &capture.Result{Sections: []capture.SectionResult{{Collector: "system", Status: capture.StatusOK}}})
		}},
		{"DiffText empty", func(w io.Writer) error { return DiffText(w, noDiff) }},
		{"DiffText aligned", func(w io.Writer) error { return DiffText(w, withDiff) }},
		{"SnapshotList", func(w io.Writer) error { return SnapshotList(w, []string{"local"}) }},
		{"SnapshotJSON", func(w io.Writer) error { return SnapshotJSON(w, snap) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run(errWriter{err: sentinel})
			if err == nil {
				t.Fatalf("%s returned nil error for a failing writer", tt.name)
			}
			if !errors.Is(err, sentinel) {
				t.Errorf("%s returned %v, want it to wrap sentinel %v", tt.name, err, sentinel)
			}
		})
	}
}
