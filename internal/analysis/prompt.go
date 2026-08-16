package analysis

import (
	"fmt"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// Prompt renders the evidence before Nyrvo's conclusions and the user's
// question. This order gives an agent the observations needed to assess those
// conclusions instead of anchoring it on them first.
func Prompt(in Input) string {
	var b strings.Builder
	b.WriteString("NYRVO ANALYSIS REQUEST\n\n")
	b.WriteString("Nyrvo is a local-first tool that captures execution environments as deterministic snapshots and compares them semantically. It has already captured these two environments and compared them. Reason only about the evidence below; do not inspect or ask to inspect either machine.\n\n")

	b.WriteString("ENVIRONMENTS AND OBSERVED FACTS\n")
	writeEnvironment(&b, "A", in.A)
	writeEnvironment(&b, "B", in.B)

	b.WriteString("\nDIFFERENCES\n")
	if len(in.Differences) == 0 {
		b.WriteString("Nyrvo found no semantic differences.\n")
	} else {
		for _, d := range in.Differences {
			fmt.Fprintf(&b, "- kind=%s %s A=%s B=%s\n", d.Kind, located(d.Component, d.Key), display(d.A), display(d.B))
		}
	}

	b.WriteString("\nNYRVO'S FINDINGS (OPEN TO DISAGREEMENT)\n")
	if len(in.Findings) == 0 {
		b.WriteString("Nyrvo reached no findings of its own.\n")
	} else {
		for _, f := range in.Findings {
			fmt.Fprintf(&b, "- severity=%s rule=%s %s: %s\n", f.Severity, display(f.Rule), located(f.Component, f.Key), display(f.Description))
			// Component, key, expected and actual are what ties a finding back to
			// the difference it came from. Without them an agent is asked to
			// weigh a conclusion it cannot trace to any evidence above.
			if f.Expected != "" || f.Actual != "" {
				fmt.Fprintf(&b, "    expected=%s actual=%s\n", display(f.Expected), display(f.Actual))
			}
			// An absent recommendation is a deliberate silence: Nyrvo prefers no
			// advice to invented advice, and printing "(not recorded)" would
			// invite an agent to fill the gap.
			if f.Recommendation != "" {
				fmt.Fprintf(&b, "    recommendation: %s\n", f.Recommendation)
			}
		}
	}

	b.WriteString("\nWHAT THE SNAPSHOTS REPORTED ABOUT THEMSELVES\n")
	if len(in.Notes) == 0 {
		b.WriteString("No snapshot notes were reported.\n")
	} else {
		for _, note := range in.Notes {
			b.WriteString("--- note ---\n")
			b.WriteString(note)
			b.WriteByte('\n')
		}
	}

	if in.PartialEnvironment {
		b.WriteString("\nINCOMPLETE ENVIRONMENT EVIDENCE\nOne side's environment variable list is incomplete. A variable absent from that list is not evidence that the variable was absent from that environment.\n")
	}

	b.WriteString("\nANSWER CONSTRAINTS\n")
	b.WriteString("- Environment variable values are absent by design and will never be supplied. Do not ask for them or assume anything about their contents.\n")
	b.WriteString("- Do not invent facts that are not in this evidence. When the evidence cannot decide something, say so instead of guessing.\n")
	b.WriteString("\nQUESTION\nWhich of these differences most plausibly explains the failure? Rank the candidates and give the specific evidence each ranking rests on. If Nyrvo found no differences, say that the supplied evidence does not identify an environmental explanation.\n")
	return b.String()
}

func writeEnvironment(b *strings.Builder, label string, s *snapshot.Snapshot) {
	if s == nil {
		fmt.Fprintf(b, "\n%s: name=(not recorded); source kind=(not recorded)\n", label)
		b.WriteString("  system: not observed\n  git: not observed\n  docker: not observed\n  runtimes: not observed\n  services: not observed\n  project requirements: not observed\n  environment variable names: not observed\n")
		return
	}

	fmt.Fprintf(b, "\n%s: name=%s; source kind=%s", label, display(s.Name), sourceKind(s))
	if s.Source != nil && s.Source.Ref != "" {
		fmt.Fprintf(b, "; source ref=%s", s.Source.Ref)
	}
	b.WriteByte('\n')
	b.WriteString("  provenance: " + sourceProvenance(s) + "\n")

	if s.System == nil {
		b.WriteString("  system: not observed\n")
	} else {
		fmt.Fprintf(b, "  system: os=%s arch=%s kernel=%s\n", display(s.System.OS), display(s.System.Arch), display(s.System.Kernel))
	}
	if s.Git == nil {
		b.WriteString("  git: not observed\n")
	} else {
		fmt.Fprintf(b, "  git: sha=%s branch=%s dirty=%t\n", display(s.Git.SHA), display(s.Git.Branch), s.Git.Dirty)
	}
	if s.Docker == nil {
		b.WriteString("  docker: not observed\n")
	} else {
		fmt.Fprintf(b, "  docker: client=%s server=%s daemon_running=%t compose=%s\n", display(s.Docker.ClientVersion), display(s.Docker.ServerVersion), s.Docker.DaemonRunning, display(s.Docker.ComposeVersion))
	}
	if len(s.Runtimes) == 0 {
		b.WriteString("  runtimes: not observed\n")
	} else {
		b.WriteString("  runtimes:\n")
		for _, rt := range s.Runtimes {
			// The install path is worth its line: two environments running the
			// same version from different installs is a real cause of drift, and
			// the path is already sanitized on the way into the Input.
			fmt.Fprintf(b, "    - %s version=%s path=%s\n", display(rt.Name), display(rt.Version), display(rt.Path))
		}
	}
	// A job's backing containers are frequently the whole answer — a suite that
	// cannot reach a database fails for a reason no version comparison shows —
	// so omitting them left an agent reasoning about the wrong evidence.
	if len(s.Services) == 0 {
		b.WriteString("  services: none observed or declared\n")
	} else {
		b.WriteString("  services:\n")
		for _, svc := range s.Services {
			line := "    - " + display(svc.Image)
			if svc.ID != "" {
				line += " (reached as " + svc.ID + ")"
			}
			if len(svc.Ports) > 0 {
				line += " published on " + strings.Join(svc.Ports, ", ")
			}
			b.WriteString(line + "\n")
		}
	}
	if len(s.Requirements) == 0 {
		b.WriteString("  project requirements: not observed\n")
	} else {
		b.WriteString("  project requirements:\n")
		for _, requirement := range s.Requirements {
			fmt.Fprintf(b, "    - %s constraint=%s source=%s\n", display(requirement.Runtime), display(requirement.Constraint), display(requirement.Source))
		}
	}
	if s.Environment == nil {
		b.WriteString("  environment variable names: not observed\n")
	} else if len(s.Environment.Names) == 0 {
		// The list was observed; it is this document that carries only the names
		// the diagnosis refers to. "None recorded" would read as a machine with
		// no environment at all, which is never true.
		b.WriteString("  environment variable names: none that this diagnosis refers to; the rest are withheld, not absent\n")
	} else {
		fmt.Fprintf(b, "  environment variable names: %s\n", strings.Join(s.Environment.Names, ", "))
	}
}

func sourceKind(s *snapshot.Snapshot) string {
	if s.Source == nil || s.Source.Kind == "" {
		return "(not recorded)"
	}
	return s.Source.Kind
}

func sourceProvenance(s *snapshot.Snapshot) string {
	if s.Source == nil {
		return "not recorded; its evidentiary weight is unknown"
	}
	switch s.Source.Kind {
	case snapshot.SourceLocal:
		return "local observations captured on the machine itself"
	case snapshot.SourceGitHubActions:
		return "derived from a GitHub Actions workflow file; this is weaker evidence describing what a job is expected to get, not what actually happened"
	case snapshot.SourceGitHubActionsRun:
		return "derived from a GitHub Actions run that actually happened; it reports the environment the job received"
	default:
		return "unrecognized source kind; do not assume how directly it was observed"
	}
}

// located names where an observation sits. An empty key is not a missing key:
// it means the difference is about the whole component — one side described it
// and the other said nothing at all — and "(not recorded)" would report that as
// a gap in Nyrvo's own bookkeeping.
func located(component, key string) string {
	if key == "" {
		return fmt.Sprintf("component=%s (the whole component, not one value)", display(component))
	}
	return fmt.Sprintf("component=%s key=%s", display(component), key)
}

func display(value string) string {
	if value == "" {
		return "(not recorded)"
	}
	return value
}
