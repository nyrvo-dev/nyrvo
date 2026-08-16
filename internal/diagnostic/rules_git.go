package diagnostic

import (
	"fmt"

	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// GitRules returns the rules that diagnose the git section.
func GitRules() []Rule {
	return []Rule{
		{
			ID: finding.GitSHAMismatch,
			Evaluate: func(in Input) []finding.Finding {
				return gitSHAMismatch(in)
			},
		},
		{
			ID: finding.GitDirty,
			Evaluate: func(in Input) []finding.Finding {
				return gitDirty(in)
			},
		},
	}
}

// gitSHAMismatch reports two observed commits that differ. The diff only
// produces a sha comparison when both sides have a git section, so driving from
// it enforces the "both sides observed a commit" condition.
func gitSHAMismatch(in Input) []finding.Finding {
	if in.Diff == nil {
		return nil
	}
	for _, d := range in.Diff.Differences {
		if d.Component == diff.ComponentGit && d.Key == "sha" && d.Kind == diff.KindChanged {
			return []finding.Finding{{
				Rule:        finding.GitSHAMismatch,
				Severity:    finding.SeverityMedium,
				Component:   diff.ComponentGit,
				Key:         "sha",
				Expected:    d.A,
				Actual:      d.B,
				Description: fmt.Sprintf("%s and %s are not running the same commit, which makes every other comparison harder to trust.", Name(in.A), Name(in.B)),
			}}
		}
	}
	return nil
}

// gitDirty reports each observed environment whose working tree has uncommitted
// changes, because its recorded SHA does not fully describe what ran. Dirtiness
// is a property of one side, not a comparison, so it is read from the snapshots
// rather than the diff. A declared environment never observes a working tree
// and therefore can never be dirty.
func gitDirty(in Input) []finding.Finding {
	var out []finding.Finding
	for _, s := range []*snapshot.Snapshot{in.A, in.B} {
		if s == nil || s.Git == nil || !s.Git.Dirty || Declared(s) {
			continue
		}
		out = append(out, finding.Finding{
			Rule:        finding.GitDirty,
			Severity:    finding.SeverityLow,
			Component:   diff.ComponentGit,
			Key:         "dirty",
			Description: fmt.Sprintf("The recorded SHA in %s does not fully describe what ran because the working tree has uncommitted changes.", Name(s)),
		})
	}
	return out
}
