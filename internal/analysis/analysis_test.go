package analysis

import (
	"encoding/json"
	"path/filepath"
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

// homeDirFixture and under build the test's paths with the separator of the
// platform the tests run on. redactHome compares against filepath.Separator, so
// a hardcoded "/Users/ada" tested nothing on Windows except that the fixture was
// written on a Unix machine.
var homeDirFixture = filepath.Join(string(filepath.Separator)+"Users", "ada")

func under(root string, parts ...string) string {
	return filepath.Join(append([]string{root}, parts...)...)
}

func TestRedactHome(t *testing.T) {
	home := homeDirFixture
	elsewhere := filepath.Join(string(filepath.Separator)+"usr", "local", "bin", "node")
	// A path that merely starts with the same characters is a different
	// directory: /Users/adamant is not inside /Users/ada.
	sibling := under(filepath.Join(string(filepath.Separator)+"Users", "adamant"), "bin", "go")

	cases := []struct {
		path, home, want string
	}{
		{under(home, ".local", "bin", "node"), home, "~" + string(filepath.Separator) + filepath.Join(".local", "bin", "node")},
		{home, home, "~"},
		{elsewhere, home, elsewhere},
		{sibling, home, sibling},
		{"", home, ""},
		{filepath.Join(string(filepath.Separator)+"usr", "bin", "go"), "", filepath.Join(string(filepath.Separator)+"usr", "bin", "go")},
	}
	for _, tc := range cases {
		if got := redactHome(tc.path, tc.home); got != tc.want {
			t.Errorf("redactHome(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
		}
	}
}

func TestBuildRedactsRuntimePathsWithoutTouchingTheSnapshot(t *testing.T) {
	pinHome(t, homeDirFixture)
	installed := under(homeDirFixture, ".nvm", "bin", "node")
	local := &snapshot.Snapshot{
		Name:     "local",
		Runtimes: []snapshot.Runtime{{Name: "node", Version: "24.4.0", Path: installed}},
	}

	in := Build(local, &snapshot.Snapshot{Name: "ci"}, nil, nil, nil)

	want := "~" + string(filepath.Separator) + filepath.Join(".nvm", "bin", "node")
	if got := in.A.Runtimes[0].Path; got != want {
		t.Errorf("path in input = %q, want %q", got, want)
	}
	// The caller is still printing its own report from this snapshot.
	if got := local.Runtimes[0].Path; got != installed {
		t.Errorf("Build modified the caller's snapshot: path = %q", got)
	}
}

func TestBuildCarriesOnlyTheEnvironmentNamesTheDiagnosisMentions(t *testing.T) {
	pinHome(t, homeDirFixture)
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
	pinHome(t, homeDirFixture)
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
	pinHome(t, homeDirFixture)
	in := Build(nil, nil, nil, nil, nil)
	if in.A == nil || in.B == nil {
		t.Fatal("Build returned a nil environment; every renderer would have to guard it")
	}
}

// A value can only escape through a field that can hold one. Serializing the
// whole document and looking for a value that was never captured is the check
// that keeps holding when someone adds a section to the snapshot.
func TestInputCannotCarryAnEnvironmentValue(t *testing.T) {
	pinHome(t, homeDirFixture)
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
	pinHome(t, homeDirFixture)
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
