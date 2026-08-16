package githubactions

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// readFixture loads one of the recorded API responses from testdata/runs.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "runs", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

func TestParseRunFailed(t *testing.T) {
	run, err := ParseRun(readFixture(t, "run-failed.json"), readFixture(t, "jobs-failed.json"))
	if err != nil {
		t.Fatalf("ParseRun: %v", err)
	}

	if run.ID != 31921289286 {
		t.Errorf("ID = %d, want 31921289286", run.ID)
	}
	if run.Repository != "cli/cli" {
		t.Errorf("Repository = %q, want cli/cli", run.Repository)
	}
	if run.WorkflowPath != ".github/workflows/dependabot-triage.lock.yml" {
		t.Errorf("WorkflowPath = %q", run.WorkflowPath)
	}
	if run.HeadSHA != "0eeec0b92edbe70199f9768522f831d3534f41ad" {
		t.Errorf("HeadSHA = %q", run.HeadSHA)
	}
	if run.HeadBranch != "trunk" {
		t.Errorf("HeadBranch = %q, want trunk", run.HeadBranch)
	}
	if run.Event != "schedule" {
		t.Errorf("Event = %q, want schedule", run.Event)
	}
	if run.Status != "completed" {
		t.Errorf("Status = %q", run.Status)
	}
	if run.Conclusion != "failure" {
		t.Errorf("Conclusion = %q, want failure", run.Conclusion)
	}
	if run.Attempt != 1 {
		t.Errorf("Attempt = %d, want 1", run.Attempt)
	}
	if run.Number != 365 {
		t.Errorf("Number = %d, want 365", run.Number)
	}
	if run.URL != "https://github.com/cli/cli/actions/runs/31921289286" {
		t.Errorf("URL = %q", run.URL)
	}
	if got := run.CreatedAt.UTC().Format(time.RFC3339); got != "2026-08-16T02:10:34Z" {
		t.Errorf("CreatedAt = %q, want 2026-08-16T02:10:34Z", got)
	}

	if len(run.Jobs) != 5 {
		t.Fatalf("jobs = %d, want 5", len(run.Jobs))
	}

	act := run.Job("activation")
	if act == nil {
		t.Fatal("job activation not found")
	}
	if act.Conclusion != "failure" {
		t.Errorf("activation conclusion = %q, want failure", act.Conclusion)
	}
	fs := act.FailedStep()
	if fs == nil {
		t.Fatal("activation has no failed step")
	}
	if fs.Name != "Create prompt with built-in context" {
		t.Errorf("failed step = %q, want Create prompt with built-in context", fs.Name)
	}
	if fs.Number != 13 {
		t.Errorf("failed step number = %d, want 13", fs.Number)
	}

	failed := run.FailedJobs()
	if len(failed) != 1 || failed[0].Name != "activation" {
		t.Errorf("FailedJobs = %+v, want only activation", failed)
	}

	// A skipped job reports a null runner_name; parsing must not fail and the
	// value must come through empty, not as a fabricated string.
	agent := run.Job("agent")
	if agent == nil {
		t.Fatal("job agent not found")
	}
	if agent.RunnerName != "" {
		t.Errorf("skipped job runner_name = %q, want empty", agent.RunnerName)
	}
	if agent.FailedStep() != nil {
		t.Errorf("skipped job reports a failed step: %+v", agent.FailedStep())
	}

	if run.Job("does-not-exist") != nil {
		t.Error("Job() returned a job for an absent name")
	}
}

func TestParseRunMatrix(t *testing.T) {
	run, err := ParseRun(readFixture(t, "run-matrix.json"), readFixture(t, "jobs-matrix.json"))
	if err != nil {
		t.Fatalf("ParseRun: %v", err)
	}
	if run.Conclusion != "success" {
		t.Errorf("Conclusion = %q, want success", run.Conclusion)
	}
	if len(run.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(run.Jobs))
	}
	if run.Jobs[0].Name != "CodeQL-Build (actions, none, security-and-quality)" {
		t.Errorf("job 0 name = %q", run.Jobs[0].Name)
	}
	if run.Jobs[1].Name != "CodeQL-Build (go, manual, ./.github/codeql/codeql-config.yml)" {
		t.Errorf("job 1 name = %q", run.Jobs[1].Name)
	}
	for _, j := range run.Jobs {
		if len(j.Labels) != 1 || j.Labels[0] != "ubuntu-latest" {
			t.Errorf("job %q labels = %v, want [ubuntu-latest]", j.Name, j.Labels)
		}
	}
	if len(run.FailedJobs()) != 0 {
		t.Errorf("FailedJobs = %+v, want none", run.FailedJobs())
	}
}

