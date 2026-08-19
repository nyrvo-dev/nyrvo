package snapshot

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// newTestSnapshot returns a fully-populated snapshot in canonical order. It is
// shared with store_test.go; the fixed CreatedAt (no monotonic clock, UTC)
// keeps round-trip and byte-stability assertions deterministic.
func newTestSnapshot(name string) *Snapshot {
	return &Snapshot{
		SchemaVersion: SchemaVersion,
		Name:          name,
		CreatedAt:     time.Date(2026, 6, 15, 10, 30, 0, 123456789, time.UTC),
		System:        &System{OS: "darwin", Arch: "arm64", Kernel: "25.5.0"},
		Git:           &Git{SHA: "abc123", Branch: "main", Dirty: true},
		Runtimes: []Runtime{
			{Name: "go", Version: "1.25.0", Path: "/usr/local/go/bin/go"},
			{Name: "node", Version: "24.4.0", Path: "/usr/local/bin/node"},
		},
		Environment: &Environment{Names: []string{"DATABASE_URL", "PATH", "REDIS_URL"}},
	}
}

// clone returns a deep copy so a normalized snapshot can be compared against a
// second Normalize without aliasing the same backing slices.
func clone(s *Snapshot) *Snapshot {
	c := *s
	c.Runtimes = append([]Runtime(nil), s.Runtimes...)
	c.Unmeasured = append([]string(nil), s.Unmeasured...)
	c.Unusable = append([]string(nil), s.Unusable...)
	if s.Environment != nil {
		env := *s.Environment
		env.Names = append([]string(nil), s.Environment.Names...)
		c.Environment = &env
	}
	return &c
}

func TestNewStampsVersionNameAndUTCTime(t *testing.T) {
	// A non-UTC zone is the interesting case: New must fold it to UTC so two
	// machines capturing the same instant serialize identically regardless of
	// their local timezone.
	created := time.Date(2026, 6, 15, 10, 30, 0, 123456789, time.FixedZone("BRT", -3*60*60))
	s := New("staging", created)

	if s.SchemaVersion != SchemaVersion {
		t.Fatalf("New() SchemaVersion = %d, want %d", s.SchemaVersion, SchemaVersion)
	}
	if s.Name != "staging" {
		t.Fatalf("New() Name = %q, want %q", s.Name, "staging")
	}
	if !s.CreatedAt.Equal(created.UTC()) {
		t.Fatalf("New() CreatedAt = %v, want %v", s.CreatedAt, created.UTC())
	}
	if s.CreatedAt.Location() != time.UTC {
		t.Fatalf("New() CreatedAt location = %v, want UTC", s.CreatedAt.Location())
	}
}

func TestNormalizeSortsCollections(t *testing.T) {
	s := &Snapshot{
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("BRT", -3*60*60)),
		Runtimes: []Runtime{
			{Name: "node", Version: "24.4.0"},
			{Name: "go", Version: "1.25.0"},
			{Name: "python", Version: "3.13.1"},
		},
		Environment: &Environment{Names: []string{"REDIS_URL", "PATH", "DATABASE_URL"}},
	}
	s.Normalize()

	wantRuntimes := []Runtime{
		{Name: "go", Version: "1.25.0"},
		{Name: "node", Version: "24.4.0"},
		{Name: "python", Version: "3.13.1"},
	}
	if !reflect.DeepEqual(s.Runtimes, wantRuntimes) {
		t.Fatalf("Normalize() Runtimes = %+v, want %+v", s.Runtimes, wantRuntimes)
	}
	if wantEnv := []string{"DATABASE_URL", "PATH", "REDIS_URL"}; !reflect.DeepEqual(s.Environment.Names, wantEnv) {
		t.Fatalf("Normalize() Environment.Names = %v, want %v", s.Environment.Names, wantEnv)
	}
	if s.CreatedAt.Location() != time.UTC {
		t.Fatalf("Normalize() left CreatedAt outside UTC: %v", s.CreatedAt.Location())
	}
}

