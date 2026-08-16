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

func TestJavaProbesBothSpellingsAndAsksForStderrOnlyWhereItAnswers(t *testing.T) {
	c, ok := Java().(*runtimeCollector)
	if !ok {
		t.Fatal("Java() no longer returns the shared collector")
	}
	if len(c.probes) != 2 {
		t.Fatalf("java has %d probes, want --version first and -version as the JDK 8 fallback", len(c.probes))
	}
	// --version answers on stdout like every other runtime and must be tried
	// first; -version is the older spelling that answers on stderr, and reading
	// stderr has to be asked for deliberately or any tool's warning becomes a
	// version.
	if c.probes[0].args[0] != "--version" || c.probes[0].stderr {
		t.Errorf("first probe = %+v, want --version without stderr", c.probes[0])
	}
	if c.probes[1].args[0] != "-version" || !c.probes[1].stderr {
		t.Errorf("second probe = %+v, want -version with stderr", c.probes[1])
	}
}

func TestStderrIsReadOnlyWhenTheProbeAsksForIt(t *testing.T) {
	dir := t.TempDir()
	// A JDK 8 answering the only way it can.
	script := filepath.Join(dir, "nyrvo-oldjava")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'openjdk version \"1.8.0_402\"' >&2\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	asking := newCollector("java", probe{binary: "nyrvo-oldjava", args: []string{"-version"}, stderr: true})
	snap := snapshot.New("local", time.Time{})
	if err := asking.Collect(context.Background(), snap); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(snap.Runtimes) != 1 || snap.Runtimes[0].Version != "1.8.0" {
		t.Fatalf("runtimes = %+v, want the version read from stderr", snap.Runtimes)
	}

	// The same output without the flag must not be mistaken for an answer.
	notAsking := newCollector("java", probe{binary: "nyrvo-oldjava", args: []string{"-version"}})
	snap = snapshot.New("local", time.Time{})
	if err := notAsking.Collect(context.Background(), snap); !errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect() error = %v, want ErrUnavailable when stderr was not asked for", err)
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

func TestNPMIsCollectedSoItsRequirementCanBeJudged(t *testing.T) {
	// package.json declares engines.npm, and the requirements collector records
	// it. Without an npm observation that constraint could never be met or
	// violated — only stored.
	if got := NPM().Name(); got != "npm" {
		t.Fatalf("NPM().Name() = %q, want the name engines.npm is recorded under", got)
	}
}