// jobsPaginated is the same two jobs as jobsMatrix split across two page
// documents, so both code paths must yield identical job lists. A plain
// json.Unmarshal over the concatenated stream would fail here.
func TestParseJobsPaginatedMatchesSinglePage(t *testing.T) {
	one, err := ParseJobs(readFixture(t, "jobs-matrix.json"))
	if err != nil {
		t.Fatalf("ParseJobs single page: %v", err)
	}
	paged, err := ParseJobs(readFixture(t, "jobs-paginated.json"))
	if err != nil {
		t.Fatalf("ParseJobs paginated: %v", err)
	}
	if len(one) != len(paged) {
		t.Fatalf("jobs = %d single, %d paginated, want equal", len(one), len(paged))
	}
	for i := range one {
		if one[i].ID != paged[i].ID || one[i].Name != paged[i].Name {
			t.Errorf("job %d = %d/%q vs %d/%q, want equal", i, one[i].ID, one[i].Name, paged[i].ID, paged[i].Name)
		}
	}

	a, err := ParseRun(readFixture(t, "run-matrix.json"), readFixture(t, "jobs-matrix.json"))
	if err != nil {
		t.Fatalf("ParseRun single: %v", err)
	}
	b, err := ParseRun(readFixture(t, "run-matrix.json"), readFixture(t, "jobs-paginated.json"))
	if err != nil {
		t.Fatalf("ParseRun paginated: %v", err)
	}
	if !slices.Equal(jobIDs(a), jobIDs(b)) {
		t.Errorf("run job ids differ: %v vs %v", jobIDs(a), jobIDs(b))
	}
}

