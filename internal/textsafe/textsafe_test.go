package textsafe

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStrip(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{name: "plain", in: "hello", want: "hello"},
		{name: "empty", in: "", want: ""},
		{name: "tab preserved", in: "a\tb", want: "a\tb"},
		{name: "CSI clear screen", in: "keep\x1b[2Jhidden", want: "keephidden"},
		{name: "CSI color", in: "\x1b[31mred\x1b[0m", want: "red"},
		{name: "OSC BEL", in: "ci.yml#job\x1b]0;owned\x07done", want: "ci.yml#jobdone"},
		{name: "OSC ST", in: "a\x1b]0;title\x1b\\b", want: "ab"},
		{name: "DCS BEL", in: "keep\x1bPpayload\x07hidden", want: "keephidden"},
		{name: "DCS ST", in: "a\x1bPdata\x1b\\b", want: "ab"},
		{name: "8-bit DCS", in: "x\x90payload\x07y", want: "xy"},
		{name: "8-bit CSI", in: "x\x9b2Jy", want: "xy"},
		{name: "8-bit OSC BEL", in: "x\x9d0;owned\x07y", want: "xy"},
		{name: "other C0 dropped", in: "a\x00b\nc", want: "abc"},
		{name: "idempotent", in: "already clean", want: "already clean"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Strip(tt.in)
			if got != tt.want {
				t.Errorf("Strip(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if again := Strip(got); again != got {
				t.Errorf("Strip is not idempotent: %q -> %q", got, again)
			}
		})
	}
}

func TestStripAll(t *testing.T) {
	in := []string{"ok", "\x1b[2Jbad"}
	got := StripAll(in)
	if got[0] != "ok" || got[1] != "bad" {
		t.Errorf("StripAll = %q, want [ok bad]", got)
	}
}

// 0x9B and 0x9D are C1 introducers and also ordinary UTF-8 continuation bytes.
// Scanning bytes conflated the two: "\u201b" is E2 80 9B and "\uff9d" is EF BE 9D,
// so the tail of a good character was read as a control introducer and the
// stripper skipped forward hunting a terminator that never came -- it ate the
// rest of the string and left invalid UTF-8. A lone 0x9D byte, which only a raw
// byte stream produces, still has to be treated as a control.
func TestStripKeepsMultibyteTextAndStillCatchesLoneC1(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"right single quote survives", "quote \u201b then more", "quote \u201b then more"},
		{"halfwidth katakana survives", "half \uff9d width", "half \uff9d width"},
		{"cjk branch name survives", "feature/\u65e5\u672c\u8a9e", "feature/\u65e5\u672c\u8a9e"},
		{"lone C1 OSC byte is a control", "x\x9d0;owned\ay", "xy"},
		{"encoded C1 OSC rune is a control", "x\u009d0;owned\ay", "xy"},
		{"lone C1 CSI byte is a control", "x\x9b31mred", "xred"},
		{"encoded C1 CSI rune is a control", "x\u009b31mred", "xred"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Strip(tc.in)
			if got != tc.want {
				t.Errorf("Strip(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("Strip(%q) returned invalid UTF-8: %q", tc.in, got)
			}
		})
	}
}

// The agent's cleaner and the snapshot's stripper must not drift apart again:
// they were separate copies, and only one of them was ever fixed. The invariant
// is exact rather than a spot check — for any input, the two differ only in
// whether newlines survive — so a future edit to one behaviour cannot quietly
// change the other.
func TestStripKeepingNewlinesDiffersOnlyByNewlines(t *testing.T) {
	esc := "\x1b"
	inputs := []string{
		"",
		"plain text",
		"line one\nline two\nline three",
		"half \uff9d width\nquote \u201b here",
		"a" + esc + "[31mred\nb" + esc + "]0;title\ac",
		"tabs\tkept\nand\tkept",
		"lone C1 \x9d0;owned\ay\nafter",
		"\n\n\n",
		"trailing newline\n",
		"\x00\x01\x02control\nbytes\x7f",
	}
	for _, in := range inputs {
		gotKeep := StripKeepingNewlines(in)
		gotStrip := Strip(in)
		// Removing the newlines from the newline-keeping result must give
		// exactly the result that never kept them.
		if want := strings.ReplaceAll(gotKeep, "\n", ""); want != gotStrip {
			t.Errorf("for %q:\n  StripKeepingNewlines = %q (newlines removed: %q)\n  Strip                = %q",
				in, gotKeep, want, gotStrip)
		}
		// Neither may keep anything a terminal acts on, and both must stay
		// decodable.
		for _, got := range []string{gotKeep, gotStrip} {
			if !utf8.ValidString(got) {
				t.Errorf("for %q: result is not valid UTF-8: %q", in, got)
			}
			for _, r := range got {
				if r == '\n' || r == '\t' {
					continue
				}
				if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
					t.Errorf("for %q: control %#U survived in %q", in, r, got)
				}
			}
		}
	}
	// And the difference is not vacuous: newlines really do survive one and not
	// the other, or the equality above would hold for two identical functions.
	if !strings.Contains(StripKeepingNewlines("a\nb"), "\n") {
		t.Error("StripKeepingNewlines dropped a newline")
	}
	if strings.Contains(Strip("a\nb"), "\n") {
		t.Error("Strip kept a newline")
	}
}
