package githubactions

import (
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// Snapshot converts a parsed workflow job into a snapshot describing the
// environment that job is expected to run in.
//
// Everything derived from a workflow is an expectation, not an observation: the
// file declares intent, and Nyrvo never executes it. The snapshot is therefore
// marked with snapshot.SourceGitHubActions so a reader can tell a declared
// environment from one that was actually captured. name becomes the snapshot's
// name and now its creation time. It returns an error when w or j is nil.
func Snapshot(w *Workflow, j *Job, name string, now time.Time) (*snapshot.Snapshot, error) {
	if w == nil || j == nil {
		return nil, errors.New("githubactions: cannot build a snapshot without a workflow and a job")
	}

	snap := snapshot.New(name, now)

	// Provenance carries the workflow's own notes first (file-level constructs
	// the parser saw), then the job's, then anything this conversion adds while
	// reading the job, so a reader sees file-level caveats before job-level ones.
	notes := make([]string, 0, len(w.Notes)+len(j.Notes)+8)
	notes = append(notes, w.Notes...)
	notes = append(notes, j.Notes...)

	applySystem(snap, j, &notes)
	applyRuntimes(snap, j, &notes)
	applyEnvironment(snap, j)
	applyServices(snap, j, &notes)

	// No Git section on purpose: a workflow file declares which environment a
	// job expects, not which commit will be checked out — that is decided at run
	// time by the checkout action and the runner. Inventing a SHA would fabricate
	// evidence, so the section is left absent: "not observed" is the honest
	// signal here.

	snap.Source = &snapshot.Source{
		Kind:  snapshot.SourceGitHubActions,
		Ref:   w.Path + "#" + j.ID,
		Notes: notes,
	}

	snap.Normalize()
	return snap, nil
}

// hostedRunners maps the GitHub-hosted runner labels to the platform they
// provide.
//
// The list is exhaustive and exact rather than prefix-matched. A prefix rule
// reads as harmless and is not: "ubuntu-24.04-arm" is arm64, so a rule that
// answers amd64 for anything starting with "ubuntu-" states the wrong
// architecture with full confidence, and any self-hosted label a team invents
// ("ubuntu-slim") would be reported as a GitHub runner Nyrvo never identified.
// An unrecognized label must produce no claim at all — see docs/adr/0009.
var hostedRunners = map[string]snapshot.System{
	"ubuntu-latest":    {OS: "linux", Arch: "amd64"},
	"ubuntu-24.04":     {OS: "linux", Arch: "amd64"},
	"ubuntu-22.04":     {OS: "linux", Arch: "amd64"},
	"ubuntu-24.04-arm": {OS: "linux", Arch: "arm64"},
	"ubuntu-22.04-arm": {OS: "linux", Arch: "arm64"},
	"windows-latest":   {OS: "windows", Arch: "amd64"},
	"windows-2025":     {OS: "windows", Arch: "amd64"},
	"windows-2022":     {OS: "windows", Arch: "amd64"},
	"windows-11-arm":   {OS: "windows", Arch: "arm64"},
	"macos-latest":     {OS: "darwin", Arch: "arm64"},
	"macos-15":         {OS: "darwin", Arch: "arm64"},
	"macos-14":         {OS: "darwin", Arch: "arm64"},
	"macos-13":         {OS: "darwin", Arch: "amd64"},
}

// runnerToSystem maps a runner label to the platform the job runs on, or nil
// when the label is not a GitHub-hosted runner Nyrvo knows. Only the first
// runs-on label is considered, since GitHub uses it to choose the runner pool.
//
// Labels are matched case-insensitively because GitHub treats them that way.
func runnerToSystem(label string) *snapshot.System {
	if sys, ok := hostedRunners[strings.ToLower(strings.TrimSpace(label))]; ok {
		return &sys
	}
	return nil
}

// applySystem fills snap.System from the job's runner and container. The runner
// label is the primary signal; a container, when present, overrides the OS.
func applySystem(snap *snapshot.Snapshot, j *Job, notes *[]string) {
	label := ""
	if len(j.RunsOn) > 0 {
		label = j.RunsOn[0]
	}

	sys := runnerToSystem(label)

	// An unknown, empty, or expression label is recorded rather than guessed:
	// claiming a platform for a label we do not recognize would fabricate
	// evidence about a machine that was never observed.
	switch {
	case label == "":
		*notes = append(*notes, "job declares no runs-on label; platform not guessed")
	case strings.Contains(label, "${{"):
		*notes = append(*notes, "runs-on label "+label+" is an expression; platform not guessed")
	case sys == nil:
		*notes = append(*notes, "runs-on label "+label+" is not a known GitHub-hosted runner; platform not guessed")
	}

	if j.Container == "" {
		// nil for unknown labels: an absent System means "not known", which is
		// itself a meaningful signal when diffing environments.
		snap.System = sys
		return
	}

	// A container tells us the OS even when the runner is unknown: containers on
	// GitHub-hosted runners are always Linux, so the image alone is evidence.
	// Only the architecture still has to fall back when the runner gave us none.
	arch := "amd64"
	if sys != nil {
		arch = sys.Arch
	}
	*notes = append(*notes, "job runs in container "+j.Container+"; container OS is linux")
	snap.System = &snapshot.System{OS: "linux", Arch: arch}
}

// setupAction is one runtime setup action Nyrvo understands.
type setupAction struct {
	path    string // action path, e.g. "actions/setup-node"
	runtime string // snapshot runtime name, e.g. "node"
	version string // With key carrying the requested version
	file    string // With key carrying a version-file reference
}

// setupActions maps each supported setup action to the runtime it configures.
var setupActions = []setupAction{
	{path: "actions/setup-node", runtime: "node", version: "node-version", file: "node-version-file"},
	{path: "actions/setup-python", runtime: "python", version: "python-version", file: "python-version-file"},
	{path: "actions/setup-go", runtime: "go", version: "go-version", file: "go-version-file"},
	{path: "actions/setup-java", runtime: "java", version: "java-version", file: "java-version-file"},
	// The Ruby, PHP and Rust actions are the community ones every workflow uses;
	// GitHub publishes no first-party equivalent, so matching only actions/* here
	// would leave those ecosystems unreadable.
	{path: "ruby/setup-ruby", runtime: "ruby", version: "ruby-version", file: "ruby-version-file"},
	{path: "shivammathur/setup-php", runtime: "php", version: "php-version", file: "php-version-file"},
	// dtolnay/rust-toolchain spells the version in the action's own tag
	// (@1.75.0, @stable) as well as in a toolchain input. Only the input is read
	// here; a version hidden in the tag is left unknown rather than guessed,
	// which is the same rule the rest of the parser follows.
	{path: "dtolnay/rust-toolchain", runtime: "rust", version: "toolchain"},
}

// matchSetupAction reports the setup action a step's Uses references, or nil.
// The action path is matched case-insensitively; the @version suffix is
// irrelevant to what the action configures, so it is stripped before matching.
func matchSetupAction(uses string) *setupAction {
	// Uses is "owner/repo@version"; only the path identifies the action.
	ref, _, _ := strings.Cut(uses, "@")
	ref = strings.TrimSpace(strings.ToLower(ref))
	for i := range setupActions {
		if ref == setupActions[i].path {
			return &setupActions[i]
		}
	}
	return nil
}

// bareVersion matches one concrete runtime version: bare dotted digits such as
// "20", "3.13", or "24.4.0".
var bareVersion = regexp.MustCompile(`^\d+(\.\d+)*$`)

// normalizeVersion trims a setup action's version input down to a bare dotted
// version, or reports why the input is not one concrete version. Anything else —
// a range ("20.x"), operator (">=20"), channel ("lts/*"), file reference
// (".nvmrc") or expression ("${{ matrix.node }}") — describes a set of versions
// or a value resolved later, not a version the workflow pins. Recording any of
// those as a version would make the diff lie, so they are reported instead.
func normalizeVersion(raw string) (version, why string) {
	version = strings.TrimSpace(raw)
	version = strings.TrimPrefix(version, "v")
	if version == "" {
		return "", "empty"
	}
	if strings.Contains(version, "${{") {
		return "", "an expression"
	}
	if !bareVersion.MatchString(version) {
		return "", "not a concrete version"
	}
	return version, ""
}

// applyRuntimes fills snap.Runtimes from the job's setup actions. Setup actions
// are the only runtime signal a workflow carries: a bare run step cannot say
// which runtime version is installed — only the runner image could, and that is
// not modelled — so ignoring them would drop the one thing the file does pin.
func applyRuntimes(snap *snapshot.Snapshot, j *Job, notes *[]string) {
	configured := make(map[string]bool)
	for _, step := range j.Steps {
		act := matchSetupAction(step.Uses)
		if act == nil {
			continue
		}
		if configured[act.runtime] {
			// First declaration wins so two reads of the same file cannot flip
			// which version gets recorded.
			*notes = append(*notes, act.path+" configured twice; keeping the first "+act.version)
			continue
		}
		configured[act.runtime] = true

		if raw := step.With[act.file]; raw != "" {
			// A version-file input is resolved by the action at run time, not by
			// the workflow, so its value is unknowable here.
			*notes = append(*notes, act.path+": "+act.file+" ("+raw+") is resolved at run time, not by the workflow; version not recorded")
			continue
		}

		raw := step.With[act.version]
		if raw == "" {
			*notes = append(*notes, act.path+" declares no "+act.version+"; version not recorded")
			continue
		}
		version, why := normalizeVersion(raw)
		if why != "" {
			*notes = append(*notes, act.path+": "+act.version+" \""+raw+"\" is "+why+"; version not recorded")
			continue
		}
		snap.Runtimes = append(snap.Runtimes, snapshot.Runtime{Name: act.runtime, Version: version})
	}
}

// applyEnvironment fills snap.Environment with the names of every variable the
// job, its steps, and its services declare. Only names are collected, never
// values: a workflow's values are already visible in the repository, but the
// snapshot is a file users paste into bug reports and commit by accident, so
// persisting a value there would leak secrets and let a diff compare values when
// the documented contract is presence-only (see docs/adr/0003). Presence alone
// is sufficient for drift detection ("REDIS_URL is missing in CI"). No value —
// literal, truncated, hashed, or wrapped in a Note — ever leaves this function.
func applyEnvironment(snap *snapshot.Snapshot, j *Job) {
	seen := make(map[string]struct{})
	for name := range j.Env {
		seen[name] = struct{}{}
	}
	for _, step := range j.Steps {
		for name := range step.Env {
			seen[name] = struct{}{}
		}
	}
	for _, svc := range j.Services {
		for name := range svc.Env {
			seen[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)

	// The section is always present, even when the job declares nothing, and is
	// always marked partial. A workflow states the variables it sets; the runner
	// supplies hundreds more that the file never mentions. Leaving the section
	// absent would make a diff treat every local variable as missing from CI —
	// one noise line per shell variable — while marking it partial tells the
	// diff that this list cannot testify to absence.
	snap.Environment = &snapshot.Environment{Names: names, Partial: true}
	// The runtime list needs the same treatment for the same reason. A workflow
	// declares the runtimes it sets up; the runner image already carries several
	// it never mentions, so this list cannot testify to absence either.
	snap.PartialRuntimes = true
}

// applyServices records every service a job declares. Services are not modelled
// as a snapshot section yet — there is no container or database section — so
// each one is surfaced as a note: the rule is that anything Nyrvo recognizes
// but does not model must stay visible, otherwise a diff would silently ignore
// a dependency the job actually needs. Notes are appended in service ID order,
// the order the workflow declares them.
func applyServices(snap *snapshot.Snapshot, j *Job, notes *[]string) {
	for _, svc := range j.Services {
		note := "job declares service " + strconv.Quote(svc.ID)
		if svc.Image != "" {
			note += " (image " + svc.Image + ")"
		}
		*notes = append(*notes, note+"; services are not modelled as a snapshot section yet")
	}
}
