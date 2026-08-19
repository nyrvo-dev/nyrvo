// Package output renders domain results for humans and for machines.
//
// Domain packages never print: capture and diff return values, and every
// rendering decision lives here. That separation is what makes the same result
// usable from a terminal, a CI log, a JSON consumer, and a test without
// duplicating logic.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/nyrvo-dev/nyrvo/internal/capture"
	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// JSON writes v as an indented, newline-terminated document.
//
// Every --json form goes through here so machine-readable output has one shape
// and one place to keep it stable.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

// CaptureHeader announces that a capture is starting.
//
// It is written before the collectors run, not after: the line says what is
// about to happen, and printing it alongside the finished result made it a
// report of work already done.
func CaptureHeader(w io.Writer) error {
	_, err := io.WriteString(w, "Capturing environment...\n\n")
	return err
}

// CaptureSection reports what one collector observed, as a single line.
//
// Written as each collector finishes rather than gathered and printed at the
// end, so the terminal shows progress through a run that spawns a dozen
// external tools.
//
// Unavailable sections are shown rather than hidden: "Python not installed" is
// itself a fact about this environment, and silently omitting it would leave
// the user guessing whether Nyrvo looked at all.
func CaptureSection(w io.Writer, s capture.SectionResult) error {
	var line string
	switch s.Status {
	case capture.StatusOK:
		line = fmt.Sprintf("  ok        %s\n", s.Collector)
	case capture.StatusUnavailable:
		// "Not available" covers two very different situations: nothing is
		// installed, and something is installed but refused to answer — rustc
		// under a toolchain the machine does not have, rbenv on a version that
		// was never installed. The second is often the drift the user is
		// capturing to find, so its reason is printed.
		if reason := unavailableReason(s); reason != "" {
			line = fmt.Sprintf("  skipped   %s (%s)\n", s.Collector, reason)
			break
		}
		line = fmt.Sprintf("  skipped   %s (not available here)\n", s.Collector)
	default:
		line = fmt.Sprintf("  FAILED    %s: %s\n", s.Collector, s.Error)
	}
	_, err := io.WriteString(w, line)
	return err
}

// unavailableReason pulls the explanation out of a wrapped ErrUnavailable,
// returning "" when the message says nothing the collector's own name does not
// already say.
func unavailableReason(s capture.SectionResult) string {
	reason := strings.TrimSuffix(s.Error, ": "+collector.ErrUnavailable.Error())
	reason = strings.TrimPrefix(reason, s.Collector+": ")
	if reason == "" || reason == s.Collector {
		return ""
	}
	return reason
}

