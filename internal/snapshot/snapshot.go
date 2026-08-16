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
	CreatedAt   time.Time    `json:"created_at"`
	System      *System      `json:"system,omitempty"`
	Git         *Git         `json:"git,omitempty"`
	Runtimes    []Runtime    `json:"runtimes,omitempty"`
	Environment *Environment `json:"environment,omitempty"`
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
