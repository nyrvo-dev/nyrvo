package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/ci/githubactions"
	"github.com/nyrvo-dev/nyrvo/internal/output"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// ciSnapshotName is the name a captured CI environment is stored under, so the
// documented `nyrvo diff local ci` works without the user inventing a name.
const ciSnapshotName = "ci"

// newCIClient builds the GitHub client. It is a variable so tests can supply a
// client with a stubbed Exec and drive the whole import command without a
// network or a token.
var newCIClient = func() *githubactions.Client { return &githubactions.Client{} }

func runCI(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageErr("ci needs a subcommand: inspect, capture or import")
	}
	switch args[0] {
	case "inspect":
		return runCIInspect(args[1:], stdout)
	case "capture":
		return runCICapture(args[1:], stdout)
	case "import":
		return runCIImport(ctx, args[1:], stdout)
	default:
		return usageErr("unknown ci subcommand %q", args[0])
	}
}

// runCIImport turns a run that actually happened into the "ci" snapshot.
//
// It replaces any previous "ci" snapshot on purpose: a real run is strictly
// better evidence about the same environment than the workflow file's
// declaration, and keeping two names would make `nyrvo doctor` ambiguous for
// the sake of a distinction the snapshot's own source already records.
func runCIImport(ctx context.Context, args []string, stdout io.Writer) error {
	flags, operands := splitFlags(args)
	if len(flags) > 0 {
		return usageErr("ci import takes no flags")
	}
	if len(operands) < 1 || len(operands) > 2 {
		return usageErr("ci import takes a run id or run URL, and optionally a job name")
	}

	imported, err := importRun(ctx, operands[0], operands[1:])
	if err != nil {
		return err
	}

	if err := snapshot.NewStore("").Save(imported.Snapshot); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(stdout, "Imported %s job %q (%s).\nSnapshot saved: %s, replacing any previous %s snapshot.\n",
		imported.Ref, imported.Job, imported.Why, ciSnapshotName, ciSnapshotName); err != nil {
		return err
	}
	if imported.LogNote != "" {
		if _, err := fmt.Fprintf(stdout, "\nWarning: %s\n", imported.LogNote); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(stdout, "\nDiagnose it against this machine:\n  nyrvo capture local\n  nyrvo doctor\n")
	return err
}

// importedRun is a run turned into a snapshot, with what the user should be
// told about how it was chosen.
type importedRun struct {
	Snapshot *snapshot.Snapshot
	// Ref, Job and Why describe the selection: which run, which job, and on
	// what grounds, so the caller can say it rather than leaving the user to
	// wonder which of a run's jobs was diagnosed.
	Ref, Job, Why string
	// LogNote is set when the job log could not be read, in which case the
	// snapshot carries the same sentence.
	LogNote string
}

// importRun fetches a run, picks a job, and builds the snapshot.
//
// It is shared by `ci import`, which saves the result, and `doctor <run>`,
// which diagnoses it without saving: both need exactly the same evidence, and
// duplicating the fetch would let the two commands drift apart.
func importRun(ctx context.Context, ref string, jobOperands []string) (importedRun, error) {
	client := newCIClient()
	runJSON, jobsJSON, runRef, err := client.FetchRun(ctx, ref)
	if err != nil {
		return importedRun{}, err
	}
	run, err := githubactions.ParseRun(runJSON, jobsJSON)
	if err != nil {
		return importedRun{}, fmt.Errorf("%s: %w", runRef, err)
	}

	job, why, err := selectRunJob(run, jobOperands)
	if err != nil {
		return importedRun{}, err
	}

	snap, err := githubactions.RunSnapshot(run, job, ciSnapshotName, time.Now())
	if err != nil {
		return importedRun{}, err
	}

	out := importedRun{Snapshot: snap, Ref: runRef, Job: job.Name, Why: why}

	// The log is what turns an imported run from metadata into evidence: the
	// versions the runner really installed and the error that stopped the job.
	// It is also the part most likely to be unavailable — logs expire, and a
	// token may not reach them — so a log that cannot be read degrades the
	// import to a note instead of failing it.
	if rawLog, logErr := client.FetchJobLog(ctx, run.Repository, job.ID); logErr != nil {
		out.LogNote = "The job log could not be read, so installed runtime versions and the failing output are missing: " + logErr.Error()
		snap.Source.Notes = append(snap.Source.Notes, out.LogNote)
		snap.Normalize()
	} else {
		githubactions.ApplyJobLog(snap, githubactions.ParseJobLog(rawLog))
	}
	return out, nil
}

// selectRunJob picks the job to import and explains the choice.
//
// A run has many jobs and the user is almost always asking about the one that
// failed, so that is the default. Anything ambiguous is refused with the list
// rather than resolved by guessing: importing the wrong job produces a
// diagnosis that looks authoritative and describes something else entirely.
func selectRunJob(run *githubactions.Run, operands []string) (*githubactions.RunJob, string, error) {
	if len(operands) == 1 {
		job := run.Job(operands[0])
		if job == nil {
			return nil, "", fmt.Errorf("run has no job named %q; jobs: %s", operands[0], strings.Join(runJobNames(run.Jobs), ", "))
		}
		return job, "named on the command line", nil
	}

	if failed := run.FailedJobs(); len(failed) == 1 {
		return run.Job(failed[0].Name), "the only job that failed", nil
	} else if len(failed) > 1 {
		return nil, "", usageErr("run has %d failed jobs; name the one to import: %s", len(failed), strings.Join(runJobNames(failed), ", "))
	}

	switch len(run.Jobs) {
	case 0:
		return nil, "", fmt.Errorf("run has no jobs to import")
	case 1:
		return &run.Jobs[0], "the run's only job", nil
	default:
		return nil, "", usageErr("run has %d jobs and none failed; name the one to import: %s", len(run.Jobs), strings.Join(runJobNames(run.Jobs), ", "))
	}
}

func runJobNames(jobs []githubactions.RunJob) []string {
	names := make([]string, 0, len(jobs))
	for _, j := range jobs {
		names = append(names, strconv.Quote(j.Name))
	}
	return names
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
