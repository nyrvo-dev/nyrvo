package diagnostic

import (
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// gitSnap builds a snapshot with a git section.
func gitSnap(name, sha string, dirty bool) *snapshot.Snapshot {
	s := snapshot.New(name, time.Time{})
	s.Git = &snapshot.Git{SHA: sha, Dirty: dirty}
	return s
}

// declaredGitSnap builds the impossible combination a declared environment with
// a dirty git section, to guard the rule against it.
func declaredGitSnap(name, sha string, dirty bool) *snapshot.Snapshot {
	s := gitSnap(name, sha, dirty)
	s.Source = &snapshot.Source{Kind: snapshot.SourceGitHubActions}
	return s
}

func TestGitSHAMismatch(t *testing.T) {
	tests := []struct {
		name     string
		a, b     *snapshot.Snapshot
		want     int
		expected string
		actual   string
	}{
		{"different commits",
			gitSnap("a", "abc123", false), gitSnap("b", "def456", false),
			1, "abc123", "def456"},
		{"same commit",
			gitSnap("a", "abc123", false), gitSnap("b", "abc123", false),
			0, "", ""},
		{"one side has no git section",
			gitSnap("a", "abc123", false), snapshot.New("b", time.Time{}),
			0, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Input{A: tt.a, B: tt.b, Diff: diff.Compare(tt.a, tt.b)}
			findings := evalRule(t, finding.GitSHAMismatch, in)
			if len(findings) != tt.want {
				t.Fatalf("findings = %d, want %d: %+v", len(findings), tt.want, findings)
			}
			if tt.want == 0 {
				return
			}
			f := findings[0]
			if f.Rule != finding.GitSHAMismatch || f.Severity != finding.SeverityMedium ||
				f.Component != diff.ComponentGit || f.Key != "sha" ||
				f.Expected != tt.expected || f.Actual != tt.actual {
				t.Errorf("finding = %+v", f)
			}
		})
	}
}

func TestGitDirty(t *testing.T) {
	tests := []struct {
		name  string
		a, b  *snapshot.Snapshot
		want  int
		names []string // snapshot names that must appear in the findings
	}{
		{"one dirty side",
			gitSnap("a", "abc123", true), gitSnap("b", "abc123", false),
			1, []string{"a"}},
		{"both dirty",
			gitSnap("a", "abc123", true), gitSnap("b", "def456", true),
			2, []string{"a", "b"}},
		{"both clean",
			gitSnap("a", "abc123", false), gitSnap("b", "abc123", false),
			0, nil},
		{"no git sections",
			snapshot.New("a", time.Time{}), snapshot.New("b", time.Time{}),
			0, nil},
		{"declared cannot be dirty",
			declaredGitSnap("ci", "abc123", true), gitSnap("b", "abc123", false),
			0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Input{A: tt.a, B: tt.b, Diff: diff.Compare(tt.a, tt.b)}
			findings := evalRule(t, finding.GitDirty, in)
			if len(findings) != tt.want {
				t.Fatalf("findings = %d, want %d: %+v", len(findings), tt.want, findings)
			}
			for _, f := range findings {
				if f.Rule != finding.GitDirty || f.Severity != finding.SeverityLow ||
					f.Component != diff.ComponentGit || f.Key != "dirty" {
					t.Errorf("finding = %+v", f)
				}
				if !strings.Contains(f.Description, "does not fully describe") {
					t.Errorf("description = %q", f.Description)
				}
			}
			for _, n := range tt.names {
				found := false
				for _, f := range findings {
					if strings.Contains(f.Description, n) {
						found = true
					}
				}
				if !found {
					t.Errorf("no finding names %q: %+v", n, findings)
				}
			}
		})
	}
}

func TestGitRulesNilInput(t *testing.T) {
	if got := evalRule(t, finding.GitSHAMismatch, Input{}); len(got) != 0 {
		t.Errorf("sha mismatch on empty input: %+v", got)
	}
	if got := evalRule(t, finding.GitDirty, Input{}); len(got) != 0 {
		t.Errorf("dirty on empty input: %+v", got)
	}
	empty := diff.Compare(nil, nil)
	if got := evalRule(t, finding.GitSHAMismatch, Input{Diff: empty}); len(got) != 0 {
		t.Errorf("sha mismatch on empty diff: %+v", got)
	}
}
