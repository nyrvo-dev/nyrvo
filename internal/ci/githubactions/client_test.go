package githubactions

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// execRecorder stands in for the gh CLI. It captures every call so the tests
// can pin down exactly what FetchRun would execute, and hands back canned
// bytes or a canned error, so no test ever touches the network.
type execRecorder struct {
	ctxs []context.Context
	args [][]string
	outs [][]byte
	errs []error
}

// exec implements the Client.Exec contract. When a per-call error is set, that
// call fails; otherwise the matching canned output is returned.
func (r *execRecorder) exec(ctx context.Context, args ...string) ([]byte, error) {
	r.ctxs = append(r.ctxs, ctx)
	r.args = append(r.args, append([]string(nil), args...))
	i := len(r.args) - 1
	if i < len(r.errs) && r.errs[i] != nil {
		return nil, r.errs[i]
	}
	if i < len(r.outs) {
		return r.outs[i], nil
	}
	return []byte("{}"), nil
}

func TestParseRunRefValid(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want runRef
	}{
		{"bare id", "123456789", runRef{ID: "123456789"}},
		{"bare id with surrounding whitespace", "  123456789  ", runRef{ID: "123456789"}},
		{"run url", "https://github.com/cli/cli/actions/runs/31921289286", runRef{Repo: "cli/cli", ID: "31921289286"}},
		{"run url with job suffix", "https://github.com/cli/cli/actions/runs/31921289286/job/123", runRef{Repo: "cli/cli", ID: "31921289286"}},
		{"run url with attempts suffix", "https://github.com/cli/cli/actions/runs/31921289286/attempts/2", runRef{Repo: "cli/cli", ID: "31921289286"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRunRef(tt.arg)
			if err != nil {
				t.Fatalf("parseRunRef(%q): %v", tt.arg, err)
			}
			if got != tt.want {
				t.Errorf("parseRunRef(%q) = %+v, want %+v", tt.arg, got, tt.want)
			}
		})
	}
}

// TestParseRunRefRejects is a security boundary, not a formatting preference:
// every accepted reference is interpolated into an API path and handed to gh,
// so a malformed one must be rejected rather than quietly sanitized. Sanitizing
// would send a request somewhere the user did not ask for.
func TestParseRunRefRejects(t *testing.T) {
	tests := []struct {
		name string
		arg  string
	}{
		{"empty", ""},
		{"letters", "abc"},
		{"trailing letter", "12a"},
		{"negative", "-1"},
		{"too many digits", "1234567890123456789012"},
		{"path traversal", "../../etc"},
		{"non-numeric run id in url", "https://github.com/cli/cli/actions/runs/abc"},
		{"not a run url", "https://example.com/not/a/run"},
		{"pull request url", "https://github.com/cli/cli/pulls/12"},
		{"owner-repo shorthand", "cli/cli#123"},
		{"embedded space", "123 456"},
		{"embedded newline", "123\n456"},
		{"only whitespace", "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseRunRef(tt.arg); err == nil {
				t.Errorf("parseRunRef(%q) accepted a reference that must be rejected", tt.arg)
			}
		})
	}
}

