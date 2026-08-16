package analysis

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

func pinHome(t *testing.T, home string) {
	t.Helper()
	original := homeDir
	homeDir = func() string { return home }
	t.Cleanup(func() { homeDir = original })
}

func TestRedactHome(t *testing.T) {
	cases := []struct {
		path, home, want string
	}{
		{"/Users/ada/.local/bin/node", "/Users/ada", "~/.local/bin/node"},
		{"/Users/ada", "/Users/ada", "~"},
		{"/usr/local/bin/node", "/Users/ada", "/usr/local/bin/node"},
		// A path that merely starts with the same characters is a different
		// directory: /Users/adam is not inside /Users/ada.
		{"/Users/adamant/bin/go", "/Users/ada", "/Users/adamant/bin/go"},
		{"", "/Users/ada", ""},
		{"/usr/bin/go", "", "/usr/bin/go"},
	}
	for _, tc := range cases {
		if got := redactHome(tc.path, tc.home); got != tc.want {
			t.Errorf("redactHome(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
		}
	}
}

func TestBuildRedactsRuntimePathsWithoutTouchingTheSnapshot(t *testing.T) {
	pinHome(t, "/Users/ada")
	local := &snapshot.Snapshot{
		Name:     "local",
		Runtimes: []snapshot.Runtime{{Name: "node", Version: "24.4.0", Path: "/Users/ada/.nvm/bin/node"}},
	}

	in := Build(local, &snapshot.Snapshot{Name: "ci"}, nil, nil, nil)

	if got := in.A.Runtimes[0].Path; got != "~/.nvm/bin/node" {
		t.Errorf("path in input = %q, want the home directory redacted", got)
	}
	// The caller is still printing its own report from this snapshot.
	if got := local.Runtimes[0].Path; got != "/Users/ada/.nvm/bin/node" {
		t.Errorf("Build modified the caller's snapshot: path = %q", got)
	}
}

func TestBuildCarriesOnlyTheEnvironmentNamesTheDiagnosisMentions(t *testing.T) {
	pinHome(t, "/Users/ada")
	local := &snapshot.Snapshot{
		Name: "local",
		Environment: &snapshot.Environment{Names: []string{
			"AWS_PROFILE", "DATABASE_URL", "HOME", "REDIS_URL",
		}},
	}
	ci := &snapshot.Snapshot{Name: "ci", Environment: &snapshot.Environment{Names: []string{"HOME"}, Partial: true}}

	d := &diff.Result{
		Differences: []diff.Difference{
			{Component: diff.ComponentEnvironment, Key: "REDIS_URL", Kind: diff.KindOnlyInA, A: "present"},
		},
		PartialEnvironment: true,
	}
	findings := []finding.Finding{
		{Rule: finding.EnvMissing, Component: diff.ComponentEnvironment, Key: "DATABASE_URL", Severity: finding.SeverityMedium},
	}

	in := Build(local, ci, d, findings, nil)

	got := strings.Join(in.A.Environment.Names, ",")
	if got != "DATABASE_URL,REDIS_URL" {
		t.Errorf("names = %q, want only the ones a difference or a finding names", got)
	}
	if !in.PartialEnvironment {
		t.Error("PartialEnvironment was not carried; an agent would read silence as absence")
	}
}

func TestBuildDropsCaptureTime(t *testing.T) {
	pinHome(t, "/Users/ada")
	at := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	in := Build(
		&snapshot.Snapshot{Name: "local", CreatedAt: at},
		&snapshot.Snapshot{Name: "ci", CreatedAt: at},
		nil, nil, nil,
	)
	if !in.A.CreatedAt.IsZero() || !in.B.CreatedAt.IsZero() {
		t.Error("capture time survived into the input; two runs over the same evidence would differ")
	}
}

func TestBuildToleratesNilSnapshots(t *testing.T) {
	pinHome(t, "/Users/ada")
	in := Build(nil, nil, nil, nil, nil)
	if in.A == nil || in.B == nil {
		t.Fatal("Build returned a nil environment; every renderer would have to guard it")
	}
}

// A value can only escape through a field that can hold one. Serializing the
// whole document and looking for a value that was never captured is the check
// that keeps holding when someone adds a section to the snapshot.
func TestInputCannotCarryAnEnvironmentValue(t *testing.T) {
	pinHome(t, "/Users/ada")
	local := &snapshot.Snapshot{
		Name:        "local",
		Environment: &snapshot.Environment{Names: []string{"DATABASE_URL"}},
	}
	d := &diff.Result{Differences: []diff.Difference{
		{Component: diff.ComponentEnvironment, Key: "DATABASE_URL", Kind: diff.KindOnlyInA, A: "present"},
	}}

	doc, err := json.Marshal(Build(local, &snapshot.Snapshot{Name: "ci"}, d, nil, nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(doc), "postgres://") {
		t.Fatalf("input carried an environment value: %s", doc)
	}
	if !strings.Contains(string(doc), "DATABASE_URL") {
		t.Fatalf("input dropped the name it needed to keep: %s", doc)
	}
}

func TestBuildCopiesEvidenceRatherThanAliasingIt(t *testing.T) {
	pinHome(t, "/Users/ada")
	d := &diff.Result{Differences: []diff.Difference{
		{Component: diff.ComponentRuntime, Key: "node", Kind: diff.KindChanged, A: "24.4.0", B: "22.0.0"},
	}}
	findings := []finding.Finding{{Rule: finding.RuntimeVersionMismatch, Component: "runtime", Key: "node"}}

	in := Build(&snapshot.Snapshot{Name: "local"}, &snapshot.Snapshot{Name: "ci"}, d, findings, []string{"note"})

	in.Differences[0].A = "mutated"
	in.Findings[0].Rule = "mutated"
	in.Notes[0] = "mutated"
	if d.Differences[0].A != "24.4.0" || findings[0].Rule != finding.RuntimeVersionMismatch {
		t.Error("Build aliased the caller's evidence")
	}
}
