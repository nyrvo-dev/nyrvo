// Package diagnostic turns differences into findings.
//
// Rules are deterministic and offline: given the same two snapshots they always
// produce the same findings, with no network call, no model, and no clock. That
// is what makes a finding something a user can act on and a test can assert.
package diagnostic

import (
	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// Input is everything a rule may look at: both snapshots and the comparison
// already computed from them.
//
// Rules get the snapshots as well as the diff because severity often depends on
// context the diff has deliberately dropped — which side merely declares its
// environment, whether a section was observed at all.
type Input struct {
	A, B *snapshot.Snapshot
	Diff *diff.Result
}

// Rule is one deterministic check.
//
// It is a struct with a function rather than an interface because a rule has no
// state and no lifecycle: an interface here would add a type per rule and buy
// nothing. Adding a rule means appending one value to a set.
type Rule struct {
	// ID is the stable rule identifier from the finding package. Every finding
	// a rule produces must carry it.
	ID string
	// Evaluate returns the findings this rule sees in the input, or nil.
	Evaluate func(Input) []finding.Finding
}

// Declared reports whether a snapshot describes an environment that was
// declared rather than observed — a workflow file rather than a machine.
//
// Rules use this to phrase and weigh findings honestly: "CI does not install
// node" is a statement about a file, and a version a file states may be a
// prefix ("1.26") that the runner later resolves to a patch release.
func Declared(s *snapshot.Snapshot) bool {
	return s != nil && s.Source != nil && s.Source.Kind == snapshot.SourceGitHubActions
}

// CIDerived reports whether a snapshot describes a CI environment, whether it
// came from a workflow file or from a run that happened.
//
// Neither source enumerates what a runner image already provides: a workflow
// file lists the setup actions it asks for, and a job log shows the versions
// those actions installed. Both are silent about everything else the image
// ships. A rule may therefore say "CI does not set this up" but never "CI does
// not have this" — the difference between a source's silence and a fact.
func CIDerived(s *snapshot.Snapshot) bool {
	if s == nil || s.Source == nil {
		return false
	}
	return s.Source.Kind == snapshot.SourceGitHubActions || s.Source.Kind == snapshot.SourceGitHubActionsRun
}

// Name returns a snapshot's name, or a placeholder, for use in descriptions.
func Name(s *snapshot.Snapshot) string {
	if s == nil || s.Name == "" {
		return "the other environment"
	}
	return s.Name
}

// Run evaluates every rule against the input and returns the findings, sorted.
func Run(rules []Rule, in Input) []finding.Finding {
	var findings []finding.Finding
	for _, r := range rules {
		findings = append(findings, r.Evaluate(in)...)
	}
	finding.Sort(findings)
	return findings
}

// DefaultRules is the rule set the CLI runs. Rules are grouped by the section
// they diagnose, one file per group, so adding a group means appending one
// call here.
func DefaultRules() []Rule {
	var rules []Rule
	rules = append(rules, RuntimeRules()...)
	rules = append(rules, RequirementRules()...)
	rules = append(rules, SystemRules()...)
	rules = append(rules, GitRules()...)
	rules = append(rules, EnvironmentRules()...)
	return rules
}

// Analyze compares two snapshots and diagnoses the result with the default rule
// set. It is the entry point the CLI uses.
func Analyze(a, b *snapshot.Snapshot) (*diff.Result, []finding.Finding) {
	d := diff.Compare(a, b)
	return d, Run(DefaultRules(), Input{A: a, B: b, Diff: d})
}
