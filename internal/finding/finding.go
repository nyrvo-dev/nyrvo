// Package finding defines what Nyrvo reports when it has an opinion.
//
// A diff states that two environments differ. A finding states that a
// difference matters, why, and what to do about it. The two are kept apart on
// purpose: the diff is evidence and stays free of judgement, so a user who
// disagrees with Nyrvo's rules can still trust the comparison underneath.
//
// Rule identifiers and severities are a public contract once released: scripts
// filter on them and future output formats carry them.
package finding

import "sort"

// Severity is how much a finding should worry the reader.
type Severity string

const (
	// SeverityHigh means this difference is a plausible cause of a failure:
	// something declares a requirement the other environment does not meet.
	SeverityHigh Severity = "high"
	// SeverityMedium means the difference can change behaviour and deserves a
	// look, but nothing observed proves it breaks anything.
	SeverityMedium Severity = "medium"
	// SeverityLow means the difference is real and usually expected — a laptop
	// and a CI runner are not the same machine.
	SeverityLow Severity = "low"
)

// rank orders severities for output, most serious first. Unknown severities
// sort last rather than crashing: a finding with a typo in it should still be
// printed.
var rank = map[Severity]int{SeverityHigh: 0, SeverityMedium: 1, SeverityLow: 2}

// Rule identifiers. They are stable strings, not an enum, because they end up
// in JSON, in scripts, and in issue trackers. Renaming one breaks whatever was
// filtering on it.
const (
	// RuntimeVersionMismatch: both environments have the runtime, at versions
	// that are not compatible.
	RuntimeVersionMismatch = "runtime.version_mismatch"
	// RuntimeMissing: one environment has a runtime the other does not.
	RuntimeMissing = "runtime.missing"
	// EnvMissing: a variable one environment declares is absent from the other.
	EnvMissing = "env.missing"
	// SystemOSMismatch and SystemArchMismatch: the two environments are not the
	// same kind of machine.
	SystemOSMismatch   = "system.os_mismatch"
	SystemArchMismatch = "system.arch_mismatch"
	// GitSHAMismatch: the two environments are not running the same commit.
	GitSHAMismatch = "git.sha_mismatch"
	// GitDirty: a working tree has uncommitted changes, so its SHA does not
	// describe what actually ran.
	GitDirty = "git.dirty"
)

// Finding is one diagnosed difference.
type Finding struct {
	// Rule is the stable identifier of the rule that produced this finding.
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	// Component and Key locate the finding in the same vocabulary the diff
	// uses ("runtime"/"node"), so a finding can always be traced back to the
	// evidence it came from.
	Component string `json:"component"`
	Key       string `json:"key,omitempty"`
	// Expected and Actual are strings rather than free-form values: everything
	// Nyrvo compares is already text, and keeping them typed makes findings
	// stable to serialize and simple to assert on in tests.
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
	// Description states what is wrong in one sentence, without repeating the
	// values, which the renderer prints separately.
	Description string `json:"description"`
	// Recommendation is the concrete next step, when there is one worth
	// stating. An honest empty recommendation beats an invented one.
	Recommendation string `json:"recommendation,omitempty"`
}

// Sort orders findings most serious first, then by component and key, so two
// runs over the same evidence print in the same order.
func Sort(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if rank[a.Severity] != rank[b.Severity] {
			return rank[a.Severity] < rank[b.Severity]
		}
		if a.Component != b.Component {
			return a.Component < b.Component
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return a.Rule < b.Rule
	})
}

// Count returns how many findings carry each severity, for a summary line.
func Count(findings []Finding) map[Severity]int {
	counts := make(map[Severity]int, 3)
	for _, f := range findings {
		counts[f.Severity]++
	}
	return counts
}
