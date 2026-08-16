// Package snapshot defines Nyrvo's core contract: a versioned, deterministic
// description of one execution environment.
//
// Everything else in Nyrvo either produces a Snapshot (collectors, capture) or
// consumes one (diff, doctor, renderers). Because snapshots are written to disk
// and printed as JSON, the shape declared here is a public contract: changing a
// field name or its meaning requires bumping SchemaVersion.
package snapshot

import (
	"sort"
	"time"
)

// SchemaVersion is the version of the snapshot document format. Bump it when a
// change would make an older Nyrvo misread a newer snapshot (renamed field,
// changed units, changed semantics). Purely additive optional fields do not
// require a bump.
const SchemaVersion = 1

// Snapshot is one captured environment.
//
// Sections are pointers or slices and omitted when empty: a machine without
// Docker, or a directory that is not a Git repository, still produces a valid
// snapshot. Absent means "not observed here", which is itself a meaningful
// signal when diffing environments.
type Snapshot struct {
	SchemaVersion int    `json:"schema_version"`
	Name          string `json:"name"`
	// CreatedAt records when the capture ran. It is deliberately excluded from
	// semantic comparison: two captures of an unchanged machine must not drift
	// merely because time passed.
	CreatedAt time.Time `json:"created_at"`
	// Source records where the observations came from. A snapshot captured on
	// this machine and one derived from a CI workflow file describe the same
	// kind of thing with very different confidence, and a reader must be able
	// to tell them apart. It is never compared: provenance always differs
	// between the two environments being diffed, which is the point.
	Source *Source `json:"source,omitempty"`
	System *System `json:"system,omitempty"`
	Docker *Docker `json:"docker,omitempty"`
	// Requirements are what the checked-out project says it needs, as opposed
	// to what this machine happens to provide. They are never compared between
	// environments — both sides usually read the same repository, and a CI
	// snapshot cannot read it at all — but they are what lets a rule say a
	// version is wrong rather than merely different.
	Requirements []Requirement `json:"requirements,omitempty"`
	Git          *Git          `json:"git,omitempty"`
	Runtimes     []Runtime     `json:"runtimes,omitempty"`
	Environment  *Environment  `json:"environment,omitempty"`
}

// Source kinds. They are stable identifiers: output and future diagnostic
// rules match on them.
const (
	// SourceLocal marks a snapshot observed by running on the machine itself.
	SourceLocal = "local"
	// SourceGitHubActions marks a snapshot derived from a workflow file. It
	// describes the environment a job is *expected* to run in, which is not
	// the same as having watched it run.
	SourceGitHubActions = "github-actions"
	// SourceGitHubActionsRun marks a snapshot derived from a run that actually
	// happened. It reports what a job really got — the commit that was checked
	// out, the runner it landed on — rather than what the file asked for.
	SourceGitHubActionsRun = "github-actions-run"
)

// Source describes where a snapshot's observations came from.
type Source struct {
	Kind string `json:"kind"`
	// Ref locates the origin within its kind: a workflow file and job for
	// github-actions, empty for a local capture.
	Ref string `json:"ref,omitempty"`
	// Notes lists what the source declared but Nyrvo does not model, so a
	// snapshot never implies coverage it does not have.
	Notes []string `json:"notes,omitempty"`
}

// System describes the host operating system and CPU architecture.
type System struct {
	// OS and Arch use Go's GOOS/GOARCH vocabulary ("darwin", "linux",
	// "arm64", "amd64") so values are comparable across machines without
	// per-platform normalization.
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// Kernel is the raw kernel release string when available. It is
	// informational: kernel strings differ too often to drive diagnostics.
	Kernel string `json:"kernel,omitempty"`
}

// Git describes the checked-out repository state at capture time.
type Git struct {
	SHA    string `json:"sha"`
	Branch string `json:"branch,omitempty"`
	// Dirty reports uncommitted changes in the working tree. A dirty
	// environment means its SHA does not fully describe the code that ran.
	Dirty bool `json:"dirty"`
}

