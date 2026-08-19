// Package snapshot defines Nyrvo's core contract: a versioned, deterministic
// description of one execution environment.
//
// Everything else in Nyrvo either produces a Snapshot (collectors, capture) or
// consumes one (diff, doctor, renderers). Because snapshots are written to disk
// and printed as JSON, the shape declared here is a public contract: changing a
// field name or its meaning requires bumping SchemaVersion.
package snapshot

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/textsafe"
)

// SchemaVersion is the version of the snapshot document format. Bump it when a
// change would make an older Nyrvo misread a newer snapshot (renamed field,
// changed units, changed semantics). Purely additive optional fields do not
// require a bump.
//
// Version 2 added Unusable, and that is why it is a bump rather than an
// additive field. The field itself is optional, but it changed what an existing
// shape *means*: before it, a runtime absent from Runtimes meant "not observed
// here". After it, the same absence can mean "installed, and it refused to
// report a version" — the fact is recorded in Unusable instead. A build that
// predates the field reads a version-2 document, sees no dotnet entry, ignores
// the key it does not know, and reports dotnet as missing: precisely the
// untruth the field was added to prevent. An old binary must refuse a document
// it would misread rather than quietly misreading it, and refusing requires a
// version it does not recognize.
const SchemaVersion = 2

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
	// Services are the backing containers an environment has: what a CI job
	// declares under `services:`, and what is actually running here.
	//
	// Like Requirements, they are never diffed. A laptop runs whatever its owner
	// happens to have up, a CI job runs the two sidecars it asked for, and
	// listing the difference between those two sets produces a page of drift
	// that describes a desk rather than a defect. They exist so a rule can ask
	// the one question worth asking: does this machine provide what the job
	// declared it needs?
	Services []Service `json:"services,omitempty"`
	Git      *Git      `json:"git,omitempty"`
	Runtimes []Runtime `json:"runtimes,omitempty"`
	// PartialRuntimes marks a runtime list that is known to be incomplete, for
	// the same reason Environment.Partial exists.
	//
	// A machine can be asked which runtimes it has. A workflow file only states
	// the ones a job sets up explicitly, and says nothing about the many a
	// runner image already provides — ubuntu-latest ships node, python, ruby and
	// php whether or not the workflow mentions them. Treating that silence as
	// absence reports "python is missing in CI" for a Go project that was never
	// going to use it, and buries the one difference that mattered.
	PartialRuntimes bool         `json:"partial_runtimes,omitempty"`
	Environment     *Environment `json:"environment,omitempty"`
	// Unmeasured names observations that were attempted and did not complete,
	// as "<component>.<key>" — "runtime.npm", "docker.compose_version".
	//
	// It exists because a probe that runs out of time and a tool that is not
	// installed produce the same silence, and recording that silence as absence
	// is a claim Nyrvo never observed. A cold Windows runner takes longer than
	// the probe deadline to answer `npm --version`; the machine has npm, and a
	// snapshot saying otherwise makes two captures of one machine disagree and
	// invents drift that never happened.
	//
	// Anything listed here is not absent. It is unknown, and consumers must
	// treat it as a question they cannot answer rather than as a negative one.
	Unmeasured []string `json:"unmeasured,omitempty"`
	// Unusable names runtimes that are installed but would not report a version,
	// as "<component>.<key>" — "runtime.dotnet", "runtime.rust".
	//
	// It is the deterministic counterpart to Unmeasured. A probe that runs out
	// of time says nothing about the tool, and the diff skips it. A probe that
	// answers by refusing says a great deal: the binary was found on PATH, so
	// the runtime is installed, and it declined to answer — usually because a
	// pinned toolchain (a global.json, a rust-toolchain.toml, an rbenv version)
	// names a version this machine does not have. Asking again gives the same
	// answer, so this is drift to report, not noise to skip.
	//
	// Only the component.key is recorded, never the tool's error text: the real
	// messages embed the user's absolute paths, and snapshots end up pasted into
	// bug reports. A runtime listed here is not missing.
	Unusable []string `json:"unusable,omitempty"`
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

