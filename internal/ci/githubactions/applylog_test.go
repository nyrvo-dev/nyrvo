package githubactions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

func parseFixtureLog(t *testing.T, name string) *JobLog {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "logs", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return ParseJobLog(raw)
}

// A run's metadata reports no runtime versions at all. The log is the only
// place they exist, so applying it is what closes the gap the import otherwise
// has to admit in a note.
func TestApplyJobLogAddsObservedRuntimes(t *testing.T) {
	snap := snapshot.New("ci", time.Now())
	snap.Source = &snapshot.Source{Kind: snapshot.SourceGitHubActionsRun}

	ApplyJobLog(snap, parseFixtureLog(t, "log-go.txt"))

	rt := snap.Runtime("go")
	if rt == nil {
		t.Fatalf("go runtime not applied; runtimes = %+v", snap.Runtimes)
	}
	if rt.Version != "1.26.6" {
		t.Errorf("go version = %q, want 1.26.6", rt.Version)
	}
}

// The image names the platform even when the runner label could not: a
// self-hosted label tells Nyrvo nothing, but the log observed the OS.
func TestApplyJobLogFillsSystemOnlyWhenUnknown(t *testing.T) {
	jl := parseFixtureLog(t, "log-go.txt")
	if jl.Image.OS == "" {
		t.Fatal("fixture no longer carries a VM image; the test cannot check the fallback")
	}

	unknown := snapshot.New("ci", time.Now())
	unknown.Source = &snapshot.Source{Kind: snapshot.SourceGitHubActionsRun}
	ApplyJobLog(unknown, jl)
	if unknown.System == nil || unknown.System.OS != jl.Image.OS {
		t.Errorf("System = %+v, want it filled from the image", unknown.System)
	}

	// A platform already derived from a known hosted runner label is an
	// observation of its own and is not second-guessed.
	known := snapshot.New("ci", time.Now())
	known.Source = &snapshot.Source{Kind: snapshot.SourceGitHubActionsRun}
	known.System = &snapshot.System{OS: "darwin", Arch: "arm64"}
	ApplyJobLog(known, jl)
	if known.System.OS != "darwin" || known.System.Arch != "arm64" {
		t.Errorf("System = %+v, want the existing observation kept", known.System)
	}
}

// The failure excerpt is the reason to read a log at all, and it must arrive
// without the environment values the runner echoes before each step.
func TestApplyJobLogRecordsFailureWithoutEnvValues(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "logs", "log-failure.txt"))
	if err != nil {
		t.Fatal(err)
	}
	snap := snapshot.New("ci", time.Now())
	snap.Source = &snapshot.Source{Kind: snapshot.SourceGitHubActionsRun}

	ApplyJobLog(snap, ParseJobLog(raw))

	notes := strings.Join(snap.Source.Notes, "\n")
	if !strings.Contains(notes, "exit code 127") {
		t.Errorf("notes should carry the failure:\n%s", notes)
	}
	// The fixture's step configuration block echoes this value; it must not
	// reach a snapshot (docs/adr/0003, docs/adr/0011).
	for _, secret := range []string{"sergiou87", "GH_AW_GITHUB_ACTOR"} {
		if strings.Contains(notes, secret) {
			t.Errorf("notes leaked %q from the log's env echo:\n%s", secret, notes)
		}
	}
	if strings.Contains(notes, "\x1b") {
		t.Error("notes carry an ANSI escape sequence")
	}
}

func TestApplyJobLogNilInputs(t *testing.T) {
	ApplyJobLog(nil, &JobLog{})
	snap := snapshot.New("ci", time.Now())
	ApplyJobLog(snap, nil)
	if snap.Source != nil {
		t.Errorf("a nil log must change nothing, got %+v", snap.Source)
	}
}
