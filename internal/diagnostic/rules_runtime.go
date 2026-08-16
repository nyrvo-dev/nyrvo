package diagnostic

import (
	"fmt"

	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// RuntimeRules returns the rules that diagnose the runtime section.
func RuntimeRules() []Rule {
	return []Rule{
		{
			ID: finding.RuntimeVersionMismatch,
			Evaluate: func(in Input) []finding.Finding {
				return runtimeVersionMismatch(in)
			},
		},
		{
			ID: finding.RuntimeMissing,
			Evaluate: func(in Input) []finding.Finding {
				return runtimeMissing(in)
			},
		},
	}
}

// runtimeVersionMismatch reports runtimes both environments have at versions
// that are not compatible.
func runtimeVersionMismatch(in Input) []finding.Finding {
	if in.Diff == nil {
		return nil
	}
	var out []finding.Finding
	for _, d := range in.Diff.Differences {
		if d.Component != diff.ComponentRuntime || d.Key == "" || d.Kind != diff.KindChanged {
			continue
		}
		if f, ok := versionMismatchFinding(in, d); ok {
			out = append(out, f)
		}
	}
	return out
}

// versionMismatchFinding turns one changed runtime difference into a finding,
// unless a declared prefix version is satisfied by the observed one.
func versionMismatchFinding(in Input, d diff.Difference) (finding.Finding, bool) {
	declaredA, declaredB := Declared(in.A), Declared(in.B)
	expected, actual := d.A, d.B

	if declaredA != declaredB {
		// Exactly one side declares its environment. Its stated version is a
		// prefix the observed side may satisfy: a setup action asked for "1.26"
		// installs the latest 1.26.x, so an observed 1.26.6 is a match, not a
		// drift. Missing this is the false positive that makes a diagnostic
		// tool worth ignoring, so it is guarded explicitly.
		declared, observed := d.A, d.B
		if declaredB {
			declared, observed = d.B, d.A
		}
		if Satisfies(declared, observed) {
			return finding.Finding{}, false
		}
		expected, actual = declared, observed
	}

	f := finding.Finding{
		Rule:        finding.RuntimeVersionMismatch,
		Severity:    mismatchSeverity(Distance(expected, actual)),
		Component:   diff.ComponentRuntime,
		Key:         d.Key,
		Expected:    expected,
		Actual:      actual,
		Description: fmt.Sprintf("The %s versions in %s and %s are not compatible.", d.Key, Name(in.A), Name(in.B)),
	}
	if declaredA != declaredB {
		// When a workflow states the version, the concrete next step is to edit
		// that file's version input.
		f.Recommendation = "Update the setup action's version input in " + declaredRef(in) + "."
	}
	return f, true
}

// mismatchSeverity maps a version gap to how alarming it is: a different major
// version is effectively a different runtime, a patch gap is usually noise, and
// a gap Distance cannot name (a declaration more precise than the observation)
// is at most a patch's worth of concern.
func mismatchSeverity(dist string) finding.Severity {
	switch dist {
	case "major":
		return finding.SeverityHigh
	case "minor":
		return finding.SeverityMedium
	default:
		return finding.SeverityLow
	}
}

// runtimeMissing reports a runtime present in one environment and absent from
// the other.
func runtimeMissing(in Input) []finding.Finding {
	if in.Diff == nil {
		return nil
	}
	var out []finding.Finding
	for _, d := range in.Diff.Differences {
		if d.Component != diff.ComponentRuntime || d.Key == "" {
			continue
		}
		switch d.Kind {
		case diff.KindOnlyInA, diff.KindOnlyInB:
			out = append(out, runtimeMissingFinding(in, d))
		}
	}
	return out
}

func runtimeMissingFinding(in Input, d diff.Difference) finding.Finding {
	hasSide, lacksSide := in.A, in.B
	expected := d.A
	if d.Kind == diff.KindOnlyInB {
		hasSide, lacksSide = in.B, in.A
		expected = d.B
	}

	// The side that lacks the runtime may be a workflow that never mentions it.
	// A file's silence is not proof the runner image lacks the runtime, so the
	// wording must stay honest about what is actually known.
	var description string
	switch {
	case Declared(lacksSide):
		description = fmt.Sprintf("The workflow %s does not set up %s.", declaredRef(in), d.Key)
	case Declared(hasSide):
		description = fmt.Sprintf("%s sets up %s, but %s does not have it installed.", Name(hasSide), d.Key, Name(lacksSide))
	default:
		description = fmt.Sprintf("%s has %s installed, but %s does not.", Name(hasSide), d.Key, Name(lacksSide))
	}

	var recommendation string
	switch {
	case Declared(lacksSide):
		recommendation = fmt.Sprintf("Add a setup action for %s to the workflow, or confirm the runner image provides it; a workflow's silence is not proof the runner lacks it.", d.Key)
	case Declared(hasSide):
		recommendation = fmt.Sprintf("Install %s in %s to match what the workflow sets up, or adjust the workflow.", d.Key, Name(lacksSide))
	default:
		recommendation = fmt.Sprintf("Install %s in %s, or confirm it is genuinely not required there.", d.Key, Name(lacksSide))
	}

	return finding.Finding{
		Rule:           finding.RuntimeMissing,
		Severity:       finding.SeverityMedium,
		Component:      diff.ComponentRuntime,
		Key:            d.Key,
		Expected:       expected,
		Actual:         "missing",
		Description:    description,
		Recommendation: recommendation,
	}
}

// declaredSnapshot returns the snapshot that declares its environment, or nil.
// The declared side is what the observed side must be checked against.
func declaredSnapshot(in Input) *snapshot.Snapshot {
	for _, s := range []*snapshot.Snapshot{in.A, in.B} {
		if Declared(s) {
			return s
		}
	}
	return nil
}

// declaredRef names where the declared environment lives, so a recommendation
// can point at the file to edit.
func declaredRef(in Input) string {
	if s := declaredSnapshot(in); s != nil && s.Source != nil && s.Source.Ref != "" {
		return s.Source.Ref
	}
	return "the workflow file"
}
