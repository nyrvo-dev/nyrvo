package diagnostic

import (
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// sysSnap builds a snapshot with a system section, or without one when both
// fields are empty.
func sysSnap(name, os, arch string) *snapshot.Snapshot {
	s := snapshot.New(name, time.Time{})
	if os != "" || arch != "" {
		s.System = &snapshot.System{OS: os, Arch: arch}
	}
	return s
}

func TestSystemMismatch(t *testing.T) {
	tests := []struct {
		name     string
		rule     string
		a, b     *snapshot.Snapshot
		want     int
		key      string
		expected string
		actual   string
	}{
		{"os mismatch",
			finding.SystemOSMismatch, sysSnap("a", "darwin", "arm64"), sysSnap("b", "linux", "amd64"),
			1, "os", "darwin", "linux"},
		{"arch mismatch",
			finding.SystemArchMismatch, sysSnap("a", "darwin", "arm64"), sysSnap("b", "linux", "amd64"),
			1, "arch", "arm64", "amd64"},
		{"same os",
			finding.SystemOSMismatch, sysSnap("a", "linux", "amd64"), sysSnap("b", "linux", "amd64"),
			0, "", "", ""},
		{"same arch",
			finding.SystemArchMismatch, sysSnap("a", "darwin", "arm64"), sysSnap("b", "darwin", "arm64"),
			0, "", "", ""},
		{"one side has no system section",
			finding.SystemOSMismatch, sysSnap("a", "linux", "amd64"), snapshot.New("b", time.Time{}),
			0, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Input{A: tt.a, B: tt.b, Diff: diff.Compare(tt.a, tt.b)}
			findings := evalRule(t, tt.rule, in)
			if len(findings) != tt.want {
				t.Fatalf("findings = %d, want %d: %+v", len(findings), tt.want, findings)
			}
			if tt.want == 0 {
				return
			}
			f := findings[0]
			if f.Rule != tt.rule || f.Severity != finding.SeverityLow ||
				f.Component != diff.ComponentSystem || f.Key != tt.key ||
				f.Expected != tt.expected || f.Actual != tt.actual {
				t.Errorf("finding = %+v", f)
			}
			if f.Description == "" {
				t.Error("description empty")
			}
		})
	}
}

func TestSystemRulesNilInput(t *testing.T) {
	if got := evalRule(t, finding.SystemOSMismatch, Input{}); len(got) != 0 {
		t.Errorf("os mismatch on empty input: %+v", got)
	}
	if got := evalRule(t, finding.SystemArchMismatch, Input{}); len(got) != 0 {
		t.Errorf("arch mismatch on empty input: %+v", got)
	}
	empty := diff.Compare(nil, nil)
	if got := evalRule(t, finding.SystemOSMismatch, Input{Diff: empty}); len(got) != 0 {
		t.Errorf("os mismatch on empty diff: %+v", got)
	}
}
