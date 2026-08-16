package diagnostic

import (
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// evalRule runs the one rule carrying id against the input, so a test asserts
// on a single rule without noise from the rest of the default set.
func evalRule(t *testing.T, id string, in Input) []finding.Finding {
	t.Helper()
	for _, r := range DefaultRules() {
		if r.ID == id {
			return r.Evaluate(in)
		}
	}
	t.Fatalf("no rule with id %q", id)
	return nil
}

// rt is shorthand for a runtime struct literal in table rows.
func rt(name, version string) snapshot.Runtime {
	return snapshot.Runtime{Name: name, Version: version}
}

// runtimeSnap builds an observed snapshot carrying the given runtimes.
func runtimeSnap(name string, runtimes ...snapshot.Runtime) *snapshot.Snapshot {
	s := snapshot.New(name, time.Time{})
	s.Runtimes = runtimes
	return s
}

// declaredSnap builds a workflow-derived snapshot, so rules see one side that
// states intent rather than fact.
func declaredSnap(name string, runtimes ...snapshot.Runtime) *snapshot.Snapshot {
	s := runtimeSnap(name, runtimes...)
	s.Source = &snapshot.Source{Kind: snapshot.SourceGitHubActions, Ref: "ci.yml#test"}
	return s
}

func TestRuntimeVersionMismatch(t *testing.T) {
	tests := []struct {
		name     string
		a, b     *snapshot.Snapshot
		want     int
		severity finding.Severity
		expected string
		actual   string
	}{
		{"observed major gap",
			runtimeSnap("a", rt("node", "20.11.1")), runtimeSnap("b", rt("node", "22.0.0")),
			1, finding.SeverityHigh, "20.11.1", "22.0.0"},
		{"declared prefix satisfied",
			declaredSnap("ci", rt("node", "20")), runtimeSnap("local", rt("node", "20.11.1")),
			0, "", "", ""},
		{"declared on the other side satisfied",
			runtimeSnap("local", rt("node", "1.26.6")), declaredSnap("ci", rt("node", "1.26")),
			0, "", "", ""},
		{"declared major mismatch",
			declaredSnap("ci", rt("node", "20")), runtimeSnap("local", rt("node", "22")),
			1, finding.SeverityHigh, "20", "22"},
		{"declared minor mismatch",
			declaredSnap("ci", rt("node", "1.25")), runtimeSnap("local", rt("node", "1.26.6")),
			1, finding.SeverityMedium, "1.25", "1.26.6"},
		{"declared patch mismatch",
			declaredSnap("ci", rt("node", "20.11.1")), runtimeSnap("local", rt("node", "20.11.2")),
			1, finding.SeverityLow, "20.11.1", "20.11.2"},
		{"declared more precise than observed",
			declaredSnap("ci", rt("node", "1.26.6")), runtimeSnap("local", rt("node", "1.26")),
			1, finding.SeverityLow, "1.26.6", "1.26"},
		{"identical observed versions",
			runtimeSnap("a", rt("node", "20.11.1")), runtimeSnap("b", rt("node", "20.11.1")),
			0, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Input{A: tt.a, B: tt.b, Diff: diff.Compare(tt.a, tt.b)}
			findings := evalRule(t, finding.RuntimeVersionMismatch, in)
			if len(findings) != tt.want {
				t.Fatalf("findings = %d, want %d: %+v", len(findings), tt.want, findings)
			}
			if tt.want == 0 {
				return
			}
			f := findings[0]
			if f.Rule != finding.RuntimeVersionMismatch || f.Severity != tt.severity ||
				f.Component != diff.ComponentRuntime || f.Key != "node" ||
				f.Expected != tt.expected || f.Actual != tt.actual {
				t.Errorf("finding = %+v", f)
			}
			if f.Description == "" {
				t.Error("description empty")
			}
		})
	}
}

