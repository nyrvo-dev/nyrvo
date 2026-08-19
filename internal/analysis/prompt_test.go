package analysis

import (
	"strings"
	"testing"

	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

func TestPromptIsDeterministic(t *testing.T) {
	in := Input{
		A: &snapshot.Snapshot{
			Name:        "laptop",
			Source:      &snapshot.Source{Kind: snapshot.SourceLocal},
			Environment: &snapshot.Environment{Names: []string{"DATABASE_URL", "HOME", "PATH", "REDIS_URL"}},
		},
		B: &snapshot.Snapshot{
			Name:        "ci",
			Source:      &snapshot.Source{Kind: snapshot.SourceGitHubActionsRun},
			Environment: &snapshot.Environment{Names: []string{"CI", "HOME", "PATH"}},
		},
		Differences: []diff.Difference{
			{Component: diff.ComponentEnvironment, Key: "DATABASE_URL", Kind: diff.KindOnlyInA, A: "present"},
			{Component: diff.ComponentRuntime, Key: "go", Kind: diff.KindChanged, A: "1.26.1", B: "1.25.5"},
			{Component: diff.ComponentEnvironment, Key: "CI", Kind: diff.KindOnlyInB, B: "present"},
		},
	}

	first := Prompt(in)
	second := Prompt(in)
	if first != second {
		t.Fatal("Prompt returned different bytes for the same input")
	}
}

func TestPromptStatesGitHubActionsProvenanceAndAbsentSections(t *testing.T) {
	in := Input{
		A: &snapshot.Snapshot{Name: "local", Source: &snapshot.Source{Kind: snapshot.SourceLocal}, Docker: &snapshot.Docker{ClientVersion: "28.0.0"}},
		B: &snapshot.Snapshot{Name: "declared-ci", Source: &snapshot.Source{Kind: snapshot.SourceGitHubActions}},
	}

	got := Prompt(in)
	for _, want := range []string{
		"source kind=github-actions",
		"weaker evidence describing what a job is expected to get, not what actually happened",
		"B: name=declared-ci",
		"docker: not observed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Prompt() does not contain %q", want)
		}
	}
}

func TestPromptIncludesFindingsAndNotes(t *testing.T) {
	note := "conclusion=failure\nfailed step=Run integration tests\nerror=connection refused"
	in := Input{
		A: &snapshot.Snapshot{Name: "local"},
		B: &snapshot.Snapshot{Name: "ci"},
		Findings: []finding.Finding{{
			Rule:           finding.RuntimeRequirementUnsatisfied,
			Severity:       finding.SeverityHigh,
			Description:    "CI does not satisfy the declared Go version",
			Recommendation: "install Go 1.26",
		}},
		Notes: []string{note},
	}

	got := Prompt(in)
	for _, want := range []string{
		"severity=high rule=runtime.requirement_unsatisfied",
		"CI does not satisfy the declared Go version",
		"recommendation: install Go 1.26",
		"WHAT THE SNAPSHOTS REPORTED ABOUT THEMSELVES",
		note,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Prompt() does not contain %q", want)
		}
	}
}

func TestPromptWarnsOnlyForPartialEnvironment(t *testing.T) {
	base := Input{A: &snapshot.Snapshot{Name: "local"}, B: &snapshot.Snapshot{Name: "ci"}}
	const warning = "A variable absent from that list is not evidence that the variable was absent"

	if got := Prompt(base); strings.Contains(got, warning) {
		t.Errorf("Prompt() warned about a complete environment list: %q", warning)
	}
	base.PartialEnvironment = true
	if got := Prompt(base); !strings.Contains(got, warning) {
		t.Errorf("Prompt() does not contain partial-environment warning %q", warning)
	}
}

func TestPromptStatesAnswerConstraints(t *testing.T) {
	got := Prompt(Input{})
	for _, want := range []string{
		"values are absent by design and will never be supplied",
		"Do not ask for them or assume anything about their contents",
		"Do not invent facts that are not in this evidence",
		"say so instead of guessing",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Prompt() does not contain %q", want)
		}
	}
}

func TestPromptHandlesNoDifferencesOrFindings(t *testing.T) {
	got := Prompt(Input{A: &snapshot.Snapshot{Name: "local"}, B: &snapshot.Snapshot{Name: "ci"}})
	for _, want := range []string{"Nyrvo found no semantic differences", "Nyrvo reached no findings of its own", "supplied evidence does not identify an environmental explanation"} {
		if !strings.Contains(got, want) {
			t.Errorf("Prompt() does not contain %q", want)
		}
	}
}

func TestPromptCarriesTheInstallPath(t *testing.T) {
	got := Prompt(Input{
		A: &snapshot.Snapshot{
			Name:     "local",
			Runtimes: []snapshot.Runtime{{Name: "node", Version: "24.4.0", Path: "~/.nvm/bin/node"}},
		},
		B: &snapshot.Snapshot{Name: "ci"},
	})
	// Same version from a different install is drift an agent can only see if
	// the path is in front of it.
	if !strings.Contains(got, "path=~/.nvm/bin/node") {
		t.Errorf("Prompt() dropped the runtime path:\n%s", got)
	}
}

