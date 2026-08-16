package diagnostic

import "testing"

// TestSatisfiesConstraint covers the two answers separately: whether a
// constraint is met, and whether it was understood at all. Conflating them is
// the failure mode that matters — an unparsed constraint that reports "not
// met" would invent a high-severity finding out of syntax Nyrvo simply does
// not read.
func TestSatisfiesConstraint(t *testing.T) {
	tests := []struct {
		constraint string
		observed   string
		met        bool
		decided    bool
	}{
		// The constitution's own example.
		{">=24", "22.11.0", false, true},
		{">=24", "24.0.0", true, true},
		{">=24", "25.1.0", true, true},

		{">18", "18.0.0", false, true},
		{">18", "18.1.0", true, true},
		{"<21", "20.11.1", true, true},
		{"<21", "21.0.0", false, true},
		{"<=20", "20.11.1", false, true},
		{"<=20.11.1", "20.11.1", true, true},
		{"=20.11.1", "20.11.1", true, true},

		// A bare version is a prefix, matching how these files are written.
		{"20", "20.11.1", true, true},
		{"20", "22.0.0", false, true},
		{"1.25", "1.25.3", true, true},
		{"20.11.1", "20.11.1", true, true},
		{"20.11.1", "20.11.2", false, true},
		// Segment equality, not string prefix: "2" must not admit "20".
		{"2", "20.0.0", false, true},

		// Wildcards state precision the prefix rule already expresses.
		{"20.x", "20.11.1", true, true},
		{"20.X", "21.0.0", false, true},
		{"3.11.*", "3.11.7", true, true},
		{"*", "1.2.3", true, true},
		{"x", "1.2.3", true, true},

		// Caret pins the leftmost non-zero segment.
		{"^20", "20.11.1", true, true},
		{"^20", "21.0.0", false, true},
		{"^20.1", "20.1.0", true, true},
		{"^20.1", "20.0.9", false, true},
		{"^20.1", "20.99.0", true, true},
		{"^0.2.3", "0.2.9", true, true},
		{"^0.2.3", "0.3.0", false, true},
		{"^0.2.3", "0.2.2", false, true},

		// Tilde pins the minor when one is stated.
		{"~3.11", "3.11.7", true, true},
		{"~3.11", "3.12.0", false, true},
		{"~3.11", "3.10.9", false, true},
		{"~3.11.2", "3.11.9", true, true},
		{"~3.11.2", "3.11.1", false, true},
		{"~3", "3.9.9", true, true},
		{"~3", "4.0.0", false, true},

		// Conjunctions: every term must hold.
		{">=18.0.0 <21", "20.11.1", true, true},
		{">=18.0.0 <21", "21.0.0", false, true},
		{">=18.0.0 <21", "17.9.0", false, true},
		{">=18, <21", "20.0.0", true, true},

		// Alternatives: one satisfied branch settles it.
		{">=14 || >=16", "16.0.0", true, true},
		{"^18 || ^20", "20.1.0", true, true},
		{"^18 || ^20", "19.0.0", false, true},

		// Undecidable input is declined, never guessed at.
		{"", "20.0.0", false, false},
		{"latest", "20.0.0", false, false},
		{"lts/iron", "20.0.0", false, false},
		{"workspace:*", "20.0.0", false, false},
		{"github:nodejs/node", "20.0.0", false, false},
		{"1.2 - 2.3", "1.5.0", false, false},
		{">=24", "", false, false},
		{">=24", "system", false, false},
		// An unreadable branch cannot be dismissed just because a readable one
		// failed: the unreadable one might have been the satisfied branch.
		{"^18 || lts/iron", "20.0.0", false, false},
		// A decisive failure settles a conjunction even next to an unreadable
		// term, because nothing a second condition says can rescue it.
		{">=24 nonsense", "20.0.0", false, true},
	}

	for _, tt := range tests {
		met, decided := SatisfiesConstraint(tt.constraint, tt.observed)
		if met != tt.met || decided != tt.decided {
			t.Errorf("SatisfiesConstraint(%q, %q) = (%v, %v), want (%v, %v)",
				tt.constraint, tt.observed, met, decided, tt.met, tt.decided)
		}
	}
}

// TestRequirementMetImpreciseVersion covers the declared side: a workflow that
// asks for node "20" stands for a whole range, and a verdict is only honest
// when it holds across that entire range.
func TestRequirementMetImpreciseVersion(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
		version    string
		precise    bool
		met        bool
		decided    bool
	}{
		{"declared major fails a floor above it", ">=24", "20", false, false, true},
		{"declared major clears a floor below it", ">=18", "20", false, true, true},
		{"declared major is too coarse for a minor floor", "^20.1", "20", false, false, false},
		{"declared major satisfies its own prefix", "20", "20", false, true, true},
		{"precise version is judged as itself", "^20.1", "20.0.5", true, false, true},
		{"precise version meets the same range", "^20.1", "20.1.0", true, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			met, decided := requirementMet(tt.constraint, tt.version, tt.precise)
			if met != tt.met || decided != tt.decided {
				t.Errorf("requirementMet(%q, %q, precise=%v) = (%v, %v), want (%v, %v)",
					tt.constraint, tt.version, tt.precise, met, decided, tt.met, tt.decided)
			}
		})
	}
}

func TestSpacedComparatorsAreOneTerm(t *testing.T) {
	// Rubygems and Composer both write the spaced form. Split on whitespace, it
	// became a bare ">=" that decides nothing plus a bare "3.1" read as a
	// prefix — which then convicted every 3.3.0 in existence.
	cases := []struct {
		constraint, observed string
		met, decided         bool
	}{
		{">= 3.1", "3.3.0", true, true},
		{">= 3.1", "3.0.0", false, true},
		{">=3.1", "3.3.0", true, true},
		{"^ 8.2", "8.5.9", true, true},
		{">= 8.1, < 9.0", "8.5.9", true, true},
		{">= 8.1, < 9.0", "9.1.0", false, true},
	}
	for _, tc := range cases {
		met, decided := SatisfiesConstraint(tc.constraint, tc.observed)
		if met != tc.met || decided != tc.decided {
			t.Errorf("SatisfiesConstraint(%q, %q) = (%v, %v), want (%v, %v)",
				tc.constraint, tc.observed, met, decided, tc.met, tc.decided)
		}
	}
}

func TestPessimisticOperatorIsNotTheTilde(t *testing.T) {
	// "~> 3.1" allows every 3.x and stops at 4.0; "~3.1" stops at 3.2. Reading
	// one as the other rejects the versions the declaration exists to allow.
	cases := []struct {
		constraint, observed string
		met                  bool
	}{
		{"~> 3.1", "3.3.0", true},
		{"~> 3.1", "3.1.0", true},
		{"~> 3.1", "4.0.0", false},
		{"~> 3.1", "3.0.9", false},
		{"~> 3.1.4", "3.1.9", true},
		{"~> 3.1.4", "3.2.0", false},
		{"~3.1", "3.1.9", true},
		{"~3.1", "3.2.0", false},
	}
	for _, tc := range cases {
		met, decided := SatisfiesConstraint(tc.constraint, tc.observed)
		if !decided {
			t.Errorf("SatisfiesConstraint(%q, %q) was undecided; the operator is well defined", tc.constraint, tc.observed)
			continue
		}
		if met != tc.met {
			t.Errorf("SatisfiesConstraint(%q, %q) = %v, want %v", tc.constraint, tc.observed, met, tc.met)
		}
	}
}