// Service is one backing container an environment provides or asks for.
//
// What is deliberately absent is as important as what is here. A container name
// is not recorded: `docker ps` is a view of the whole machine rather than of one
// project, so names would write the user's unrelated work — compose project
// names, and through labels the absolute paths of their other repositories —
// into a file people paste into bug reports. The image answers the only question
// a rule asks, and answers it without that cost.
type Service struct {
	// Image is the reference as declared or as run ("postgres:16"). It is the
	// identity: a job asks for an image, and a machine either runs one or does
	// not.
	Image string `json:"image"`
	// ID is the key under a workflow's `services:`, which is also the hostname
	// the job reaches it by. It is empty for an observed container, where the
	// equivalent would be a name Nyrvo declines to record.
	ID string `json:"id,omitempty"`
	// Ports are the host ports an observed container publishes ("5432"). A
	// workflow's services are reached by hostname on the container's own port,
	// so a declared service usually publishes nothing and this stays empty.
	Ports []string `json:"ports,omitempty"`
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
	// Stripped before anything is sorted or deduplicated, so canonicalization
	// sees the values that will actually be stored: two entries differing only
	// by an escape sequence must collapse into one, not survive as a duplicate
	// pair that looks identical when printed.
	s.stripControlBytes()
	s.CreatedAt = s.CreatedAt.UTC()
	sort.Slice(s.Runtimes, func(i, j int) bool { return s.Runtimes[i].Name < s.Runtimes[j].Name })
	sort.Slice(s.Services, func(i, j int) bool {
		if s.Services[i].Image != s.Services[j].Image {
			return s.Services[i].Image < s.Services[j].Image
		}
		return s.Services[i].ID < s.Services[j].ID
	})
	sort.Slice(s.Requirements, func(i, j int) bool {
		if s.Requirements[i].Runtime != s.Requirements[j].Runtime {
			return s.Requirements[i].Runtime < s.Requirements[j].Runtime
		}
		return s.Requirements[i].Source < s.Requirements[j].Source
	})
	if s.Environment != nil {
		sort.Strings(s.Environment.Names)
	}
	// Sorted and deduplicated for the same reason every other list is: two
	// captures of one machine must produce byte-identical documents, and a
	// runtime with several probes can report the same key more than once.
	if len(s.Unmeasured) > 0 {
		sort.Strings(s.Unmeasured)
		s.Unmeasured = slices.Compact(s.Unmeasured)
	}
	if len(s.Unusable) > 0 {
		sort.Strings(s.Unusable)
		s.Unusable = slices.Compact(s.Unusable)
	}
}

// stripControlBytes removes terminal escape sequences from every string a
// snapshot carries.
//
// The collectors that build a snapshot already sanitize what they store, but a
// snapshot is also a document Nyrvo reads: one written by an older build, edited
// by hand, or sent by someone else to be diffed. Sanitizing only on the way in
// would leave the way out unguarded, and an escape sequence can clear the screen
// or repaint the lines a person is reading to make a decision (docs/adr/0011).
// Normalize is the one point every snapshot passes through, whether it was just
// captured or just loaded.
//
// Every string is stripped rather than the subset that looked externally
// sourced. A first version of this covered notes, services, requirements and
// runtimes, and missed Source.Ref -- which is workflow-derived, reaches the
// terminal inside a doctor recommendation, and is forwarded to an agent -- and
// Environment.Names, which becomes a difference key. Enumerating the risky
// fields is how that gap appeared; a control byte is never legitimate anywhere
// in a snapshot, so the rule is now the whole document and there is no list to
// keep in step with the struct.
func (s *Snapshot) stripControlBytes() {
	s.Name = textsafe.Strip(s.Name)
	if s.Source != nil {
		s.Source.Kind = textsafe.Strip(s.Source.Kind)
		s.Source.Ref = textsafe.Strip(s.Source.Ref)
		s.Source.Notes = textsafe.StripAll(s.Source.Notes)
	}
	if s.System != nil {
		s.System.OS = textsafe.Strip(s.System.OS)
		s.System.Arch = textsafe.Strip(s.System.Arch)
		s.System.Kernel = textsafe.Strip(s.System.Kernel)
	}
	if s.Docker != nil {
		s.Docker.ClientVersion = textsafe.Strip(s.Docker.ClientVersion)
		s.Docker.ServerVersion = textsafe.Strip(s.Docker.ServerVersion)
		s.Docker.ComposeVersion = textsafe.Strip(s.Docker.ComposeVersion)
	}
	if s.Git != nil {
		s.Git.SHA = textsafe.Strip(s.Git.SHA)
		s.Git.Branch = textsafe.Strip(s.Git.Branch)
	}
	if s.Environment != nil {
		s.Environment.Names = textsafe.StripAll(s.Environment.Names)
	}
	for i := range s.Services {
		s.Services[i].Image = textsafe.Strip(s.Services[i].Image)
		s.Services[i].ID = textsafe.Strip(s.Services[i].ID)
		s.Services[i].Ports = textsafe.StripAll(s.Services[i].Ports)
	}
	for i := range s.Requirements {
		s.Requirements[i].Runtime = textsafe.Strip(s.Requirements[i].Runtime)
		s.Requirements[i].Constraint = textsafe.Strip(s.Requirements[i].Constraint)
		s.Requirements[i].Source = textsafe.Strip(s.Requirements[i].Source)
	}
	for i := range s.Runtimes {
		s.Runtimes[i].Name = textsafe.Strip(s.Runtimes[i].Name)
		s.Runtimes[i].Version = textsafe.Strip(s.Runtimes[i].Version)
		s.Runtimes[i].Path = textsafe.Strip(s.Runtimes[i].Path)
	}
	s.Unmeasured = textsafe.StripAll(s.Unmeasured)
	s.Unusable = textsafe.StripAll(s.Unusable)
}

