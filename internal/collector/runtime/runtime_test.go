package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// versionLike mirrors the contract that Runtime.Version is bare dotted digits
// starting with major.minor; it must always match real normalized versions.
var versionLike = regexp.MustCompile(`^\d+\.\d+`)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		err  bool
	}{
		{"go full output", "go version go1.24.2 darwin/arm64", "1.24.2", false},
		{"v prefix", "v24.4.0", "24.4.0", false},
		{"python label", "Python 3.13.3", "3.13.3", false},
		{"short go version", "go1.22", "1.22", false},
		{"release candidate", "Python 3.11.9rc1", "3.11.9", false},
		{"empty output", "", "", true},
		{"garbage", "abc", "", true},
		{"bare v", "v", "", true},
		{"single number is not a version", "version 1", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeVersion(tt.in)
			if tt.err {
				if err == nil {
					t.Fatalf("NormalizeVersion(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeVersion(%q): unexpected error %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeVersion(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCollectGo(t *testing.T) {
	snap := &snapshot.Snapshot{}
	c := Go()

	if got := c.Name(); got != "go" {
		t.Fatalf("Name() = %q, want %q", got, "go")
	}
	if err := c.Collect(context.Background(), snap); err != nil {
		t.Fatalf("Go().Collect: %v", err)
	}
	if len(snap.Runtimes) != 1 {
		t.Fatalf("got %d runtimes, want 1", len(snap.Runtimes))
	}
	rt := snap.Runtimes[0]
	if rt.Name != "go" {
		t.Fatalf("Runtime.Name = %q, want %q", rt.Name, "go")
	}
	if rt.Path == "" {
		t.Error("Runtime.Path is empty, want a path from LookPath")
	}
	if !versionLike.MatchString(rt.Version) {
		t.Fatalf("Runtime.Version = %q, want a version matching ^\\d+\\.\\d+", rt.Version)
	}
}

func TestCollectAbsentRuntime(t *testing.T) {
	// A runtime whose probe binary cannot exist on any host makes the
	// not-installed path deterministic regardless of the test machine.
	c := newCollector("nyrvo-absent",
		probe{binary: "nyrvo-no-such-binary-9f3a", args: []string{"--version"}},
	)
	snap := &snapshot.Snapshot{}

	err := c.Collect(context.Background(), snap)
	if !errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect() = %v, want an error wrapping %v", err, collector.ErrUnavailable)
	}
	if len(snap.Runtimes) != 0 {
		t.Fatalf("Collect() appended %d runtimes for an absent tool", len(snap.Runtimes))
	}
}

func TestCollectCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Go().Collect(ctx, &snapshot.Snapshot{})
	if err == nil {
		t.Fatal("Collect with an already-cancelled context returned nil")
	}
	// Cancellation is the caller's failure, not the tool being absent.
	if errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect() = %v, want a cancellation error, not ErrUnavailable", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect() = %v, want an error wrapping %v", err, context.Canceled)
	}
}

func TestCollectPreservesExistingRuntimes(t *testing.T) {
	existing := snapshot.Runtime{Name: "zulu", Version: "1.0", Path: "/bin/zulu"}
	snap := &snapshot.Snapshot{Runtimes: []snapshot.Runtime{existing}}

	if err := Go().Collect(context.Background(), snap); err != nil {
		t.Fatalf("Go().Collect: %v", err)
	}
	if len(snap.Runtimes) != 2 {
		t.Fatalf("got %d runtimes, want 2", len(snap.Runtimes))
	}
	// The pre-existing entry must keep its position and content: merge order
	// is the caller's business, and snapshot.Normalize reorders deterministically.
	if snap.Runtimes[0] != existing {
		t.Fatalf("pre-existing entry changed or moved: got %+v", snap.Runtimes[0])
	}
	if snap.Runtimes[1].Name != "go" {
		t.Fatalf("Runtimes[1].Name = %q, want %q", snap.Runtimes[1].Name, "go")
	}
}

func TestJavaAsksForTheVersionOnStdout(t *testing.T) {
	c, ok := Java().(*runtimeCollector)
	if !ok {
		t.Fatal("Java() no longer returns the shared collector")
	}
	// `java -version` writes to stderr, which collector.Run does not read: the
	// probe would return empty and be reported as an unparseable version rather
	// than as a missing runtime. Only --version answers on stdout.
	for _, p := range c.probes {
		for _, arg := range p.args {
			if arg == "-version" {
				t.Fatalf("java probed with %q, which writes to stderr", arg)
			}
		}
	}
}

func TestEveryRuntimeHasADistinctName(t *testing.T) {
	// The name is the diff key and the string a requirement matches on. Two
	// collectors sharing one would silently overwrite each other's observation.
	seen := map[string]bool{}
	for _, c := range []collector.Collector{Go(), Node(), Python(), Ruby(), PHP(), Rust(), Java()} {
		if seen[c.Name()] {
			t.Fatalf("two runtime collectors are both named %q", c.Name())
		}
		seen[c.Name()] = true
	}
}

func TestProbeThatRefusesToAnswerIsUnavailableNotFatal(t *testing.T) {
	dir := t.TempDir()
	// A binary that exists and exits non-zero is what rustup, rbenv and pyenv
	// all produce when a project pins a toolchain the machine does not have.
	// That is the drift Nyrvo is being run to find, not a reason to throw away
	// every other observation in the capture.
	script := filepath.Join(dir, "nyrvo-refuses")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"error: Missing manifest in toolchain\" >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	c := newCollector("refuses", probe{binary: "nyrvo-refuses", args: []string{"--version"}})
	snap := snapshot.New("local", time.Time{})
	err := c.Collect(context.Background(), snap)

	if !errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect() error = %v, want it to wrap ErrUnavailable", err)
	}
	// The reason has to survive: "not installed" and "installed but unusable
	// here" are different answers and the second one is the interesting one.
	if !strings.Contains(err.Error(), "Missing manifest") {
		t.Errorf("Collect() error = %q, want the probe's own complaint in it", err)
	}
	if len(snap.Runtimes) != 0 {
		t.Errorf("a runtime that never answered was recorded: %+v", snap.Runtimes)
	}
}

func TestUnparseableVersionIsUnavailableNotFatal(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "nyrvo-babbles")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'not a version at all'\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	c := newCollector("babbles", probe{binary: "nyrvo-babbles", args: []string{"--version"}})
	snap := snapshot.New("local", time.Time{})
	if err := c.Collect(context.Background(), snap); !errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect() error = %v, want it to wrap ErrUnavailable", err)
	}
	if len(snap.Runtimes) != 0 {
		t.Errorf("an unparseable version was recorded: %+v", snap.Runtimes)
	}
}
