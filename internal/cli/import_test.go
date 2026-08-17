package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nyrvo-dev/nyrvo/internal/ci/githubactions"
)

// ciExecStub is the fake gh the import command runs through. It answers with
// the recorded run and jobs documents by looking at which call it is serving,
// and counts every invocation so a test can prove the command never reached a
// real network.
type ciExecStub struct {
	runDoc  []byte
	jobsDoc []byte
	logDoc  []byte
	runErr  error
	logErr  error
	calls   int
	args    [][]string
}

func (s *ciExecStub) exec(_ context.Context, args ...string) ([]byte, error) {
	s.calls++
	s.args = append(s.args, append([]string(nil), args...))
	last := args[len(args)-1]
	if strings.HasSuffix(last, "/jobs?per_page=100") {
		return s.jobsDoc, nil
	}
	// The import also reads the selected job's log, which is what supplies the
	// installed runtime versions and the failing output.
	if strings.HasSuffix(last, "/logs") {
		return s.logDoc, s.logErr
	}
	if s.runErr != nil {
		return nil, s.runErr
	}
	return s.runDoc, nil
}

// stubCIClient swaps the package's client constructor for one whose Exec is the
// fake, and restores the real constructor when the test ends. Without this the
// import command would shell out to a real gh.
func stubCIClient(t *testing.T, s *ciExecStub) {
	t.Helper()
	orig := newCIClient
	newCIClient = func() *githubactions.Client { return &githubactions.Client{Exec: s.exec} }
	t.Cleanup(func() { newCIClient = orig })
}

// readFixture loads one recorded API response. The path is relative to the
// package directory (internal/cli), which is the working directory only until a
// test calls t.Chdir, so fixtures are always loaded before chdir.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "ci", "githubactions", "testdata", "runs", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func TestCIImportFailedRunPicksFailedJob(t *testing.T) {
	stub := &ciExecStub{
		runDoc:  readFixture(t, "run-failed.json"),
		jobsDoc: readFixture(t, "jobs-failed.json"),
	}
	stubCIClient(t, stub)
	chdirWorkDir(t)

	code, stdout, errOut := run(t, "ci", "import", "31921289286")
	if code != ExitOK {
		t.Fatalf("ci import: exit %d, stderr: %s", code, errOut)
	}
	// The run has exactly one failed job, so no job argument is needed, and the
	// message must explain the choice was the failure rather than a guess.
	if !strings.Contains(stdout, `job "activation"`) {
		t.Errorf("stdout missing the job name:\n%s", stdout)
	}
	if !strings.Contains(stdout, "the only job that failed") {
		t.Errorf("stdout should explain the job was chosen because it failed:\n%s", stdout)
	}
	if stub.calls != 3 {
		t.Errorf("stub called %d times, want 3 (run + jobs + job log)", stub.calls)
	}
}

// The imported snapshot records what the run observed: the source kind says a
// real run produced it, the git section carries the head commit the runner
// checked out, and the environment is present, empty, and partial so a diff
// cannot report every local variable as missing from CI.
func TestCIImportWritesRunSnapshot(t *testing.T) {
	stub := &ciExecStub{
		runDoc:  readFixture(t, "run-failed.json"),
		jobsDoc: readFixture(t, "jobs-failed.json"),
	}
	stubCIClient(t, stub)
	dir := t.TempDir()
	t.Chdir(dir)

	if code, _, errOut := run(t, "ci", "import", "31921289286"); code != ExitOK {
		t.Fatalf("ci import: exit %d, stderr: %s", code, errOut)
	}

	var snap struct {
		Source struct {
			Kind string `json:"kind"`
		} `json:"source"`
		Git *struct {
			SHA string `json:"sha"`
		} `json:"git"`
		Environment *struct {
			Names   []string `json:"names"`
			Partial bool     `json:"partial"`
		} `json:"environment"`
	}
	data, err := os.ReadFile(filepath.Join(".nyrvo", "snapshots", "ci.json"))
	if err != nil {
		t.Fatalf("read saved snapshot: %v", err)
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("snapshot is not valid JSON: %v", err)
	}
	if snap.Source.Kind != "github-actions-run" {
		t.Errorf("source.kind = %q, want github-actions-run", snap.Source.Kind)
	}
	if snap.Git == nil || snap.Git.SHA != "0eeec0b92edbe70199f9768522f831d3534f41ad" {
		t.Errorf("git.sha = %+v, want the run's head sha", snap.Git)
	}
	if snap.Environment == nil || !snap.Environment.Partial {
		t.Errorf("environment should be present and partial: %+v", snap.Environment)
	}
	if snap.Environment != nil && len(snap.Environment.Names) != 0 {
		t.Errorf("environment.names = %v, want empty", snap.Environment.Names)
	}
}

// A matrix run has several jobs and none of them failed, so importing it
// without naming one must refuse and list every job verbatim, parentheses
// included, instead of guessing.
func TestCIImportMatrixNeedsJobName(t *testing.T) {
	stub := &ciExecStub{
		runDoc:  readFixture(t, "run-matrix.json"),
		jobsDoc: readFixture(t, "jobs-matrix.json"),
	}
	stubCIClient(t, stub)
	chdirWorkDir(t)

	code, _, errOut := run(t, "ci", "import", "31916576297")
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitUsage, errOut)
	}
	for _, name := range []string{
		"CodeQL-Build (actions, none, security-and-quality)",
		"CodeQL-Build (go, manual, ./.github/codeql/codeql-config.yml)",
	} {
		if !strings.Contains(errOut, name) {
			t.Errorf("stderr missing job %q:\n%s", name, errOut)
		}
	}
}

