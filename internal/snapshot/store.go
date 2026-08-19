package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DirName is the per-project directory holding captured snapshots. It lives in
// the working directory so snapshots follow the repository they describe, and
// so Nyrvo needs no global state or account.
const DirName = ".nyrvo"

// maxSnapshotSize caps how much of one snapshot file is read. A snapshot file
// is local input the store is pointed at, and a generated or hostile file must
// not exhaust memory just because Nyrvo loaded it. One megabyte is far beyond
// anything a captured environment legitimately serializes to.
const maxSnapshotSize = 1 << 20 // 1 MiB

// ErrNotFound is returned when a named snapshot does not exist.
var ErrNotFound = errors.New("snapshot not found")

// validName restricts snapshot names to characters that are safe as a single
// path element on every supported platform. Names come from the command line
// and are used to build a file path, so anything containing a separator or
// ".." must be rejected rather than sanitized silently.
var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Store persists snapshots as JSON files under Root/.nyrvo/snapshots.
type Store struct {
	Root string
}

// NewStore returns a store rooted at dir. An empty dir means the current
// working directory.
func NewStore(dir string) *Store {
	if dir == "" {
		dir = "."
	}
	return &Store{Root: dir}
}

// ValidateName reports whether name is usable as a snapshot identifier.
func ValidateName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid snapshot name %q: use letters, digits, '.', '_' or '-' (max 64 chars, must start alphanumeric)", name)
	}
	return nil
}

func (s *Store) dir() string { return filepath.Join(s.Root, DirName, "snapshots") }

func (s *Store) path(name string) string {
	return filepath.Join(s.dir(), name+".json")
}

// Save writes snap under its name, replacing any previous capture with that
// name. The snapshot is normalized first so repeated captures of an unchanged
// environment produce identical bytes.
func (s *Store) Save(snap *Snapshot) error {
	if snap == nil {
		return errors.New("save snapshot: nothing to save")
	}
	if err := ValidateName(snap.Name); err != nil {
		return err
	}
	if err := snap.Validate(); err != nil {
		return fmt.Errorf("save snapshot %q: %w", snap.Name, err)
	}
	snap.Normalize()
	data, err := Marshal(snap)
	if err != nil {
		return err
	}
	// A symlinked .nyrvo or .nyrvo/snapshots would route every write of this
	// capture — the .gitignore and the snapshot file itself — outside the
	// repository. Refuse before anything touches disk.
	if err := s.checkNoSymlink(filepath.Join(s.Root, DirName)); err != nil {
		return err
	}
	if err := s.checkNoSymlink(s.dir()); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	// MkdirAll only applies the mode to directories it creates, and an older
	// 0755 tree would otherwise keep world-readable snapshots. Match the
	// config store: directories 0700, files 0600.
	if err := os.Chmod(filepath.Join(s.Root, DirName), 0o700); err != nil {
		return fmt.Errorf("set snapshot directory permissions: %w", err)
	}
	if err := os.Chmod(s.dir(), 0o700); err != nil {
		return fmt.Errorf("set snapshot directory permissions: %w", err)
	}
	if err := s.writeGitignore(); err != nil {
		return err
	}
	// Write to a temporary file and rename so an interrupted save cannot leave
	// a truncated snapshot behind.
	tmp, err := os.CreateTemp(s.dir(), "."+snap.Name+".*.tmp")
	if err != nil {
		return fmt.Errorf("create snapshot file: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup: a no-op once the rename succeeds, and a leftover
	// temp file is not worth failing an otherwise good save.
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	// Snapshots contain no secrets but may describe private infrastructure, so
	// keep them owner-readable rather than inheriting a permissive umask.
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("set snapshot permissions: %w", err)
	}
	if err := os.Rename(tmpName, s.path(snap.Name)); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	return nil
}

// checkNoSymlink returns an error when path exists as a symbolic link.
//
// The store writes through .nyrvo and .nyrvo/snapshots, and a repository can
// contain a .nyrvo that is a symlink pointing elsewhere: `nyrvo capture` would
// then write the .gitignore and every snapshot into the link's target, which
// may be another repository or any directory the user never consented to
// touch. A capture must never write outside the repository's own .nyrvo, so the
// write path refuses to follow a link instead of guessing where the user meant
// it to go. A path that does not exist yet is fine — it will be created as a
// real directory.
func (s *Store) checkNoSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link; refusing to write a capture through it", path)
	}
	return nil
}

// writeGitignore makes the snapshot directory ignore itself.
//
// Without it, capturing inside a repository leaves an untracked .nyrvo
// directory, which the Git collector then reports as a dirty working tree: the
// tool would change the very thing it measures, and a second capture would
// differ from the first for no real reason. Ignoring the directory also keeps
// snapshots — which list environment variable names — from being committed by
// accident.
//
// It is written once and never overwritten, so a project that deliberately
// tracks its snapshots can empty the file and keep it that way.
func (s *Store) writeGitignore() error {
	path := filepath.Join(s.Root, DirName, ".gitignore")
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link; refusing to write through it", path)
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("check %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte("*\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Load reads the snapshot stored under name.
func (s *Store) Load(name string) (*Snapshot, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	f, err := os.Open(s.path(name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%q: %w", name, ErrNotFound)
		}
		return nil, fmt.Errorf("read snapshot %q: %w", name, err)
	}
	// The file is only read, so a failed close carries no lost data to report.
	defer func() { _ = f.Close() }()
	// LimitReader keeps the allocation bounded to the cap: an oversized file
	// costs us one byte of evidence past the cap, not a second copy of its full
	// content, and never gets parsed.
	data, err := io.ReadAll(io.LimitReader(f, maxSnapshotSize+1))
	if err != nil {
		return nil, fmt.Errorf("read snapshot %q: %w", name, err)
	}
	if len(data) > maxSnapshotSize {
		return nil, fmt.Errorf("snapshot %q is larger than %d bytes; refusing to read it", name, maxSnapshotSize)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parse snapshot %q: %w", name, err)
	}
	if snap.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("snapshot %q uses schema version %d, this build understands up to %d: upgrade nyrvo", name, snap.SchemaVersion, SchemaVersion)
	}
	if err := snap.Validate(); err != nil {
		return nil, fmt.Errorf("snapshot %q: %w", name, err)
	}
	// The name is the identity a user types on the command line; a document
	// carrying a different one is ambiguous about which environment was meant.
	if snap.Name != name {
		return nil, fmt.Errorf("snapshot %q has name %q; load it as %q instead", name, snap.Name, snap.Name)
	}
	snap.Normalize()
	return &snap, nil
}

// List returns the names of stored snapshots in lexical order.
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(names)
	return names, nil
}

// Marshal renders a snapshot as the canonical JSON document written to disk and
// printed by --json: indented, newline-terminated, and stable across runs.
//
// It normalizes snap in place, so a snapshot must not be marshalled while
// another goroutine is still writing to it.
//
// A nil snapshot is an error rather than the literal document "null": writing
// that to a file would produce something that parses but describes nothing, and
// the mistake would only surface much later, at diff time.
func Marshal(snap *Snapshot) ([]byte, error) {
	if snap == nil {
		return nil, errors.New("encode snapshot: no snapshot")
	}
	snap.Normalize()
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode snapshot: %w", err)
	}
	return append(data, '\n'), nil
}
