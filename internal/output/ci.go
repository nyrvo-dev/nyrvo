package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// CIJob pairs a CI job with the environment Nyrvo derived from its declaration.
type CIJob struct {
	Workflow string             `json:"workflow"`
	Job      string             `json:"job"`
	Snapshot *snapshot.Snapshot `json:"snapshot"`
}

// CIInspectText renders what CI declares, job by job.
//
// Notes are printed, not hidden: a CI environment is assembled from a
// configuration file Nyrvo only partly models, and a user comparing local
// against CI has to know which parts were skipped before trusting the diff.
func CIInspectText(w io.Writer, jobs []CIJob) error {
	if len(jobs) == 0 {
		_, err := fmt.Fprintf(w, "No workflow jobs found in %s.\n", GitHubWorkflowsDir)
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "CI environments declared in %s\n", GitHubWorkflowsDir)
	for _, j := range jobs {
		fmt.Fprintf(&b, "\n%s  job %s\n", j.Workflow, j.Job)
		snap := j.Snapshot
		if snap.System != nil {
			fmt.Fprintf(&b, "  system     %s/%s\n", snap.System.OS, snap.System.Arch)
		} else {
			// Saying so beats printing nothing: an absent platform is a gap the
			// user can act on, a blank line is one they will not notice.
			b.WriteString("  system     not determined\n")
		}
		if len(snap.Runtimes) > 0 {
			parts := make([]string, 0, len(snap.Runtimes))
			for _, r := range snap.Runtimes {
				parts = append(parts, r.Name+" "+r.Version)
			}
			fmt.Fprintf(&b, "  runtimes   %s\n", strings.Join(parts, ", "))
		} else {
			b.WriteString("  runtimes   none declared\n")
		}
		// Services used to reach this report as "not modelled" notes. Modelling
		// them moved them out of that list, and without a line of their own a
		// job's backing containers would have quietly stopped being shown at
		// all — the report would look complete while saying less than before.
		if len(snap.Services) > 0 {
			parts := make([]string, 0, len(snap.Services))
			for _, s := range snap.Services {
				parts = append(parts, s.ID+" "+s.Image)
			}
			fmt.Fprintf(&b, "  services   %s\n", strings.Join(parts, ", "))
		}
		if snap.Environment != nil && len(snap.Environment.Names) > 0 {
			fmt.Fprintf(&b, "  env        %s\n", strings.Join(snap.Environment.Names, ", "))
		}
		if snap.Source != nil && len(snap.Source.Notes) > 0 {
			b.WriteString("  not modelled\n")
			for _, n := range snap.Source.Notes {
				fmt.Fprintf(&b, "    - %s\n", n)
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// GitHubWorkflowsDir is where GitHub Actions workflows live, relative to the
// repository root. It is referenced in output so the message tells the user
// exactly which directory was read.
const GitHubWorkflowsDir = ".github/workflows"