// A second Normalize must be a no-op; callers like Marshal and Save normalize
// repeatedly and must never drift between runs.
func TestNormalizeIsIdempotent(t *testing.T) {
	s := &Snapshot{
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("BRT", -3*60*60)),
		Runtimes:    []Runtime{{Name: "z"}, {Name: "a"}, {Name: "m"}},
		Environment: &Environment{Names: []string{"Z", "A", "M"}},
	}
	s.Normalize()
	want := clone(s)
	s.Normalize()
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("second Normalize() changed the snapshot:\n got: %+v\nwant: %+v", s, want)
	}
}

// Normalize runs on every store path, including on captures that never got a
// Name or section populated, so a nil receiver must be a safe no-op rather
// than a panic.
func TestNormalizeNilSnapshotDoesNotPanic(t *testing.T) {
	var s *Snapshot
	s.Normalize()
}

func TestRuntimeReturnsLivePointer(t *testing.T) {
	s := &Snapshot{
		Runtimes: []Runtime{
			{Name: "go", Version: "1.25.0"},
			{Name: "node", Version: "24.4.0"},
		},
	}
	got := s.Runtime("node")
	if got == nil {
		t.Fatal(`Runtime("node") = nil, want a pointer`)
	}
	// The pointer is how the diff package rewrites a runtime version in place;
	// the mutation must be visible back in the snapshot.
	got.Version = "22.18.0"
	if s.Runtimes[1].Version != "22.18.0" {
		t.Fatalf("mutating through Runtime() did not update the snapshot: %+v", s.Runtimes)
	}
}

func TestRuntimeAbsentReturnsNil(t *testing.T) {
	s := &Snapshot{Runtimes: []Runtime{{Name: "go", Version: "1.25.0"}}}
	if got := s.Runtime("ruby"); got != nil {
		t.Fatalf(`Runtime("ruby") = %+v, want nil`, got)
	}
}

// Marshal normalizes as a side effect, so repeated calls on the same snapshot
// must be byte-identical and the document must be newline-terminated (the
// convention the store and --json output rely on).
func TestMarshalStableAcrossCalls(t *testing.T) {
	s := newTestSnapshot("local")
	first, err := Marshal(s)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	second, err := Marshal(s)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("Marshal() not stable across calls:\nfirst:  %q\nsecond: %q", first, second)
	}
	if !strings.HasSuffix(string(first), "\n") {
		t.Fatalf("Marshal() output does not end in a newline: %q", first)
	}
}

// Collectors append runtimes and env names in whichever order they finish;
// the same content appended in a different order must still serialize to
// identical bytes, otherwise diffs and golden files would report noise.
func TestMarshalIndependentOfAppendOrder(t *testing.T) {
	a := newTestSnapshot("same")
	b := newTestSnapshot("same")
	a.Runtimes = []Runtime{
		{Name: "go", Version: "1.25.0"},
		{Name: "node", Version: "24.4.0"},
		{Name: "python", Version: "3.13.1"},
	}
	b.Runtimes = []Runtime{
		{Name: "python", Version: "3.13.1"},
		{Name: "node", Version: "24.4.0"},
		{Name: "go", Version: "1.25.0"},
	}
	a.Environment = &Environment{Names: []string{"PATH", "REDIS_URL", "DATABASE_URL"}}
	b.Environment = &Environment{Names: []string{"REDIS_URL", "DATABASE_URL", "PATH"}}

	ab, err := Marshal(a)
	if err != nil {
		t.Fatalf("Marshal(a) error = %v", err)
	}
	bb, err := Marshal(b)
	if err != nil {
		t.Fatalf("Marshal(b) error = %v", err)
	}
	if !bytes.Equal(ab, bb) {
		t.Fatalf("Marshal() order-dependent:\na: %s\nb: %s", ab, bb)
	}
}

// Two captures of one machine must produce byte-identical documents, and a
// runtime with several probes can name the same key twice.
func TestNormalizeSortsAndDeduplicatesUnmeasured(t *testing.T) {
	s := New("local", time.Time{})
	s.MarkUnmeasured("runtime", "python")
	s.MarkUnmeasured("docker", "compose_version")
	s.MarkUnmeasured("runtime", "python")

	s.Normalize()

	want := []string{"docker.compose_version", "runtime.python"}
	if !slices.Equal(s.Unmeasured, want) {
		t.Errorf("Unmeasured = %v, want %v", s.Unmeasured, want)
	}
}

