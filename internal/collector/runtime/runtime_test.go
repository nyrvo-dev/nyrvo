package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	// Aliased because this package is itself called runtime, and its doc says
	// the standard library one is never imported unqualified here.
	goruntime "runtime"
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
		{"bare dotnet version", "9.0.100", "9.0.100", false},
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

// The dotnet probe must read a bare SDK version. The real binary may not exist
// on the test machine, so a fake answers for it; the collector itself is what
// is under test, and it must ask exactly for --version.
func TestCollectDotNet(t *testing.T) {
	dir := fakeProbe(t, "dotnet", "9.0.100\n", "", 0)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	snap := &snapshot.Snapshot{}
	c := DotNet()

	if got := c.Name(); got != "dotnet" {
		t.Fatalf("Name() = %q, want %q", got, "dotnet")
	}
	if err := c.Collect(context.Background(), snap); err != nil {
		t.Fatalf("DotNet().Collect: %v", err)
	}
	if len(snap.Runtimes) != 1 {
		t.Fatalf("got %d runtimes, want 1", len(snap.Runtimes))
	}
	rt := snap.Runtimes[0]
	if rt.Name != "dotnet" {
		t.Fatalf("Runtime.Name = %q, want %q", rt.Name, "dotnet")
	}
	// A bare "9.0.100" is the whole answer: the SDK version the muxer reports,
	// which is what a global.json requirement will be judged against.
	if rt.Version != "9.0.100" {
		t.Fatalf("Runtime.Version = %q, want %q", rt.Version, "9.0.100")
	}
	if rt.Path == "" {
		t.Error("Runtime.Path is empty, want a path from LookPath")
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
	// A JDK 8 answering the only way it can.
	dir := fakeProbe(t, "nyrvo-oldjava", "", "openjdk version \"1.8.0_402\"\n", 0)
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

// fakeProbe puts an executable named binary on PATH whose behaviour is written
// in Go and compiled for whatever platform the tests run on.
//
// These used to be "#!/bin/sh" files. A shebang means nothing on Windows, so
// the fakes never ran there: two tests failed outright and a third passed for
// the wrong reason, because a binary that cannot start and a binary that
// answers nothing both end in ErrUnavailable. A compiled helper is the only
// version that tests the same behaviour everywhere.
func fakeProbe(t *testing.T, binary, stdout, stderr string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()

	source := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
)

func main() {
	if s := %q; s != "" {
		fmt.Fprint(os.Stdout, s)
	}
	if s := %q; s != "" {
		fmt.Fprint(os.Stderr, s)
	}
	os.Exit(%d)
}
`, stdout, stderr, exitCode)

	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatalf("write fake probe source: %v", err)
	}
	// A module file, because `go build` outside a module is an error rather
	// than a fallback.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module nyrvofakeprobe\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write fake probe module: %v", err)
	}

	name := binary
	if goruntime.GOOS == "windows" {
		// LookPath finds "name" through "name.exe"; the built file has to
		// carry the extension for Windows to consider it executable at all.
		name += ".exe"
	}
	build := exec.Command("go", "build", "-o", filepath.Join(dir, name), src)
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake probe: %v: %s", err, out)
	}
	return dir
}

func TestEveryRuntimeHasADistinctName(t *testing.T) {
	// The name is the diff key and the string a requirement matches on. Two
	// collectors sharing one would silently overwrite each other's observation.
	seen := map[string]bool{}
	for _, c := range []collector.Collector{Go(), Node(), NPM(), Python(), Ruby(), PHP(), Rust(), Java(), DotNet()} {
		if seen[c.Name()] {
			t.Fatalf("two runtime collectors are both named %q", c.Name())
		}
		seen[c.Name()] = true
	}
}

func TestProbeThatRefusesToAnswerIsUnavailableNotFatal(t *testing.T) {
	// A binary that exists and exits non-zero is what rustup, rbenv and pyenv
	// all produce when a project pins a toolchain the machine does not have.
	// That is the drift Nyrvo is being run to find, not a reason to throw away
	// every other observation in the capture.
	dir := fakeProbe(t, "nyrvo-refuses", "", "error: Missing manifest in toolchain\n", 1)
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
	dir := fakeProbe(t, "nyrvo-babbles", "not a version at all\n", "", 0)
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

// The defect this guards against was found on a cold windows-latest runner:
// `npm --version` took longer than the probe deadline, npm was recorded as
// absent, and a second capture of the same machine a moment later found it. The
// two snapshots then disagreed about a machine that had not changed.
//
// A probe that runs out of time proves nothing about the runtime. LookPath
// already found the binary, so it is installed; only its version is unknown.
func TestProbeThatRunsOutOfTimeIsUnmeasuredNotAbsent(t *testing.T) {
	restore := collector.DefaultTimeout
	collector.DefaultTimeout = 50 * time.Millisecond
	t.Cleanup(func() { collector.DefaultTimeout = restore })

	dir := fakeSleepingProbe(t, "nyrvo-slow")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	c := newCollector("slowlang", probe{binary: "nyrvo-slow", args: []string{"--version"}})
	snap := snapshot.New("local", time.Time{})
	err := c.Collect(context.Background(), snap)

	// Still unavailable to the capture engine: there is no version to record, so
	// the section is genuinely empty and the capture must not fail over it.
	if !errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect() error = %v, want it to wrap ErrUnavailable", err)
	}
	if len(snap.Runtimes) != 0 {
		t.Errorf("a runtime that never answered was recorded with a version: %+v", snap.Runtimes)
	}
	// The part that matters: the snapshot says the answer is unknown rather than
	// letting silence be read as absence.
	if got, want := snap.Unmeasured, []string{"runtime.slowlang"}; !slices.Equal(got, want) {
		t.Errorf("Unmeasured = %v, want %v", got, want)
	}
	// A timeout proves nothing about whether the tool works, so it must never be
	// published as unusable — a refusal is a fact, a timeout is a question.
	if len(snap.Unusable) != 0 {
		t.Errorf("a probe that ran out of time was marked unusable: %v", snap.Unusable)
	}
}

// A runtime that is simply not installed must not be marked unmeasured: that
// would suppress a real difference, which is the opposite failure and just as
// wrong. It must not be marked unusable either — nothing was found on PATH, so
// there is nothing to call installed.
func TestAbsentRuntimeIsNotMarkedUnmeasured(t *testing.T) {
	c := newCollector("nyrvo-absent", probe{binary: "nyrvo-definitely-absent", args: []string{"--version"}})
	snap := snapshot.New("local", time.Time{})
	if err := c.Collect(context.Background(), snap); !errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect() error = %v, want it to wrap ErrUnavailable", err)
	}
	if len(snap.Unmeasured) != 0 {
		t.Errorf("an absent runtime was marked unmeasured: %v", snap.Unmeasured)
	}
	if len(snap.Unusable) != 0 {
		t.Errorf("an absent runtime was marked unusable: %v", snap.Unusable)
	}
}

// A binary that exists and exits non-zero is what dotnet, rustc and ruby all
// produce when a project pins a toolchain the machine does not have. The
// runtime is installed — LookPath found it — and refusing is deterministic:
// asking again gives the same answer. That is not unmeasured, which the diff
// would skip; the snapshot must record it so doctor can report it.
func TestProbeThatRefusesToAnswerIsUnusableNotUnmeasured(t *testing.T) {
	dir := fakeProbe(t, "nyrvo-refuses", "", "error: toolchain not found\n", 1)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	c := newCollector("refuses", probe{binary: "nyrvo-refuses", args: []string{"--version"}})
	snap := snapshot.New("local", time.Time{})
	if err := c.Collect(context.Background(), snap); !errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect() error = %v, want it to wrap ErrUnavailable", err)
	}
	if got, want := snap.Unusable, []string{"runtime.refuses"}; !slices.Equal(got, want) {
		t.Errorf("Unusable = %v, want %v", got, want)
	}
	if len(snap.Unmeasured) != 0 {
		t.Errorf("a refusal was marked unmeasured: %v", snap.Unmeasured)
	}
}

// fakeSleepingProbe builds a binary that outlives the probe deadline.
func fakeSleepingProbe(t *testing.T, binary string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	source := "package main\nimport \"time\"\nfunc main() { time.Sleep(time.Minute) }\n"
	if err := os.WriteFile(src, []byte(source), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	out := filepath.Join(dir, binary)
	if goruntime.GOOS == "windows" {
		out += ".exe"
	}
	if b, err := exec.Command("go", "build", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build sleeping probe: %v: %s", err, b)
	}
	return dir
}
