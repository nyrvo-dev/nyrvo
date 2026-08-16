// Package analysis builds the evidence Nyrvo hands to an AI agent.
//
// The division of labour is deliberate and stated in one line: Nyrvo gathers
// the evidence, your agent reasons about it. Nothing here calls a model, runs a
// command, or reaches the network — it produces a document, and what happens to
// that document is decided elsewhere.
//
// The document is also the privacy boundary. Everything Nyrvo prints locally is
// already the user's own screen; an Input may leave the machine entirely, so
// what goes into one is chosen rather than inherited.
package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/diff"
	"github.com/nyrvo-dev/nyrvo/internal/finding"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// Input is everything an agent is given to reason about, and nothing else.
//
// It carries snapshots rather than a parallel description of them because the
// snapshot is already the project's contract for "what was observed", and a
// second model of the same facts would drift from it. That contract also
// settles the question this type would otherwise have to answer itself: a
// snapshot has no field capable of holding an environment variable's value, so
// neither has an Input.
type Input struct {
	SchemaVersion int `json:"schema_version"`
	// A and B are the two environments, sanitized. They are pointers because a
	// snapshot section may legitimately be absent, but they are never nil here.
	A *snapshot.Snapshot `json:"a"`
	B *snapshot.Snapshot `json:"b"`
	// Differences is the evidence: what actually differs, free of judgement.
	Differences []diff.Difference `json:"differences,omitempty"`
	// Findings is what Nyrvo already concluded on its own. It is included so an
	// agent can agree, disagree, or rank — not so it can repeat it back.
	Findings []finding.Finding `json:"findings,omitempty"`
	// Notes is what the snapshots said about themselves: a run's conclusion, the
	// step that failed, what Nyrvo could not model. For a failed CI run this is
	// usually the most informative part of the document.
	Notes []string `json:"notes,omitempty"`
	// PartialEnvironment repeats the diff's warning that one side's environment
	// list was incomplete. An agent that does not know this will read a silence
	// as an absence.
	PartialEnvironment bool `json:"partial_environment,omitempty"`
}

// Build assembles the document from a diagnosis that already happened.
//
// It takes the diff and the findings rather than recomputing them so that what
// an agent sees is exactly what the user saw, and no analysis can be produced
// from evidence the deterministic report never showed.
func Build(a, b *snapshot.Snapshot, d *diff.Result, findings []finding.Finding, notes []string) Input {
	in := Input{
		SchemaVersion: snapshot.SchemaVersion,
		Findings:      append([]finding.Finding(nil), findings...),
		Notes:         append([]string(nil), notes...),
	}
	if d != nil {
		in.Differences = append([]diff.Difference(nil), d.Differences...)
		in.PartialEnvironment = d.PartialEnvironment
	}
	mentioned := mentionedNames(in.Differences, in.Findings)
	in.A = sanitize(a, mentioned)
	in.B = sanitize(b, mentioned)
	return in
}

// sanitize returns a copy of the snapshot with the two things that are safe to
// keep on a local screen but not to send elsewhere: absolute paths under the
// user's home directory, and the full list of environment variable names.
//
// The original is never modified — the caller is still printing its own report
// from these snapshots.
func sanitize(s *snapshot.Snapshot, mentioned map[string]bool) *snapshot.Snapshot {
	if s == nil {
		return &snapshot.Snapshot{SchemaVersion: snapshot.SchemaVersion}
	}
	out := *s
	// CreatedAt is dropped rather than kept: it says when the user was at their
	// desk, no comparison Nyrvo makes reads it, and keeping it would make two
	// documents built from identical evidence differ.
	out.CreatedAt = time.Time{}

	out.Runtimes = make([]snapshot.Runtime, len(s.Runtimes))
	copy(out.Runtimes, s.Runtimes)
	home := homeDir()
	for i := range out.Runtimes {
		out.Runtimes[i].Path = redactHome(out.Runtimes[i].Path, home)
	}

	if s.Environment != nil {
		env := *s.Environment
		env.Names = keepNames(s.Environment.Names, mentioned)
		out.Environment = &env
	}
	return &out
}

// keepNames drops the environment variable names nothing in the diagnosis
// refers to.
//
// A laptop reports a few hundred of them, and a name is not nothing: the set of
// variables on a machine describes what is installed on it and who works there.
// The ones that appear in a difference or a finding are the evidence an agent
// needs; the rest are a list of the user's tooling with no analytic value.
func keepNames(names []string, mentioned map[string]bool) []string {
	kept := make([]string, 0, len(mentioned))
	for _, n := range names {
		if mentioned[n] {
			kept = append(kept, n)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// mentionedNames collects the environment variable names the diagnosis already
// speaks about, which are the only ones worth carrying.
func mentionedNames(differences []diff.Difference, findings []finding.Finding) map[string]bool {
	names := map[string]bool{}
	for _, d := range differences {
		if d.Component == diff.ComponentEnvironment && d.Key != "" {
			names[d.Key] = true
		}
	}
	for _, f := range findings {
		if f.Component == diff.ComponentEnvironment && f.Key != "" {
			names[f.Key] = true
		}
	}
	return names
}

// redactHome replaces the user's home directory with "~". A runtime's path is
// worth keeping — two environments running the same version from different
// installs is a real cause of drift — but the prefix of that path is the user's
// name and the layout of their machine.
func redactHome(path, home string) string {
	if path == "" || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

// homeDir is a variable so tests can pin it: the redaction must be asserted on a
// known path, not on whatever machine happens to run the suite.
var homeDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
