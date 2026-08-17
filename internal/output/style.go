package output

import (
	"io"
	"os"
	"runtime"
)

// Style wraps tokens in ANSI SGR escape sequences when the destination is a
// real terminal that can interpret them, and returns tokens unchanged
// otherwise. Every yes/no decision about colour is made once, in NewStyle, from
// the writer and the process environment; a renderer never reasons about where
// it is writing, so the same bytes go to a terminal, a file, and a test, and
// only the terminal gets colour.
type Style struct {
	enabled bool
}

// NewStyle decides whether colour is acceptable for w and returns a Style that
// enforces that decision. Colour is enabled only when every one of these holds:
//
//   - w is a real terminal, not a buffer, file, or pipe;
//   - NO_COLOR is unset in the environment;
//   - TERM is not "dumb";
//   - on Windows, the terminal is known to interpret SGR sequences.
//
// A bytes.Buffer fails the first check, which is exactly why the existing golden
// tests keep passing untouched: they all render through a buffer, and nothing
// here makes a buffer look like a terminal.
func NewStyle(w io.Writer) Style {
	if !isTerminal(w) {
		return Style{}
	}
	// The no-color.org convention is that the variable's presence is the
	// instruction, whatever its value — NO_COLOR= (set to the empty string)
	// must disable colour just as firmly as NO_COLOR=1. LookupEnv is used
	// because Getenv cannot distinguish "unset" from "set to empty".
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return Style{}
	}
	if os.Getenv("TERM") == "dumb" {
		return Style{}
	}
	// The legacy Windows console does not process SGR sequences unless its VT
	// mode flag is set, and would print the escapes as literal garbage such as
	// ESC-bracket-1-m. Colour is therefore offered on Windows only to terminals
	// that announce themselves as modern: Windows Terminal, a terminal app such
	// as VS Code, or ConEmu. On other operating systems these variables are
	// irrelevant to the console and are not consulted.
	if runtime.GOOS == "windows" &&
		os.Getenv("WT_SESSION") == "" && os.Getenv("TERM_PROGRAM") == "" && os.Getenv("ConEmuANSI") == "" {
		return Style{}
	}
	return Style{enabled: true}
}

// statter is the smallest part of *os.File that identifies a terminal without
// adding a dependency: Stat reports the file's mode, and a terminal is a
// character device. Nothing else in the standard library exposes that fact, and
// a bytes.Buffer has no Stat method at all.
type statter interface {
	Stat() (os.FileInfo, error)
}

func isTerminal(w io.Writer) bool {
	s, ok := w.(statter)
	if !ok {
		return false
	}
	fi, err := s.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// SGR (Select Graphic Rendition) escapes. "\x1b" is ESC; the codes are opened
// with a parameter and closed with a full "all attributes off" reset. The
// reset is not a per-attribute undo because styled tokens are single strings on
// their own lines, never nested, so there is no surrounding style to preserve.
const (
	sgrPrefix = "\x1b["
	sgrReset  = sgrPrefix + "0m"
)

// wrap encloses text in an SGR sequence when colour is on and returns it
// untouched when it is not.
//
// The callers that use these methods style only lines that carry no tab:
// text/tabwriter counts raw bytes, not display width, so an escape sequence on
// a tab-terminated line would push every column on that line to the right.
func (st Style) wrap(code, text string) string {
	if !st.enabled {
		return text
	}
	return sgrPrefix + code + "m" + text + sgrReset
}

// Bold, Dim, Red and Yellow are the named terminal colours used by the
// renderers. Each wraps one token and nothing more.
func (st Style) Bold(text string) string   { return st.wrap("1", text) }
func (st Style) Dim(text string) string    { return st.wrap("2", text) }
func (st Style) Red(text string) string    { return st.wrap("31", text) }
func (st Style) Yellow(text string) string { return st.wrap("33", text) }

// Accent is 256-colour index 141, the nearest match to the purple Nyrvo uses on
// nyrvo.dev. It is reserved for the one identifying token a report repeats — the
// rule id — which sits on a line that carries no tab.
func (st Style) Accent(text string) string { return st.wrap("38;5;141", text) }
