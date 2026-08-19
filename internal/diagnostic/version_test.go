package diagnostic

import "testing"

// Satisfies is the rule that decides whether a version difference is reported
// at all, so its edge cases are the difference between a useful tool and one
// that cries wolf on every correctly configured project.
func TestSatisfies(t *testing.T) {
	tests := []struct {
		runtime  string
		declared string
		observed string
		want     bool
	}{
		// A declared prefix is what setup actions actually mean.
		{"go", "1.26", "1.26.6", true},
		{"node", "20", "20.11.1", true},
		{"go", "1.26.6", "1.26.6", true},
		{"python", "3", "3.13.3", true},

		// Segments, not characters: "2" must not match "20".
		{"node", "2", "20.11.1", false},
		{"go", "1.2", "1.26.6", false},

		// Java 8 is 1.8.0 on the wire (JDK 8's -version) and "8" in setup-java.
		{"java", "8", "1.8.0", true},
		{"java", "1.8", "8", true},
		{"java", "1.8.0", "1.8.0", true},
		{"java", "8", "11.0.2", false},
		{"java", "11", "1.8.0", false},
		// Go 1.8 must not satisfy a declared "8".
		{"go", "8", "1.8.0", false},

		// Real mismatches.
		{"node", "22", "24.4.0", false},
		{"go", "1.25", "1.26.6", false},
		{"go", "1.26.5", "1.26.6", false},

		// A declaration more precise than the observation cannot be confirmed.
		{"go", "1.26.6", "1.26", false},

		// Nothing to compare.
		{"go", "", "1.26.6", false},
		{"go", "1.26", "", false},
		{"go", "", "", false},
	}
	for _, tt := range tests {
		if got := Satisfies(tt.runtime, tt.declared, tt.observed); got != tt.want {
			t.Errorf("Satisfies(%q, %q, %q) = %v, want %v", tt.runtime, tt.declared, tt.observed, got, tt.want)
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
	if !Satisfies("go", "1.26", "1.26.0-rc1") {
		t.Error(`Satisfies("go", "1.26", "1.26.0-rc1") = false, want true`)
	}
	if got := Distance("1.26.0-rc1", "1.25.0"); got != "minor" {
		t.Errorf("Distance with a pre-release = %q, want minor", got)
	}
}

func TestSatisfiesJavaLegacyIsJavaOnly(t *testing.T) {
	for _, tc := range []struct {
		runtime, declared, observed string
		want                        bool
	}{
		{"java", "1.8", "8", true},
		{"java", "8", "1.8.0_412", true},
		{"java", "1.5", "5", true},
		{"go", "1.26", "26", false},
		{"go", "8", "1.8.0", false},
		{"java", "1.9", "9", false},
		{"go", "1.26", "1.26.6", true},
		{"node", "2", "20", false},
		{"java", "2.8", "8", false},
	} {
		if got := Satisfies(tc.runtime, tc.declared, tc.observed); got != tc.want {
			t.Errorf("Satisfies(%q, %q, %q) = %v, want %v", tc.runtime, tc.declared, tc.observed, got, tc.want)
		}
	}
}
