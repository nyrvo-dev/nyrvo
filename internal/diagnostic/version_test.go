package diagnostic

import "testing"

// Satisfies is the rule that decides whether a version difference is reported
// at all, so its edge cases are the difference between a useful tool and one
// that cries wolf on every correctly configured project.
func TestSatisfies(t *testing.T) {
	tests := []struct {
		declared string
		observed string
		want     bool
	}{
		// A declared prefix is what setup actions actually mean.
		{"1.26", "1.26.6", true},
		{"20", "20.11.1", true},
		{"1.26.6", "1.26.6", true},
		{"3", "3.13.3", true},

		// Segments, not characters: "2" must not match "20".
		{"2", "20.11.1", false},
		{"1.2", "1.26.6", false},

		// Java 8 is 1.8.0 on the wire (JDK 8's -version) and "8" in setup-java.
		{"8", "1.8.0", true},
		{"1.8", "8", true},
		{"1.8.0", "1.8.0", true},
		{"8", "11.0.2", false},
		{"11", "1.8.0", false},

		// Real mismatches.
		{"22", "24.4.0", false},
		{"1.25", "1.26.6", false},
		{"1.26.5", "1.26.6", false},

		// A declaration more precise than the observation cannot be confirmed.
		{"1.26.6", "1.26", false},

		// Nothing to compare.
		{"", "1.26.6", false},
		{"1.26", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		if got := Satisfies(tt.declared, tt.observed); got != tt.want {
			t.Errorf("Satisfies(%q, %q) = %v, want %v", tt.declared, tt.observed, got, tt.want)
		}
	}
}

func TestDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want string
	}{
		{"24.4.0", "22.18.0", "major"},
		{"1.26.6", "1.25.0", "minor"},
		{"1.26.6", "1.26.5", "patch"},
		{"1.26.6", "1.26.6", ""},
		{"1.26", "1.26.6", ""},
		{"1.2.3.4", "1.2.3.9", "patch"},
		{"", "1.26", ""},
		{"abc", "1.26", ""},
	}
	for _, tt := range tests {
		if got := Distance(tt.a, tt.b); got != tt.want {
			t.Errorf("Distance(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

// A version with a pre-release suffix must not panic and must compare on the
// numeric part it does have.
func TestSegmentsStopAtNonNumeric(t *testing.T) {
	if !Satisfies("1.26", "1.26.0-rc1") {
		t.Error(`Satisfies("1.26", "1.26.0-rc1") = false, want true`)
	}
	if got := Distance("1.26.0-rc1", "1.25.0"); got != "minor" {
		t.Errorf("Distance with a pre-release = %q, want minor", got)
	}
}

// The Java 1.x rewrite has an upper bound, and the bound is the whole reason it
// is safe. Java's old scheme only ever reached 1.8, so 1.5 through 1.8 collapse
// to their major. Go 1.26 shares that shape and must not: a declared "1.26"
// satisfied by "26" would silently accept a toolchain 25 versions apart.
// Widening the guard to any 1.x passes every other test in this file, so this
// is the one that holds it.
func TestSatisfiesDoesNotTreatEveryOnePointXAsJavaLegacy(t *testing.T) {
	for _, tc := range []struct {
		declared, observed string
		want               bool
	}{
		{"1.8", "8", true},       // real Java legacy
		{"8", "1.8.0_412", true}, // and the other direction
		{"1.5", "5", true},
		{"1.26", "26", false}, // Go, not Java 26
		{"1.9", "9", false},   // above the legacy scheme
		{"1.26", "1.26.6", true},
		{"2", "20", false},  // the original segment rule still holds
		{"2.8", "8", false}, // only 1.x was Java's old scheme, not any x.8
		{"1.8", "1.8.0_412", true},
	} {
		if got := Satisfies(tc.declared, tc.observed); got != tc.want {
			t.Errorf("Satisfies(%q, %q) = %v, want %v", tc.declared, tc.observed, got, tc.want)
		}
	}
}
