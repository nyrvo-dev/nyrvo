package cli

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/ci/githubactions"
	"github.com/nyrvo-dev/nyrvo/internal/output"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// ciSnapshotName is the name a captured CI environment is stored under, so the
// documented `nyrvo diff local ci` works without the user inventing a name.
const ciSnapshotName = "ci"

func runCI(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageErr("ci needs a subcommand: inspect or capture")
	}
	switch args[0] {
	case "inspect":
		return runCIInspect(args[1:], stdout)
	case "capture":
		return runCICapture(args[1:], stdout)
	default:
		return usageErr("unknown ci subcommand %q", args[0])
	}
}

// ciJobs reads every workflow and converts every job into the environment it
// declares. Conversion happens up front because a job is only interesting here
// together with what Nyrvo could and could not derive from it.
func ciJobs(now time.Time) ([]output.CIJob, error) {
	workflows, err := githubactions.ParseDir(output.GitHubWorkflowsDir)
	if err != nil {
		return nil, err
	}
	var jobs []output.CIJob
	for _, w := range workflows {
		for i := range w.Jobs {
			job := &w.Jobs[i]
			snap, err := githubactions.Snapshot(w, job, ciSnapshotName, now)
			if err != nil {
				return nil, err
			}
			jobs = append(jobs, output.CIJob{
				Workflow: filepath.Base(w.Path),
				Job:      job.ID,
				Snapshot: snap,
			})
		}
	}
	return jobs, nil
}

func runCIInspect(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("ci inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "write the declared environments as JSON")
	flags, operands := splitFlags(args)
	if err := fs.Parse(flags); err != nil {
		return usageErr("ci inspect: %v", err)
	}
	if len(operands) != 0 {
		return usageErr("ci inspect takes no arguments")
	}

	jobs, err := ciJobs(time.Now())
	if err != nil {
		return err
	}
	if *asJSON {
		return output.JSON(stdout, jobs)
	}
	return output.CIInspectText(stdout, jobs)
}

func runCICapture(args []string, stdout io.Writer) error {
	flags, operands := splitFlags(args)
	if len(flags) > 0 {
		return usageErr("ci capture takes no flags")
	}
	if len(operands) != 1 {
		return usageErr("ci capture takes exactly one job selector")
	}

	jobs, err := ciJobs(time.Now())
	if err != nil {
		return err
	}
	job, err := selectCIJob(jobs, operands[0])
	if err != nil {
		return err
	}

	if err := snapshot.NewStore("").Save(job.Snapshot); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Snapshot saved: %s (from %s job %s)\n\nCompare it with this machine:\n  nyrvo capture local\n  nyrvo diff local %s\n",
		ciSnapshotName, job.Workflow, job.Job, ciSnapshotName)
	return err
}

// selectCIJob resolves a job selector against the parsed jobs.
//
// A bare job id is convenient and usually unique, but "test" in two workflows
// must not silently pick one: an ambiguous selector is rejected with the exact
// alternatives, because capturing the wrong job would produce a diff that looks
// authoritative and is about the wrong CI run.
func selectCIJob(jobs []output.CIJob, selector string) (output.CIJob, error) {
	workflow, id, qualified := strings.Cut(selector, ":")
	if !qualified {
		id = selector
	}

	var matches []output.CIJob
	for _, j := range jobs {
		if j.Job != id {
			continue
		}
		if qualified && j.Workflow != workflow {
			continue
		}
		matches = append(matches, j)
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		if len(jobs) == 0 {
			return output.CIJob{}, fmt.Errorf("no workflow jobs found in %s", output.GitHubWorkflowsDir)
		}
		return output.CIJob{}, fmt.Errorf("no CI job matches %q; available: %s", selector, strings.Join(jobSelectors(jobs), ", "))
	default:
		return output.CIJob{}, usageErr("job %q is declared in more than one workflow; use one of: %s", id, strings.Join(jobSelectors(matches), ", "))
	}
}

func jobSelectors(jobs []output.CIJob) []string {
	out := make([]string, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.Workflow+":"+j.Job)
	}
	return out
}
