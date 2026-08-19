package diagnostic

import (
	"fmt"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// componentService is not a diff component, and deliberately so: services are
// never diffed. It exists only to locate these findings in the same vocabulary
// the others use.
const componentService = "service"

// ServiceRules returns rules that judge the services a CI job declares against
// the running containers another environment observed.
func ServiceRules() []Rule {
	return []Rule{
		{
			ID: finding.ServiceMissing,
			Evaluate: func(in Input) []finding.Finding {
				return evaluateServices(in, finding.ServiceMissing)
			},
		},
		{
			ID: finding.ServiceImageMismatch,
			Evaluate: func(in Input) []finding.Finding {
				return evaluateServices(in, finding.ServiceImageMismatch)
			},
		},
	}
}

func evaluateServices(in Input, rule string) []finding.Finding {
	declared, observed := serviceSides(in)
	if declared == nil || observed == nil || len(observed.Services) == 0 {
		return nil
	}
	// docker ps ran out of time: an empty list is not an observation of no
	// containers, so this rule must not treat it as one.
	if observed.IsUnmeasured("docker", "services") {
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
		case rule == finding.ServiceMissing && !matchedName:
			findings = append(findings, missingServiceFinding(declared, observed, want, wantName))
		case rule == finding.ServiceImageMismatch && matchedName && mismatchedImage != "":
			findings = append(findings, mismatchedServiceFinding(declared, observed, want, wantName, mismatchedImage))
		}
	}
	return findings
}

// serviceSides picks which snapshot is judging and which is judged. Only a
// CI-derived side declares services worth checking, and only a non-CI side can
// have been asked what it actually runs — a workflow file and a job log are both
// silent about containers on the machine reading them.
func serviceSides(in Input) (declared, observed *snapshot.Snapshot) {
	switch {
	case CIDerived(in.A) && !CIDerived(in.B):
		return in.A, in.B
	case CIDerived(in.B) && !CIDerived(in.A):
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
		Rule:      finding.ServiceMissing,
		Severity:  finding.SeverityMedium,
		Component: componentService,
		Key:       name,
		Expected:  service.Image,
		Actual:    "no matching running container",
		Description: fmt.Sprintf("%s declares the service %s, but %s's running containers include no image named %s.",
			Name(declared), service.Image, Name(observed), name),
		Recommendation: fmt.Sprintf("Verify that %s can reach the required service. Nyrvo only sees running containers, so a natively installed service or one using a different image name is invisible to Nyrvo.",
			Name(observed)),
	}
}

func mismatchedServiceFinding(declared, observed *snapshot.Snapshot, service snapshot.Service, name, actual string) finding.Finding {
	return finding.Finding{
		Rule:      finding.ServiceImageMismatch,
		Severity:  finding.SeverityMedium,
		Component: componentService,
		Key:       name,
		Expected:  service.Image,
		Actual:    actual,
		Description: fmt.Sprintf("%s declares the service %s, but %s has %s running instead.",
			Name(declared), service.Image, Name(observed), actual),
		Recommendation: fmt.Sprintf("Run %s in %s, or change the job's declared service image if %s is intentional.",
			service.Image, Name(observed), actual),
	}
}
