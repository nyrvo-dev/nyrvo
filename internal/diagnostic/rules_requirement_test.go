package diagnostic

import (
	"strings"
	"testing"

	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// req is shorthand for a declared requirement in table rows.
func req(runtime, constraint, source string) snapshot.Requirement {
	return snapshot.Requirement{Runtime: runtime, Constraint: constraint, Source: source}
}

// withReqs attaches project requirements to a snapshot, the way the
// requirements collector does for whichever side read the checkout.
func withReqs(s *snapshot.Snapshot, reqs ...snapshot.Requirement) *snapshot.Snapshot {
	s.Requirements = reqs
	return s
}

func TestRequirementUnsatisfied(t *testing.T) {
	tests := []struct {
		name     string
		a, b     *snapshot.Snapshot
		want     int
		expected string
		actual   string
	}{
		{
			// The constitution's own example: the project asks for node 24, the
			// CI job installs 22. Nothing here is a matter of taste.
			"ci runs a version the project forbids",
			withReqs(runtimeSnap("local", rt("node", "24.1.0")), req("node", ">=24", "package.json engines.node")),
			runtimeSnap("ci", rt("node", "22.11.0")),
			1, ">=24", "22.11.0",
		},
		{
			"local violates the project's own requirement",
			withReqs(runtimeSnap("local", rt("node", "18.0.0")), req("node", ">=24", "package.json engines.node")),
			runtimeSnap("ci", rt("node", "24.1.0")),
			1, ">=24", "18.0.0",
		},
		{
			"both sides violate it",
			withReqs(runtimeSnap("local", rt("node", "18.0.0")), req("node", ">=24", "package.json engines.node")),
			runtimeSnap("ci", rt("node", "22.11.0")),
			2, "", "",
		},
		{
			"satisfied requirement is silent",
			withReqs(runtimeSnap("local", rt("node", "24.1.0")), req("node", ">=24", "package.json engines.node")),
			runtimeSnap("ci", rt("node", "24.2.0")),
			0, "", "",
		},
		{
			// A workflow asking for "20" gets whatever 20.x the runner picks, so
			// a constraint that some 20.x would satisfy cannot convict it.
			"declared version too coarse to convict",
			withReqs(runtimeSnap("local", rt("node", "20.5.0")), req("node", "^20.1", ".nvmrc")),
			declaredSnap("ci", rt("node", "20")),
			0, "", "",
		},
		{
			// Coarse or not, a declared major below the floor fails at every
			// version it could resolve to.
			"declared version fails at every resolution",
			withReqs(runtimeSnap("local", rt("node", "24.1.0")), req("node", ">=24", "package.json engines.node")),
			declaredSnap("ci", rt("node", "20")),
			1, ">=24", "20",
		},
		{
			// Unreadable constraints must never produce a finding: Nyrvo does not
			// know what lts/iron resolves to.
			"unparseable constraint is silent",
			withReqs(runtimeSnap("local", rt("node", "18.0.0")), req("node", "lts/iron", ".nvmrc")),
			runtimeSnap("ci", rt("node", "18.0.0")),
			0, "", "",
		},
		{
			// A runtime nobody observed is runtime.missing's business, not this
			// rule's, so the same fact is not charged twice at high severity.
			"requirement for an absent runtime is silent",
			withReqs(runtimeSnap("local", rt("go", "1.25.0")), req("node", ">=24", "package.json engines.node")),
			runtimeSnap("ci", rt("go", "1.25.0")),
			0, "", "",
		},
		{
			// Two sources pinning the same runtime differently are both enforced:
			// finding out they contradict each other is the point.
			"contradicting sources both report",
			withReqs(runtimeSnap("local", rt("node", "22.0.0")),
				req("node", ">=24", "package.json engines.node"),
				req("node", "18.19.0", ".nvmrc")),
			runtimeSnap("ci", rt("node", "24.0.0")),
			3, "", "",
		},
		{
			// Both sides captured the same checkout, so the requirement appears
			// twice and must be enforced once per environment, not twice.
			"identical requirements on both sides collapse",
			withReqs(runtimeSnap("local", rt("node", "22.0.0")), req("node", ">=24", "package.json engines.node")),
			withReqs(runtimeSnap("ci", rt("node", "24.0.0")), req("node", ">=24", "package.json engines.node")),
			1, ">=24", "22.0.0",
		},
		{
			// Diagnosing an environment against itself is a degenerate but legal
			// command; the same violation must be stated once, not once per side.
			"the same environment twice reports once",
			withReqs(runtimeSnap("local", rt("node", "22.0.0")), req("node", ">=24", "package.json engines.node")),
			withReqs(runtimeSnap("local", rt("node", "22.0.0")), req("node", ">=24", "package.json engines.node")),
			1, ">=24", "22.0.0",
		},
		{
			"no requirements at all",
			runtimeSnap("local", rt("node", "18.0.0")),
			runtimeSnap("ci", rt("node", "22.0.0")),
			0, "", "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evalRule(t, finding.RuntimeRequirementUnsatisfied, Input{A: tt.a, B: tt.b})
			if len(got) != tt.want {
				t.Fatalf("got %d findings, want %d: %+v", len(got), tt.want, got)
			}
			if tt.want != 1 {
				return
			}
			f := got[0]
			if f.Severity != finding.SeverityHigh {
				t.Errorf("severity = %q, want high", f.Severity)
			}
			if f.Expected != tt.expected || f.Actual != tt.actual {
				t.Errorf("expected/actual = %q/%q, want %q/%q", f.Expected, f.Actual, tt.expected, tt.actual)
			}
			if f.Key != "node" {
				t.Errorf("key = %q, want node", f.Key)
			}
			// The finding has to name the file that made the claim, or the user
			// cannot tell which of several pins they are being held to.
			if !strings.Contains(f.Description, "package.json") && !strings.Contains(f.Description, ".nvmrc") {
				t.Errorf("description %q names no source", f.Description)
			}
			if f.Recommendation == "" {
				t.Error("finding carries no recommendation")
			}
		})
	}
}

// TestRequirementRecommendationTargetsTheWorkflow checks that a failing CI side
// is told to edit the workflow rather than to install something locally.
func TestRequirementRecommendationTargetsTheWorkflow(t *testing.T) {
	in := Input{
		A: withReqs(runtimeSnap("local", rt("node", "24.1.0")), req("node", ">=24", "package.json engines.node")),
		B: declaredSnap("ci", rt("node", "20")),
	}
	got := evalRule(t, finding.RuntimeRequirementUnsatisfied, in)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	if !strings.Contains(got[0].Recommendation, "ci.yml#test") {
		t.Errorf("recommendation %q does not point at the workflow", got[0].Recommendation)
	}
}

// TestRequirementsNeverCompared guards the boundary between reading and
// judging: requirements describe the project, so two sides carrying different
// ones must not be reported as drift by the diff.
func TestRequirementsNeverCompared(t *testing.T) {
	a := withReqs(runtimeSnap("local", rt("node", "24.1.0")), req("node", ">=24", "package.json engines.node"))
	b := runtimeSnap("ci", rt("node", "24.1.0"))
	d, _ := Analyze(a, b)
	for _, diff := range d.Differences {
		if strings.Contains(diff.Key, "requirement") || diff.Component == "requirements" {
			t.Errorf("diff reported a requirement difference: %+v", diff)
		}
	}
}
