package githubactions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
	"github.com/nyrvo-dev/nyrvo/internal/textsafe"
)

// Run is one workflow run that actually happened, as reported by the GitHub
// API. Where a workflow file states intent, a run records fact: the matrix
// expression has been resolved into the jobs that ran, and the head SHA names
// the commit that was checked out.
type Run struct {
	ID           int64
	Name         string
	Repository   string // "owner/name", from repository.full_name
	WorkflowPath string // from the run's "path"
	HeadSHA      string
	HeadBranch   string
	Event        string
	Status       string
	Conclusion   string
	Attempt      int // run_attempt
	Number       int // run_number
	URL          string
	CreatedAt    time.Time
	Jobs         []RunJob
}

// RunJob is one job of a run.
type RunJob struct {
	ID         int64
	Name       string
	Status     string
	Conclusion string
	// Labels are the RESOLVED runner labels: the API expands a workflow's
	// ${{ matrix.os }} into the concrete runner the job actually used.
	Labels     []string
	RunnerName string
	URL        string
	Steps      []RunStep
}

// RunStep is one step of a job.
type RunStep struct {
	Name       string
	Number     int
	Status     string
	Conclusion string
}

// runDoc is the wire shape of one run document. The field names are the API's
// own, and the fixtures they were recorded from keep them honest: parsing into
// a typed struct means a renamed or absent field fails loudly instead of being
// silently read as the zero value.
type runDoc struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Path       string    `json:"path"`
	HeadSHA    string    `json:"head_sha"`
	HeadBranch string    `json:"head_branch"`
	Event      string    `json:"event"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	RunAttempt int       `json:"run_attempt"`
	RunNumber  int       `json:"run_number"`
	HTMLURL    string    `json:"html_url"`
	CreatedAt  time.Time `json:"created_at"`
}

// jobsPage is the wire shape of one page of a jobs listing.
type jobsPage struct {
	Jobs []runJobDoc `json:"jobs"`
}

// runJobDoc is the wire shape of one job within a jobs page. RunnerName is a
// pointer because the API omits it (emits null) for jobs that never started.
type runJobDoc struct {
	ID         int64        `json:"id"`
	Name       string       `json:"name"`
	Status     string       `json:"status"`
	Conclusion string       `json:"conclusion"`
	Labels     []string     `json:"labels"`
	RunnerName *string      `json:"runner_name"`
	HTMLURL    string       `json:"html_url"`
	Steps      []runStepDoc `json:"steps"`
}

type runStepDoc struct {
	Name       string `json:"name"`
	Number     int    `json:"number"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// ParseRun reads one run document and the stream of jobs pages for it. The
// jobs input is exactly what `gh api --paginate` emits: one JSON document per
// page, concatenated, so it must be decoded with a streaming loop rather than a
// single json.Unmarshal. A run that changes mid-fetch can repeat a job across
// pages, so jobs are de-duplicated by id. Either malformed input produces an
// error naming which document was bad.
func ParseRun(runJSON, jobsJSON []byte) (*Run, error) {
	var d runDoc
	if err := json.Unmarshal(runJSON, &d); err != nil {
		return nil, fmt.Errorf("malformed run JSON: %w", err)
	}
	jobs, err := ParseJobs(jobsJSON)
	if err != nil {
		return nil, err
	}
	return &Run{
		ID:           d.ID,
		Name:         d.Name,
		Repository:   d.Repository.FullName,
		WorkflowPath: d.Path,
		HeadSHA:      d.HeadSHA,
		HeadBranch:   d.HeadBranch,
		Event:        d.Event,
		Status:       d.Status,
		Conclusion:   d.Conclusion,
		Attempt:      d.RunAttempt,
		Number:       d.RunNumber,
		URL:          d.HTMLURL,
		CreatedAt:    d.CreatedAt,
		Jobs:         jobs,
	}, nil
}

// ParseJobs decodes a stream of jobs page documents into one ordered list,
// de-duplicating by job id. Pages can overlap when a run changes between
// fetches; a job that appears twice must be recorded once.
func ParseJobs(jobsJSON []byte) ([]RunJob, error) {
	dec := json.NewDecoder(bytes.NewReader(jobsJSON))
	seen := make(map[int64]bool)
	var jobs []RunJob
	for {
		var page jobsPage
		err := dec.Decode(&page)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("malformed jobs JSON: %w", err)
		}
		for _, d := range page.Jobs {
			if seen[d.ID] {
				continue
			}
			seen[d.ID] = true
			jobs = append(jobs, toRunJob(d))
		}
	}
	return jobs, nil
}

func toRunJob(d runJobDoc) RunJob {
	runner := ""
	if d.RunnerName != nil {
		runner = *d.RunnerName
	}
	// The wire and domain step types happen to carry the same fields today, so
	// a conversion says it in one line. Should the API grow a field the domain
	// does not want, the compiler will stop here and force the decision rather
	// than letting the shapes drift apart quietly.
	steps := make([]RunStep, 0, len(d.Steps))
	for _, s := range d.Steps {
		steps = append(steps, RunStep(s))
	}
	return RunJob{
		ID:         d.ID,
		Name:       d.Name,
		Status:     d.Status,
		Conclusion: d.Conclusion,
		Labels:     d.Labels,
		RunnerName: runner,
		URL:        d.HTMLURL,
		Steps:      steps,
	}
}

// Job returns the job with the given name, or nil when absent. Names are kept
// verbatim, matrix parentheses included, so the match is exact.
func (r *Run) Job(name string) *RunJob {
	for i := range r.Jobs {
		if r.Jobs[i].Name == name {
			return &r.Jobs[i]
		}
	}
	return nil
}

