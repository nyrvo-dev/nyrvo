package output

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nyrvo-dev/nyrvo/internal/capture"
	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// Braille was chosen over a geometric glyph because every frame occupies
// exactly one column, so the label cannot wobble as frames swap. A frame set
// that is empty or multi-rune would break that guarantee.
func TestSpinnerFramesAreSingleRunes(t *testing.T) {
	if len(spinnerFrames) == 0 {
		t.Fatal("spinnerFrames must not be empty")
	}
	for _, f := range spinnerFrames {
		if got := utf8.RuneCountInString(string(f)); got != 1 {
			t.Errorf("frame %q is %d runes, want exactly 1", string(f), got)
		}
	}
}

// The core contract: a non-terminal writer makes the spinner a no-op. A bytes
// .Buffer has no Stat method, so NewStyle disables colour for it, and the same
// single check must keep the spinner silent — not one byte, no carriage return,
// no escape sequence.
func TestSpinnerOnBufferWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	s := NewSpinner(&buf)
	s.Start("git")
	time.Sleep(30 * time.Millisecond)
	s.Stop()
	s.Stop()
	if buf.Len() != 0 {
		t.Errorf("spinner wrote %d bytes to a non-terminal, want 0", buf.Len())
	}
}

// A character-device writer with a clean environment is exactly the case the
// spinner is for: it must animate, styled in the accent colour, and erase the
// line when stopped.
func TestSpinnerAnimatesOnTerminal(t *testing.T) {
	unsetNOCOLOR(t)
	t.Setenv("TERM", "xterm")
	t.Setenv("TERM_PROGRAM", "vscode")

	var tb terminalBuffer
	s := NewSpinner(&tb)
	s.tick = 5 * time.Millisecond
	s.Start("git")
	time.Sleep(30 * time.Millisecond)
	s.Stop()

	out := tb.String()
	if !strings.Contains(out, "git") {
		t.Errorf("spinner output %q has no label", out)
	}
	if !strings.Contains(out, "\r") {
		t.Errorf("spinner output %q has no carriage return", out)
	}
	if !strings.Contains(out, "\x1b[38;5;141m") {
		t.Errorf("spinner output %q has no accent-styled frame", out)
	}
	if !strings.HasSuffix(out, "\r\x1b[K") {
		t.Errorf("spinner output %q does not end with the line erasure", out)
	}
	if strings.Contains(out, "\x1b[?25") {
		t.Errorf("spinner output %q hides or shows the cursor; that is out of scope", out)
	}
}

// Stop without a Start must do nothing, not even erase a line that was never
// drawn, and must be safe to repeat.
func TestSpinnerStopWithoutStartIsSafe(t *testing.T) {
	unsetNOCOLOR(t)
	t.Setenv("TERM", "xterm")
	t.Setenv("TERM_PROGRAM", "vscode")

	var tb terminalBuffer
	s := NewSpinner(&tb)
	s.Stop()
	s.Stop()
	if tb.Len() != 0 {
		t.Errorf("Stop without Start wrote %d bytes, want 0", tb.Len())
	}
}

// A second Start while the first is still running must retire the first
// animation first, so the newest label is what the caller sees.
func TestSpinnerStartWhileRunningReplacesLabel(t *testing.T) {
	unsetNOCOLOR(t)
	t.Setenv("TERM", "xterm")
	t.Setenv("TERM_PROGRAM", "vscode")

	var tb terminalBuffer
	s := NewSpinner(&tb)
	s.tick = 5 * time.Millisecond
	s.Start("git")
	time.Sleep(20 * time.Millisecond)
	s.Start("docker")
	time.Sleep(20 * time.Millisecond)
	s.Stop()

	out := tb.String()
	if !strings.Contains(out, "docker") {
		t.Errorf("second label missing from %q", out)
	}
	if !strings.HasSuffix(out, "\r\x1b[K") {
		t.Errorf("output %q does not end with the line erasure", out)
	}
}

// spinnerStub is a collector that answers immediately, so a capture can be
// exercised without any real tool being installed.
type spinnerStub struct{ name string }

func (c spinnerStub) Name() string { return c.name }

func (c spinnerStub) Collect(context.Context, *snapshot.Snapshot) error { return nil }

// TestCaptureWithSpinnerIsByteIdenticalToCaptureWithout is the invariant that
// matters: when the destination is not a terminal, a capture that runs the
// spinner through its hooks must emit exactly the same bytes as one that does
// not. A CI log, a pipe, or a file must not gain a single escape sequence or
// carriage return. If this test needs editing to pass, the spinner is leaking
// animation into a non-terminal writer.
func TestCaptureWithSpinnerIsByteIdenticalToCaptureWithout(t *testing.T) {
	collectors := []collector.Collector{
		spinnerStub{name: "system"},
		spinnerStub{name: "git"},
		spinnerStub{name: "node"},
	}

	run := func(withSpinner bool) []byte {
		var buf bytes.Buffer
		if err := CaptureHeader(&buf); err != nil {
			t.Fatalf("CaptureHeader: %v", err)
		}
		sp := NewSpinner(&buf)
		opts := capture.Options{
			Name: "local",
			Now:  func() time.Time { return time.Unix(0, 0).UTC() },
			OnSection: func(s capture.SectionResult) {
				if withSpinner {
					sp.Stop()
				}
				_ = CaptureSection(&buf, s)
			},
		}
		if withSpinner {
			opts.OnSectionStart = func(string) { sp.Start("x") }
		}
		if _, err := capture.Run(context.Background(), collectors, opts); err != nil {
			t.Fatalf("capture.Run: %v", err)
		}
		if withSpinner {
			sp.Stop()
		}
		return buf.Bytes()
	}

	with := run(true)
	without := run(false)
	if !bytes.Equal(with, without) {
		t.Errorf("capture with spinner differs from capture without on a non-terminal\nwith:   %q\nwithout: %q", with, without)
	}
}
