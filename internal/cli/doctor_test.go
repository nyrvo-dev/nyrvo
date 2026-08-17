package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// The first thing a new user types is `nyrvo doctor`, before capturing
// anything. "snapshot not found" alone would leave them guessing, so the error
// has to name the command that fixes it.
func TestDoctorMissingSnapshotsExplainHowToCreateThem(t *testing.T) {
	chdirWorkDir(t)

	code, _, errOut := run(t, "doctor")
	if code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(errOut, "nyrvo capture local") {
		t.Errorf("stderr should tell the user how to capture this machine, got: %s", errOut)
	}

	mustCapture(t, "local")
	code, _, errOut = run(t, "doctor")
	if code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(errOut, "nyrvo ci capture") {
		t.Errorf("stderr should tell the user how to capture a CI job, got: %s", errOut)
	}

	// A name the user chose themselves gets no invented advice.
	if _, _, errOut := run(t, "doctor", "local", "staging"); strings.Contains(errOut, "nyrvo ci capture") {
		t.Errorf("a user-chosen name should not suggest the ci workflow, got: %s", errOut)
	}
}

// Diagnosing a machine against itself must be quiet, and must say that no rule
// matched rather than claiming the environments are equivalent — Nyrvo's rule
// set is small and the output must not imply coverage it does not have.
func TestDoctorCleanRunIsHonest(t *testing.T) {
	chdirWorkDir(t)
	mustCapture(t, "local")
	mustCapture(t, "other")

	code, stdout, errOut := run(t, "doctor", "local", "other")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitOK, errOut)
	}
	if !strings.Contains(strings.ToLower(stdout), "rule") {
		t.Errorf("a clean run must say no rule matched, got:\n%s", stdout)
	}
}

// Findings are an answer, not a failure: a diagnosis that found something must
// still exit 0, or every CI job running doctor would go red on a low-severity
// note.
func TestDoctorExitsZeroWithFindings(t *testing.T) {
	chdirWorkDir(t)
	// Two captures of one machine are identical, so the old version of this test
	// had no findings to exit zero despite — it asserted its own name and proved
	// nothing. These two differ by construction.
	writeSnapshotFile(t, "here", &snapshot.System{OS: "linux", Arch: "amd64"})
	writeSnapshotFile(t, "there", &snapshot.System{OS: "darwin", Arch: "amd64"})

	code, stdout, errOut := run(t, "doctor", "here", "there")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitOK, errOut)
	}
	if !strings.Contains(stdout, "finding") {
		t.Fatalf("the test needs a diagnosis with findings to be about anything:\n%s", stdout)
	}
}

