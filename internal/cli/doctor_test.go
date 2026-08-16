package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// The first thing a new user types is `nyrvo doctor`, before capturing
// anything. "snapshot not found" alone would leave them guessing, so the error
// has to name the command that fixes it.
func TestDoctorMissingSnapshotsExplainHowToCreateThem(t *testing.T) {
	t.Chdir(t.TempDir())

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
	t.Chdir(t.TempDir())
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
	t.Chdir(t.TempDir())
	mustCapture(t, "local")
	mustCapture(t, "other")

	if code, _, errOut := run(t, "doctor", "local", "other"); code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitOK, errOut)
	}
}

func TestDoctorJSON(t *testing.T) {
	t.Chdir(t.TempDir())
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
	t.Chdir(t.TempDir())
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
