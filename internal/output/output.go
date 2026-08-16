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

// CaptureText reports what each collector observed.
//
// Unavailable sections are shown rather than hidden: "Python not installed" is
// itself a fact about this environment, and silently omitting it would leave
// the user guessing whether Nyrvo looked at all.
func CaptureText(w io.Writer, res *capture.Result) error {
	var b strings.Builder
	b.WriteString("Capturing environment...\n\n")
	for _, s := range res.Sections {
		switch s.Status {
		case capture.StatusOK:
			fmt.Fprintf(&b, "  ok        %s\n", s.Collector)
		case capture.StatusUnavailable:
			fmt.Fprintf(&b, "  skipped   %s (not available here)\n", s.Collector)
		default:
			fmt.Fprintf(&b, "  FAILED    %s: %s\n", s.Collector, s.Error)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// DiffText renders semantic differences grouped by component.
//
// Only differences are printed. A drift report that also listed everything
// identical would bury the few lines that matter.
func DiffText(w io.Writer, res *diff.Result) error {
	if res.Empty() {
		_, err := fmt.Fprintf(w, "No differences between %s and %s.\n", res.A, res.B)
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Differences between %s and %s\n", res.A, res.B)

	component := ""
	for _, d := range res.Differences {
		if d.Component != component {
			component = d.Component
			fmt.Fprintf(&b, "\n%s\n\n", title(component))
		}
		fmt.Fprintf(&b, "  %s\n", d.Key)
		// Both sides are always shown, with an absent observation spelled out,
		// so a reader never has to infer which environment lacks something.
		fmt.Fprintf(&b, "    %s\t%s\n", res.A, valueOr(d.A))
		fmt.Fprintf(&b, "    %s\t%s\n", res.B, valueOr(d.B))
	}
	return writeAligned(w, b.String())
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