func TestDoctorJSON(t *testing.T) {
	chdirWorkDir(t)
	mustCapture(t, "local")
	mustCapture(t, "other")

	code, stdout, errOut := run(t, "doctor", "local", "other", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitOK, errOut)
	}
	var res struct {
		A        string `json:"a"`
		B        string `json:"b"`
		Findings []struct {
			Rule     string `json:"rule"`
			Severity string `json:"severity"`
		} `json:"findings"`
		Summary map[string]int `json:"summary"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout)
	}
	if res.A != "local" || res.B != "other" {
		t.Errorf("a/b = %q/%q, want local/other", res.A, res.B)
	}
	// All three severities are always present so a consumer never has to
	// distinguish "zero" from "absent".
	for _, sev := range []string{"high", "medium", "low"} {
		if _, ok := res.Summary[sev]; !ok {
			t.Errorf("summary is missing the %q key: %v", sev, res.Summary)
		}
	}
}

func TestDoctorUsageErrors(t *testing.T) {
	chdirWorkDir(t)
	mustCapture(t, "local")

	tests := []struct {
		name string
		args []string
	}{
		{"one name", []string{"doctor", "local"}},
		{"three names", []string{"doctor", "a", "b", "c"}},
		{"unknown flag", []string{"doctor", "--nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _, _ := run(t, tt.args...); code != ExitUsage {
				t.Errorf("exit = %d, want %d", code, ExitUsage)
			}
		})
	}
}

// readLogFixture loads one recorded job log. Like readFixture, the path is
// relative to the package directory, so it must be called before t.Chdir.
func readLogFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "ci", "githubactions", "testdata", "logs", name))
	if err != nil {
		t.Fatalf("read log fixture %s: %v", name, err)
	}
	return data
}

// `nyrvo doctor <run>` is the whole point of the run-import work: one command
// from a run reference to a diagnosis, with no snapshots to manage first.
func TestDoctorImportsRunAndDiagnoses(t *testing.T) {
	stub := &ciExecStub{
		runDoc:  readFixture(t, "run-failed.json"),
		jobsDoc: readFixture(t, "jobs-failed.json"),
		logDoc:  readLogFixture(t, "log-failure.txt"),
	}
	stubCIClient(t, stub)
	chdirWorkDir(t)

	code, stdout, errOut := run(t, "doctor", "31921289286")
	if code != ExitOK {
		t.Fatalf("doctor <run>: exit %d, stderr: %s", code, errOut)
	}

	// The question being asked is "why did this fail?", so the failing step has
	// to appear. A report that listed only platform differences would be
	// technically correct and useless.
	if !strings.Contains(stdout, "Create prompt with built-in context") {
		t.Errorf("diagnosis does not name the failing step:\n%s", stdout)
	}
	if !strings.Contains(stdout, "WHAT THE EVIDENCE REPORTS") {
		t.Errorf("diagnosis has no evidence section:\n%s", stdout)
	}
	// Which job was chosen, and why, belongs on stderr so --json stays clean.
	if !strings.Contains(errOut, "activation") {
		t.Errorf("stderr should name the job being diagnosed, got: %s", errOut)
	}

	// A one-shot question must not overwrite snapshots the user captured
	// deliberately.
	if _, err := os.Stat(filepath.Join(".nyrvo", "snapshots", "ci.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("doctor <run> wrote a snapshot; it must diagnose without saving (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(".nyrvo", "snapshots", "local.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("doctor <run> wrote a local snapshot; it must capture in memory")
	}
}

// The evidence a run reports must reach the JSON form too, or a machine
// consumer would see two low-severity findings and no failure.
func TestDoctorRunJSONCarriesContext(t *testing.T) {
	stub := &ciExecStub{
		runDoc:  readFixture(t, "run-failed.json"),
		jobsDoc: readFixture(t, "jobs-failed.json"),
		logDoc:  readLogFixture(t, "log-failure.txt"),
	}
	stubCIClient(t, stub)
	chdirWorkDir(t)

	code, stdout, errOut := run(t, "doctor", "31921289286", "--json")
	if code != ExitOK {
		t.Fatalf("doctor --json: exit %d, stderr: %s", code, errOut)
	}
	var res struct {
		Context []string `json:"context"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout)
	}
	if len(res.Context) == 0 {
		t.Fatal("context is empty; the run's own notes must reach the JSON report")
	}
	joined := strings.Join(res.Context, "\n")
	if !strings.Contains(joined, "Create prompt with built-in context") {
		t.Errorf("context does not name the failing step: %v", res.Context)
	}
	// The log fixture really does echo environment values; none may survive
	// into a report (docs/adr/0011).
	for _, secret := range []string{"sergiou87", "GH_AW_GITHUB_ACTOR", "/home/runner/work/cli/cli"} {
		if strings.Contains(stdout, secret) {
			t.Errorf("doctor output leaked %q from the job log", secret)
		}
	}
}

// A single operand that is not a run reference is a mistyped command, not a
// failed fetch: it must be answered with usage.
func TestDoctorRejectsNonRunOperand(t *testing.T) {
	chdirWorkDir(t)
	for _, arg := range []string{"local", "staging", "not-a-run"} {
		if code, _, _ := run(t, "doctor", arg); code != ExitUsage {
			t.Errorf("doctor %q: exit = %d, want %d", arg, code, ExitUsage)
		}
	}
}

