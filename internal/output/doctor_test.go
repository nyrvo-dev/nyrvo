package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/nyrvo-dev/nyrvo/internal/finding"
)

func highFinding() finding.Finding {
	return finding.Finding{
		Rule:           finding.RuntimeVersionMismatch,
		Severity:       finding.SeverityHigh,
		Component:      "runtime",
		Key:            "node",
		Expected:       "22",
		Actual:         "24.4.0",
		Description:    "CI installs a Node version this machine does not match.",
		Recommendation: "update actions/setup-node to 24",
	}
}

func mediumFinding() finding.Finding {
	return finding.Finding{
		Rule:        finding.EnvMissing,
		Severity:    finding.SeverityMedium,
		Component:   "environment",
		Key:         "CI",
		Expected:    "present",
		Actual:      "missing",
		Description: "The CI environment declares CI but the local capture does not.",
	}
}

// TestDoctorTextExactBytes locks in the layout and column alignment of a
// two-finding report: one high finding with a recommendation, one medium
// without. The expected/actual/fix values must line up in one column while the
// rule ids keep their own column, and a severity heading appears only when that
// severity has findings.
func TestDoctorTextExactBytes(t *testing.T) {
	findings := []finding.Finding{highFinding(), mediumFinding()}
	want := "NYRVO DOCTOR\n\nlocal vs ci\n\n" +
		"HIGH\n\n" +
		"  runtime.version_mismatch  node\n" +
		"    expected  22\n" +
		"    actual    24.4.0\n" +
		"    CI installs a Node version this machine does not match.\n" +
		"    fix  update actions/setup-node to 24\n" +
		"\n" +
		"MEDIUM\n\n" +
		"  env.missing  CI\n" +
		"    expected  present\n" +
		"    actual    missing\n" +
		"    The CI environment declares CI but the local capture does not.\n" +
		"\n" +
		"2 findings: 1 high, 1 medium\n"
	if got := render(t, func(w io.Writer) error { return DoctorText(w, "local", "ci", findings) }); got != want {
		t.Errorf("DoctorText exact bytes\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// TestDoctorTextGrouping locks in that findings are grouped under exactly the
// severities present, high before medium, without re-sorting and without a
// heading for a severity that has no findings.
func TestDoctorTextGrouping(t *testing.T) {
	findings := []finding.Finding{
		highFinding(),
		highFinding(),
		mediumFinding(),
	}
	got := render(t, func(w io.Writer) error { return DoctorText(w, "local", "ci", findings) })

	if strings.Count(got, "HIGH\n") != 1 {
		t.Errorf("expected exactly one HIGH heading:\n%q", got)
	}
	if strings.Count(got, "MEDIUM\n") != 1 {
		t.Errorf("expected exactly one MEDIUM heading:\n%q", got)
	}
	if strings.Contains(got, "LOW") {
		t.Errorf("no heading expected for a severity with no findings:\n%q", got)
	}
	if strings.Index(got, "HIGH") > strings.Index(got, "MEDIUM") {
		t.Errorf("headings out of order:\n%q", got)
	}
	if !strings.Contains(got, "3 findings: 2 high, 1 medium") {
		t.Errorf("summary counts wrong:\n%q", got)
	}
}

// TestDoctorTextEmpty locks in the no-findings wording: it must state that
// nothing was diagnosed because no rule matched — not because the environments
// are identical — so the reader is never led to over-trust a small rule set.
func TestDoctorTextEmpty(t *testing.T) {
	got := render(t, func(w io.Writer) error { return DoctorText(w, "local", "ci", nil) })
	for _, phrase := range []string{
		"Nothing was diagnosed",
		"no rule matched",
		"not the same as the environments being identical",
	} {
		if !strings.Contains(got, phrase) {
			t.Errorf("no-findings output missing %q:\n%q", phrase, got)
		}
	}
	if !strings.HasPrefix(got, "NYRVO DOCTOR\n\nlocal vs ci\n") {
		t.Errorf("no-findings output lost the header:\n%q", got)
	}
}

// TestDoctorTextMinimalFinding locks in that a finding with no key, no
// expected/actual values, and no recommendation renders without stray labels or
// blank columns: the rule line and the description are all that remain.
func TestDoctorTextMinimalFinding(t *testing.T) {
	f := finding.Finding{
		Rule:        finding.GitDirty,
		Severity:    finding.SeverityLow,
		Component:   "git",
		Description: "The working tree has uncommitted changes.",
	}
	got := render(t, func(w io.Writer) error { return DoctorText(w, "local", "ci", []finding.Finding{f}) })
	want := "NYRVO DOCTOR\n\nlocal vs ci\n\n" +
		"LOW\n\n" +
		"  git.dirty\n" +
		"    The working tree has uncommitted changes.\n" +
		"\n" +
		"1 finding: 1 low\n"
	if got != want {
		t.Errorf("DoctorText minimal finding\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

func TestDoctorTextSummaryCounts(t *testing.T) {
	tests := []struct {
		name     string
		findings []finding.Finding
		want     string
	}{
		{
			name:     "one high",
			findings: []finding.Finding{highFinding()},
			want:     "1 finding: 1 high",
		},
		{
			name:     "mixed",
			findings: []finding.Finding{highFinding(), mediumFinding(), mediumFinding()},
			want:     "3 findings: 1 high, 2 medium",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := render(t, func(w io.Writer) error { return DoctorText(w, "local", "ci", tt.findings) })
			if !strings.Contains(got, tt.want) {
				t.Errorf("summary %s: want %q in:\n%q", tt.name, tt.want, got)
			}
		})
	}
}

func TestDoctorJSON(t *testing.T) {
	findings := []finding.Finding{highFinding(), mediumFinding()}
	got := render(t, func(w io.Writer) error { return DoctorJSON(w, "local", "ci", findings) })

	var doc struct {
		A        string            `json:"a"`
		B        string            `json:"b"`
		Findings []finding.Finding `json:"findings"`
		Summary  map[string]int    `json:"summary"`
	}
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.A != "local" || doc.B != "ci" {
		t.Errorf("a/b = %q/%q, want local/ci", doc.A, doc.B)
	}
	for _, k := range []string{"high", "medium", "low"} {
		if _, ok := doc.Summary[k]; !ok {
			t.Errorf("summary missing key %q: %v", k, doc.Summary)
		}
	}
	if doc.Summary["high"] != 1 || doc.Summary["medium"] != 1 || doc.Summary["low"] != 0 {
		t.Errorf("summary = %v, want 1 high, 1 medium, 0 low", doc.Summary)
	}
	if !reflect.DeepEqual(doc.Findings, findings) {
		t.Errorf("findings did not round-trip:\ngot:  %+v\nwant: %+v", doc.Findings, findings)
	}
}

// TestDoctorJSONEmptySummaryHasZeros pins the always-present-summary contract:
// consumers must be able to read all three severity keys even when there are no
// findings at all.
func TestDoctorJSONEmptySummaryHasZeros(t *testing.T) {
	got := render(t, func(w io.Writer) error { return DoctorJSON(w, "local", "ci", nil) })

	var doc struct {
		Summary map[string]int `json:"summary"`
	}
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"high", "medium", "low"} {
		if v, ok := doc.Summary[k]; !ok {
			t.Errorf("summary missing %q", k)
		} else if v != 0 {
			t.Errorf("summary[%q] = %d, want 0", k, v)
		}
	}
}

// TestDoctorRenderersPropagateWriterError covers both doctor renderers against a
// failing writer: the error must surface to the caller, never panic. The
// tabwriter-backed DoctorText path is included because its error only appears at
// Flush time, not at Write time.
func TestDoctorRenderersPropagateWriterError(t *testing.T) {
	sentinel := errors.New("disk full")
	findings := []finding.Finding{highFinding()}

	tests := []struct {
		name string
		run  func(w io.Writer) error
	}{
		{"DoctorText", func(w io.Writer) error { return DoctorText(w, "local", "ci", findings) }},
		{"DoctorText empty", func(w io.Writer) error { return DoctorText(w, "local", "ci", nil) }},
		{"DoctorJSON", func(w io.Writer) error { return DoctorJSON(w, "local", "ci", findings) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run(errWriter{err: sentinel})
			if err == nil {
				t.Fatalf("%s returned nil for a failing writer", tt.name)
			}
			if !errors.Is(err, sentinel) {
				t.Errorf("%s returned %v, want it to wrap sentinel %v", tt.name, err, sentinel)
			}
		})
	}
}

// Evidence the snapshots reported about themselves is printed above the
// findings and never ranked: a run's failing step answers "why did this fail?"
// in a way no environment rule can, and it must appear even when no rule
// matched.
func TestDoctorTextContext(t *testing.T) {
	context := []string{
		"The run concluded with failure.",
		`The job failed at step "Run npm test".`,
	}

	got := render(t, func(w io.Writer) error { return DoctorText(w, "local", "ci", nil, context...) })
	for _, want := range append([]string{"WHAT THE EVIDENCE REPORTS"}, context...) {
		if !strings.Contains(got, want) {
			t.Errorf("context line %q missing from a findings-free report:\n%s", want, got)
		}
	}
	// A clean rule set still has to say what "no findings" means.
	if !strings.Contains(got, "no rule matched") {
		t.Errorf("clean report lost its wording:\n%s", got)
	}

	withFinding := []finding.Finding{{
		Rule: "system.os_mismatch", Severity: finding.SeverityLow,
		Component: "system", Key: "os", Expected: "darwin", Actual: "linux",
		Description: "Different platforms.",
	}}
	got = render(t, func(w io.Writer) error { return DoctorText(w, "local", "ci", withFinding, context...) })
	if idx, jdx := strings.Index(got, "WHAT THE EVIDENCE REPORTS"), strings.Index(got, "LOW"); idx < 0 || jdx < 0 || idx > jdx {
		t.Errorf("evidence must precede the findings:\n%s", got)
	}

	// No context means no heading: an empty section would imply the evidence
	// said nothing when it was simply never asked.
	if got := render(t, func(w io.Writer) error { return DoctorText(w, "local", "ci", withFinding) }); strings.Contains(got, "WHAT THE EVIDENCE REPORTS") {
		t.Errorf("empty context printed a heading:\n%s", got)
	}
}

func TestDoctorJSONContext(t *testing.T) {
	var buf bytes.Buffer
	if err := DoctorJSON(&buf, "local", "ci", nil, "The run concluded with failure."); err != nil {
		t.Fatalf("DoctorJSON: %v", err)
	}
	var doc struct {
		Context []string `json:"context"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(doc.Context) != 1 || doc.Context[0] != "The run concluded with failure." {
		t.Errorf("context = %v, want the single evidence line", doc.Context)
	}

	// Absent context must be omitted rather than serialized as null, so a
	// consumer never has to distinguish the two.
	buf.Reset()
	if err := DoctorJSON(&buf, "local", "ci", nil); err != nil {
		t.Fatalf("DoctorJSON: %v", err)
	}
	if strings.Contains(buf.String(), "context") {
		t.Errorf("empty context should be omitted: %s", buf.String())
	}
}

// A clean diagnosis must serialize as an empty list, not null: a consumer that
// reads doc.findings.length crashes on null, and "absent" versus "empty" is the
// exact ambiguity a frozen machine contract has to settle.
func TestDoctorJSONEmptyFindingsIsAnEmptyArray(t *testing.T) {
	var b bytes.Buffer
	if err := DoctorJSON(&b, "local", "ci", nil); err != nil {
		t.Fatalf("DoctorJSON() error = %v", err)
	}
	if !strings.Contains(b.String(), `"findings": []`) {
		t.Errorf("clean diagnosis does not emit an empty array:\n%s", b.String())
	}
	if strings.Contains(b.String(), "null") {
		t.Errorf("clean diagnosis emits null:\n%s", b.String())
	}
}