// Docker describes the container tooling available here.
//
// The section is absent when Docker is not installed. It is present with
// DaemonRunning false when the CLI exists but the daemon does not answer —
// which is a different fact, and a common reason a compose-backed test suite
// passes in CI and not on a laptop.
type Docker struct {
	// ClientVersion is known even when the daemon is down; ServerVersion is the
	// engine's own version and is only knowable when it answers.
	ClientVersion  string `json:"client_version,omitempty"`
	ServerVersion  string `json:"server_version,omitempty"`
	DaemonRunning  bool   `json:"daemon_running"`
	ComposeVersion string `json:"compose_version,omitempty"`
}

// Requirement is a version the project declares it needs.
type Requirement struct {
	// Runtime matches the runtime naming used everywhere else ("go", "node",
	// "python"), so a requirement can be matched against what was observed.
	Runtime string `json:"runtime"`
	// Constraint is the declaration verbatim (">=24", "1.25", "^20"). It is
	// kept as written because the file is the evidence; interpreting it is the
	// job of whatever evaluates it.
	Constraint string `json:"constraint"`
	// Source names the file and field it came from, so a finding can point at
	// the line to edit.
	Source string `json:"source"`
	// Minimum marks a declaration that is a floor rather than a pin. The go
	// directive in go.mod is the case that matters: since Go 1.21 it states the
	// lowest version the module accepts, so a newer toolchain satisfies it.
	// Reading it as a pin makes every project developed on a newer Go than it
	// supports look broken.
	//
	// The constraint is still stored exactly as the file writes it. What this
	// records is how the file means it, which only the collector that read it
	// can know.
	Minimum bool `json:"minimum,omitempty"`
}

// Runtime is one detected language runtime.
type Runtime struct {
	// Name is a stable lowercase identifier ("go", "node", "python") used as
	// the diff key, so it must not carry version or path information.
	Name string `json:"name"`
	// Version is normalized to bare dotted digits ("24.4.0"), stripped of
	// tool-specific prefixes such as "v" or "go", so versions from different
	// tools compare consistently.
	Version string `json:"version"`
	// Path is where the runtime was found. Two environments can run the same
	// version from different installs, which matters when diagnosing PATH
	// problems.
	Path string `json:"path,omitempty"`
}

// Environment records which environment variables exist.
//
// Values are intentionally never stored. Presence is sufficient for drift
// detection ("REDIS_URL is missing in CI"), while persisting values would write
// credentials into a file users share in bug reports and commit by accident.
// See docs/adr/0003-never-store-environment-values.md.
type Environment struct {
	Names []string `json:"names"`
	// Partial marks a list that is known to be incomplete. A machine can be
	// asked for its whole environment; a CI workflow file only states the
	// variables it sets explicitly, and says nothing about the hundreds the
	// runner will also provide.
	//
	// Comparing a complete list against a partial one without this flag
	// produces one "missing" line per variable the other side never claimed to
	// describe, burying the few that matter. Diff uses it to suppress absences
	// the partial side could not have reported.
	Partial bool `json:"partial,omitempty"`
}

// New returns an empty snapshot stamped with the current schema version.
func New(name string, createdAt time.Time) *Snapshot {
	return &Snapshot{
		SchemaVersion: SchemaVersion,
		Name:          name,
		CreatedAt:     createdAt.UTC(),
	}
}

// Normalize sorts every collection into a canonical order.
//
// Collectors may run concurrently and finish in any order; two captures of the
// same machine must still serialize byte-for-byte identically, otherwise diffs
// and golden tests report noise. Callers must Normalize before writing or
// comparing a snapshot.
func (s *Snapshot) Normalize() {
	if s == nil {
		return
	}
	s.CreatedAt = s.CreatedAt.UTC()
	sort.Slice(s.Runtimes, func(i, j int) bool { return s.Runtimes[i].Name < s.Runtimes[j].Name })
	sort.Slice(s.Requirements, func(i, j int) bool {
		if s.Requirements[i].Runtime != s.Requirements[j].Runtime {
			return s.Requirements[i].Runtime < s.Requirements[j].Runtime
		}
		return s.Requirements[i].Source < s.Requirements[j].Source
	})
	if s.Environment != nil {
		sort.Strings(s.Environment.Names)
	}
}

// Runtime returns the runtime with the given name, or nil when absent.
func (s *Snapshot) Runtime(name string) *Runtime {
	for i := range s.Runtimes {
		if s.Runtimes[i].Name == name {
			return &s.Runtimes[i]
		}
	}
	return nil
}
