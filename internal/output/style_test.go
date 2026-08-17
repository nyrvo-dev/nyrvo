package output

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/finding"
)

// terminalFileInfo is an os.FileInfo that reports a character device, so a
// writer wrapping it passes NewStyle's terminal check without a real terminal
// being involved.
type terminalFileInfo struct{}

func (terminalFileInfo) Name() string       { return "terminal" }
func (terminalFileInfo) Size() int64        { return 0 }
func (terminalFileInfo) Mode() os.FileMode  { return os.ModeCharDevice }
func (terminalFileInfo) ModTime() time.Time { return time.Time{} }
func (terminalFileInfo) IsDir() bool        { return false }
func (terminalFileInfo) Sys() any           { return nil }

// terminalBuffer is a bytes.Buffer that also reports itself as a character
// device, so a renderer writing to it takes the colour path while the test can
// still read what was written.
type terminalBuffer struct {
	bytes.Buffer
}

func (terminalBuffer) Stat() (os.FileInfo, error) { return terminalFileInfo{}, nil }

// unsetNOCOLOR removes NO_COLOR for the duration of a test and restores it
// afterwards. The test harness may run under a shell that exports NO_COLOR, and
// a test of colour must control that variable itself rather than inherit it.
func unsetNOCOLOR(t *testing.T) {
	t.Helper()
	old, had := os.LookupEnv("NO_COLOR")
	os.Unsetenv("NO_COLOR")
	t.Cleanup(func() {
		if had {
			os.Setenv("NO_COLOR", old)
		} else {
			os.Unsetenv("NO_COLOR")
		}
	})
}

// A bytes.Buffer has no Stat method, so it must never be treated as a terminal.
// This is what keeps every pre-existing golden test passing unchanged: they all
// render through a buffer, and a buffer yields a Style that returns input
// untouched.
func TestStyleBufferDisabled(t *testing.T) {
	var buf bytes.Buffer
	st := NewStyle(&buf)
	methods := []struct {
		name string
		run  func(string) string
	}{
		{"Bold", st.Bold},
		{"Dim", st.Dim},
		{"Accent", st.Accent},
		{"Red", st.Red},
		{"Yellow", st.Yellow},
	}
	for _, m := range methods {
		if got := m.run("token"); got != "token" {
			t.Errorf("%s on a buffer wrote %q, want the input unchanged", m.name, got)
		}
	}
}

// A forcibly enabled Style emits SGR escapes and closes them with a full reset.
func TestStyleEnabledEmitsEscapes(t *testing.T) {
	st := Style{enabled: true}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"Bold", st.Bold("x"), "\x1b[1mx\x1b[0m"},
		{"Dim", st.Dim("x"), "\x1b[2mx\x1b[0m"},
		{"Red", st.Red("x"), "\x1b[31mx\x1b[0m"},
		{"Yellow", st.Yellow("x"), "\x1b[33mx\x1b[0m"},
		{"Accent", st.Accent("x"), "\x1b[38;5;141mx\x1b[0m"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

// A character-device writer with a clean environment is exactly the case
// colour is for: the Style must be enabled end to end, not just when forced.
func TestStyleTerminalEnabled(t *testing.T) {
	unsetNOCOLOR(t)
	t.Setenv("TERM", "xterm")
	t.Setenv("TERM_PROGRAM", "vscode")
	st := NewStyle(&terminalBuffer{})
	if got := st.Bold("x"); got != "\x1b[1mx\x1b[0m" {
		t.Errorf("a character-device writer with a clean environment should enable colour, got %q", got)
	}
}

// Per no-color.org, NO_COLOR set to the empty string is still the user saying
// "no colour": LookupEnv, not Getenv, must drive the decision.
func TestStyleNOCOLOREmptyDisables(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm")
	t.Setenv("TERM_PROGRAM", "vscode")
	st := NewStyle(&terminalBuffer{})
	if got := st.Bold("x"); got != "x" {
		t.Errorf("NO_COLOR= must disable colour, got %q", got)
	}
}

// TERM=dumb is a terminal that cannot render SGR sequences, so colour must stay
// off for it no matter how capable the writer looks.
func TestStyleTermDumbDisables(t *testing.T) {
	unsetNOCOLOR(t)
	t.Setenv("TERM", "dumb")
	t.Setenv("TERM_PROGRAM", "vscode")
	st := NewStyle(&terminalBuffer{})
	if got := st.Red("x"); got != "x" {
		t.Errorf("TERM=dumb must disable colour, got %q", got)
	}
}

// The tab-invariant, tested at the layer that matters. text/tabwriter aligns
// columns by raw byte count, so an escape sequence on a tab-terminated line
// would push every column on that line to the right. renderFinding is the
// renderer that builds tabbed lines, so its raw output — before alignment —
// must never put an escape byte on a line that carries a tab.
func TestRenderFindingTabLinesNeverStyled(t *testing.T) {
	f := finding.Finding{
		Rule:           "runtime.version_mismatch",
		Severity:       finding.SeverityHigh,
		Key:            "node",
		Expected:       "22",
		Actual:         "24.4.0",
		Description:    "CI installs a Node version this machine does not match.",
		Recommendation: "update actions/setup-node to 24",
	}
	out := renderFinding(f, Style{enabled: true})
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected styled output, got none:\n%q", out)
	}
	for n, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "\t") && strings.Contains(line, "\x1b") {
			t.Errorf("line %d carries both a tab and an escape byte: %q", n+1, line)
		}
	}
}

// The same invariant through the public renderer, driven by a forcibly enabled
// Style (a character-device writer with a clean environment). Alignment strips
// tabs, so the binding check lives in TestRenderFindingTabLinesNeverStyled;
// this guards the whole DoctorText path against styling a tabbed line.
func TestDoctorTextTabLinesNeverStyled(t *testing.T) {
	unsetNOCOLOR(t)
	t.Setenv("TERM", "xterm")
	t.Setenv("TERM_PROGRAM", "vscode")
	w := &terminalBuffer{}
	findings := []finding.Finding{highFinding(), mediumFinding()}
	if err := DoctorText(w, "local", "ci", findings); err != nil {
		t.Fatalf("DoctorText: %v", err)
	}
	out := w.String()
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected styled output, got none:\n%q", out)
	}
	for n, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "\t") && strings.Contains(line, "\x1b") {
			t.Errorf("line %d carries both a tab and an escape byte: %q", n+1, line)
		}
	}
}