// DiffText renders semantic differences grouped by component.
//
// Only differences are printed. A drift report that also listed everything
// identical would bury the few lines that matter.
func DiffText(w io.Writer, res *diff.Result) error {
	st := NewStyle(w)
	if res.Empty() {
		var b strings.Builder
		fmt.Fprintf(&b, "No differences between %s and %s.\n", res.A, res.B)
		writePartialEnvironmentNote(&b, res)
		writePartialRuntimesNote(&b, res)
		writeUnmeasuredNote(&b, res)
		writeUnusableNote(&b, res)
		_, err := io.WriteString(w, b.String())
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Differences between %s and %s\n", res.A, res.B)

	component := ""
	for _, d := range res.Differences {
		if d.Component != component {
			component = d.Component
			// The component heading is a tab-free line of its own, so styling it
			// cannot shift the value columns aligned below it.
			fmt.Fprintf(&b, "\n%s\n\n", st.Bold(title(component)))
		}
		// An empty key marks a whole section one side never described. Saying so
		// in one line avoids implying the other side observed something and
		// found it absent.
		if d.Key == "" {
			seen, missing := res.A, res.B
			if d.Kind == diff.KindOnlyInB {
				seen, missing = res.B, res.A
			}
			fmt.Fprintf(&b, "  described in %s, not described in %s\n", seen, missing)
			continue
		}
		fmt.Fprintf(&b, "  %s\n", d.Key)
		// Both sides are always shown, with an absent observation spelled out,
		// so a reader never has to infer which environment lacks something.
		fmt.Fprintf(&b, "    %s\t%s\n", res.A, sideValue(d.A, d.AUnusable))
		fmt.Fprintf(&b, "    %s\t%s\n", res.B, sideValue(d.B, d.BUnusable))
	}
	writePartialEnvironmentNote(&b, res)
	writePartialRuntimesNote(&b, res)
	writeUnmeasuredNote(&b, res)
	writeUnusableNote(&b, res)
	return writeAligned(w, b.String())
}

// writePartialEnvironmentNote tells the reader that the environment comparison
// was narrowed. Without it the report would look exhaustive while quietly
// skipping every variable the partial side never claimed to describe.
func writePartialEnvironmentNote(b *strings.Builder, res *diff.Result) {
	if !res.PartialEnvironment {
		return
	}
	b.WriteString("\nOne side lists only the environment variables it declares, so variables\n")
	b.WriteString("absent from it were not compared. Only variables it does declare are shown.\n")
}

// writePartialRuntimesNote does for runtimes what the environment note does for
// variables. A silently narrowed comparison is worse than a noisy one: a reader
// who is not told will read the absence of a runtime line as agreement.
func writePartialRuntimesNote(b *strings.Builder, res *diff.Result) {
	if !res.PartialRuntimes {
		return
	}
	b.WriteString("\nOne side lists only the runtimes it sets up, so runtimes absent from it\n")
	b.WriteString("were not compared; a runner image provides more than a workflow mentions.\n")
}

// writeUnmeasuredNote reports the comparison Nyrvo declined to make. The other
// two notes describe a source that never claimed to be complete; this one
// describes a probe that was asked and ran out of time, which is a fact about
// this capture rather than about the kind of source it came from.
func writeUnmeasuredNote(b *strings.Builder, res *diff.Result) {
	if !res.Unmeasured {
		return
	}
	b.WriteString("\nSomething did not answer in time and was left unmeasured, so it was not\n")
	b.WriteString("compared. Unmeasured is not missing: run the capture again to settle it.\n")
}

// writeUnusableNote reports that the comparison includes a runtime that was
// installed but refused to report a version. That refusal is deterministic and
// is usually the drift being sought, so it is kept as a difference rather than
// dropped; this note keeps a reader from mistaking it for an absence.
func writeUnusableNote(b *strings.Builder, res *diff.Result) {
	if !res.Unusable {
		return
	}
	b.WriteString("\nA runtime was installed but refused to report a version. That is recorded\n")
	b.WriteString("as \"installed, not usable\", not as missing: the tool is present, and the\n")
	b.WriteString("usual cause is a pinned toolchain this machine does not have.\n")
}

// SnapshotList renders stored snapshot names.
func SnapshotList(w io.Writer, names []string) error {
	if len(names) == 0 {
		_, err := fmt.Fprintf(w, "No snapshots yet. Run: nyrvo capture local\n")
		return err
	}
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s\n", n)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// SnapshotJSON writes the canonical snapshot document, the same bytes stored on
// disk, so piping and reading a snapshot file are interchangeable.
func SnapshotJSON(w io.Writer, snap *snapshot.Snapshot) error {
	data, err := snapshot.Marshal(snap)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// valueOr spells out an unobserved value. The word is deliberate: for
// environment variables the recorded value is only ever "present", so its
// opposite must read as absence, not as an empty string.
func valueOr(v string) string {
	if v == "" {
		return "missing"
	}
	return v
}

// sideValue renders one side of a difference. An empty value is normally
// "missing", but a side that recorded the observation as installed yet refusing
// to answer must not read as absent: the tool is on PATH, it just would not
// report a version. The diff distinguishes the two, so the terminal keeps them
// apart too — this is the word the diagnostic rule uses for the same state.
func sideValue(v string, unusable bool) string {
	if unusable {
		return "installed, not usable"
	}
	return valueOr(v)
}

func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// writeAligned expands the tab-separated value columns so the two environments
// line up under each key.
func writeAligned(w io.Writer, s string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := io.WriteString(tw, s); err != nil {
		return err
	}
	return tw.Flush()
}
