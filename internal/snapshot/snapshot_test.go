package snapshot

import (
	"bytes"
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