func TestParseJobsDeduplicates(t *testing.T) {
	matrix := readFixture(t, "jobs-matrix.json")
	double := append(append([]byte{}, matrix...), matrix...)
	jobs, err := ParseJobs(double)
	if err != nil {
		t.Fatalf("ParseJobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Errorf("jobs = %d after feeding the same page twice, want 2", len(jobs))
	}
}

func TestParseRunMalformed(t *testing.T) {
	jobs := readFixture(t, "jobs-failed.json")
	if _, err := ParseRun([]byte("{not json"), jobs); err == nil || !strings.Contains(err.Error(), "run") {
		t.Errorf("run error = %v, want one naming the run document", err)
	}
	run := readFixture(t, "run-failed.json")
	if _, err := ParseRun(run, []byte(`{"jobs":[}`)); err == nil || !strings.Contains(err.Error(), "jobs") {
		t.Errorf("jobs error = %v, want one naming the jobs document", err)
	}
}

func TestRunSnapshotFailedJob(t *testing.T) {
	run, err := ParseRun(readFixture(t, "run-failed.json"), readFixture(t, "jobs-failed.json"))
	if err != nil {
		t.Fatalf("ParseRun: %v", err)
	}
	snap, err := RunSnapshot(run, run.Job("activation"), "ci", time.Now())
	if err != nil {
		t.Fatalf("RunSnapshot: %v", err)
	}

	if snap.Source == nil || snap.Source.Kind != snapshot.SourceGitHubActionsRun {
		t.Errorf("source = %+v, want kind github-actions-run", snap.Source)
	}
	act := run.Job("activation")
	if snap.Source.Ref != act.URL {
		t.Errorf("ref = %q, want the job html_url %q", snap.Source.Ref, act.URL)
	}

	if snap.Git == nil || snap.Git.SHA != run.HeadSHA || snap.Git.Branch != "trunk" || snap.Git.Dirty {
		t.Errorf("git = %+v, want sha %s on trunk and clean", snap.Git, run.HeadSHA)
	}
	if snap.Environment == nil || snap.Environment.Partial != true || len(snap.Environment.Names) != 0 {
		t.Errorf("environment = %+v, want present, empty and partial", snap.Environment)
	}
	if len(snap.Runtimes) != 0 {
		t.Errorf("runtimes = %v, want none", snap.Runtimes)
	}

	if !notesMention(snap.Source.Notes, "Create prompt with built-in context") {
		t.Errorf("notes should name the failing step: %v", snap.Source.Notes)
	}
	if !notesMention(snap.Source.Notes, ".github/workflows/dependabot-triage.lock.yml") {
		t.Errorf("notes should name the workflow path: %v", snap.Source.Notes)
	}

	// The brief fixes the order: run conclusion, job conclusion, failing step,
	// workflow path. Lock it in so the notes stay readable as a narrative.
	assertNoteOrder(t, snap.Source.Notes,
		[]string{"The run concluded", "The job ", "failed at step", "came from the workflow"})
}

func TestRunSnapshotUnknownLabel(t *testing.T) {
	// "ubuntu-slim" is a self-hosted label, not a GitHub-hosted runner, so the
	// platform stays unknown and the label is named in the notes. This case is
	// why runner labels are matched exactly instead of by an "ubuntu-" prefix:
	// a prefix rule answers linux/amd64 for a machine nobody identified, and
	// gets ubuntu-24.04-arm wrong outright.
	run, err := ParseRun(readFixture(t, "run-failed.json"), readFixture(t, "jobs-failed.json"))
	if err != nil {
		t.Fatalf("ParseRun: %v", err)
	}
	snap, err := RunSnapshot(run, run.Job("activation"), "ci", time.Now())
	if err != nil {
		t.Fatalf("RunSnapshot: %v", err)
	}
	if snap.System != nil {
		t.Errorf("system = %+v, want nil for the unknown label ubuntu-slim", snap.System)
	}
	if !notesMention(snap.Source.Notes, "ubuntu-slim") {
		t.Errorf("notes should name the unknown label: %v", snap.Source.Notes)
	}
}

func TestRunSnapshotMatrixJob(t *testing.T) {
	run, err := ParseRun(readFixture(t, "run-matrix.json"), readFixture(t, "jobs-matrix.json"))
	if err != nil {
		t.Fatalf("ParseRun: %v", err)
	}
	j := run.Job("CodeQL-Build (go, manual, ./.github/codeql/codeql-config.yml)")
	if j == nil {
		t.Fatal("matrix job not found")
	}
	snap, err := RunSnapshot(run, j, "ci", time.Now())
	if err != nil {
		t.Fatalf("RunSnapshot: %v", err)
	}
	if snap.System == nil || snap.System.OS != "linux" || snap.System.Arch != "amd64" {
		t.Errorf("system = %+v, want linux/amd64 from the resolved label", snap.System)
	}
	if snap.Source.Ref != j.URL {
		t.Errorf("ref = %q, want the job html_url", snap.Source.Ref)
	}
}

func TestRunSnapshotNilInputs(t *testing.T) {
	run, err := ParseRun(readFixture(t, "run-failed.json"), readFixture(t, "jobs-failed.json"))
	if err != nil {
		t.Fatalf("ParseRun: %v", err)
	}
	if _, err := RunSnapshot(nil, nil, "ci", time.Now()); err == nil {
		t.Error("expected an error for nil run and job")
	}
	if _, err := RunSnapshot(run, nil, "ci", time.Now()); err == nil {
		t.Error("expected an error for nil job")
	}
	if _, err := RunSnapshot(nil, &RunJob{}, "ci", time.Now()); err == nil {
		t.Error("expected an error for nil run")
	}
}

// The snapshot must imply nothing the run did not report: no installed
// versions, no environment variable names.
func TestRunSnapshotLeaksNothing(t *testing.T) {
	run, err := ParseRun(readFixture(t, "run-failed.json"), readFixture(t, "jobs-failed.json"))
	if err != nil {
		t.Fatalf("ParseRun: %v", err)
	}
	snap, err := RunSnapshot(run, run.Job("activation"), "ci", time.Now())
	if err != nil {
		t.Fatalf("RunSnapshot: %v", err)
	}
	if len(snap.Runtimes) != 0 {
		t.Errorf("runtimes = %v, want none", snap.Runtimes)
	}
	if snap.Environment != nil && len(snap.Environment.Names) != 0 {
		t.Errorf("environment names = %v, want none", snap.Environment.Names)
	}
}

// Without a job URL the Source ref falls back to a stable human-readable
// description, and without a head SHA the git section is left absent.
func TestRunSnapshotRefAndGitFallbacks(t *testing.T) {
	r := &Run{Repository: "cli/cli", ID: 42, Conclusion: "success", WorkflowPath: "ci.yml"}
	j := &RunJob{Name: "build", Conclusion: "success", Labels: []string{"ubuntu-latest"}}

	snap, err := RunSnapshot(r, j, "ci", time.Now())
	if err != nil {
		t.Fatalf("RunSnapshot: %v", err)
	}
	if snap.Source.Ref != "cli/cli run 42 job build" {
		t.Errorf("ref = %q, want the composed fallback", snap.Source.Ref)
	}
	if snap.Git != nil {
		t.Errorf("git = %+v, want nil without a head sha", snap.Git)
	}
	if snap.System == nil || snap.System.OS != "linux" {
		t.Errorf("system = %+v, want linux from the label", snap.System)
	}

	// A job with no labels at all (a skipped one, for example) must leave the
	// platform unknown rather than guessed, and say so.
	noLabels := &RunJob{Name: "skipped", Conclusion: "skipped"}
	snap2, err := RunSnapshot(r, noLabels, "ci", time.Now())
	if err != nil {
		t.Fatalf("RunSnapshot no labels: %v", err)
	}
	if snap2.System != nil {
		t.Errorf("system = %+v, want nil without labels", snap2.System)
	}
	if !notesMention(snap2.Source.Notes, "no runner labels") {
		t.Errorf("notes should say the platform is unknown: %v", snap2.Source.Notes)
	}
}

func jobIDs(r *Run) []int64 {
	ids := make([]int64, 0, len(r.Jobs))
	for _, j := range r.Jobs {
		ids = append(ids, j.ID)
	}
	return ids
}

func notesMention(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// assertNoteOrder fails unless each substring appears in a later note than the
// previous one.
func assertNoteOrder(t *testing.T, notes []string, subs []string) {
	t.Helper()
	last := -1
	for _, s := range subs {
		idx := -1
		for i, n := range notes {
			if strings.Contains(n, s) {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Errorf("no note contains %q: %v", s, notes)
			return
		}
		if idx <= last {
			t.Errorf("note %q should come after the previous one; notes: %v", s, notes)
			return
		}
		last = idx
	}
}
