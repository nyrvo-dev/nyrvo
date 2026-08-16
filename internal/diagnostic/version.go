package diagnostic

import (
	"strconv"
	"strings"
)

// Satisfies reports whether an observed version meets a declared one.
//
// A declared version is often a prefix: `actions/setup-go` with `go-version:
// "1.26"` installs the latest 1.26.x, and `setup-node` with `node-version: 20`
// installs the latest 20.x. Comparing those literally against a machine running
// 1.26.6 or 20.11.1 would report a mismatch on every correctly configured
// project — the false positive that makes a diagnostic tool worth ignoring.
//
// Matching is per segment, not per character, so a declared "2" is not
// satisfied by "20": segment equality is what version numbers actually mean.
func Satisfies(declared, observed string) bool {
	d := segments(declared)
	o := segments(observed)
	if len(d) == 0 || len(o) == 0 || len(d) > len(o) {
		// A declaration more precise than the observation cannot be confirmed;
		// callers treat that as a mismatch worth reporting rather than a silent
		// pass.
		return false
	}
	for i := range d {
		if d[i] != o[i] {
			return false
		}
	}
	return true
}

// Distance names the most significant segment at which two versions diverge:
// "major", "minor", "patch", or "" when neither is more than a prefix of the
// other. Rules use it to weigh a mismatch — a major gap is a different runtime,
// a patch gap is usually noise.
func Distance(a, b string) string {
	as, bs := segments(a), segments(b)
	names := []string{"major", "minor", "patch"}
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		if i < len(names) {
			return names[i]
		}
		// Beyond patch, any further divergence is at most as significant as a
		// patch difference.
		return "patch"
	}
	return ""
}

// segments splits a normalized version into its numeric parts. A part that is
// not a number ends the version: "1.26.0-rc1" compares as 1.26.0, which is the
// most that can be claimed without implementing a full pre-release ordering
// nobody has asked for yet.
func segments(v string) []int {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	var out []int
	for _, part := range strings.Split(v, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}
