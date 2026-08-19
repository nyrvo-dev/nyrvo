// Package textsafe strips terminal control sequences from text Nyrvo did not
// author. Everything Nyrvo stores or shows can originate outside the machine —
// a job log is terminal output, and workflow and run metadata come from files
// and APIs anyone can publish — and a snapshot itself is a document that gets
// mailed around and pasted into bug reports. It lives in its own package
// because both the collection side and the load side need it.
package textsafe

import (
	"strings"
	"unicode/utf8"
)

// Strip removes ANSI escape sequences and every other C0 control character
// except tab. It is the sanitizer for everything Nyrvo stores from input it
// does not control: a job log is terminal output (docs/adr/0011), and workflow
// and run metadata — step names, job names, runs-on labels, container images —
// comes from files and APIs anyone can publish. A control byte could move a
// cursor, repaint a line, or hide text in a report a human is reading to make
// a decision, so nothing but printable text may survive into a snapshot.
//
// CSI (ESC [ … final), OSC (ESC ] … BEL or ST), and the 8-bit C1 introducers
// (U+009B CSI, U+009D OSC) are consumed in full. An earlier version only
// skipped CSI and one byte after any other ESC, which left OSC payloads and C1
// sequences in the string a terminal would then interpret.
//
// Decoding runes matters rather than being a detail. 0x9B and 0x9D are also
// ordinary UTF-8 continuation bytes: "‛" is E2 80 9B and "ﾝ" is EF BE 9D, so a
// byte-wise scan mistook the tail of a perfectly good character for a control
// introducer and skipped forward looking for a terminator that was never
// coming. It ate the rest of "half ﾝ width" and left invalid UTF-8 behind.
// A C1 control arrives as a rune (U+009B encodes as C2 9B), so matching runes
// catches every real one and no innocent byte.
func Strip(s string) string { return strip(s, false) }

// StripKeepingNewlines is Strip for text that is meant to stay multi-line.
// An agent's answer is paragraphs; a snapshot field is one line. Both must lose
// every sequence a terminal would act on, and they differ only in whether a
// newline is one of them.
func StripKeepingNewlines(s string) string { return strip(s, true) }

// strip is the single implementation behind both. The one difference is an
// argument rather than a second copy of this switch: two copies is exactly how
// the agent's cleaner and the snapshot's stripper drifted apart, and only one
// of them was ever fixed.
func strip(s string, keepNewlines bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == 0x1b, r == 0x9b, r == 0x9d, r == 0x90, r == 0x98, r == 0x9e, r == 0x9f:
			i = SkipControlSequence(s, i)
		case r == utf8.RuneError && size == 1 && (s[i] == 0x9b || s[i] == 0x9d):
			// A lone C1 introducer, which only a raw byte stream produces.
			i = SkipControlSequence(s, i)
		case r == utf8.RuneError && size == 1 && (s[i] == 0x90 || s[i] == 0x98 || s[i] == 0x9e || s[i] == 0x9f):
			i = skipPayloadSequence(s, i+1)
		case r == utf8.RuneError && size == 1:
			// Any other byte that is not valid UTF-8. Dropping it keeps the
			// result decodable instead of passing the damage along.
			i++
		case r == '\t', r == '\n' && keepNewlines:
			b.WriteRune(r)
			i += size
		case r < 0x20:
			i += size
		case r == 0x7f:
			i += size
		case r >= 0x80 && r <= 0x9f && r != 0x90 && r != 0x98 && r != 0x9e && r != 0x9f:
			// The rest of the C1 block. The four string-type introducers are
			// handled above, because they own a payload rather than standing
			// alone.
			i += size
		default:
			b.WriteString(s[i : i+size])
			i += size
		}
	}
	return b.String()
}

// SkipControlSequence returns the index just past a control sequence that
// starts at i. i must point at ESC (0x1B) or an 8-bit C1 introducer. The
// agent's output cleaner uses the same skip so a sequence that reaches a
// snapshot and one that reaches a terminal are stripped the same way.
func SkipControlSequence(s string, i int) int {
	if i >= len(s) {
		return i
	}
	r, size := utf8.DecodeRuneInString(s[i:])
	if r == utf8.RuneError && size == 1 {
		// A byte that is not valid UTF-8 at all. A job log is a raw byte
		// stream, so a bare 0x9B or 0x9D really is a C1 introducer a terminal
		// would act on. This is exactly the case a decoded rune cannot be: a
		// well-formed U+009B is two bytes, and a lone one is not text.
		switch s[i] {
		case 0x9b:
			return skipCSI(s, i+1)
		case 0x9d:
			return skipOSC(s, i+1)
		case 0x90, 0x98, 0x9e, 0x9f:
			// C1 DCS, SOS, PM and APC.
			return skipPayloadSequence(s, i+1)
		}
		return i + 1
	}
	switch r {
	case 0x1b:
		return skipEscapeSequence(s, i+size)
	case 0x9b: // C1 CSI
		return skipCSI(s, i+size)
	case 0x9d: // C1 OSC
		return skipOSC(s, i+size)
	case 0x90, 0x98, 0x9e, 0x9f: // C1 DCS, SOS, PM, APC
		return skipPayloadSequence(s, i+size)
	default:
		return i + size
	}
}

func skipEscapeSequence(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		return skipCSI(s, i+1)
	case ']':
		return skipOSC(s, i+1)
	case 'P', 'X', '^', '_':
		// The string-type sequences: DCS (P), SOS (X), PM (^) and APC (_).
		// All four carry a payload terminated by ST or BEL, so all four have
		// to be consumed whole. Skipping only the introducer leaves the
		// payload as text a terminal would still act on.
		return skipPayloadSequence(s, i+1)
	default:
		return i + 1
	}
}

// skipPayloadSequence returns the index just past a DCS, APC, or SOS sequence
// that started immediately after its introducer byte.
func skipPayloadSequence(s string, i int) int {
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == 0x07 { // BEL
			return i + size
		}
		if r == 0x9c { // C1 ST
			return i + size
		}
		if r == 0x1b && i+size < len(s) && s[i+size] == '\\' { // ESC \ (7-bit ST)
			return i + size + 1
		}
		i += size
	}
	return len(s)
}

func skipCSI(s string, i int) int {
	for ; i < len(s); i++ {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
	}
	return len(s)
}

func skipOSC(s string, i int) int {
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == 0x07 { // BEL
			return i + size
		}
		if r == 0x9c { // C1 ST
			return i + size
		}
		if r == 0x1b && i+size < len(s) && s[i+size] == '\\' { // ESC \ (7-bit ST)
			return i + size + 1
		}
		i += size
	}
	return len(s)
}

// StripAll strips control bytes from every note, in place. A note list is
// assembled from strings that entered the process through a workflow file or a
// run's API metadata, so no entry may carry a terminal escape sequence into a
// snapshot. It is idempotent: notes that are already printable pass through
// unchanged.
func StripAll(notes []string) []string {
	for i := range notes {
		notes[i] = Strip(notes[i])
	}
	return notes
}
