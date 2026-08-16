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
)

// componentOrder fixes the report order: the broadest facts about the machine
// first, the longest and least surprising list last.
var componentOrder = map[string]int{
	ComponentSystem:      0,
	ComponentGit:         1,
	ComponentRuntime:     2,
	ComponentEnvironment: 3,
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
}

// Empty reports whether the two environments are semantically identical.
func (r *Result) Empty() bool { return len(r.Differences) == 0 }

// present is the recorded value for an environment variable. Only its
// existence is ever known, so the diff compares presence, not content.
const present = "present"

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

	compareSection(res, ComponentSystem, systemValues(a), systemValues(b))
	compareSection(res, ComponentGit, gitValues(a), gitValues(b))
	compareSection(res, ComponentRuntime, runtimeValues(a), runtimeValues(b))
	compareSection(res, ComponentEnvironment, environmentValues(a), environmentValues(b))

	sort.SliceStable(res.Differences, func(i, j int) bool {
		di, dj := res.Differences[i], res.Differences[j]
		if componentOrder[di.Component] != componentOrder[dj.Component] {
			return componentOrder[di.Component] < componentOrder[dj.Component]
		}
		return di.Key < dj.Key
	})
	return res
}

// compareSection diffs one component as a keyed map. Representing every section
// this way means an absent section needs no special case: it simply contributes
// no keys, and its observations show up as only_in_*.
func compareSection(res *Result, component string, a, b map[string]string) {
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
			res.Differences = append(res.Differences, Difference{
				Component: component, Key: k, Kind: KindOnlyInA, A: av,
			})
		default:
			res.Differences = append(res.Differences, Difference{
				Component: component, Key: k, Kind: KindOnlyInB, B: bv,
			})
		}
	}
}

func systemValues(s *snapshot.Snapshot) map[string]string {
	if s == nil || s.System == nil {
		return nil
	}
	v := map[string]string{"os": s.System.OS, "arch": s.System.Arch}
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
