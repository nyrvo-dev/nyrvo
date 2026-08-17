package config

import (
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"
)

func useTempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous := userConfigDir
	userConfigDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userConfigDir = previous })
	return dir
}

func TestLoadMissingReturnsEmptyConfig(t *testing.T) {
	useTempConfigDir(t)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(c, &Config{}) {
		t.Fatalf("Load() = %#v, want empty config", c)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	useTempConfigDir(t)
	c := &Config{}
	if err := c.Set("ai.agent", "opencode"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := Save(c); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, c) {
		t.Fatalf("Load() = %#v, want %#v", got, c)
	}
}

func TestSaveCreatesTheDirectoryAndFile(t *testing.T) {
	root := useTempConfigDir(t)

	if err := Save(&Config{}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "nyrvo")); err != nil {
		t.Fatalf("Stat(config directory) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "nyrvo", "config.json")); err != nil {
		t.Fatalf("Stat(config file) error = %v", err)
	}
}

// TestSaveIsOwnerOnly checks the permission bits, which only exist on POSIX.
//
// Windows has no owner/group/other bits: Go's Chmod there toggles the read-only
// attribute and Stat reports 0666 or 0777 regardless. Asserting 0600 on Windows
// would not be a stricter test, it would be a test claiming a guarantee the
// platform does not offer — which is the one thing this project refuses to do
// everywhere else. The behaviour above is verified on every platform; the
// permission promise is verified where the promise means something.
func TestSaveIsOwnerOnly(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not exist on Windows")
	}
	root := useTempConfigDir(t)

	if err := Save(&Config{}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	dirInfo, err := os.Stat(filepath.Join(root, "nyrvo"))
	if err != nil {
		t.Fatalf("Stat(config directory) error = %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("config directory mode = %04o, want 0700", got)
	}
	fileInfo, err := os.Stat(filepath.Join(root, "nyrvo", "config.json"))
	if err != nil {
		t.Fatalf("Stat(config file) error = %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("config file mode = %04o, want 0600", got)
	}
}

func TestLoadMalformedReturnsPathError(t *testing.T) {
	root := useTempConfigDir(t)
	dir := filepath.Join(root, "nyrvo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want malformed config error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("Load() error = %q, want path %q", err, path)
	}
}

func TestLoadRejectsOversizedFile(t *testing.T) {
	root := useTempConfigDir(t)
	dir := filepath.Join(root, "nyrvo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, make([]byte, maxConfigSize+1), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Load() error = %v, want oversized file error", err)
	}
}

func TestSetUnknownKeyListsKnownKeys(t *testing.T) {
	useTempConfigDir(t)

	err := (&Config{}).Set("unknown", "value")
	if err == nil {
		t.Fatal("Set() error = nil, want unknown key error")
	}
	for _, key := range Keys() {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("Set() error = %q, want known key %q", err, key)
		}
	}
}

func TestSetRejectsEmptyValue(t *testing.T) {
	useTempConfigDir(t)

	for _, value := range []string{"", " \t\n"} {
		if err := (&Config{}).Set("ai.agent", value); err == nil {
			t.Errorf("Set(%q) error = nil, want empty value error", value)
		}
	}
}

func TestUnset(t *testing.T) {
	useTempConfigDir(t)
	c := &Config{}
	if err := c.Set("ai.agent", "opencode"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := c.Unset("ai.agent"); err != nil {
		t.Fatalf("Unset(set key) error = %v", err)
	}
	if value, _ := c.Get("ai.agent"); value != "" {
		t.Errorf("Get() after Unset() = %q, want empty", value)
	}
	if err := c.Unset("ai.agent"); err != nil {
		t.Errorf("Unset(empty key) error = %v", err)
	}
	if err := c.Unset("unknown"); err == nil {
		t.Error("Unset(unknown key) error = nil, want error")
	}
}

func TestGetUnknownKey(t *testing.T) {
	useTempConfigDir(t)

	if value, known := (&Config{}).Get("unknown"); known || value != "" {
		t.Errorf("Get(unknown) = (%q, %v), want (empty, false)", value, known)
	}
}

func TestKeysSorted(t *testing.T) {
	useTempConfigDir(t)

	keys := Keys()
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			t.Fatalf("Keys() = %v, want sorted keys", keys)
		}
	}
}

func TestSaveLeavesNoTempFile(t *testing.T) {
	root := useTempConfigDir(t)
	if err := Save(&Config{}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(root, "nyrvo"))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("config directory entries = %v, want only config.json", entryNames(entries))
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

func TestSetStoresTheTrimmedValue(t *testing.T) {
	var c Config
	if err := c.Set("ai.agent", "  opencode\n"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Validating a trimmed value and storing an untrimmed one hands the agent
	// lookup a name it cannot resolve, over a difference the user cannot see.
	if got, _ := c.Get("ai.agent"); got != "opencode" {
		t.Fatalf("ai.agent = %q, want %q", got, "opencode")
	}
}
