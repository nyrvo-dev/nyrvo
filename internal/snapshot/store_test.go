package snapshot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// newStore returns a store rooted at a fresh temp dir, so no test ever writes
// into the repository or the developer's home directory.
func newStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}

func entryNames(entries []os.DirEntry) []string {
	t := make([]string, len(entries))
	for i, e := range entries {
		t[i] = e.Name()
	}
	return t
}

// assertTreeEmpty fails unless root contains no entries at all. It is used to
// prove a rejected save touched nothing on disk, not even the .nyrvo dir.
func assertTreeEmpty(t *testing.T, root string) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		return fmt.Errorf("unexpected entry %s", path)
	})
	if err != nil {
		t.Fatalf("store tree not empty after rejected operation: %v", err)
	}
}

// Every persisted field must survive the JSON round-trip. The comparison is
// against an independently-built reference because Save normalizes its
// argument in place.
func TestSaveLoadRoundTrip(t *testing.T) {
	s := newStore(t)
	if err := s.Save(newTestSnapshot("local")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := s.Load("local")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if want := newTestSnapshot("local"); !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

// Save writes through a temp file then renames, so an interrupted save cannot
// leave a truncated snapshot behind. A leftover ".name.*.tmp" here would also
// silently appear as a (wrong) name through the List surface.
func TestSaveLeavesNoTempFiles(t *testing.T) {
	s := newStore(t)
	if err := s.Save(newTestSnapshot("local")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	entries, err := os.ReadDir(s.dir())
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("snapshot dir has %d entries, want exactly 1: %v", len(entries), entryNames(entries))
	}
	if entries[0].Name() != "local.json" {
		t.Fatalf("expected only local.json, got %q", entries[0].Name())
	}
}

func TestSavedFileModeIs0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows exposes only a read-only bit through os.FileMode, so the
		// POSIX 0600 assertion is meaningless there.
		t.Skip("file permission bits are not meaningful on windows")
	}
	s := newStore(t)
	if err := s.Save(newTestSnapshot("local")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(s.dir(), "local.json"))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("saved file mode = %o, want 600", perm)
	}
}

// Re-saving a name must replace the prior capture rather than failing or
// accumulating duplicates: it is the documented way to refresh a snapshot.
func TestSaveOverwritesExistingSnapshot(t *testing.T) {
	s := newStore(t)
	first := newTestSnapshot("local")
	first.System.Kernel = "old"
	if err := s.Save(first); err != nil {
		t.Fatalf("first Save() error = %v", err)
	}
	second := newTestSnapshot("local")
	second.System.Kernel = "new"
	if err := s.Save(second); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}

	names, err := s.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !reflect.DeepEqual(names, []string{"local"}) {
		t.Fatalf("List() = %v, want exactly [local]", names)
	}
	got, err := s.Load("local")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.System.Kernel != "new" {
		t.Fatalf("Load() after overwrite has Kernel %q, want %q", got.System.Kernel, "new")
	}
}

func TestLoadMissingReturnsErrNotFound(t *testing.T) {
	s := newStore(t)
	_, err := s.Load("does-not-exist")
	if err == nil {
		t.Fatal("Load() of a missing name returned no error")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load() error = %v, want errors.Is(err, ErrNotFound)", err)
	}
}

func TestLoadInvalidJSONMentionsName(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	// A file that exists but is corrupt must surface as a parse error that
	// names the snapshot, or a user cannot tell which capture is broken.
	badPath := filepath.Join(s.dir(), "broken.json")
	if err := os.WriteFile(badPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := s.Load("broken")
	if err == nil {
		t.Fatal("Load() of a corrupt file returned no error")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("Load() error %q does not mention the snapshot name", err)
	}
}

// A snapshot written by a future Nyrvo must fail loudly rather than be
// silently misread; the guard turns "quietly wrong diff" into "upgrade".
func TestLoadRejectsNewerSchemaVersion(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	future := []byte(`{"schema_version": 999, "name": "future", "created_at": "2026-06-15T10:30:00Z"}`)
	if err := os.WriteFile(filepath.Join(s.dir(), "future.json"), future, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := s.Load("future")
	if err == nil {
		t.Fatal("Load() of a newer schema version returned no error")
	}
	if !strings.Contains(err.Error(), "future") {
		t.Fatalf("Load() error %q does not mention the snapshot name", err)
	}
}

func TestValidateNameRejectsDangerousNames(t *testing.T) {
	long := strings.Repeat("x", 65)
	for _, name := range []string{
		"../escape",  // path traversal: would resolve outside the snapshots dir
		"a/b",        // a separator must never reach the filesystem path
		"",           // not a name; Save("") would otherwise write ".json"
		".hidden",    // leading dot collides with hidden dotfiles
		long,         // one over the documented 64-char ceiling
		"with space", // shell-unfriendly and ambiguous on the command line
		"-leading",   // leading hyphen parses as a flag on most CLIs
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateName(name); err == nil {
				t.Fatalf("ValidateName(%q) = nil, want an error", name)
			}
		})
	}
}

// The rejection table above must not reject the names the CLI is expected to
// accept; otherwise every valid invocation would fail at the gate.
func TestValidateNameAcceptsValidNames(t *testing.T) {
	for _, name := range []string{"local", "staging-2", "prod_us-east-1", "a.b.c"} {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
}

// Dangerous names must be rejected before any disk access, so a hostile name
// cannot create a file outside the store directory or even create the store
// directory itself.
func TestSaveAndLoadRejectDangerousNamesWithoutWriting(t *testing.T) {
	long := strings.Repeat("x", 65)
	names := []string{"../escape", "a/b", "", ".hidden", long, "with space", "-leading"}
	s := newStore(t)

	for _, name := range names {
		t.Run("save "+name, func(t *testing.T) {
			if err := s.Save(newTestSnapshot(name)); err == nil {
				t.Fatalf("Save(%q) = nil, want an error", name)
			}
			assertTreeEmpty(t, s.Root)
		})
	}
	for _, name := range names {
		t.Run("load "+name, func(t *testing.T) {
			if _, err := s.Load(name); err == nil {
				t.Fatalf("Load(%q) = nil, want an error", name)
			}
		})
	}
	assertTreeEmpty(t, s.Root)
}

// List must not fail when no snapshots have ever been captured.
func TestListMissingDirIsEmpty(t *testing.T) {
	s := newStore(t)
	names, err := s.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("List() = %v, want empty", names)
	}
}

// The snapshot dir can accumulate unrelated files (notes, stale cache dirs);
// only *.json files count as names and the result must be lexically sorted.
func TestListSortedIgnoresNonJSONAndDirs(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for _, name := range []string{"zeta.json", "alpha.json", "mid.json"} {
		if err := os.WriteFile(filepath.Join(s.dir(), name), []byte("{}"), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(s.dir(), "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	// A directory named "cache.json" would be mistaken for a snapshot if List
	// trusted suffixes alone.
	if err := os.Mkdir(filepath.Join(s.dir(), "cache.json"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	names, err := s.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if want := []string{"alpha", "mid", "zeta"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("List() = %v, want %v", names, want)
	}
}

// A caller mistake must surface as an error, not as a panic that takes the CLI
// down, and must never write a document that parses but describes nothing.
func TestNilSnapshotIsAnErrorNotAPanic(t *testing.T) {
	s := newStore(t)
	if err := s.Save(nil); err == nil {
		t.Error("Save(nil) = nil, want an error")
	}
	if data, err := Marshal(nil); err == nil {
		t.Errorf("Marshal(nil) = %q, want an error", data)
	}
}
