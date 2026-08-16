package diagnostic

import (
	"regexp"
	"strings"
)

// SatisfiesConstraint reports whether an observed version meets a version
// constraint a project declared, and whether that question could be answered at
// all.
//
// The second return value is the important one. Constraint syntax is an open
// world — npm ranges, pyenv pins, asdf lines, workspace protocols, git URLs —
// and Nyrvo reads these files from repositories it did not write. A constraint
// this function does not fully understand yields decided=false, and the caller
// reports nothing. Silence about a constraint we cannot parse is cheap; a
// confident high-severity finding built on a guess is what teaches people to
// ignore the tool.
//
// Supported: comparators (>=, >, <=, <, =), caret and tilde ranges, wildcards
// ("20.x"), bare prefixes ("20", "3.11.2"), space- or comma-separated
// conjunctions, and "||" alternatives. Hyphen ranges and anything else are
// declined rather than approximated.
func SatisfiesConstraint(constraint, observed string) (met, decided bool) {
	v := segments(observed)
	if len(v) == 0 {
		return false, false
	}
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return false, false
	}
	// A hyphen range ("1.2 - 2.3") would otherwise split into terms that each
	// look like a bare version and be judged as three separate prefixes, which
	// is not merely imprecise but wrong. Decline the whole constraint.
	if strings.Contains(constraint, " - ") {
		return false, false
	}

	undecided := false
	for _, alt := range strings.Split(constraint, "||") {
		ok, dec := satisfiesAll(alt, v)
		if dec && ok {
			// One satisfied alternative settles the constraint, whatever the
			// others are made of.
			return true, true
		}
		if !dec {
			undecided = true
		}
	}
	if undecided {
		return false, false
	}
	return false, true
}

// satisfiesAll evaluates one alternative: a conjunction of terms separated by
// commas or spaces, as in ">=18.0.0 <21".
func satisfiesAll(alt string, v []int) (met, decided bool) {
	terms := strings.FieldsFunc(spacedOperator.ReplaceAllString(alt, "$1"), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	if len(terms) == 0 {
		return false, false
	}
	undecided := false
	for _, term := range terms {
		ok, dec := satisfiesTerm(term, v)
		// A term that decisively fails settles the conjunction even if a later
		// term is unreadable: nothing a second condition says can rescue it.
		if dec && !ok {
			return false, true
		}
		if !dec {
			undecided = true
		}
	}
	if undecided {
		return false, false
	}
	return true, true
}

// spacedOperator matches a comparator separated from its version by spaces.
// Splitting on whitespace before joining them apart makes ">= 3.1" two terms:
// a bare ">=" that decides nothing and a bare "3.1" read as a prefix, which
// then convicts 3.3.0. Rubygems and Composer both write the spaced form, so
// this is the common spelling rather than an exotic one.
var spacedOperator = regexp.MustCompile(`(>=|<=|~>|[<>=^~])\s+`)

// satisfiesTerm evaluates a single comparator-and-version term.
func satisfiesTerm(term string, v []int) (met, decided bool) {
	term = strings.TrimSpace(term)
	// "*" and a bare "x" mean any version, which every observation satisfies.
	if term == "*" || term == "x" || term == "X" {
		return true, true
	}

	op := ""
	// Longer operators are tried first: "~>" must not be read as "~" with a
	// stray ">" left on the version.
	for _, candidate := range []string{">=", "<=", "~>", "^", "~", ">", "<", "="} {
		if strings.HasPrefix(term, candidate) {
			op = candidate
			term = strings.TrimPrefix(term, candidate)
			break
		}
	}
	term = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(term), "v"))

	// A trailing wildcard states the precision of the declaration, which prefix
	// matching already expresses: "20.x" and "20" allow the same versions.
	term = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(term, ".x"), ".X"), ".*")

	r := segments(term)
	if len(r) == 0 {
		return false, false
	}

	switch op {
	case ">=":
		return compareVersions(v, r) >= 0, true
	case ">":
		return compareVersions(v, r) > 0, true
	case "<=":
		return compareVersions(v, r) <= 0, true
	case "<":
		return compareVersions(v, r) < 0, true
	case "^":
		return withinRange(v, r, caretUpper(r)), true
	case "~":
		return withinRange(v, r, tildeUpper(r)), true
	case "~>":
		// Rubygems' pessimistic operator is not npm's tilde. "~> 3.1" allows
		// every 3.x and stops at 4.0, while "~3.1" stops at 3.2. Mapping one
		// onto the other rejects the versions the declaration was written to
		// allow.
		return withinRange(v, r, pessimisticUpper(r)), true
	default:
		// A bare version is a prefix, not an exact demand: engines.node "20"
		// admits 20.11.1, and .nvmrc "20.11.1" admits exactly that. This is the
		// same rule Satisfies applies to a declared workflow version.
		return prefixMatch(r, v), true
	}
}

// withinRange reports whether v is at least low and below high, the shape both
// caret and tilde ranges reduce to.
func withinRange(v, low, high []int) bool {
	return compareVersions(v, low) >= 0 && compareVersions(v, high) < 0
}

// caretUpper is the first version a caret range excludes. Caret allows changes
// that do not alter the leftmost non-zero segment, which is what makes "^0.2.3"
// far stricter than "^2.3.0": before 1.0 the minor segment carries the breaking
// changes.
func caretUpper(r []int) []int {
	for i, n := range r {
		if n != 0 {
			return bump(r, i)
		}
	}
	return bump(r, len(r)-1)
}

// pessimisticUpper is the exclusive bound of Rubygems' "~>": the last declared
// segment is dropped and the one before it is raised, so "~> 3.1" bounds at 4.0
// and "~> 3.1.4" bounds at 3.2.0.
func pessimisticUpper(r []int) []int {
	if len(r) <= 1 {
		return bump(r, 0)
	}
	return bump(r[:len(r)-1], len(r)-2)
}

// tildeUpper is the first version a tilde range excludes: tilde pins the minor
// segment when one is given ("~3.11" allows 3.11.9 but not 3.12), and behaves
// like caret when only a major is stated.
func tildeUpper(r []int) []int {
	if len(r) >= 2 {
		return bump(r, 1)
	}
	return bump(r, 0)
}

// bump returns r with segment i incremented and everything after it dropped,
// which is the exclusive upper bound of a range pinned at that segment.
func bump(r []int, i int) []int {
	out := make([]int, i+1)
	copy(out, r[:i+1])
	out[i]++
	return out
}

// compareVersions orders two segment lists, padding the shorter with zeros:
// "20" and "20.0.0" are the same version, and a declaration that stops early
// says nothing about the segments it omits.
func compareVersions(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		}
	}
	return 0
}
