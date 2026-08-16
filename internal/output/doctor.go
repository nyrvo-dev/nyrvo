package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/finding"
)

// DoctorText renders findings for a human reader.
//
// Findings arrive already sorted and grouped by severity (finding.Sort), so the
// renderer never re-sorts: it emits a severity heading only when the severity
// changes, preserving the order the caller chose. Values that are empty are
// skipped rather than printed as blanks, so a finding never reads as if it had
// more evidence than it carries.
// context, when present, is what the evidence itself reported about the
// environments — a run's conclusion, the step that failed, what Nyrvo could not
// model. It is printed as-is and never ranked: it is testimony, not diagnosis,
// and a reader asking "why did this fail?" needs it even when no rule matched.
func DoctorText(w io.Writer, a, b string, findings []finding.Finding, context ...string) error {
	var buf strings.Builder
	fmt.Fprintf(&buf, "NYRVO DOCTOR\n\n%s vs %s\n", a, b)

	if len(context) > 0 {
		buf.WriteString("\nWHAT THE EVIDENCE REPORTS\n\n")
		for _, c := range context {
			fmt.Fprintf(&buf, "  %s\n", c)
		}
	}

	if len(findings) == 0 {
		// A clean bill of health has to say what it means. "No findings" is not
		// "the environments are identical": Nyrvo reports only rules that
		// matched, and claiming more than the rule set covers would overstate
		// the comparison.
		buf.WriteString("\nNothing was diagnosed: no rule matched, which is not the same as the environments being identical.\n")
		_, err := io.WriteString(w, buf.String())
		return err
	}

	var blocks []string
	blocks = append(blocks, buf.String())
	last := finding.Severity("")
	for _, f := range findings {
		if f.Severity != last {
			blocks = append(blocks, strings.ToUpper(string(f.Severity))+"\n")
			last = f.Severity
		}
		blocks = append(blocks, renderFinding(f))
	}
	blocks = append(blocks, summaryLine(findings)+"\n")

	buf.Reset()
	// Every block already ends with a newline; the extra "\n" between blocks is
	// the blank separator line. The separator must be explicit rather than a
	// trailing empty line: tabwriter merges a blank line that directly follows
	// a tabbed line into that line's pending cell block and reformats it, which
	// would swallow the blank.
	for i, blk := range blocks {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(blk)
	}

	return writeAligned(w, buf.String())
}

// renderFinding renders one finding. The rule line carries no tab so the key
// never takes part in the detail column alignment; expected, actual, and fix
// are tab-separated so their values line up under one column, and the
// description has no tab, which terminates that block in the tabwriter exactly
// the way the diff renderer separates its environment columns.
func renderFinding(f finding.Finding) string {
	var b strings.Builder
	if f.Key != "" {
		fmt.Fprintf(&b, "  %s  %s\n", f.Rule, f.Key)
	} else {
		fmt.Fprintf(&b, "  %s\n", f.Rule)
	}
	if f.Expected != "" {
		fmt.Fprintf(&b, "    expected\t%s\n", f.Expected)
	}
	if f.Actual != "" {
		fmt.Fprintf(&b, "    actual\t%s\n", f.Actual)
	}
	if f.Description != "" {
		fmt.Fprintf(&b, "    %s\n", f.Description)
	}
	if f.Recommendation != "" {
		fmt.Fprintf(&b, "    fix\t%s\n", f.Recommendation)
	}
	return b.String()
}

// summaryLine counts findings per severity for the closing line. Zero counts
// are left out of the text (unlike JSON, where they are always present): a
// human reading "1 high, 1 medium" should not have to scan past "0 low".
func summaryLine(findings []finding.Finding) string {
	counts := finding.Count(findings)
	parts := make([]string, 0, 3)
	for _, sev := range []finding.Severity{finding.SeverityHigh, finding.SeverityMedium, finding.SeverityLow} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, sev))
		}
	}
	word := "findings"
	if len(findings) == 1 {
		word = "finding"
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d %s", len(findings), word)
	}
	return fmt.Sprintf("%d %s: %s", len(findings), word, strings.Join(parts, ", "))
}

// doctorDoc is the machine-readable doctor report.
type doctorDoc struct {
	A string `json:"a"`
	B string `json:"b"`
	// Context is what the evidence reported about itself, unranked.
	Context  []string          `json:"context,omitempty"`
	Findings []finding.Finding `json:"findings"`
	Summary  doctorSummary     `json:"summary"`
}

// doctorSummary always carries all three severity keys, even when zero, so a
// consumer can read it without checking which keys exist.
type doctorSummary struct {
	High   int `json:"high"`
	Medium int `json:"medium"`
	Low    int `json:"low"`
}

// DoctorJSON renders findings for a machine. Reusing the JSON helper keeps this
// output indented, newline-terminated, and encoded exactly like every other
// --json form in the CLI.
func DoctorJSON(w io.Writer, a, b string, findings []finding.Finding, context ...string) error {
	counts := finding.Count(findings)
	return JSON(w, doctorDoc{
		A:        a,
		B:        b,
		Context:  context,
		Findings: findings,
		Summary: doctorSummary{
			High:   counts[finding.SeverityHigh],
			Medium: counts[finding.SeverityMedium],
			Low:    counts[finding.SeverityLow],
		},
	})
}