// FailedJobs returns the jobs whose conclusion is "failure", in run order.
func (r *Run) FailedJobs() []RunJob {
	var out []RunJob
	for _, j := range r.Jobs {
		if j.Conclusion == "failure" {
			out = append(out, j)
		}
	}
	return out
}

// FailedStep returns the first step that failed, or nil when the job did not
// fail. The first failure is the one that stopped the rest of the job.
func (j *RunJob) FailedStep() *RunStep {
	for i := range j.Steps {
		if j.Steps[i].Conclusion == "failure" {
			return &j.Steps[i]
		}
	}
	return nil
}

// RunSnapshot converts a run's job into a snapshot of the environment that job
// actually executed in. Unlike a workflow snapshot, which states expectations,
// this one records observations: the resolved runner, the real head commit.
func RunSnapshot(r *Run, j *RunJob, name string, now time.Time) (*snapshot.Snapshot, error) {
	if r == nil || j == nil {
		return nil, errors.New("githubactions: cannot build a snapshot without a run and a job")
	}

	snap := snapshot.New(name, now)

	// A CI checkout is exactly the run's head commit with a clean tree: the
	// runner starts from the SHA and applies nothing on top of it. Dirty false
	// is therefore an observed claim here, not an absent default. With no SHA
	// there is nothing to observe, so the section stays absent.
	if r.HeadSHA != "" {
		snap.Git = &snapshot.Git{SHA: r.HeadSHA, Branch: r.HeadBranch, Dirty: false}
	}

	// A run reports RESOLVED runner labels, so a matrix job a workflow file
	// could only describe as ${{ matrix.os }} now names the platform it actually
	// ran on: the guess in the workflow path becomes an observation here.
	notes := []string{}
	if r.Conclusion != "" {
		notes = append(notes, fmt.Sprintf("The run concluded with %s.", r.Conclusion))
	}
	if j.Conclusion != "" {
		notes = append(notes, fmt.Sprintf("The job %s concluded with %s.", j.Name, j.Conclusion))
	}
	if fs := j.FailedStep(); fs != nil {
		notes = append(notes, fmt.Sprintf("The job failed at step %q.", fs.Name))
	}
	if r.WorkflowPath != "" {
		notes = append(notes, fmt.Sprintf("The run came from the workflow %s.", r.WorkflowPath))
	}

	// An unknown label is recorded, not guessed: claiming a platform for a
	// runner Nyrvo does not recognize would fabricate evidence about a machine
	// that was never identified.
	label := ""
	if len(j.Labels) > 0 {
		label = j.Labels[0]
	}
	switch {
	case label == "":
		notes = append(notes, "The job reports no runner labels; the platform is not known.")
	case runnerToSystem(label) != nil:
		snap.System = runnerToSystem(label)
	default:
		notes = append(notes, fmt.Sprintf("The runner label %s is not a known GitHub-hosted runner; the platform is not known.", label))
	}

	// Run metadata carries no environment variables at all, so the list is
	// empty. It must still be present and partial: an empty complete list
	// would make a diff report every local variable as missing from CI, and
	// Partial is what tells the diff this list cannot testify to absence.
	// The list is an empty slice rather than nil so the JSON carries "names":
	// [] like every other snapshot: a consumer should not have to treat null
	// and [] as the same thing.
	snap.Environment = &snapshot.Environment{Names: []string{}, Partial: true}
	// Run metadata carries no runtime versions either — they live in the job
	// logs. The list is empty and must be marked partial for the same reason:
	// an empty complete list would read as a runner with no runtimes at all.
	snap.PartialRuntimes = true

	// Installed versions live only in the job logs, which Nyrvo does not import
	// yet. Leaving Runtimes empty without a note would imply the run reported
	// none, so the note makes the gap explicit.
	// The note describes what this function alone can know. ApplyJobLog removes
	// it if the job's log turns out to name the versions, so a snapshot never
	// carries a caveat that its own contents disprove.
	notes = append(notes, runtimesFromLogNote)
	// Without this note the services section is simply empty, and an empty
	// section reads as "this job needed no backing containers". A run's metadata
	// does not list them; the workflow file does, which is what `nyrvo ci
	// capture` reads.
	notes = append(notes, servicesNotInRunNote)

	ref := j.URL
	if ref == "" {
		ref = fmt.Sprintf("%s run %d job %s", r.Repository, r.ID, j.Name)
	}
	// Notes carry job names, step names and runner labels straight from the
	// API; nothing may arrive in a snapshot carrying a control byte that a
	// terminal would interpret (docs/adr/0011).
	snap.Source = &snapshot.Source{
		Kind:  snapshot.SourceGitHubActionsRun,
		Ref:   ref,
		Notes: textsafe.StripAll(notes),
	}

	snap.Normalize()
	return snap, nil
}

// runtimesFromLogNote is the caveat a run's metadata alone must carry: the API
// reports no installed versions. ApplyJobLog deletes it when the job's log
// supplies them, so the note and the snapshot never contradict each other.
const runtimesFromLogNote = "Run metadata does not report installed runtime versions; they are read from the job log when one is available."

// servicesNotInRunNote keeps an empty services section from being read as a
// claim. A job may well have had service containers; the run API does not say
// so, and silence about them is not evidence they were absent.
const servicesNotInRunNote = "Run metadata does not report service containers; run `nyrvo ci capture <job>` to read the ones the workflow declares."
