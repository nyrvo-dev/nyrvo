package diagnostic

import (
	"strings"
	"testing"

	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

func serviceSnapshot(name, source string, images ...string) *snapshot.Snapshot {
	s := &snapshot.Snapshot{Name: name}
	if source != "" {
		s.Source = &snapshot.Source{Kind: source}
	}
	for _, image := range images {
		s.Services = append(s.Services, snapshot.Service{Image: image})
	}
	return s
}

func runServiceRules(a, b *snapshot.Snapshot) []finding.Finding {
	return Run(ServiceRules(), Input{A: a, B: b})
}

func TestServiceMissingIsReported(t *testing.T) {
	ci := serviceSnapshot("test job", snapshot.SourceGitHubActions, "postgres:16")
	local := serviceSnapshot("local", snapshot.SourceLocal, "redis:7")

	got := runServiceRules(ci, local)
	if len(got) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(got), got)
	}
	f := got[0]
	if f.Rule != finding.ServiceMissing || f.Severity != finding.SeverityMedium ||
		f.Component != componentService || f.Key != "postgres" ||
		f.Expected != "postgres:16" || f.Actual != "no matching running container" {
		t.Errorf("finding = %+v", f)
	}
	if !strings.Contains(f.Description, "postgres:16") || !strings.Contains(f.Description, "local") {
		t.Errorf("description does not state the declaration and observation: %q", f.Description)
	}
}

func TestServiceMatchingImage(t *testing.T) {
	ci := serviceSnapshot("test job", snapshot.SourceGitHubActions, "postgres:16")
	local := serviceSnapshot("local", snapshot.SourceLocal, "postgres:16")

	if got := runServiceRules(ci, local); len(got) != 0 {
		t.Fatalf("matching service produced findings: %+v", got)
	}
}

func TestServiceImageMismatchIsReported(t *testing.T) {
	ci := serviceSnapshot("test job", snapshot.SourceGitHubActions, "postgres:16")
	local := serviceSnapshot("local", snapshot.SourceLocal, "postgres:14")

	got := runServiceRules(ci, local)
	if len(got) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(got), got)
	}
	f := got[0]
	if f.Rule != finding.ServiceImageMismatch || f.Severity != finding.SeverityMedium ||
		f.Key != "postgres" || f.Expected != "postgres:16" || f.Actual != "postgres:14" {
		t.Errorf("finding = %+v", f)
	}
	if !strings.Contains(f.Description, "postgres:16") || !strings.Contains(f.Description, "postgres:14") {
		t.Errorf("description does not state both images: %q", f.Description)
	}
}

func TestServiceRegistryQualifiedImageMatches(t *testing.T) {
	ci := serviceSnapshot("test job", snapshot.SourceGitHubActions, "docker.io/library/postgres:16")
	local := serviceSnapshot("local", snapshot.SourceLocal, "postgres:16")

	if got := runServiceRules(ci, local); len(got) != 0 {
		t.Fatalf("registry-qualified equivalent produced findings: %+v", got)
	}
}

func TestServiceUntaggedImageDoesNotMismatch(t *testing.T) {
	ci := serviceSnapshot("test job", snapshot.SourceGitHubActions, "postgres")
	local := serviceSnapshot("local", snapshot.SourceLocal, "postgres:14")

	if got := runServiceRules(ci, local); len(got) != 0 {
		t.Fatalf("untagged image produced findings: %+v", got)
	}
}

func TestServiceDigestDoesNotMismatch(t *testing.T) {
	ci := serviceSnapshot("test job", snapshot.SourceGitHubActions, "postgres@sha256:0123456789abcdef")
	local := serviceSnapshot("local", snapshot.SourceLocal, "postgres:14")

	if got := runServiceRules(ci, local); len(got) != 0 {
		t.Fatalf("digest reference produced findings: %+v", got)
	}
}

func TestServiceExtraMachineServicesAreIgnoredInBothDirections(t *testing.T) {
	ci := serviceSnapshot("test job", snapshot.SourceGitHubActions, "postgres:16")
	local := serviceSnapshot("local", snapshot.SourceLocal, "postgres:16", "redis:7", "minio:latest")

	for _, in := range []struct {
		name string
		a, b *snapshot.Snapshot
	}{
		{name: "ci first", a: ci, b: local},
		{name: "ci second", a: local, b: ci},
	} {
		t.Run(in.name, func(t *testing.T) {
			if got := runServiceRules(in.a, in.b); len(got) != 0 {
				t.Fatalf("extra machine services produced findings: %+v", got)
			}
		})
	}
}

func TestServiceEmptyObservedListIsUnknown(t *testing.T) {
	ci := serviceSnapshot("test job", snapshot.SourceGitHubActions, "postgres:16")
	local := serviceSnapshot("local", snapshot.SourceLocal)

	if got := runServiceRules(ci, local); len(got) != 0 {
		t.Fatalf("empty observation was treated as absence: %+v", got)
	}
}

func TestServiceRulesRequireCIDerivedSide(t *testing.T) {
	a := serviceSnapshot("machine a", snapshot.SourceLocal, "postgres:16")
	b := serviceSnapshot("machine b", snapshot.SourceLocal, "postgres:14")

	if got := runServiceRules(a, b); len(got) != 0 {
		t.Fatalf("two observed machines produced findings: %+v", got)
	}
}

func TestServiceMissingRecommendationAcknowledgesNativeInstall(t *testing.T) {
	ci := serviceSnapshot("test job", snapshot.SourceGitHubActionsRun, "postgres:16")
	local := serviceSnapshot("local", snapshot.SourceLocal, "redis:7")

	got := runServiceRules(local, ci)
	if len(got) != 1 || got[0].Rule != finding.ServiceMissing {
		t.Fatalf("findings = %+v, want one missing service", got)
	}
	if !strings.Contains(got[0].Recommendation, "natively installed") ||
		!strings.Contains(got[0].Recommendation, "invisible to Nyrvo") {
		t.Errorf("recommendation overclaims container visibility: %q", got[0].Recommendation)
	}
}

func TestServiceRuleFindsTheOneMatchAmongSeveral(t *testing.T) {
	// The discriminating case: the machine runs exactly one of three declared
	// services. A rule that reported all three, or none, would look right on a
	// single-service fixture.
	ci := &snapshot.Snapshot{
		Name:   "ci",
		Source: &snapshot.Source{Kind: snapshot.SourceGitHubActions},
		Services: []snapshot.Service{
			{ID: "cache", Image: "redis:7"},
			{ID: "db", Image: "postgres:16"},
			{ID: "mail", Image: "axllent/mailpit:latest"},
		},
	}
	local := &snapshot.Snapshot{
		Name:     "local",
		Source:   &snapshot.Source{Kind: snapshot.SourceLocal},
		Services: []snapshot.Service{{Image: "axllent/mailpit:latest", Ports: []string{"1025", "8025"}}},
	}

	got := Run(ServiceRules(), Input{A: local, B: ci})
	if len(got) != 2 {
		t.Fatalf("findings = %+v, want exactly the two that are absent", got)
	}
	for _, f := range got {
		if f.Key == "mailpit" {
			t.Errorf("reported a service that is running: %+v", f)
		}
		if f.Rule != finding.ServiceMissing {
			t.Errorf("rule = %q, want %q", f.Rule, finding.ServiceMissing)
		}
	}
}