func TestRuntimeMissing(t *testing.T) {
	tests := []struct {
		name        string
		a, b        *snapshot.Snapshot
		want        int
		key         string
		expected    string
		description string // substring the finding's description must contain
	}{
		{"observed side lacks one runtime",
			runtimeSnap("a", rt("node", "20"), rt("go", "1.26")), runtimeSnap("b", rt("node", "20")),
			1, "go", "1.26", "b does not"},
		{"workflow lacks a runtime",
			runtimeSnap("local", rt("node", "20"), rt("go", "1.26")), declaredSnap("ci", rt("node", "20")),
			1, "go", "1.26", "does not set up"},
		{"workflow sets up a runtime the machine lacks",
			runtimeSnap("local", rt("node", "20")), declaredSnap("ci", rt("node", "20"), rt("go", "1.26")),
			1, "go", "1.26", "sets up go"},
		{"identical runtimes",
			runtimeSnap("a", rt("node", "20")), runtimeSnap("b", rt("node", "20")),
			0, "", "", ""},
		{"whole section absent is silence, not missing",
			runtimeSnap("a", rt("node", "20")), runtimeSnap("b"),
			0, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Input{A: tt.a, B: tt.b, Diff: diff.Compare(tt.a, tt.b)}
			findings := evalRule(t, finding.RuntimeMissing, in)
			if len(findings) != tt.want {
				t.Fatalf("findings = %d, want %d: %+v", len(findings), tt.want, findings)
			}
			if tt.want == 0 {
				return
			}
			f := findings[0]
			if f.Rule != finding.RuntimeMissing || f.Severity != finding.SeverityMedium ||
				f.Component != diff.ComponentRuntime || f.Key != tt.key ||
				f.Expected != tt.expected || f.Actual != "missing" {
				t.Errorf("finding = %+v", f)
			}
			if tt.description != "" && !strings.Contains(f.Description, tt.description) {
				t.Errorf("description %q missing %q", f.Description, tt.description)
			}
		})
	}
}

func TestRuntimeRulesNilInput(t *testing.T) {
	if got := evalRule(t, finding.RuntimeVersionMismatch, Input{}); len(got) != 0 {
		t.Errorf("version mismatch on empty input: %+v", got)
	}
	if got := evalRule(t, finding.RuntimeMissing, Input{}); len(got) != 0 {
		t.Errorf("missing on empty input: %+v", got)
	}
	empty := diff.Compare(nil, nil)
	if got := evalRule(t, finding.RuntimeVersionMismatch, Input{Diff: empty}); len(got) != 0 {
		t.Errorf("version mismatch on empty diff: %+v", got)
	}
	if got := evalRule(t, finding.RuntimeMissing, Input{Diff: empty}); len(got) != 0 {
		t.Errorf("missing on empty diff: %+v", got)
	}
}

// A snapshot imported from a run is an observation, but a partial one: its
// runtimes come from the setup actions the log shows, never from an inventory
// of the runner image. The finding must therefore describe the evidence, not
// claim the machine lacks the runtime.
func TestRuntimeMissingDoesNotOverclaimAgainstARun(t *testing.T) {
	local := snapshot.New("local", time.Now())
	local.Runtimes = []snapshot.Runtime{{Name: "node", Version: "23.6.0"}, {Name: "go", Version: "1.26.6"}}

	ci := snapshot.New("ci", time.Now())
	ci.Source = &snapshot.Source{Kind: snapshot.SourceGitHubActionsRun, Ref: "owner/repo run 1"}
	ci.Runtimes = []snapshot.Runtime{{Name: "go", Version: "1.26.6"}}

	findings := Run(RuntimeRules(), Input{A: local, B: ci, Diff: diff.Compare(local, ci)})
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one for the missing node", findings)
	}
	f := findings[0]
	if f.Rule != finding.RuntimeMissing || f.Key != "node" {
		t.Fatalf("finding = %+v, want runtime.missing for node", f)
	}
	// The wording must not assert the runner lacks node — only that nothing in
	// the run set it up.
	if strings.Contains(f.Description, "does not have") || strings.Contains(f.Description, "does not.") {
		t.Errorf("description overclaims what the run proves: %q", f.Description)
	}
	if !strings.Contains(f.Description, "may still provide") {
		t.Errorf("description should say the image may still provide it: %q", f.Description)
	}
}