// A runtime that refused to answer is neither absent nor measured (ADR 0017's
// third state). Without it the agent reads absence while the finding below it
// says the opposite, so both the unusable refusal and the unmeasured probe are
// printed next to the runtimes.
func TestPromptReportsUnmeasuredAndUnusable(t *testing.T) {
	got := Prompt(Input{
		A: &snapshot.Snapshot{Name: "laptop"},
		B: &snapshot.Snapshot{
			Name:       "ci",
			Runtimes:   []snapshot.Runtime{{Name: "go", Version: "1.26.0"}},
			Unmeasured: []string{"runtime.npm"},
			Unusable:   []string{"runtime.dotnet"},
		},
	})
	for _, want := range []string{
		"runtimes installed but refusing to report a version",
		"runtime.dotnet",
		"probes that did not answer (left unmeasured)",
		"runtime.npm",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Prompt() does not contain %q:\n%s", want, got)
		}
	}
}

// The difference line carries the same third state the environment block does.
// An empty side normally renders "(not recorded)", which an agent reads as
// absence — the exact untruth the unusable flag exists to prevent, restated on
// the line a reader looks at first.
func TestPromptDifferenceLineNamesARefusal(t *testing.T) {
	got := Prompt(Input{
		A: &snapshot.Snapshot{Name: "laptop"},
		B: &snapshot.Snapshot{Name: "ci"},
		Differences: []diff.Difference{{
			Component: "runtime",
			Key:       "dotnet",
			Kind:      diff.KindOnlyInA,
			A:         "8.0.100",
			BUnusable: true,
		}},
	})
	if !strings.Contains(got, "B=installed, not usable") {
		t.Errorf("difference line does not name the refusal:\n%s", got)
	}
	if strings.Contains(got, "B=(not recorded)") {
		t.Errorf("difference line still reads a refusal as absence:\n%s", got)
	}
}

func TestPromptTiesFindingsToTheEvidence(t *testing.T) {
	got := Prompt(Input{
		A: &snapshot.Snapshot{Name: "local"},
		B: &snapshot.Snapshot{Name: "ci"},
		Findings: []finding.Finding{{
			Rule:      finding.RuntimeVersionMismatch,
			Severity:  finding.SeverityHigh,
			Component: diff.ComponentRuntime,
			Key:       "node",
			Expected:  "24.4.0",
			Actual:    "22.0.0",
		}},
	})
	for _, want := range []string{"component=runtime key=node", "expected=24.4.0 actual=22.0.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("Prompt() does not contain %q:\n%s", want, got)
		}
	}
}

func TestPromptKeepsAnAbsentRecommendationSilent(t *testing.T) {
	got := Prompt(Input{
		A:        &snapshot.Snapshot{Name: "local"},
		B:        &snapshot.Snapshot{Name: "ci"},
		Findings: []finding.Finding{{Rule: finding.GitDirty, Severity: finding.SeverityMedium, Description: "the working tree is dirty"}},
	})
	// Nyrvo prefers no advice to invented advice; printing an empty slot invites
	// the agent to fill it.
	if strings.Contains(got, "recommendation") {
		t.Errorf("Prompt() announced a recommendation that does not exist:\n%s", got)
	}
}

func TestPromptDistinguishesAWholeComponentDifference(t *testing.T) {
	got := Prompt(Input{
		A:           &snapshot.Snapshot{Name: "local"},
		B:           &snapshot.Snapshot{Name: "ci"},
		Differences: []diff.Difference{{Component: diff.ComponentGit, Kind: diff.KindOnlyInA, A: "described"}},
	})
	if !strings.Contains(got, "the whole component, not one value") {
		t.Errorf("Prompt() reported an absent key as an unrecorded one:\n%s", got)
	}
	if strings.Contains(got, "key=(not recorded)") {
		t.Errorf("Prompt() reported a whole-component difference as a gap in Nyrvo's bookkeeping:\n%s", got)
	}
}

func TestPromptCarriesServices(t *testing.T) {
	// A suite that cannot reach its database fails for a reason no version
	// comparison shows. Leaving services out sent the agent to the wrong
	// evidence.
	got := Prompt(Input{
		A: &snapshot.Snapshot{
			Name:     "local",
			Services: []snapshot.Service{{Image: "axllent/mailpit:latest", Ports: []string{"1025"}}},
		},
		B: &snapshot.Snapshot{
			Name:     "ci",
			Services: []snapshot.Service{{ID: "db", Image: "postgres:16"}},
		},
	})
	for _, want := range []string{
		"postgres:16 (reached as db)",
		"axllent/mailpit:latest",
		"published on 1025",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Prompt() does not contain %q:\n%s", want, got)
		}
	}
}
