package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nodeWorkflow is a small realistic workflow whose single job runs on a known
// GitHub-hosted runner and pins a concrete Node version, so both the runner
// mapping and the runtime detection have something to report.
const nodeWorkflow = `name: CI
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version: 20
`

// writeWorkflow drops a workflow file into .github/workflows under the current
// directory, the layout ciJobs() expects.
func writeWorkflow(t *testing.T, name, content string) {
	t.Helper()
	dir := filepath.Join(".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// A repository without a workflow directory is a normal state: inspect must
// say so on stdout and still succeed.
func TestCIInspectNoWorkflows(t *testing.T) {
	chdirWorkDir(t)

	code, stdout, errOut := run(t, "ci", "inspect")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitOK, errOut)
	}
	if !strings.Contains(stdout, "No workflow jobs found") {
		t.Errorf("stdout = %q, want the no-workflow line", stdout)
	}
}

// inspect must surface the job id, the platform the runner label maps to, and
// the version the setup action pins.
func TestCIInspectJob(t *testing.T) {
	chdirWorkDir(t)
	writeWorkflow(t, "ci.yml", nodeWorkflow)

	code, stdout, errOut := run(t, "ci", "inspect")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitOK, errOut)
	}
	for _, want := range []string{"test", "linux/amd64", "node 20"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

// The --json form must emit a parseable array whose workflow, job and snapshot
// fields are populated, so a downstream tool can consume CI declarations.
func TestCIInspectJSON(t *testing.T) {
	chdirWorkDir(t)
	writeWorkflow(t, "ci.yml", nodeWorkflow)

	code, stdout, errOut := run(t, "ci", "inspect", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitOK, errOut)
	}

	var jobs []struct {
		Workflow string `json:"workflow"`
		Job      string `json:"job"`
		Snapshot struct {
			Name   string `json:"name"`
			Source struct {
				Kind string `json:"kind"`
			} `json:"source"`
			System *struct {
				OS   string `json:"os"`
				Arch string `json:"arch"`
			} `json:"system"`
			Environment *struct {
				Names   []string `json:"names"`
				Partial bool     `json:"partial"`
			} `json:"environment"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(stdout), &jobs); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", err, stdout)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if jobs[0].Workflow != "ci.yml" || jobs[0].Job != "test" {
		t.Errorf("job = %s:%s, want ci.yml:test", jobs[0].Workflow, jobs[0].Job)
	}
	if jobs[0].Snapshot.Name != "ci" {
		t.Errorf("snapshot name = %q, want ci", jobs[0].Snapshot.Name)
	}
	if jobs[0].Snapshot.Source.Kind != "github-actions" {
		t.Errorf("source kind = %q, want github-actions", jobs[0].Snapshot.Source.Kind)
	}
	if jobs[0].Snapshot.System == nil || jobs[0].Snapshot.System.OS != "linux" {
		t.Errorf("system not populated: %+v", jobs[0].Snapshot.System)
	}
	if jobs[0].Snapshot.Environment == nil || !jobs[0].Snapshot.Environment.Partial {
		t.Error("environment should be present and marked partial")
	}
}

// Every malformed CI command line must be a usage error (exit 2), never an
// operational one.
func TestCIUsageErrors(t *testing.T) {
	chdirWorkDir(t)
	writeWorkflow(t, "ci.yml", nodeWorkflow)

	tests := []struct {
		name string
		args []string
	}{
		{"inspect extra operand", []string{"ci", "inspect", "extra"}},
		{"no subcommand", []string{"ci"}},
		{"unknown subcommand", []string{"ci", "bogus"}},
		{"capture no selector", []string{"ci", "capture"}},
		{"capture two selectors", []string{"ci", "capture", "a", "b"}},
		{"capture with a flag", []string{"ci", "capture", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _, _ := run(t, tt.args...); code != ExitUsage {
				t.Errorf("exit = %d, want %d", code, ExitUsage)
			}
		})
	}
}

// Capturing a CI job must write the documented snapshot name so the advertised
// `nyrvo diff local ci` works, and the store must list it.
func TestCICaptureSavesSnapshot(t *testing.T) {
	chdirWorkDir(t)
	writeWorkflow(t, "ci.yml", nodeWorkflow)

	if code, _, errOut := run(t, "ci", "capture", "test"); code != ExitOK {
		t.Fatalf("ci capture test: exit %d, stderr: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(".nyrvo", "snapshots", "ci.json")); err != nil {
		t.Fatalf("snapshot file not written: %v", err)
	}

	code, stdout, errOut := run(t, "list")
	if code != ExitOK {
		t.Fatalf("list: exit %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(stdout, "ci") {
		t.Errorf("list output = %q, want it to report ci", stdout)
	}
}

// A selector that matches nothing is an operational failure, and the message
// must point at the selectors that do exist.
func TestCICaptureUnknownJob(t *testing.T) {
	chdirWorkDir(t)
	writeWorkflow(t, "ci.yml", nodeWorkflow)

	code, _, errOut := run(t, "ci", "capture", "nope")
	if code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if !strings.Contains(errOut, "available") || !strings.Contains(errOut, "ci.yml:test") {
		t.Errorf("stderr should list the available selectors, got: %s", errOut)
	}
}

// A bare job id shared by two workflows must be rejected with both qualified
// selectors named, and the qualified form must then resolve unambiguously.
func TestCICaptureAmbiguousJob(t *testing.T) {
	chdirWorkDir(t)
	writeWorkflow(t, "a.yml", nodeWorkflow)
	writeWorkflow(t, "b.yml", nodeWorkflow)

	code, _, errOut := run(t, "ci", "capture", "test")
	if code != ExitUsage {
		t.Fatalf("bare ambiguous id: exit = %d, want %d (stderr: %s)", code, ExitUsage, errOut)
	}
	if !strings.Contains(errOut, "a.yml:test") || !strings.Contains(errOut, "b.yml:test") {
		t.Errorf("stderr should name both selectors, got: %s", errOut)
	}

	if code, _, errOut := run(t, "ci", "capture", "a.yml:test"); code != ExitOK {
		t.Fatalf("qualified selector: exit %d, stderr: %s", code, errOut)
	}
}

// The documented workflow end to end: capture CI as "ci", the machine as
// "local", then diff. The JSON must carry partial_environment because a
// workflow file states only the variables it sets — it cannot testify to
// absence.
func TestCICaptureDiffRoundTrip(t *testing.T) {
	chdirWorkDir(t)
	writeWorkflow(t, "ci.yml", nodeWorkflow)

	if code, _, errOut := run(t, "ci", "capture", "test"); code != ExitOK {
		t.Fatalf("ci capture test: exit %d, stderr: %s", code, errOut)
	}
	if code, _, errOut := run(t, "capture", "local"); code != ExitOK {
		t.Fatalf("capture local: exit %d, stderr: %s", code, errOut)
	}

	code, stdout, errOut := run(t, "diff", "local", "ci", "--json")
	if code != ExitOK {
		t.Fatalf("diff local ci --json: exit %d, stderr: %s", code, errOut)
	}
	var res struct {
		PartialEnvironment bool `json:"partial_environment"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout)
	}
	if !res.PartialEnvironment {
		t.Error("partial_environment = false, want true: a CI-derived environment must not be treated as exhaustive")
	}
}
