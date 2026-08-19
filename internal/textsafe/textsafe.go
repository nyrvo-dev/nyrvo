// Package textsafe strips terminal control sequences from text Nyrvo did not
// author. Everything Nyrvo stores or shows can originate outside the machine —
// a job log is terminal output, and workflow and run metadata come from files
// and APIs anyone can publish — and a snapshot itself is a document that gets
// mailed around and pasted into bug reports. It lives in its own package
// because both the collection side and the load side need it.
package textsafe

import "strings"

// Strip removes ANSI escape sequences and every other C0 control character
// except tab. It is the sanitizer for everything Nyrvo stores from input it
// does not control: a job log is terminal output (docs/adr/0011), and workflow
// and run metadata — step names, job names, runs-on labels, container images —
// comes from files and APIs anyone can publish. A control byte could move a
// cursor, repaint a line, or hide text in a report a human is reading to make
// a decision, so nothing but printable text may survive into a snapshot.
func Strip(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			// CSI: ESC [ parameters... final-byte.
			if i+1 < len(s) && s[i+1] == '[' {
				i += 2
				for i < len(s) && !isCSIFinalByte(s[i]) {
					i++
				}
				continue
			}
			// Any other escape: skip the introducer and its one following byte.
			i++
			continue
		}
		if c < 0x20 && c != '\t' {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// isCSIFinalByte reports whether c terminates a CSI escape sequence. Everything
// between the introducer and this byte is parameters, which are discarded with
// the sequence itself.
func isCSIFinalByte(c byte) bool { return c >= 0x40 && c <= 0x7e }

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
