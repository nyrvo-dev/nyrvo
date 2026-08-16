package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/ci/githubactions"
)

// ReplayText renders a replay plan for a human reader: prerequisites first,
// then the numbered steps in order with commands indented so they can be
// copied, and any step Nyrvo cannot reproduce clearly marked with its reason.
// The labels under a step are tab-separated so they line up under one column,
// the same way the doctor renderer aligns its finding details.
//
// An empty plan is told apart from a silent one: a job with no steps gets a
// sentence saying so, and a plan with nothing at all gets a short message,
// never a blank screen.
func ReplayText(w io.Writer, plan githubactions.ReplayPlan) error {
	if plan.Job == "" && len(plan.Steps) == 0 && len(plan.Prerequisites) == 0 && len(plan.Notes) == 0 {
		_, err := fmt.Fprintln(w, "Nothing to reproduce.")
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "REPLAY %s\n", plan.Job)

	if len(plan.Prerequisites) > 0 {
		b.WriteString("\nPREREQUISITES\n")
		for _, p := range plan.Prerequisites {
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}

	if len(plan.Steps) == 0 {
		b.WriteString("\nThis job declares no steps, so there is nothing to reproduce locally.\n")
	} else {
		b.WriteString("\nSTEPS\n")
		for i, s := range plan.Steps {
			fmt.Fprintf(&b, "\n%d. %s\n", i+1, s.Name)
			if s.Action != "" {
				fmt.Fprintf(&b, "   uses\t%s\n", s.Action)
			}
			if s.Command != "" {
				b.WriteString("   run\n")
				// A block command ends with a newline in the file, which would
				// otherwise print as a trailing line of bare indentation.
				for _, line := range strings.Split(strings.TrimRight(s.Command, "\n"), "\n") {
					fmt.Fprintf(&b, "     %s\n", line)
				}
			}
			if s.WorkingDirectory != "" {
				fmt.Fprintf(&b, "   in\t%s\n", s.WorkingDirectory)
			}
			for _, e := range s.Env {
				fmt.Fprintf(&b, "   env\t%s\n", e)
			}
			if s.Reason != "" {
				fmt.Fprintf(&b, "   not reproducible\t%s\n", s.Reason)
			}
		}
	}

	if len(plan.Notes) > 0 {
		b.WriteString("\nNOTES\n")
		for _, n := range plan.Notes {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
	}

	return writeAligned(w, b.String())
}

// ReplayJSON renders the plan for a machine. The plan carries its own JSON
// tags, so the document is the plan itself, encoded with the same shape every
// other --json form uses.
func ReplayJSON(w io.Writer, plan githubactions.ReplayPlan) error {
	return JSON(w, plan)
}
