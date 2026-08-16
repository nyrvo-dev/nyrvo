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
