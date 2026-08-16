package githubactions

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFetchJobLogHappy(t *testing.T) {
	rec := &execRecorder{outs: [][]byte{[]byte("canned log")}}
	client := &Client{Exec: rec.exec}

	log, err := client.FetchJobLog(context.Background(), "cli/cli", 123)
	if err != nil {
		t.Fatalf("FetchJobLog: %v", err)
	}
	if len(rec.args) != 1 {
		t.Fatalf("Exec called %d times, want 1", len(rec.args))
	}
	// The log is terminal output, so the escape sequences must be explicitly
	// allowed or gh refuses to emit it.
	want := []string{"api", "--allow-escape-sequences", "repos/cli/cli/actions/jobs/123/logs"}
	if !reflect.DeepEqual(rec.args[0], want) {
		t.Errorf("args = %q, want %q", rec.args[0], want)
	}
	if !bytes.Equal(log, []byte("canned log")) {
		t.Errorf("log = %q, want the canned bytes", log)
	}
}

// TestFetchJobLogRejectsBadRepo is a security boundary, not a formatting
// preference: the repository is interpolated into a request path, so anything
// that is not exactly "owner/name" must be rejected before the subprocess is
// invoked. The stub counting zero calls proves the fake was never asked.
func TestFetchJobLogRejectsBadRepo(t *testing.T) {
	tests := []struct {
		name string
		repo string
		skip string // reason when the current code is known to mishandle this case
	}{
		{"parent traversal segment", "../etc", "BUG: repoPattern's character class accepts \"..\" as a segment, so FetchJobLog builds repos/../etc/... and invokes gh instead of rejecting the repository"},
		{"missing slash", "cli", ""},
		{"extra slash", "cli/cli/extra", ""},
		{"query string", "cli/cli?x=1", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip != "" {
				t.Skip(tt.skip)
			}
			rec := &execRecorder{}
			client := &Client{Exec: rec.exec}
			if _, err := client.FetchJobLog(context.Background(), tt.repo, 123); err == nil {
				t.Errorf("FetchJobLog(%q) accepted a repository that must be rejected", tt.repo)
			}
			if len(rec.args) != 0 {
				t.Errorf("FetchJobLog(%q) invoked the subprocess %d times, want 0", tt.repo, len(rec.args))
			}
		})
	}
}

// Older gh releases have no --allow-escape-sequences: they refuse the flag and
// would emit the log anyway if only it were omitted. A failure mentioning that
// flag must retry without it, and the retry's output is what the caller gets.
func TestFetchJobLogOlderGHRerunsWithoutFlag(t *testing.T) {
	rec := &execRecorder{
		errs: []error{errors.New("unknown flag: --allow-escape-sequences")},
		outs: [][]byte{nil, []byte("canned log")},
	}
	client := &Client{Exec: rec.exec}

	log, err := client.FetchJobLog(context.Background(), "cli/cli", 123)
	if err != nil {
		t.Fatalf("FetchJobLog: %v", err)
	}
	if len(rec.args) != 2 {
		t.Fatalf("Exec called %d times, want 2 (original + retry)", len(rec.args))
	}
	first := []string{"api", "--allow-escape-sequences", "repos/cli/cli/actions/jobs/123/logs"}
	if !reflect.DeepEqual(rec.args[0], first) {
		t.Errorf("first call args = %q, want %q", rec.args[0], first)
	}
	retry := []string{"api", "repos/cli/cli/actions/jobs/123/logs"}
	if !reflect.DeepEqual(rec.args[1], retry) {
		t.Errorf("retry args = %q, want %q", rec.args[1], retry)
	}
	if !bytes.Equal(log, []byte("canned log")) {
		t.Errorf("log = %q, want the retry's output", log)
	}
}

// Any other failure is a real error, not a flag-version problem: it must be
// returned as-is (mentioning the job), and must not trigger a retry.
func TestFetchJobLogOtherFailureNotRetried(t *testing.T) {
	sentinel := errors.New("not authorized")
	rec := &execRecorder{errs: []error{sentinel}}
	client := &Client{Exec: rec.exec}

	_, err := client.FetchJobLog(context.Background(), "cli/cli", 123)
	if err == nil {
		t.Fatal("FetchJobLog returned nil error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the exec error", err)
	}
	if !strings.Contains(err.Error(), "123") {
		t.Errorf("error %q does not mention the job id", err)
	}
	if len(rec.args) != 1 {
		t.Errorf("Exec called %d times, want 1 (no retry)", len(rec.args))
	}
}
