// Package diff compares two snapshots semantically.
//
// "Semantically" means the comparison is driven by what each value identifies,
// not by how the document happens to be laid out: collections are matched by
// key rather than by position, and fields that carry no meaning about the
// environment (capture time, schema version) are ignored. Two captures of an
// unchanged machine must always compare equal.
package diff

import (
	"sort"
	"strconv"
	"strings"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// Kind describes how a single observation differs between two environments.
type Kind string

const (
	// KindChanged means both environments observed the value and they disagree.
	KindChanged Kind = "changed"
	// KindOnlyInA means only the first environment observed it.
	KindOnlyInA Kind = "only_in_a"
	// KindOnlyInB means only the second environment observed it.
	KindOnlyInB Kind = "only_in_b"
)

// Component names group differences in output and are the stable identifiers
// diagnostic rules will match on, so they must not be renamed casually.
const (
	ComponentSystem      = "system"
	ComponentGit         = "git"
	ComponentRuntime     = "runtime"
	ComponentEnvironment = "environment"
	// ComponentDocker groups the container tooling differences.
	ComponentDocker = "docker"
)

// componentOrder fixes the report order: the broadest facts about the machine
// first, the longest and least surprising list last.
var componentOrder = map[string]int{
	ComponentSystem:      0,
	ComponentGit:         1,
	ComponentRuntime:     2,
	ComponentDocker:      3,
	ComponentEnvironment: 4,
}

// Difference is one semantic drift between two environments.
type Difference struct {
	Component string `json:"component"`
	// Key identifies the observation within its component: "os", "sha",
	// "node", or an environment variable name.
	Key  string `json:"key"`
	Kind Kind   `json:"kind"`
	// A and B hold the observed values. Environment variables use the
	// placeholder "present" — Nyrvo never stores their values.
	A string `json:"a,omitempty"`
	B string `json:"b,omitempty"`
}

// Result is the full comparison of two snapshots.
type Result struct {
	SchemaVersion int          `json:"schema_version"`
	A             string       `json:"a"`
	B             string       `json:"b"`
	Differences   []Difference `json:"differences"`
	// PartialRuntimes reports that one side's runtime list was known to be
	// incomplete, so runtimes absent from it were not compared. It is separate
	// from PartialEnvironment because the two narrow different parts of the
	// comparison and a reader has to know which.
	PartialRuntimes bool `json:"partial_runtimes,omitempty"`
	// PartialEnvironment reports that one side's environment list was known to
	// be incomplete, so variables absent from it were not compared. Consumers
	// must surface this: the comparison is narrower than it looks.
	PartialEnvironment bool `json:"partial_environment,omitempty"`
	// Unmeasured reports that a side attempted an observation and could not
	// complete it, so those keys were not compared. Unlike the two above, which
	// describe a source that never claimed to be exhaustive, this one describes
	// a probe that was supposed to answer and did not.
	Unmeasured bool `json:"unmeasured,omitempty"`
}

// Empty reports whether the two environments are semantically identical.
func (r *Result) Empty() bool { return len(r.Differences) == 0 }

// present is the recorded value for an environment variable. Only its
// existence is ever known, so the diff compares presence, not content.
const present = "present"

// described is the value of a whole-section difference: one side described the
// component and the other did not describe it at all. A difference carrying it
// has an empty Key.
const described = "described"

// Compare returns the semantic differences between snapshots a and b.
//
// Timestamps are ignored by construction: nothing reads CreatedAt. Ordering is
// ignored because every section is compared as a keyed map.
func Compare(a, b *snapshot.Snapshot) *Result {
	res := &Result{SchemaVersion: snapshot.SchemaVersion, Differences: []Difference{}}
	if a != nil {
		res.A = a.Name
	}
	if b != nil {
		res.B = b.Name
	}

	compareSection(res, ComponentSystem, systemValues(a), systemValues(b), report{a: true, b: true})
	compareSection(res, ComponentGit, gitValues(a), gitValues(b), report{a: true, b: true})
	// A runtime list derived from a workflow states what a job sets up, not what
	// the runner image provides, so it cannot testify to absence any more than a
	// partial environment list can. Without this, a Go project whose workflow
	// declares only setup-go reports node and python as missing from CI because
	// the laptop has them — a medium finding each, for runtimes the project
	// never uses.
	compareSection(res, ComponentRuntime, runtimeValues(a), runtimeValues(b), report{
		a: !partialRuntimes(b),
		b: !partialRuntimes(a),
	})
	compareSection(res, ComponentDocker, dockerValues(a), dockerValues(b), report{a: true, b: true})
	// An environment list that is only partial (a CI workflow states the
	// variables it sets, not the ones the runner adds) cannot testify to
	// absence, so absences the other side could not have reported are not
	// treated as drift. Result.PartialEnvironment tells the reader this
	// happened, because a silently narrowed comparison would be worse than a
	// noisy one.
	compareSection(res, ComponentEnvironment, environmentValues(a), environmentValues(b), report{
		a: !partialEnvironment(b),
		b: !partialEnvironment(a),
	})
	if partialEnvironment(a) || partialEnvironment(b) {
		res.PartialEnvironment = true
	}
	if partialRuntimes(a) || partialRuntimes(b) {
		res.PartialRuntimes = true
	}
	dropUnmeasured(res, a, b)

	sort.SliceStable(res.Differences, func(i, j int) bool {
		di, dj := res.Differences[i], res.Differences[j]
		if componentOrder[di.Component] != componentOrder[dj.Component] {
			return componentOrder[di.Component] < componentOrder[dj.Component]
		}
		return di.Key < dj.Key
	})
	return res
}

// report says which one-sided observations are worth reporting for a section.
// Both fields are true for every section whose sides are equally complete;
// environment lists are not always equally complete.
type report struct {
	// a reports observations found only in the first snapshot, b only in the
	// second.
	a, b bool
}

// dropUnmeasured removes differences about keys a side attempted and could not
// read, and records that the comparison was narrowed.
//
// A key is dropped whatever the difference looks like, not only when it reads as
// an absence. An unread probe leaves no value at all, so the side that failed
// carries either no key — which compares as "missing" — or the zero value its
// field type forced on it, which is worse: docker's daemon_running is a bool, so
// a probe that ran out of time records a confident "false" that is
// indistinguishable from an observation. Neither is worth comparing.
//
// This runs after the sections are compared rather than inside them because
// there is nothing section-specific about it: every component addresses its
// observations by key, so one pass over the differences covers the ones that
// exist today and any added later.
func dropUnmeasured(res *Result, a, b *snapshot.Snapshot) {
	ua, ub := unmeasuredKeys(a), unmeasuredKeys(b)
	if len(ua) == 0 && len(ub) == 0 {
		return
	}
	// Set from the snapshots rather than from what was dropped. A reader has to
	// be told the comparison could not cover everything even when the unread
	// key happened to match, exactly as a partial environment is announced
	// whether or not it hid a difference.
	res.Unmeasured = true

	kept := res.Differences[:0]
	for _, d := range res.Differences {
		// A whole-section difference carries no key: it says one side never
		// described this component at all. That side's section can be empty
		// precisely because nothing answered — a docker probe that ran out of
		// time leaves no section rather than an incomplete one — so the silent
		// side is asked whether it failed to read anything here.
		if d.Key == "" {
			silent := ub
			if d.Kind == KindOnlyInB {
				silent = ua
			}
			if silentAbout(silent, d.Component) {
				continue
			}
			kept = append(kept, d)
			continue
		}
		if ua[d.Component+"."+d.Key] || ub[d.Component+"."+d.Key] {
			continue
		}
		kept = append(kept, d)
	}
	res.Differences = kept
}

func unmeasuredKeys(s *snapshot.Snapshot) map[string]bool {
	if s == nil || len(s.Unmeasured) == 0 {
		return nil
	}
	keys := make(map[string]bool, len(s.Unmeasured))
	for _, k := range s.Unmeasured {
		keys[k] = true
	}
	return keys
}

// silentAbout reports whether a side failed to read anything in a component,
// which is what makes its empty section unusable as evidence of absence.
func silentAbout(keys map[string]bool, component string) bool {
	for k := range keys {
		if strings.HasPrefix(k, component+".") {
			return true
		}
	}
	return false
}

// partialRuntimes reports a runtime list that cannot testify to absence. It
// takes the pointer so a nil side answers false without every caller guarding it.
func partialRuntimes(s *snapshot.Snapshot) bool {
	return s != nil && s.PartialRuntimes
}

func partialEnvironment(s *snapshot.Snapshot) bool {
	return s != nil && s.Environment != nil && s.Environment.Partial
}

// compareSection diffs one component as a keyed map. Representing every section
// this way means an absent section needs no special case: it simply contributes
// no keys, and its observations show up as only_in_*.
func compareSection(res *Result, component string, a, b map[string]string, rep report) {
	// A section one side never described at all is reported once, not once per
	// key. Listing "sha missing, branch missing, dirty missing" for a CI
	// snapshot reads as if CI had a clean checkout on some other commit, when
	// the truth is simply that a workflow file says nothing about git.
	switch {
	case len(a) > 0 && len(b) == 0:
		if rep.a {
			res.Differences = append(res.Differences, Difference{
				Component: component, Kind: KindOnlyInA, A: described,
			})
		}
		return
	case len(b) > 0 && len(a) == 0:
		if rep.b {
			res.Differences = append(res.Differences, Difference{
				Component: component, Kind: KindOnlyInB, B: described,
			})
		}
		return
	}

	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	for _, k := range keys {
		av, inA := a[k]
		bv, inB := b[k]
		switch {
		case inA && inB:
			if av != bv {
				res.Differences = append(res.Differences, Difference{
					Component: component, Key: k, Kind: KindChanged, A: av, B: bv,
				})
			}
		case inA:
			if rep.a {
				res.Differences = append(res.Differences, Difference{
					Component: component, Key: k, Kind: KindOnlyInA, A: av,
				})
			}
		default:
			if rep.b {
				res.Differences = append(res.Differences, Difference{
					Component: component, Key: k, Kind: KindOnlyInB, B: bv,
				})
			}
		}
	}
}

func systemValues(s *snapshot.Snapshot) map[string]string {
	if s == nil || s.System == nil {
		return nil
	}
	v := map[string]string{}
	// A source can know the operating system without knowing the architecture:
	// a hosted runner's log names its distribution and never states an arch.
	// An unobserved field contributes no key rather than an empty value.
	if s.System.OS != "" {
		v["os"] = s.System.OS
	}
	if s.System.Arch != "" {
		v["arch"] = s.System.Arch
	}
	// An unobserved kernel contributes no key, so it is reported as only_in_*
	// rather than as a change from a real value to the empty string. "We could
	// not see this here" is itself evidence and is not silently dropped.
	if s.System.Kernel != "" {
		v["kernel"] = s.System.Kernel
	}
	return v
}

func gitValues(s *snapshot.Snapshot) map[string]string {
	if s == nil || s.Git == nil {
		return nil
	}
	v := map[string]string{
		"sha":   s.Git.SHA,
		"dirty": strconv.FormatBool(s.Git.Dirty),
	}
	// A detached HEAD has no branch name — the usual state of a CI checkout.
	// It contributes no key, so it surfaces as only_in_* instead of a change
	// to the empty string.
	if s.Git.Branch != "" {
		v["branch"] = s.Git.Branch
	}
	return v
}

// runtimeValues keys runtimes by name so capture order cannot affect the diff,
// and compares versions only. Install paths differ routinely between a laptop
// and a CI image without indicating drift; the path stays in the snapshot as
// evidence for later diagnostics.
func runtimeValues(s *snapshot.Snapshot) map[string]string {
	if s == nil || len(s.Runtimes) == 0 {
		return nil
	}
	v := make(map[string]string, len(s.Runtimes))
	for _, r := range s.Runtimes {
		v[r.Name] = r.Version
	}
	return v
}

// dockerValues describes the container tooling. The daemon state is compared
// because "installed but not running" is exactly the difference that explains a
// compose-backed suite passing in CI and failing on a laptop.
//
// Requirements are deliberately absent from every comparison: both sides
// normally read the same repository, and a CI snapshot cannot read it at all,
// so diffing them would manufacture drift out of provenance. They exist to let
// a rule call a version wrong, not to be compared.
func dockerValues(s *snapshot.Snapshot) map[string]string {
	if s == nil || s.Docker == nil {
		return nil
	}
	v := map[string]string{"daemon_running": strconv.FormatBool(s.Docker.DaemonRunning)}
	// Each version is only compared when both sides observed one: an absent
	// engine version on a machine with a stopped daemon is already reported by
	// daemon_running, and would otherwise be reported twice.
	if s.Docker.ClientVersion != "" {
		v["client_version"] = s.Docker.ClientVersion
	}
	if s.Docker.ServerVersion != "" {
		v["server_version"] = s.Docker.ServerVersion
	}
	if s.Docker.ComposeVersion != "" {
		v["compose_version"] = s.Docker.ComposeVersion
	}
	return v
}

func environmentValues(s *snapshot.Snapshot) map[string]string {
	if s == nil || s.Environment == nil {
		return nil
	}
	v := make(map[string]string, len(s.Environment.Names))
	for _, name := range s.Environment.Names {
		v[name] = present
	}
	return v
}
