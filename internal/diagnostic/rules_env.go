package diagnostic

import (
	"fmt"

	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
)

// EnvironmentRules returns the rules that diagnose the environment section.
func EnvironmentRules() []Rule {
	return []Rule{
		{
			ID: finding.EnvMissing,
			Evaluate: func(in Input) []finding.Finding {
				return envMissing(in)
			},
		},
	}
}

// envMissing reports a variable one side declares that the other does not have.
//
// The rule is driven from the diff, which already drops one-sided variables
// when the side lacking them has a partial list: a workflow states only the
// variables it sets, so its list cannot testify to absence. Re-deriving absence
// from the snapshots would reintroduce exactly the noise the diff suppresses.
func envMissing(in Input) []finding.Finding {
	if in.Diff == nil {
		return nil
	}
	var out []finding.Finding
	for _, d := range in.Diff.Differences {
		if d.Component != diff.ComponentEnvironment || d.Key == "" {
			// An empty key is a whole-section difference: one side described
			// its environment and the other never did. That is "not observed",
			// not "lacking this variable", so it is ignored.
			continue
		}
		switch d.Kind {
		case diff.KindOnlyInA, diff.KindOnlyInB:
			hasSide, lacksSide := in.A, in.B
			expected := d.A
			if d.Kind == diff.KindOnlyInB {
				hasSide, lacksSide = in.B, in.A
				expected = d.B
			}
			out = append(out, finding.Finding{
				Rule:           finding.EnvMissing,
				Severity:       finding.SeverityMedium,
				Component:      diff.ComponentEnvironment,
				Key:            d.Key,
				Expected:       expected,
				Actual:         "missing",
				Description:    fmt.Sprintf("%s sets %s, but %s does not.", Name(hasSide), d.Key, Name(lacksSide)),
				Recommendation: fmt.Sprintf("Set %s in %s, or confirm it is genuinely not required there.", d.Key, Name(lacksSide)),
			})
		}
	}
	return out
}