func TestRunRefPath(t *testing.T) {
	tests := []struct {
		ref  runRef
		want string
	}{
		// No repo: the literal gh placeholder, which gh resolves from the local
		// repository instead of Nyrvo making a second call to learn its name.
		{runRef{ID: "123"}, "repos/{owner}/{repo}/actions/runs/123"},
		{runRef{Repo: "cli/cli", ID: "123"}, "repos/cli/cli/actions/runs/123"},
	}
	for _, tt := range tests {
		if got := tt.ref.path(); got != tt.want {
			t.Errorf("runRef{%+v}.path() = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

// TestRunRefPathSafeChars pins the property that proves a hostile reference
// cannot smuggle a query string, another path segment, or a shell character
// into the request: for every reference parseRunRef accepts, the resulting path
// contains only characters that can appear inside one path segment.
func TestRunRefPathSafeChars(t *testing.T) {
	pathSafe := regexp.MustCompile(`^[A-Za-z0-9._/{}-]+$`)
	inputs := []string{
		"0",
		"123",
		"123456789",
		"12345678901234567890", // the maximum length the id accepts
		"  42  ",
		"https://github.com/cli/cli/actions/runs/123456789",
		"https://github.com/cli/cli/actions/runs/123456789/job/123",
		"https://github.com/cli/cli/actions/runs/123456789/attempts/2",
		"http://github.example.com/owner/repo/actions/runs/123",
		"https://github.com/a/repo/actions/runs/1",
	}
	for _, in := range inputs {
		r, err := parseRunRef(in)
		if err != nil {
			t.Fatalf("parseRunRef(%q): %v", in, err)
		}
		if p := r.path(); !pathSafe.MatchString(p) {
			t.Errorf("path for %q = %q contains characters outside [A-Za-z0-9._/{}-]", in, p)
		}
	}
}

func TestFetchRunHappy(t *testing.T) {
	rec := &execRecorder{
		outs: [][]byte{[]byte(`{"run":true}`), []byte(`{"jobs":[{"id":1}]}`)},
	}
	client := &Client{Exec: rec.exec}

	runJSON, jobsJSON, ref, err := client.FetchRun(context.Background(), "123456789")
	if err != nil {
		t.Fatalf("FetchRun: %v", err)
	}

	if len(rec.args) != 2 {
		t.Fatalf("Exec called %d times, want 2", len(rec.args))
	}
	wantRun := []string{"api", "repos/{owner}/{repo}/actions/runs/123456789"}
	if !reflect.DeepEqual(rec.args[0], wantRun) {
		t.Errorf("run call args = %q, want %q", rec.args[0], wantRun)
	}
	wantJobs := []string{"api", "--paginate", "repos/{owner}/{repo}/actions/runs/123456789/jobs?per_page=100"}
	if !reflect.DeepEqual(rec.args[1], wantJobs) {
		t.Errorf("jobs call args = %q, want %q", rec.args[1], wantJobs)
	}
	if !bytes.Equal(runJSON, []byte(`{"run":true}`)) {
		t.Errorf("runJSON = %q, want the canned run bytes", runJSON)
	}
	if !bytes.Equal(jobsJSON, []byte(`{"jobs":[{"id":1}]}`)) {
		t.Errorf("jobsJSON = %q, want the canned jobs bytes", jobsJSON)
	}
	if ref != "123456789" {
		t.Errorf("ref = %q, want the bare id", ref)
	}
	if !strings.Contains(ref, "123456789") {
		t.Errorf("ref %q does not mention the run id", ref)
	}
}

// A URL-form reference must resolve the repository up front, so the API paths
// handed to gh are qualified rather than left to gh's local resolution.
func TestFetchRunRefFromURL(t *testing.T) {
	rec := &execRecorder{outs: [][]byte{[]byte(`{}`), []byte(`{}`)}}
	client := &Client{Exec: rec.exec}

	_, _, ref, err := client.FetchRun(context.Background(), "https://github.com/cli/cli/actions/runs/31921289286")
	if err != nil {
		t.Fatalf("FetchRun: %v", err)
	}
	if ref != "cli/cli run 31921289286" {
		t.Errorf("ref = %q, want it to mention the repository", ref)
	}
	if got := rec.args[0][1]; !strings.HasPrefix(got, "repos/cli/cli/actions/runs/31921289286") {
		t.Errorf("run path = %q, want the resolved repository", got)
	}
}

// TestFetchRunRejectsBeforeExec pins the ordering guarantee: a reference that
// fails to parse must be stopped before the subprocess is ever invoked, so a
// bad reference can never reach the network.
func TestFetchRunRejectsBeforeExec(t *testing.T) {
	rec := &execRecorder{}
	client := &Client{Exec: rec.exec}

	for _, bad := range []string{"", "abc", "../../etc", "https://github.com/cli/cli/pulls/12"} {
		if _, _, _, err := client.FetchRun(context.Background(), bad); err == nil {
			t.Errorf("FetchRun(%q) accepted an invalid reference", bad)
		}
	}
	if len(rec.args) != 0 {
		t.Errorf("Exec invoked %d times for invalid references, want 0", len(rec.args))
	}
}

func TestFetchRunRunCallError(t *testing.T) {
	sentinel := errors.New("boom")
	rec := &execRecorder{errs: []error{sentinel}}
	client := &Client{Exec: rec.exec}

	_, _, _, err := client.FetchRun(context.Background(), "123456789")
	if err == nil {
		t.Fatal("FetchRun returned nil error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the exec error", err)
	}
	if !strings.Contains(err.Error(), "123456789") {
		t.Errorf("error %q does not mention the run it was fetching", err)
	}
	// The run call failed, so the jobs call must not have been attempted.
	if len(rec.args) != 1 {
		t.Errorf("Exec called %d times, want only the run call", len(rec.args))
	}
}

func TestFetchRunJobsCallError(t *testing.T) {
	sentinel := errors.New("boom")
	rec := &execRecorder{errs: []error{nil, sentinel}}
	client := &Client{Exec: rec.exec}

	_, _, _, err := client.FetchRun(context.Background(), "123456789")
	if err == nil {
		t.Fatal("FetchRun returned nil error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap the exec error", err)
	}
	if !strings.Contains(err.Error(), "jobs") {
		t.Errorf("error %q does not mention the jobs call", err)
	}
	if len(rec.args) != 2 {
		t.Errorf("Exec called %d times, want both calls", len(rec.args))
	}
}

// TestFetchRunContext checks that the caller's context reaches the subprocess
// layer untouched: the fake must receive a non-nil context, and a cancellation
// issued by the test must be observable from inside Exec.
func TestFetchRunContext(t *testing.T) {
	rec := &execRecorder{outs: [][]byte{[]byte(`{}`), []byte(`{}`)}}
	client := &Client{Exec: rec.exec}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := client.FetchRun(ctx, "123456789"); err != nil {
		t.Fatalf("FetchRun: %v", err)
	}

	if len(rec.ctxs) != 2 {
		t.Fatalf("Exec received %d contexts, want 2", len(rec.ctxs))
	}
	for i, c := range rec.ctxs {
		if c == nil {
			t.Errorf("call %d received a nil context", i)
			continue
		}
		if c.Err() != context.Canceled {
			t.Errorf("call %d context not cancelled: %v", i, c.Err())
		}
	}
}

// A bare run id needs gh to resolve the repository from the working directory.
// Outside a checkout gh reports a placeholder it cannot expand, which is
// accurate and useless on its own, so Nyrvo adds what to do instead — while
// keeping gh's own message.
func TestFetchRunHintsWhenRepositoryCannotBeResolved(t *testing.T) {
	ghErr := errors.New("unable to expand placeholder in path: failed to run git: fatal: not a git repository")
	c := &Client{Exec: func(context.Context, ...string) ([]byte, error) { return nil, ghErr }}

	_, _, _, err := c.FetchRun(context.Background(), "123456789")
	if err == nil {
		t.Fatal("FetchRun returned nil error")
	}
	if !strings.Contains(err.Error(), "run URL") {
		t.Errorf("error should suggest passing the run URL, got: %v", err)
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("gh's own message must be kept, got: %v", err)
	}

	// The hint is only for the unresolved-repository case: a 404 must not be
	// dressed up as a repository problem.
	c = &Client{Exec: func(context.Context, ...string) ([]byte, error) {
		return nil, errors.New("gh: Not Found (HTTP 404)")
	}}
	if _, _, _, err := c.FetchRun(context.Background(), "https://github.com/cli/cli/actions/runs/1"); err == nil {
		t.Fatal("FetchRun returned nil error")
	} else if strings.Contains(err.Error(), "run URL") {
		t.Errorf("a 404 should not suggest the run URL, got: %v", err)
	}
}