func TestCIImportNamedJob(t *testing.T) {
	stub := &ciExecStub{
		runDoc:  readFixture(t, "run-matrix.json"),
		jobsDoc: readFixture(t, "jobs-matrix.json"),
	}
	stubCIClient(t, stub)
	chdirWorkDir(t)

	code, stdout, errOut := run(t, "ci", "import", "31916576297", "CodeQL-Build (actions, none, security-and-quality)")
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(stdout, "named on the command line") {
		t.Errorf("stdout should say the job was named on the command line:\n%s", stdout)
	}
}

// Naming a job that does not exist is an operational failure, and the message
// must point at the jobs that do exist so the user can pick one.
func TestCIImportUnknownJob(t *testing.T) {
	stub := &ciExecStub{
		runDoc:  readFixture(t, "run-matrix.json"),
		jobsDoc: readFixture(t, "jobs-matrix.json"),
	}
	stubCIClient(t, stub)
	chdirWorkDir(t)

	code, _, errOut := run(t, "ci", "import", "31916576297", "no-such-job")
	if code != ExitError {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitError, errOut)
	}
	for _, name := range []string{
		"CodeQL-Build (actions, none, security-and-quality)",
		"CodeQL-Build (go, manual, ./.github/codeql/codeql-config.yml)",
	} {
		if !strings.Contains(errOut, name) {
			t.Errorf("stderr should list available job %q:\n%s", name, errOut)
		}
	}
}

// A reference that cannot be parsed must be rejected before the subprocess is
// ever invoked: the stub counting zero calls proves a bad reference never
// reaches the network.
func TestCIImportRejectsBadRefBeforeNetwork(t *testing.T) {
	stub := &ciExecStub{}
	stubCIClient(t, stub)
	chdirWorkDir(t)

	for _, bad := range []string{"../../etc", "not-a-run"} {
		if code, _, errOut := run(t, "ci", "import", bad); code != ExitError {
			t.Errorf("import %q: exit = %d, want %d (stderr: %s)", bad, code, ExitError, errOut)
		}
	}
	if stub.calls != 0 {
		t.Errorf("stub called %d times for invalid references, want 0", stub.calls)
	}
}

// A failed run fetch is an operational error surfaced to the user.
func TestCIImportRunCallError(t *testing.T) {
	stub := &ciExecStub{
		runDoc:  readFixture(t, "run-failed.json"),
		jobsDoc: readFixture(t, "jobs-failed.json"),
		runErr:  errors.New("boom"),
	}
	stubCIClient(t, stub)
	chdirWorkDir(t)

	code, _, errOut := run(t, "ci", "import", "31921289286")
	if code != ExitError {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitError, errOut)
	}
	if !strings.Contains(errOut, "boom") {
		t.Errorf("stderr should surface the stub's error:\n%s", errOut)
	}
}

// The spinner on ci import must be a no-op for a non-terminal destination. The
// message a pipe, a file or a CI log receives is byte-for-byte what it was
// before the spinner existed — the animation exists only on a terminal, and
// nothing here may leak into the stream.
func TestCIImportNonTerminalBytesUnchanged(t *testing.T) {
	stub := &ciExecStub{
		runDoc:  readFixture(t, "run-failed.json"),
		jobsDoc: readFixture(t, "jobs-failed.json"),
		logDoc:  readLogFixture(t, "log-failure.txt"),
	}
	stubCIClient(t, stub)
	chdirWorkDir(t)

	code, stdout, errOut := run(t, "ci", "import", "31921289286")
	if code != ExitOK {
		t.Fatalf("ci import: exit %d, stderr: %s", code, errOut)
	}
	want := `Imported 31921289286 job "activation" (the only job that failed).
Snapshot saved: ci, replacing any previous ci snapshot.

Diagnose it against this machine:
  nyrvo capture local
  nyrvo doctor
`
	if stdout != want {
		t.Errorf("non-terminal ci import output changed\ngot:\n%q\nwant:\n%q", stdout, want)
	}
	if strings.Contains(stdout, "\x1b") || strings.Contains(stdout, "\r") {
		t.Errorf("ci import stdout carries animation bytes:\n%s", stdout)
	}
}

func TestCIImportUsage(t *testing.T) {
	stub := &ciExecStub{}
	stubCIClient(t, stub)
	chdirWorkDir(t)

	tests := []struct {
		name string
		args []string
	}{
		{"no arguments", []string{"ci", "import"}},
		{"three arguments", []string{"ci", "import", "123", "job", "extra"}},
		{"with a flag", []string{"ci", "import", "--json", "123"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _, _ := run(t, tt.args...); code != ExitUsage {
				t.Errorf("%s: exit = %d, want %d", tt.name, code, ExitUsage)
			}
		})
	}
	// Usage errors are caught before any fetching, so the network must not be
	// touched either.
	if stub.calls != 0 {
		t.Errorf("stub called %d times for usage errors, want 0", stub.calls)
	}
}

// A failing import must not leave a snapshot behind: a partial write would make
// `nyrvo diff local ci` compare against an environment that was never saved.
func TestCIImportFailureLeavesNoSnapshot(t *testing.T) {
	stub := &ciExecStub{
		runDoc:  readFixture(t, "run-matrix.json"),
		jobsDoc: readFixture(t, "jobs-matrix.json"),
	}
	stubCIClient(t, stub)
	dir := t.TempDir()
	t.Chdir(dir)

	// Two jobs, none failed, no job name: selectRunJob refuses, so the command
	// must exit before Save runs.
	if code, _, _ := run(t, "ci", "import", "31916576297"); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if _, err := os.Stat(filepath.Join(".nyrvo", "snapshots", "ci.json")); err == nil {
		t.Error("a failing import left a snapshot behind")
	}
}