// The unusable list is sorted and deduplicated for the same reason the
// unmeasured one is: several probes can report the same runtime, and two
// captures of one machine must serialize identically.
func TestNormalizeSortsAndDeduplicatesUnusable(t *testing.T) {
	s := New("local", time.Time{})
	s.MarkUnusable("runtime", "dotnet")
	s.MarkUnusable("runtime", "rust")
	s.MarkUnusable("runtime", "dotnet")

	s.Normalize()

	want := []string{"runtime.dotnet", "runtime.rust"}
	if !slices.Equal(s.Unusable, want) {
		t.Errorf("Unusable = %v, want %v", s.Unusable, want)
	}
}

func TestIsUnusable(t *testing.T) {
	s := New("local", time.Time{})
	s.MarkUnusable("runtime", "dotnet")

	if !s.IsUnusable("runtime", "dotnet") {
		t.Error(`IsUnusable("runtime", "dotnet") = false, want true`)
	}
	if s.IsUnusable("runtime", "rust") {
		t.Error(`IsUnusable("runtime", "rust") = true, want false`)
	}
	if s.IsUnusable("docker", "dotnet") {
		t.Error(`IsUnusable("docker", "dotnet") = true, want false`)
	}
	var nilSnap *Snapshot
	if nilSnap.IsUnusable("runtime", "dotnet") {
		t.Error("nil IsUnusable = true, want false")
	}
}

func TestIsUnmeasured(t *testing.T) {
	s := New("local", time.Time{})
	s.MarkUnmeasured("docker", "services")

	if !s.IsUnmeasured("docker", "services") {
		t.Error(`IsUnmeasured("docker", "services") = false, want true`)
	}
	if s.IsUnmeasured("docker", "compose_version") {
		t.Error(`IsUnmeasured("docker", "compose_version") = true, want false`)
	}
	var nilSnap *Snapshot
	if nilSnap.IsUnmeasured("docker", "services") {
		t.Error("nil IsUnmeasured = true, want false")
	}
}

// The field is additive and optional, so a snapshot that measured everything
// must serialise exactly as it did before this field existed.
func TestUnmeasuredIsOmittedWhenEverythingWasMeasured(t *testing.T) {
	s := New("local", time.Time{})
	data, err := Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "unmeasured") {
		t.Errorf("a complete snapshot carries an unmeasured key:\n%s", data)
	}
}

// The field is additive and optional, so a snapshot with nothing unusable must
// serialise exactly as it did before this field existed.
func TestUnusableIsOmittedWhenEverythingWasUsable(t *testing.T) {
	s := New("local", time.Time{})
	data, err := Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "unusable") {
		t.Errorf("a snapshot with nothing unusable carries an unusable key:\n%s", data)
	}
}

func TestValidateAcceptsValidSnapshot(t *testing.T) {
	if err := newTestSnapshot("local").Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsNil(t *testing.T) {
	var s *Snapshot
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() of nil returned no error")
	}
}

// A document with no version stamp is indistinguishable from a JSON file that
// merely parses; whatever it says, Nyrvo cannot trust it as a snapshot.
func TestValidateRejectsZeroSchemaVersion(t *testing.T) {
	s := newTestSnapshot("local")
	s.SchemaVersion = 0
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() of schema_version 0 returned no error")
	}
}

func TestValidateRejectsNegativeSchemaVersion(t *testing.T) {
	s := newTestSnapshot("local")
	s.SchemaVersion = -1
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() of negative schema_version returned no error")
	}
}

func TestValidateRejectsEmptyName(t *testing.T) {
	s := newTestSnapshot("local")
	s.Name = ""
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() of an empty name returned no error")
	}
}

// A runtime without a name cannot be keyed for diffing or named in a finding,
// so it is not an observation Nyrvo can reason about.
func TestValidateRejectsRuntimeWithoutName(t *testing.T) {
	s := newTestSnapshot("local")
	s.Runtimes = append(s.Runtimes, Runtime{Name: "", Version: "1.0"})
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() of a nameless runtime returned no error")
	}
}

// Two entries sharing one runtime name are one observation recorded twice, and
// a diff reading them would invent a second runtime that does not exist.
func TestValidateRejectsDuplicateRuntimes(t *testing.T) {
	s := newTestSnapshot("local")
	s.Runtimes = append(s.Runtimes, Runtime{Name: "node", Version: "22.0.0"})
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() of duplicated runtimes returned no error")
	}
}

