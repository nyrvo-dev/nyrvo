package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/nyrvo-dev/nyrvo/internal/ci/githubactions"
)

func fullReplayPlan() githubactions.ReplayPlan {
	return githubactions.ReplayPlan{
		Job: "build",
		Prerequisites: []string{
			"steps run inside container node:24, not on the host",
			`postgres:15 must be reachable as "postgres"`,
		},
		Steps: []githubactions.ReplayStep{
			{
				Name:   "Set up Go",
				Action: "actions/setup-go@v5",
				Reason: "actions/setup-go@v5 installs go 1.25; install it yourself",
			},
			{
				Name:             "Run tests",
				Command:          "go test ./...",
				WorkingDirectory: "./src",
				Env:              []string{"CI=true", "GOFLAGS=-mod=readonly"},
			},
			{
				Name:    "Say version",
				Command: "node --version ${{ matrix.node }}",
				Reason:  "command contains ${{ matrix.node }}, whose value is unknown outside the runner",
			},
		},
		Notes: []string{`job "build": if conditions are not modelled`},
	}
}

// TestReplayTextLayout locks in the exact rendered bytes for one fully
// populated plan: prerequisites before steps, numbered steps with the command
// indented under a run label, the step labels aligned under one column, and a
// step that cannot be reproduced marked with its reason.
func TestReplayTextLayout(t *testing.T) {
	want := `REPLAY build

PREREQUISITES
  steps run inside container node:24, not on the host
  postgres:15 must be reachable as "postgres"

STEPS

1. Set up Go
   uses              actions/setup-go@v5
   not reproducible  actions/setup-go@v5 installs go 1.25; install it yourself

2. Run tests
   run
     go test ./...
   in   ./src
   env  CI=true
   env  GOFLAGS=-mod=readonly

3. Say version
   run
     node --version ${{ matrix.node }}
   not reproducible  command contains ${{ matrix.node }}, whose value is unknown outside the runner

NOTES
  - job "build": if conditions are not modelled
`
	if got := render(t, func(w io.Writer) error { return ReplayText(w, fullReplayPlan()) }); got != want {
		t.Errorf("ReplayText layout\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// TestReplayTextEmptyPlan locks in the honest no-work message: a plan with
// nothing in it must say so, never print a blank screen.
func TestReplayTextEmptyPlan(t *testing.T) {
	got := render(t, func(w io.Writer) error { return ReplayText(w, githubactions.ReplayPlan{}) })
	want := "Nothing to reproduce.\n"
	if got != want {
		t.Errorf("ReplayText empty plan\ngot:  %q\nwant: %q", got, want)
	}
}

// A job that exists but declares no steps still gets a sentence, not silence.
func TestReplayTextJobWithoutSteps(t *testing.T) {
	got := render(t, func(w io.Writer) error {
		return ReplayText(w, githubactions.ReplayPlan{Job: "test"})
	})
	want := "REPLAY test\n\nThis job declares no steps, so there is nothing to reproduce locally.\n"
	if got != want {
		t.Errorf("ReplayText job without steps\ngot:  %q\nwant: %q", got, want)
	}
}

// TestReplayJSONRoundTrip asserts the plan document is the plan itself: it
// round-trips through encoding/json to an identical value, and the snake_case
// tags the domain structs carry are the ones the wire uses.
func TestReplayJSONRoundTrip(t *testing.T) {
	plan := fullReplayPlan()
	var buf bytes.Buffer
	if err := ReplayJSON(&buf, plan); err != nil {
		t.Fatalf("ReplayJSON: %v", err)
	}
	out := buf.String()

	if !strings.HasSuffix(out, "\n") {
		t.Errorf("ReplayJSON output not newline-terminated: %q", out)
	}
	for _, field := range []string{`"job"`, `"steps"`, `"working_directory"`, `"prerequisites"`, `"reason"`} {
		if !strings.Contains(out, field) {
			t.Errorf("ReplayJSON output missing %s: %s", field, out)
		}
	}

	var got githubactions.ReplayPlan
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, plan) {
		t.Errorf("ReplayJSON round-trip mismatch:\ngot:  %+v\nwant: %+v", got, plan)
	}
}

// A broken writer must surface its error from both replay renderers, matching
// the rest of the output package.
func TestReplayRenderersPropagateWriterError(t *testing.T) {
	sentinel := errors.New("disk full")
	tests := []struct {
		name string
		run  func(w io.Writer) error
	}{
		{"ReplayText full", func(w io.Writer) error { return ReplayText(w, fullReplayPlan()) }},
		{"ReplayText empty", func(w io.Writer) error { return ReplayText(w, githubactions.ReplayPlan{}) }},
		{"ReplayJSON", func(w io.Writer) error { return ReplayJSON(w, fullReplayPlan()) }},
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

// TestReplayTextBlockCommand covers a `run: |` body, which carries its trailing
// newline from the file: printing it naively ends the step with a line of bare
// indentation that looks like a missing command.
func TestReplayTextBlockCommand(t *testing.T) {
	var buf bytes.Buffer
	plan := githubactions.ReplayPlan{
		Job: "build",
		Steps: []githubactions.ReplayStep{
			{Name: "Test", Command: "go build ./...\ngo test ./...\n"},
		},
	}
	if err := ReplayText(&buf, plan); err != nil {
		t.Fatalf("ReplayText() error = %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "     go build ./...\n     go test ./...\n") {
		t.Errorf("both command lines are not indented as written:\n%s", got)
	}
	if strings.Contains(got, "     \n") {
		t.Errorf("output has a line of bare indentation:\n%q", got)
	}
}
