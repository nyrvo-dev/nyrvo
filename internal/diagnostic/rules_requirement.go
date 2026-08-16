package diagnostic

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// RequirementRules returns the rules that judge an environment against what the
// project itself declares.
//
// This is the only rule family that can call an environment wrong rather than
// merely different. Every other rule compares two environments and reports that
// they disagree; here the project has stated a requirement, so one side can
// simply fail to meet it.
func RequirementRules() []Rule {
	return []Rule{
		{
			ID: finding.RuntimeRequirementUnsatisfied,
			Evaluate: func(in Input) []finding.Finding {
				return requirementUnsatisfied(in)
			},
		},
	}
}

func requirementUnsatisfied(in Input) []finding.Finding {
	reqs := projectRequirements(in)
	if len(reqs) == 0 {
		return nil
	}

	var out []finding.Finding
	// Findings collapse by value because the two sides may be the same
	// environment named twice, and stating the same violation twice would read
	// as two problems.
	seen := map[finding.Finding]struct{}{}
	// Both sides are checked. Requirements describe the project, not a machine,
	// so a laptop that violates its own package.json is exactly as broken as a
	// CI job that does — and reporting only the remote side would hide the case
	// the user can fix fastest.
	for _, side := range []*snapshot.Snapshot{in.A, in.B} {
		if side == nil {
			continue
		}
		for _, rt := range side.Runtimes {
			for _, req := range reqs {
				if req.Runtime != rt.Name {
					continue
				}
				// A runtime a side never observed is already reported by
				// runtime.missing; repeating it here as a requirement failure
				// would charge the same fact twice at a higher severity.
				if rt.Version == "" {
					continue
				}
				met, decided := requirementMet(req.Constraint, rt.Version, !Declared(side))
				if !decided || met {
					continue
				}
				f := unsatisfiedFinding(in, side, req, rt.Version)
				if _, dup := seen[f]; dup {
					continue
				}
				seen[f] = struct{}{}
				out = append(out, f)
			}
		}
	}
	return out
}

// requirementMet answers whether an observed version meets a constraint, and
// whether the question could be answered.
//
// A declared version is imprecise on purpose: a workflow asking for node "20"
// gets whatever 20.x the runner installs. Judging such a version against a
// constraint means judging the whole range it stands for, so the constraint is
// evaluated at both ends of that range. When the two ends disagree — "20"
// against "^20.1" could go either way — the declaration is too coarse to
// convict, and the rule stays silent rather than guessing.
func requirementMet(constraint, version string, precise bool) (met, decided bool) {
	if precise {
		return SatisfiesConstraint(constraint, version)
	}
	low, lowOK := SatisfiesConstraint(constraint, padVersion(version, 0))
	high, highOK := SatisfiesConstraint(constraint, padVersion(version, maxSegment))
	if !lowOK || !highOK || low != high {
		return false, false
	}
	return low, true
}

// maxSegment stands in for "any version the declaration could resolve to". It
// only has to exceed every real segment number, which it does by orders of
// magnitude.
const maxSegment = 999999

// padVersion extends a version to three segments with the given filler, turning
// a declared prefix into a concrete end of the range it represents.
func padVersion(v string, filler int) string {
	parts := segments(v)
	if len(parts) == 0 {
		return v
	}
	out := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		n := filler
		if i < len(parts) {
			n = parts[i]
		}
		out = append(out, fmt.Sprint(n))
	}
	return strings.Join(out, ".")
}

// projectRequirements gathers the declarations from both snapshots.
//
// Both sides can carry them — two captures of the same checkout do — so
// identical entries collapse. Entries that genuinely differ are all kept and
// all enforced: a project that pins node in .nvmrc and again in package.json
// has to satisfy both, and finding out they contradict each other is worth the
// two findings it costs.
func projectRequirements(in Input) []snapshot.Requirement {
	seen := map[snapshot.Requirement]struct{}{}
	var out []snapshot.Requirement
	for _, s := range []*snapshot.Snapshot{in.A, in.B} {
		if s == nil {
			continue
		}
		for _, r := range s.Requirements {
			if _, dup := seen[r]; dup {
				continue
			}
			seen[r] = struct{}{}
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Runtime != out[j].Runtime {
			return out[i].Runtime < out[j].Runtime
		}
		return out[i].Source < out[j].Source
	})
	return out
}

func unsatisfiedFinding(in Input, side *snapshot.Snapshot, req snapshot.Requirement, version string) finding.Finding {
	f := finding.Finding{
		Rule:      finding.RuntimeRequirementUnsatisfied,
		Severity:  finding.SeverityHigh,
		Component: diff.ComponentRuntime,
		Key:       req.Runtime,
		Expected:  req.Constraint,
		Actual:    version,
		Description: fmt.Sprintf("%s requires %s %s, but %s has %s.",
			req.Source, req.Runtime, req.Constraint, Name(side), version),
	}

	switch {
	case Declared(side):
		f.Recommendation = fmt.Sprintf("Change the %s version input in %s to one that satisfies %s.",
			req.Runtime, declaredRef(in), req.Constraint)
	case CIDerived(side):
		f.Recommendation = fmt.Sprintf("Set up a %s version that satisfies %s in CI, or relax the constraint in %s.",
			req.Runtime, req.Constraint, req.Source)
	default:
		f.Recommendation = fmt.Sprintf("Install a %s version that satisfies %s, or relax the constraint in %s.",
			req.Runtime, req.Constraint, req.Source)
	}
	return f
}
