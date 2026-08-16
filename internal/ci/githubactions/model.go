// Package githubactions reads GitHub Actions workflow files and describes the
// environment a job is expected to run in.
//
// Nyrvo parses CI configuration; it does not run it. A workflow file is a
// declaration of intent, so everything derived from one is an expectation, not
// an observation — snapshots built here are marked with
// snapshot.SourceGitHubActions to keep that distinction visible.
//
// The model below is deliberately a subset. GitHub Actions is large, and
// pretending to understand a feature is worse than admitting the gap: anything
// recognized but not modelled is recorded in Notes and surfaces to the user.
package githubactions

// Workflow is the parsed subset of one workflow file.
type Workflow struct {
	// Path is the file the workflow was read from, used to locate findings.
	Path string
	Name string
	Jobs []Job
	// Notes records workflow-level constructs Nyrvo saw but does not model.
	Notes []string
}

// Job is one job in a workflow: the unit that maps to an environment.
type Job struct {
	// ID is the key under `jobs:`, the stable identifier a user passes on the
	// command line. Name is the optional human label.
	ID   string
	Name string
	// RunsOn holds the raw runner labels ("ubuntu-latest"). It is a slice
	// because `runs-on` accepts a list, and the values stay unresolved: an
	// expression such as ${{ matrix.os }} is kept verbatim rather than guessed.
	RunsOn []string
	// Container is the job container image, empty when the job runs directly
	// on the runner. A container replaces the runner's OS, so it outranks
	// RunsOn when deciding what the job actually executes in.
	Container string
	Services  []Service
	// Env holds job-level environment declarations as written in the file.
	// Values live here because they are literal file content, already visible
	// in the repository; they are dropped when a snapshot is built, since a
	// snapshot never stores environment values.
	Env   map[string]string
	Steps []Step
	// Matrix holds simple scalar matrices only: matrix.<key> -> values. A
	// matrix with include/exclude, objects, or expressions is left empty and
	// reported in Notes, because a wrong expansion would describe an
	// environment that never existed.
	Matrix map[string][]string
	// Notes records job-level constructs Nyrvo saw but does not model.
	Notes []string
}

// Service is a service container declared by a job, such as a database the
// tests connect to.
type Service struct {
	// ID is the key under `services:` ("postgres"), which is also the hostname
	// the job reaches it by.
	ID    string
	Image string
	Env   map[string]string
}

// Step is one step of a job.
type Step struct {
	Name string
	// Uses is the action reference as written ("actions/setup-node@v4"). It is
	// not resolved or fetched: Nyrvo reads configuration, it does not run it.
	Uses string
	With map[string]string
	// Run is the shell body of a run step, kept verbatim. It is evidence for
	// later diagnostics (which commands CI actually executes) and is never
	// executed by Nyrvo.
	Run              string
	Env              map[string]string
	WorkingDirectory string
}

// Job returns the job with the given id, or nil when absent.
func (w *Workflow) Job(id string) *Job {
	for i := range w.Jobs {
		if w.Jobs[i].ID == id {
			return &w.Jobs[i]
		}
	}
	return nil
}
