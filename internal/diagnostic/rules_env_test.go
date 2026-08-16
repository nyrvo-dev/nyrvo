package diagnostic

import (
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// envSnap builds a snapshot with an environment section, or without one when
// there are no names and the list is complete.
func envSnap(name string, partial bool, names ...string) *snapshot.Snapshot {
	s := snapshot.New(name, time.Time{})
	if len(names) > 0 || partial {
		s.Environment = &snapshot.Environment{Names: names, Partial: partial}
	}
	return s
}

func TestEnvMissing(t *testing.T) {
	tests := []struct {
		name string
		a, b *snapshot.Snapshot
		want int
		keys []string // variable names the findings must cover
	}{
		{"each side sets a variable the other lacks",
			envSnap("a", false, "REDIS_URL"), envSnap("b", false, "CI"),
			2, []string{"REDIS_URL", "CI"}},
		{"same variables",
			envSnap("a", false, "CI", "REDIS_URL"), envSnap("b", false, "CI", "REDIS_URL"),
			0, nil},
		{"partial side cannot testify to absence",
			envSnap("a", false, "REDIS_URL", "CI"), envSnap("b", true, "CI"),
			0, nil},
		{"whole section absent is silence",
			envSnap("a", false, "REDIS_URL"), snapshot.New("b", time.Time{}),
			0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Input{A: tt.a, B: tt.b, Diff: diff.Compare(tt.a, tt.b)}
			findings := evalRule(t, finding.EnvMissing, in)
			if len(findings) != tt.want {
				t.Fatalf("findings = %d, want %d: %+v", len(findings), tt.want, findings)
			}
			for _, f := range findings {
				if f.Rule != finding.EnvMissing || f.Severity != finding.SeverityMedium ||
					f.Component != diff.ComponentEnvironment ||
					f.Expected != "present" || f.Actual != "missing" {
					t.Errorf("finding = %+v", f)
				}
				if f.Description == "" {
					t.Error("description empty")
				}
			}
			for _, k := range tt.keys {
				found := false
				for _, f := range findings {
					if f.Key == k {
						found = true
					}
				}
				if !found {
					t.Errorf("no finding for %q: %+v", k, findings)
				}
			}
		})
	}
}

func TestEnvMissingNilInput(t *testing.T) {
	if got := evalRule(t, finding.EnvMissing, Input{}); len(got) != 0 {
		t.Errorf("env missing on empty input: %+v", got)
	}
	empty := diff.Compare(nil, nil)
	if got := evalRule(t, finding.EnvMissing, Input{Diff: empty}); len(got) != 0 {
		t.Errorf("env missing on empty diff: %+v", got)
	}
}
