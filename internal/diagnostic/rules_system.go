package diagnostic

import (
	"fmt"

	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
)

// SystemRules returns the rules that diagnose the system section.
func SystemRules() []Rule {
	return []Rule{
		{
			ID: finding.SystemOSMismatch,
			Evaluate: func(in Input) []finding.Finding {
				return systemMismatch(in, "os", "operating system", finding.SystemOSMismatch)
			},
		},
		{
			ID: finding.SystemArchMismatch,
			Evaluate: func(in Input) []finding.Finding {
				return systemMismatch(in, "arch", "architecture", finding.SystemArchMismatch)
			},
		},
	}
}

// systemMismatch reports when two environments run different operating systems
// or CPU architectures. A laptop and a CI runner rarely share a machine type,
// so the severity stays low: the finding exists so a reader can confirm or rule
// out the obvious, not to alarm them.
func systemMismatch(in Input, key, noun, ruleID string) []finding.Finding {
	if in.Diff == nil {
		return nil
	}
	for _, d := range in.Diff.Differences {
		if d.Component != diff.ComponentSystem || d.Key != key || d.Kind != diff.KindChanged {
			continue
		}
		return []finding.Finding{{
			Rule:        ruleID,
			Severity:    finding.SeverityLow,
			Component:   diff.ComponentSystem,
			Key:         key,
			Expected:    d.A,
			Actual:      d.B,
			Description: fmt.Sprintf("%s and %s run different %s; a laptop and a CI runner rarely share one, so this is usually expected, not a bug.", Name(in.A), Name(in.B), noun),
		}}
	}
	return nil
}