// A requirement that does not say which runtime it constrains is a claim
// without a subject, and cannot be matched against anything observed.
func TestValidateRejectsRequirementWithoutRuntime(t *testing.T) {
	s := newTestSnapshot("local")
	s.Requirements = []Requirement{{Runtime: "", Constraint: "1.25", Source: "go.mod go directive"}}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() of a runtime-less requirement returned no error")
	}
}

func TestValidateRejectsDuplicateRequirements(t *testing.T) {
	s := newTestSnapshot("local")
	s.Requirements = []Requirement{
		{Runtime: "go", Constraint: "1.25", Source: "go.mod go directive"},
		{Runtime: "go", Constraint: "1.25", Source: "go.mod go directive"},
	}
	if err := s.Validate(); err == nil {
		t.Fatal("Validate() of duplicated requirements returned no error")
	}
}

// The same runtime from different sources is deliberate — .nvmrc and
// package.json disagreeing is itself a fact worth seeing — and must not be
// mistaken for a duplicate.
func TestValidateAllowsSameRuntimeFromDifferentSources(t *testing.T) {
	s := newTestSnapshot("local")
	s.Requirements = []Requirement{
		{Runtime: "node", Constraint: ">=24", Source: "package.json engines.node"},
		{Runtime: "node", Constraint: "^20", Source: ".nvmrc"},
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// Normalize sorts Requirements the way it sorts every other collection; without
// that sort two captures of one machine could serialize differently depending
// on which file the requirements collector read first. This test pins the
// Requirements sort specifically: it was the one collection whose unsorted
// state left the whole suite green.
func TestNormalizeSortsRequirements(t *testing.T) {
	s := New("local", time.Time{})
	s.Requirements = []Requirement{
		{Runtime: "node", Constraint: ">=24", Source: "package.json engines.node"},
		{Runtime: "python", Constraint: "3.13", Source: ".python-version"},
		{Runtime: "go", Constraint: "1.25", Source: "go.mod go directive", Minimum: true},
		{Runtime: "node", Constraint: "^20", Source: ".nvmrc"},
	}
	want := []Requirement{
		{Runtime: "go", Constraint: "1.25", Source: "go.mod go directive", Minimum: true},
		{Runtime: "node", Constraint: "^20", Source: ".nvmrc"},
		{Runtime: "node", Constraint: ">=24", Source: "package.json engines.node"},
		{Runtime: "python", Constraint: "3.13", Source: ".python-version"},
	}
	s.Normalize()
	if !reflect.DeepEqual(s.Requirements, want) {
		t.Fatalf("Normalize() Requirements = %+v, want %+v", s.Requirements, want)
	}
}

// The Marshal-level guarantee for requirements: the same set appended in a
// different order must still serialize byte-for-byte identically.
func TestMarshalIndependentOfRequirementAppendOrder(t *testing.T) {
	a := newTestSnapshot("same")
	b := newTestSnapshot("same")
	a.Requirements = []Requirement{
		{Runtime: "go", Constraint: "1.25", Source: "go.mod go directive", Minimum: true},
		{Runtime: "node", Constraint: ">=24", Source: "package.json engines.node"},
		{Runtime: "node", Constraint: "^20", Source: ".nvmrc"},
	}
	b.Requirements = []Requirement{
		{Runtime: "node", Constraint: "^20", Source: ".nvmrc"},
		{Runtime: "go", Constraint: "1.25", Source: "go.mod go directive", Minimum: true},
		{Runtime: "node", Constraint: ">=24", Source: "package.json engines.node"},
	}
	ab, err := Marshal(a)
	if err != nil {
		t.Fatalf("Marshal(a) error = %v", err)
	}
	bb, err := Marshal(b)
	if err != nil {
		t.Fatalf("Marshal(b) error = %v", err)
	}
	if !bytes.Equal(ab, bb) {
		t.Fatalf("Marshal() requirement order-dependent:\na: %s\nb: %s", ab, bb)
	}
}

// A snapshot is not only written by Nyrvo, it is also read by it: one produced
// by an older build, edited by hand, or sent by someone else to be diffed. The
// collectors sanitize what they store, so this covers the way out — an escape
// sequence in a note can clear the screen a person is reading a diagnosis on.
func TestNormalizeStripsControlBytesFromLoadedText(t *testing.T) {
	esc := "\x1b"
	s := &Snapshot{
		SchemaVersion: SchemaVersion,
		Name:          "ci",
		Source:        &Source{Kind: SourceLocal, Notes: []string{"step " + esc + "[2J" + esc + "[31mHIDDEN" + esc + "[0m"}},
		Services:      []Service{{Image: "postgres:16" + esc + "[2J", ID: "db" + esc + "[1m"}},
		Runtimes:      []Runtime{{Name: "go", Version: "1.26.0" + esc + "[2J", Path: "/usr/bin/go" + esc + "[0m"}},
		Requirements:  []Requirement{{Runtime: "node", Constraint: ">=24" + esc + "[2J", Source: "package.json" + esc + "[0m"}},
	}
	s.Normalize()

	for _, got := range []string{
		s.Source.Notes[0],
		s.Services[0].Image, s.Services[0].ID,
		s.Runtimes[0].Version, s.Runtimes[0].Path,
		s.Requirements[0].Constraint, s.Requirements[0].Source,
	} {
		for _, r := range got {
			if r < 0x20 && r != '\t' {
				t.Errorf("control byte %#U survived Normalize in %q", r, got)
			}
		}
	}
	if s.Source.Notes[0] != "step HIDDEN" {
		t.Errorf("note = %q, want %q", s.Source.Notes[0], "step HIDDEN")
	}
}

// Every string in a snapshot is stripped, not a hand-picked subset. The first
// version of this covered notes, services, requirements and runtimes and missed
// Source.Ref -- which reaches the terminal inside a doctor recommendation and is
// forwarded to an agent -- and Environment.Names, which becomes a difference key
// the diff prints. Enumerating the risky fields is how that gap appeared, so
// this walks the whole document.
func TestNormalizeStripsControlBytesFromEveryField(t *testing.T) {
	esc := "\x1b[2J"
	s := &Snapshot{
		SchemaVersion: SchemaVersion,
		Name:          "ci" + esc,
		Source:        &Source{Kind: "github-actions" + esc, Ref: "ci.yml#job" + esc, Notes: []string{"note" + esc}},
		System:        &System{OS: "linux" + esc, Arch: "amd64" + esc, Kernel: "6.8" + esc},
		Docker:        &Docker{ClientVersion: "28" + esc, ServerVersion: "28" + esc, ComposeVersion: "2.40" + esc},
		Git:           &Git{SHA: "abc123" + esc, Branch: "main" + esc},
		Environment:   &Environment{Names: []string{"PATH" + esc}},
		Services:      []Service{{Image: "postgres:16" + esc, ID: "db" + esc, Ports: []string{"5432" + esc}}},
		Requirements:  []Requirement{{Runtime: "node" + esc, Constraint: ">=24" + esc, Source: "package.json" + esc}},
		Runtimes:      []Runtime{{Name: "go" + esc, Version: "1.26" + esc, Path: "/usr/bin/go" + esc}},
		Unmeasured:    []string{"runtime.npm" + esc},
		Unusable:      []string{"runtime.dotnet" + esc},
	}
	s.Normalize()

	// Re-encoding is how the whole document is checked without naming each
	// field again: a field added later is covered without editing this test.
	doc, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	// The encoder writes a control byte as the six characters \u001b rather than
	// the byte itself, so scanning the encoded bytes for 0x1b can never match --
	// an earlier version of this test did exactly that and passed with the
	// stripping removed. Both forms are checked.
	if bytes.ContainsRune(doc, 0x1b) {
		t.Errorf("a raw escape byte survived Normalize:\n%s", doc)
	}
	// Only the control range. The encoder also HTML-escapes >, < and & as
	// \u003e, \u003c and \u0026, which are ordinary characters a constraint like
	// ">=24" legitimately contains.
	for _, esc := range []string{`\u000`, `\u001`} {
		if bytes.Contains(doc, []byte(esc)) {
			t.Errorf("an escaped control character (%s...) survived Normalize:\n%s", esc, doc)
		}
	}
}