// Validate reports whether the snapshot is internally consistent enough to be
// trusted as evidence about one environment.
//
// It checks invariants of fields this build understands — the document version,
// and the identity of the keyed collections — and deliberately nothing else: a
// machine without Docker still produces a valid snapshot with a nil Docker
// section. Unknown additive fields are not this method's concern either; it
// only ever sees what this build decoded, and a document that carries extra
// keys it does not know about is exactly the compatibility ADR 0002 promises.
func (s *Snapshot) Validate() error {
	if s == nil {
		return errors.New("snapshot is nil")
	}
	if s.SchemaVersion <= 0 {
		return fmt.Errorf("schema_version is %d; it must be a positive integer (this build writes %d)", s.SchemaVersion, SchemaVersion)
	}
	if s.Name == "" {
		return errors.New("name is empty; a snapshot must name the environment it describes")
	}
	if err := s.validateRuntimes(); err != nil {
		return err
	}
	if err := s.validateRequirements(); err != nil {
		return err
	}
	return nil
}

// validateRuntimes checks that every runtime has its name and that no name is
// used twice. Name is the diff key, so two entries sharing one name are not two
// observations — they are one observation recorded twice, and a diff reading
// them would invent an entry that does not exist.
func (s *Snapshot) validateRuntimes() error {
	seen := make(map[string]int, len(s.Runtimes))
	for i, r := range s.Runtimes {
		if r.Name == "" {
			return fmt.Errorf("runtime %d has no name", i+1)
		}
		if prev, ok := seen[r.Name]; ok {
			return fmt.Errorf("runtime %q appears twice (entries %d and %d)", r.Name, prev+1, i+1)
		}
		seen[r.Name] = i
	}
	return nil
}

// validateRequirements checks that every requirement names the runtime it
// constrains and that no (runtime, source) pair is repeated. The same runtime
// from different sources is deliberate — .nvmrc and package.json disagreeing is
// itself a fact worth seeing — so the pair, not the runtime alone, is the key.
func (s *Snapshot) validateRequirements() error {
	seen := make(map[string]int, len(s.Requirements))
	for i, r := range s.Requirements {
		if r.Runtime == "" {
			return fmt.Errorf("requirement %d has no runtime", i+1)
		}
		key := r.Runtime + "\x00" + r.Source
		if prev, ok := seen[key]; ok {
			return fmt.Errorf("requirement for runtime %q from %q appears twice (entries %d and %d)", r.Runtime, r.Source, prev+1, i+1)
		}
		seen[key] = i
	}
	return nil
}

// MarkUnmeasured records that an observation was attempted and did not
// complete. Collectors call it instead of appending, so no caller has to
// remember that the list is deduplicated by Normalize.
func (s *Snapshot) MarkUnmeasured(component, key string) {
	s.Unmeasured = append(s.Unmeasured, component+"."+key)
}

// MarkUnusable records that a runtime is installed but would not report a
// version. Collectors call it instead of appending, so no caller has to
// remember that the list is deduplicated by Normalize.
//
// Only the component and key are stored, never the error. A tool's refusal
// often carries the user's absolute paths, and a snapshot must not write them:
// MarkUnusable takes the two identifiers and nothing else.
func (s *Snapshot) MarkUnusable(component, key string) {
	s.Unusable = append(s.Unusable, component+"."+key)
}

// IsUnusable reports whether the runtime was recorded as installed but
// refusing to answer, so the diagnostic layer can tell "missing" apart from
// "present but not usable".
func (s *Snapshot) IsUnusable(component, key string) bool {
	if s == nil {
		return false
	}
	target := component + "." + key
	for _, k := range s.Unusable {
		if k == target {
			return true
		}
	}
	return false
}

// IsUnmeasured reports whether an observation was attempted and did not
// complete, so a consumer can tell "looked for and not found" apart from
// "asked and did not finish".
func (s *Snapshot) IsUnmeasured(component, key string) bool {
	if s == nil {
		return false
	}
	target := component + "." + key
	for _, k := range s.Unmeasured {
		if k == target {
			return true
		}
	}
	return false
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