// Naming a run's job is a supported idea but not a supported form here, so the
// error teaches the two-step route instead of reporting a run URL as an invalid
// snapshot name.
func TestDoctorRunPlusJobExplainsTheTwoStepRoute(t *testing.T) {
	chdirWorkDir(t)
	code, _, errOut := run(t, "doctor", "https://github.com/cli/cli/actions/runs/1", "some job")
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errOut, "nyrvo ci import") {
		t.Errorf("stderr should point at ci import, got: %s", errOut)
	}
	if strings.Contains(errOut, "invalid snapshot name") {
		t.Errorf("a run URL must not be reported as a snapshot name: %s", errOut)
	}
}

// --fail-on is how a CI job opts into a non-zero exit. Without it a diagnosis
// never changes the exit code, because drift is an answer and a low-severity
// note should not turn a pipeline red by default.
func TestDoctorFailOn(t *testing.T) {
	chdirWorkDir(t)
	// Two snapshots that differ in exactly one known way, rather than a capture
	// of whatever machine runs the tests.
	//
	// An earlier version diagnosed a fixture run against a live capture and
	// relied on "this yields low findings only". That held on a laptop, where
	// darwin/arm64 disagrees with the fixture's linux/amd64, and produced no
	// findings at all on the Linux runner, where they match — so --fail-on=low
	// exited 0 and the suite failed on CI while passing locally. A test about
	// drift must not itself depend on the platform it runs on.
	writeSnapshotFile(t, "here", &snapshot.System{OS: "linux", Arch: "amd64"})
	writeSnapshotFile(t, "there", &snapshot.System{OS: "darwin", Arch: "amd64"})

	// A diagnosis is an answer, so by default it never changes the exit code.
	if code, _, _ := run(t, "doctor", "here", "there"); code != ExitOK {
		t.Fatalf("without --fail-on: exit = %d, want %d", code, ExitOK)
	}
	if code, _, errOut := run(t, "doctor", "here", "there", "--fail-on=low"); code != ExitError {
		t.Errorf("--fail-on=low with low findings: exit = %d, want %d (stderr: %s)", code, ExitError, errOut)
	}
	// A threshold nothing reaches must not fail the command.
	if code, _, errOut := run(t, "doctor", "here", "there", "--fail-on=high"); code != ExitOK {
		t.Errorf("--fail-on=high with only low findings: exit = %d, want %d (stderr: %s)", code, ExitOK, errOut)
	}

	// The report still has to be produced: the exit code is a signal about the
	// diagnosis, not a replacement for it.
	code, stdout, errOut := run(t, "doctor", "here", "there", "--fail-on=low")
	if code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(stdout, "NYRVO DOCTOR") {
		t.Error("the diagnosis must still be written when the threshold trips")
	}
	if !strings.Contains(errOut, "--fail-on threshold") {
		t.Errorf("stderr should say why the exit code is non-zero, got: %s", errOut)
	}
}

// writeSnapshotFile stores a snapshot whose only observation is a platform, so
// a test can state exactly which findings it expects.
func writeSnapshotFile(t *testing.T, name string, system *snapshot.System) {
	t.Helper()
	snap := snapshot.New(name, time.Time{})
	snap.Source = &snapshot.Source{Kind: snapshot.SourceLocal}
	snap.System = system
	snap.Normalize()
	if err := snapshot.NewStore("").Save(snap); err != nil {
		t.Fatalf("save snapshot %s: %v", name, err)
	}
}

func TestDoctorFailOnUsage(t *testing.T) {
	chdirWorkDir(t)
	mustCapture(t, "local")
	mustCapture(t, "other")

	// An unknown severity is a mistyped command, not a diagnosis.
	if code, _, _ := run(t, "doctor", "local", "other", "--fail-on=critical"); code != ExitUsage {
		t.Error("--fail-on=critical should be a usage error")
	}
	// Nyrvo's flag splitting assumes boolean flags, so the separated form must
	// fail loudly and say how to write it instead of eating the value.
	code, _, errOut := run(t, "doctor", "local", "other", "--fail-on", "high")
	if code != ExitUsage {
		t.Fatalf("separated --fail-on: exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errOut, "--fail-on=high") {
		t.Errorf("the error should show the --flag=value form, got: %s", errOut)
	}
}
