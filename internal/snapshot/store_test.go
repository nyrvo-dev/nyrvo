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

func TestSavedDirectoryModeIs0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits are not meaningful on windows")
	}
	s := newStore(t)
	if err := s.Save(newTestSnapshot("local")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	for _, path := range []string{filepath.Join(s.Root, DirName), s.dir()} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%s) error = %v", path, err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Fatalf("%s mode = %o, want 700", path, perm)
		}
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
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
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
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
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
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
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

// A document that parses as JSON but is too incomplete to describe an
// environment must be refused at load, not diffed as "no differences".
func TestLoadRejectsIncompleteDocument(t *testing.T) {
	for _, tt := range []struct {
		name string
		doc  string
	}{
		// The bare document: no version, no name, nothing observed.
		{"empty", `{}`},
		// A name with no version stamp: the name is fine, so only the schema
		// check can reject it.
		{"no-schema-version", `{"name":"empty"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			if err := os.MkdirAll(s.dir(), 0o700); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(s.dir(), "empty.json"), []byte(tt.doc), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			_, err := s.Load("empty")
			if err == nil {
				t.Fatal("Load() of an incomplete document returned no error")
			}
			if !strings.Contains(err.Error(), "empty") {
				t.Fatalf("Load() error %q does not mention the snapshot name", err)
			}
		})
	}
}

// A document whose name disagrees with the file it was loaded as is ambiguous
// about which environment was meant, and must be refused.
func TestLoadRejectsNameMismatch(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	doc := []byte(`{"schema_version":1,"name":"other","created_at":"2026-06-15T10:30:00Z"}`)
	if err := os.WriteFile(filepath.Join(s.dir(), "local.json"), doc, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := s.Load("local")
	if err == nil {
		t.Fatal("Load() of a mismatched name returned no error")
	}
	if !strings.Contains(err.Error(), "local") {
		t.Fatalf("Load() error %q does not mention the loaded name", err)
	}
}

// A snapshot file larger than the read cap must be refused on size alone,
// before it is ever parsed, so a hostile or generated file cannot exhaust
// memory just by being loaded. The content is deliberately valid JSON padded
// with an unknown field: if the size guard were missing, this document would
// load fine, which is exactly what the guard exists to prevent.
func TestLoadRejectsOversizedSnapshot(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	doc := `{"schema_version":1,"name":"huge","created_at":"2026-06-15T10:30:00Z","pad":"` +
		strings.Repeat("x", maxSnapshotSize) + `"}`
	if err := os.WriteFile(filepath.Join(s.dir(), "huge.json"), []byte(doc), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := s.Load("huge")
	if err == nil {
		t.Fatal("Load() of an oversized snapshot returned no error")
	}
	if !strings.Contains(err.Error(), "huge") {
		t.Fatalf("Load() error %q does not mention the snapshot name", err)
	}
}

// ADR 0002: purely additive optional fields stay compatible without a version
// bump. A document carrying an unknown key must still load, as long as the
// invariants this build understands hold.
func TestLoadAllowsUnknownFields(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	doc := []byte(`{"schema_version":1,"name":"local","created_at":"2026-06-15T10:30:00Z","mystery_field":{"anything":1}}`)
	if err := os.WriteFile(filepath.Join(s.dir(), "local.json"), doc, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := s.Load("local")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Name != "local" {
		t.Fatalf("Load() Name = %q, want %q", got.Name, "local")
	}
}

// A snapshot that fails Validate (bad version, empty name, duplicated keys)
// must be refused before anything is written to disk.
func TestSaveRejectsInvalidSnapshot(t *testing.T) {
	s := newStore(t)
	bad := newTestSnapshot("local")
	bad.SchemaVersion = 0
	if err := s.Save(bad); err == nil {
		t.Fatal("Save() of a schema_version 0 snapshot returned no error")
	}
	assertTreeEmpty(t, s.Root)
}

// A symlinked .nyrvo would route every write of a capture — the .gitignore and
// the snapshot file itself — outside the repository. The store must refuse the
// save rather than follow the link.
func TestSaveRejectsSymlinkedNyrvo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires privileges on windows")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	// The link target is a directory the user never consented to touch; the
	// whole point of the guard is that nothing lands there.
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(repo, ".nyrvo")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	s := NewStore(repo)
	if err := s.Save(newTestSnapshot("local")); err == nil {
		t.Fatal("Save() through a symlinked .nyrvo returned no error")
	}
	if entries, err := os.ReadDir(target); err != nil {
		t.Fatalf("ReadDir(target) error = %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("symlink target was written to: %v", entryNames(entries))
	}
}

// Same for a symlinked .nyrvo/snapshots: the directory that actually receives
// the snapshot files must be a real directory inside the repository.
func TestSaveRejectsSymlinkedSnapshotsDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires privileges on windows")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, DirName), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(repo, DirName, "snapshots")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	s := NewStore(repo)
	if err := s.Save(newTestSnapshot("local")); err == nil {
		t.Fatal("Save() through a symlinked snapshots dir returned no error")
	}
	if entries, err := os.ReadDir(target); err != nil {
		t.Fatalf("ReadDir(target) error = %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("symlink target was written to: %v", entryNames(entries))
	}
}

// A symlinked snapshot file would make Load read a document the user never
// saved under that name. Refuse rather than follow the link.
func TestLoadRejectsSymlinkedSnapshotFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires privileges on windows")
	}
	s := newStore(t)
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	foreign := filepath.Join(t.TempDir(), "foreign.json")
	if err := os.WriteFile(foreign, []byte(`{"schema_version":1,"name":"local","created_at":"2026-06-15T10:30:00Z"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(foreign) error = %v", err)
	}
	link := filepath.Join(s.dir(), "local.json")
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if _, err := s.Load("local"); err == nil {
		t.Fatal("Load() through a symlinked snapshot returned no error")
	}
}

// writeGitignore must not follow a .gitignore symlink: WriteFile would create
// or truncate the link target, which can live outside the repository.
func TestSaveRefusesSymlinkedGitignore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires privileges on windows")
	}
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, DirName), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}
	if err := os.Symlink(target, filepath.Join(repo, DirName, ".gitignore")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	s := NewStore(repo)
	if err := s.Save(newTestSnapshot("local")); err == nil {
		t.Fatal("Save() through a symlinked .gitignore returned no error")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile(target) error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("symlink target was written to: %q", got)
	}
}

// A snapshot mailed around can carry control bytes in fields that reach the
// terminal. Load must strip them, not retain an ESC in Source.Ref.
func TestLoadStripsControlBytesFromRef(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	doc := []byte(`{"schema_version":1,"name":"ci","created_at":"2026-06-15T10:30:00Z","source":{"kind":"github-actions","ref":"ci.yml#job\u001b]0;owned\u0007"}}`)
	if err := os.WriteFile(filepath.Join(s.dir(), "ci.json"), doc, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := s.Load("ci")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Source == nil {
		t.Fatal("Source is nil")
	}
	if strings.ContainsRune(got.Source.Ref, 0x1b) || strings.ContainsRune(got.Source.Ref, 0x07) {
		t.Fatalf("Ref retained a control sequence: %q", got.Source.Ref)
	}
	if got.Source.Ref != "ci.yml#job" {
		t.Errorf("Ref = %q, want %q", got.Source.Ref, "ci.yml#job")
	}
}
