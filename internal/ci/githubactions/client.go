package githubactions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
)

// fetchTimeout bounds one API call. It is far longer than the timeout for local
// tool probes because this one crosses a network: a laptop on hotel wifi should
// wait, not fail.
const fetchTimeout = 30 * time.Second

// runRef identifies a workflow run: a repository and a run id.
type runRef struct {
	// Repo is "owner/name", or empty to let gh resolve the repository from the
	// current directory's git remote.
	Repo string
	ID   string
}

// path returns the API path for this run. The id is validated as digits before
// it ever reaches here, so it cannot escape the path it is placed in.
func (r runRef) path() string {
	repo := r.Repo
	if repo == "" {
		// gh substitutes this placeholder from the current repository, which
		// avoids a second call just to learn the repository's name.
		repo = "{owner}/{repo}"
	}
	return "repos/" + repo + "/actions/runs/" + r.ID
}

// String renders the reference the way a user would recognize it.
func (r runRef) String() string {
	if r.Repo == "" {
		return r.ID
	}
	return r.Repo + " run " + r.ID
}

var (
	// runIDPattern is the whole of what may be interpolated into an API path
	// from user input. Anything else is rejected rather than escaped.
	runIDPattern = regexp.MustCompile(`^[0-9]{1,20}$`)
	// runURLPattern accepts the URL people copy out of the browser.
	runURLPattern = regexp.MustCompile(`^https?://[^/]+/([A-Za-z0-9._-]+/[A-Za-z0-9._-]+)/actions/runs/([0-9]{1,20})(?:/.*)?$`)
)

// parseRunRef accepts either a bare run id or the run URL from the browser.
//
// Both forms end up inside an API path, so neither is trusted: the id must be
// digits and the repository must look like "owner/name". Rejecting is the point
// — quietly sanitizing a malformed reference would send a request somewhere the
// user did not ask for.
func parseRunRef(arg string) (runRef, error) {
	arg = strings.TrimSpace(arg)
	if runIDPattern.MatchString(arg) {
		return runRef{ID: arg}, nil
	}
	if m := runURLPattern.FindStringSubmatch(arg); m != nil {
		return runRef{Repo: m[1], ID: m[2]}, nil
	}
	return runRef{}, fmt.Errorf("%q is not a run id or a run URL (expected 123456789 or https://github.com/owner/repo/actions/runs/123456789)", arg)
}

// Client fetches workflow run data from the GitHub API.
//
// It shells out to the user's own `gh`, which is already authenticated, rather
// than asking for a token and storing one. Nyrvo never owns GitHub credentials:
// a local-first tool that starts collecting API tokens becomes something users
// have to trust differently. See docs/adr/0010.
type Client struct {
	// Exec runs gh and returns its stdout. Tests substitute it; production
	// leaves it nil and gets ghExec.
	Exec func(ctx context.Context, args ...string) ([]byte, error)
}

func (c *Client) exec(ctx context.Context, args ...string) ([]byte, error) {
	if c.Exec != nil {
		return c.Exec(ctx, args...)
	}
	return ghExec(ctx, args...)
}

// FetchRun returns the raw run and jobs documents for a run reference.
//
// The documents are returned as bytes and parsed elsewhere: keeping the network
// call and the parsing apart is what lets the parser be tested against recorded
// API responses without a network or a token.
func (c *Client) FetchRun(ctx context.Context, arg string) (runJSON, jobsJSON []byte, ref string, err error) {
	r, err := parseRunRef(arg)
	if err != nil {
		return nil, nil, "", err
	}

	runJSON, err = c.exec(ctx, "api", r.path())
	if err != nil {
		return nil, nil, "", fmt.Errorf("fetch %s: %w", r, hintUnresolvedRepo(r, err))
	}
	// --paginate matters: a matrix run easily exceeds one page of jobs, and a
	// silently truncated job list would make Nyrvo report that a job does not
	// exist. gh concatenates one JSON document per page rather than merging
	// them, so the parser reads a stream of pages — see ParseJobs. (--slurp
	// would wrap them in an array, but it is a newer gh flag and buys nothing
	// a json.Decoder loop does not already handle.)
	jobsJSON, err = c.exec(ctx, "api", "--paginate", r.path()+"/jobs?per_page=100")
	if err != nil {
		return nil, nil, "", fmt.Errorf("fetch jobs for %s: %w", r, err)
	}
	return runJSON, jobsJSON, r.String(), nil
}

// FetchJobLog returns the raw log of one job.
//
// repo must be "owner/name" as reported by the API, and jobID is an integer, so
// neither can carry anything unexpected into the path.
//
// Job logs are terminal output: they contain ANSI escape sequences, which is
// why gh refuses to emit them without --allow-escape-sequences. Nyrvo asks for
// them anyway — the log is the evidence — and strips the escapes before any of
// it is stored or shown, so nothing that reaches a terminal can move the cursor
// or hide text. See docs/adr/0011.
func (c *Client) FetchJobLog(ctx context.Context, repo string, jobID int64) ([]byte, error) {
	if !repoPattern.MatchString(repo) {
		return nil, fmt.Errorf("%q is not an owner/name repository", repo)
	}
	path := "repos/" + repo + "/actions/jobs/" + strconv.FormatInt(jobID, 10) + "/logs"

	log, err := c.exec(ctx, "api", "--allow-escape-sequences", path)
	if err == nil {
		return log, nil
	}
	// Older gh releases have no such flag and no such refusal: they simply emit
	// the log. Retrying without it keeps those versions working instead of
	// requiring an upgrade for a flag that only exists to protect terminals.
	if strings.Contains(err.Error(), "unknown flag") || strings.Contains(err.Error(), "--allow-escape-sequences") {
		return c.exec(ctx, "api", path)
	}
	return nil, fmt.Errorf("fetch log for job %d: %w", jobID, err)
}

// repoPattern is what the API's repository.full_name may look like before it is
// placed in a request path.
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// hintUnresolvedRepo adds the missing half of gh's answer.
//
// A bare run id relies on gh resolving the repository from the working
// directory, so outside a checkout gh reports that it cannot expand a
// placeholder — accurate, and useless to someone who just wanted to paste a run
// id. The hint says what to do instead. gh's own message is kept: if this
// string match ever stops matching, the user still sees the real error and only
// loses the suggestion.
func hintUnresolvedRepo(r runRef, err error) error {
	if r.Repo != "" || !strings.Contains(err.Error(), "unable to expand placeholder") {
		return err
	}
	return fmt.Errorf("%w\nrun this inside the repository, or pass the full run URL (https://github.com/owner/repo/actions/runs/%s)", err, r.ID)
}

// ghExec runs the gh CLI with an argument vector and no shell.
func ghExec(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return nil, fmt.Errorf("the GitHub CLI (gh) is required to import a run and was not found: install it from https://cli.github.com and run `gh auth login`: %w", collector.ErrUnavailable)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("gh api timed out: %w", ctxErr)
	}
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		return nil, fmt.Errorf("gh api failed: %w", err)
	}
	// gh's own message names the real problem — not authenticated, no such run,
	// no access — far better than anything Nyrvo could infer from an exit code.
	return nil, fmt.Errorf("gh api failed: %s", firstLine(msg))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
