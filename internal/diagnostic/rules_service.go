package diagnostic

import (
	"fmt"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// These identifiers live here temporarily so this isolated change does not alter
// the public finding contract; the lead will move them into the finding package.
const (
	ServiceMissing       = "service.missing"
	ServiceImageMismatch = "service.image_mismatch"
)

const componentService = "service"

// ServiceRules returns rules that judge the services a CI job declares against
// the running containers another environment observed.
func ServiceRules() []Rule {
	return []Rule{
		{
			ID: ServiceMissing,
			Evaluate: func(in Input) []finding.Finding {
				return evaluateServices(in, ServiceMissing)
			},
		},
		{
			ID: ServiceImageMismatch,
			Evaluate: func(in Input) []finding.Finding {
				return evaluateServices(in, ServiceImageMismatch)
			},
		},
	}
}

func evaluateServices(in Input, rule string) []finding.Finding {
	declared, observed := serviceSides(in)
	if declared == nil || observed == nil || len(observed.Services) == 0 {
		return nil
	}

	var findings []finding.Finding
	for _, want := range declared.Services {
		wantName, wantTag, wantTagComparable := serviceImage(want.Image)
		if wantName == "" {
			continue
		}

		matchedName := false
		mismatchedImage := ""
		for _, have := range observed.Services {
			haveName, haveTag, haveTagComparable := serviceImage(have.Image)
			if haveName != wantName {
				continue
			}
			matchedName = true
			if !wantTagComparable || !haveTagComparable || wantTag == haveTag {
				mismatchedImage = ""
				break
			}
			if mismatchedImage == "" {
				mismatchedImage = have.Image
			}
		}

		switch {
		case rule == ServiceMissing && !matchedName:
			findings = append(findings, missingServiceFinding(declared, observed, want, wantName))
		case rule == ServiceImageMismatch && matchedName && mismatchedImage != "":
			findings = append(findings, mismatchedServiceFinding(declared, observed, want, wantName, mismatchedImage))
		}
	}
	return findings
}

func serviceSides(in Input) (declared, observed *snapshot.Snapshot) {
	switch {
	case CIDerived(in.A) && !CIDerived(in.B):
		return in.A, in.B
	case CIDerived(in.B) && !CIDerived(in.A):
		return in.B, in.A
	case Declared(in.A) && !Declared(in.B):
		return in.A, in.B
	case Declared(in.B) && !Declared(in.A):
		return in.B, in.A
	default:
		return nil, nil
	}
}

func serviceImage(ref string) (name, tag string, tagComparable bool) {
	nameRef := ref
	if before, _, digest := strings.Cut(ref, "@"); digest {
		// A digest identifies exact content but provides no readable tag, so
		// translating it into version drift would require a guess.
		segment := lastImageSegment(before)
		if colon := strings.LastIndexByte(segment, ':'); colon >= 0 {
			segment = segment[:colon]
		}
		return segment, "", false
	}

	segment := lastImageSegment(nameRef)
	colon := strings.LastIndexByte(segment, ':')
	if colon < 0 || colon == len(segment)-1 {
		// An omitted tag conventionally resolves to latest, but materializing that
		// convention would turn an unknown resolved tag into asserted drift.
		return strings.TrimSuffix(segment, ":"), "", false
	}
	return segment[:colon], segment[colon+1:], true
}

func lastImageSegment(ref string) string {
	if slash := strings.LastIndexByte(ref, '/'); slash >= 0 {
		return ref[slash+1:]
	}
	return ref
}

func missingServiceFinding(declared, observed *snapshot.Snapshot, service snapshot.Service, name string) finding.Finding {
	return finding.Finding{
		Rule:      ServiceMissing,
		Severity:  finding.SeverityMedium,
		Component: componentService,
		Key:       name,
		Expected:  service.Image,
		Actual:    "no matching running container",
		Description: fmt.Sprintf("The job %s declares %s, but %s's running containers do not include image name %s.",
			Name(declared), service.Image, Name(observed), name),
		Recommendation: fmt.Sprintf("Verify that %s can reach the required service. Nyrvo only sees running containers, so a natively installed service or one using a different image name is invisible to Nyrvo.",
			Name(observed)),
	}
}

func mismatchedServiceFinding(declared, observed *snapshot.Snapshot, service snapshot.Service, name, actual string) finding.Finding {
	return finding.Finding{
		Rule:      ServiceImageMismatch,
		Severity:  finding.SeverityMedium,
		Component: componentService,
		Key:       name,
		Expected:  service.Image,
		Actual:    actual,
		Description: fmt.Sprintf("The job %s declares %s, but %s has %s running for the same image name.",
			Name(declared), service.Image, Name(observed), actual),
		Recommendation: fmt.Sprintf("Run %s in %s, or change the job's declared service image if %s is intentional.",
			service.Image, Name(observed), actual),
	}
}
