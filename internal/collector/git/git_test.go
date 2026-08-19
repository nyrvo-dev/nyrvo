package git_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/collector/git"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// requireGit skips the suite when git is absent, so CI images without git still
// pass instead of failing on an environment problem unrelated to the code.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available; skipping git collector tests")
	}
}

func TestCollect(t *testing.T) {
	requireGit(t)

	tests := []struct {
		name       string
		setup      func(t *testing.T, dir string)
		wantBranch string
		wantDirty  bool
		wantErr    error
	}{
		{
			name: "clean repo with one commit",
			setup: func(t *testing.T, dir string) {
				initRepo(t, dir)
				commitFile(t, dir, "a.txt", "hello")
			},
			wantBranch: "main",
		},
		{
			name: "uncommitted modification",
			setup: func(t *testing.T, dir string) {
				initRepo(t, dir)
				commitFile(t, dir, "a.txt", "hello")
				writeFile(t, dir, "a.txt", "changed")
			},
			wantBranch: "main",
			wantDirty:  true,
		},
		{
			name: "detached HEAD",
			setup: func(t *testing.T, dir string) {
				initRepo(t, dir)
				commitFile(t, dir, "a.txt", "hello")
				runGit(t, dir, "checkout", "--detach")
			},
			wantBranch: "",
		},
		{
			name:    "directory is not a repository",
			setup:   func(t *testing.T, dir string) {},
			wantErr: collector.ErrUnavailable,
		},
		{
			name: "repository with no commits",
			setup: func(t *testing.T, dir string) {
				initRepo(t, dir)
			},
			wantErr: collector.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)

			snap := snapshot.New("test", time.Now())
			err := (&git.Git{Dir: dir}).Collect(context.Background(), snap)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Collect() error = %v, want errors.Is(err, %v)", err, tt.wantErr)
				}
				if snap.Git != nil {
					t.Fatalf("snap.Git = %+v, want nil when git is unavailable", snap.Git)
				}
				return
			}

			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if snap.Git == nil {
				t.Fatal("snap.Git = nil, want a Git section")
			}
			if want := runGit(t, dir, "rev-parse", "HEAD"); snap.Git.SHA != want {
				t.Errorf("SHA = %q, want %q", snap.Git.SHA, want)
			}
			if len(snap.Git.SHA) != 40 {
				t.Errorf("SHA = %q, want 40 characters", snap.Git.SHA)
			}
			if snap.Git.Branch != tt.wantBranch {
				t.Errorf("Branch = %q, want %q", snap.Git.Branch, tt.wantBranch)
			}
			if snap.Git.Dirty != tt.wantDirty {
				t.Errorf("Dirty = %v, want %v", snap.Git.Dirty, tt.wantDirty)
			}
		})
	}
}

// TestCollectCancelledContext proves a cancelled context surfaces as a real
// error instead of ErrUnavailable and returns without hanging.
func TestCollectCancelledContext(t *testing.T) {
	requireGit(t)

	dir := t.TempDir()
	initRepo(t, dir)
	commitFile(t, dir, "a.txt", "hello")

	// The watchdog outer context fails this test if Collect ever stalls past
	// its deadline, turning an endless hang into a bounded assertion.
	watchdog, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, cancelCtx := context.WithCancel(watchdog)
	cancelCtx()

	snap := snapshot.New("test", time.Now())
	err := (&git.Git{Dir: dir}).Collect(ctx, snap)

	if watchdog.Err() != nil {
		t.Fatalf("Collect() hung: %v", watchdog.Err())
	}
	if err == nil {
		t.Fatal("Collect() with cancelled context returned nil error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Collect() error = %v, want errors.Is(err, context.Canceled)", err)
	}
	if errors.Is(err, collector.ErrUnavailable) {
		t.Errorf("Collect() error = %v, must not be ErrUnavailable", err)
	}
	if snap.Git != nil {
		t.Errorf("snap.Git = %+v, want nil after cancellation", snap.Git)
	}
	if len(snap.Unmeasured) != 0 {
		t.Errorf("a cancelled capture was marked unmeasured: %v", snap.Unmeasured)
	}
}

// A probe deadline is not absence. Returning DeadlineExceeded made capture
// mark git failed, left snap.Git nil, and the next diff reported the
// repository as not described — inventing drift from a slow filesystem.
func TestProbeThatRunsOutOfTimeIsUnmeasuredNotAbsent(t *testing.T) {
	restore := collector.DefaultTimeout
	collector.DefaultTimeout = 50 * time.Millisecond
	t.Cleanup(func() { collector.DefaultTimeout = restore })

	dir := fakeSleepingGit(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	snap := snapshot.New("test", time.Now())
	err := (&git.Git{Dir: t.TempDir()}).Collect(context.Background(), snap)

	// ErrUnavailable, not nil. There is no Git section either way, and nil
	// would have capture record the section as "ok" — a collector announcing
	// success for something it never observed. ErrUnavailable is what capture
	// maps to "skipped", which is what actually happened, and it is explicitly
	// not StatusFailed: a slow filesystem does not fail a capture.
	if !errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect() error = %v, want it to wrap ErrUnavailable", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Collect() error = %v, must not surface the raw deadline: that is what made capture mark git failed", err)
	}
	if snap.Git != nil {
		t.Fatalf("snap.Git = %+v, want nil: a timeout must not invent a checkout", snap.Git)
	}
	want := []string{"git.branch", "git.dirty", "git.sha"}
	got := append([]string(nil), snap.Unmeasured...)
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Errorf("Unmeasured = %v, want %v", got, want)
	}
}

func fakeSleepingGit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	source := "package main\nimport \"time\"\nfunc main() { time.Sleep(time.Minute) }\n"
	if err := os.WriteFile(src, []byte(source), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	out := filepath.Join(dir, "git")
	if goruntime.GOOS == "windows" {
		out += ".exe"
	}
	if b, err := exec.Command("go", "build", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build sleeping git: %v: %s", err, b)
	}
	return dir
}

// TestTimedOutGitProbeIsUnmeasuredNotAbsent proves a git that answers every
// probe but runs out of time on the last one records the unread fact as
// unmeasured instead of letting the missing section read as "not a repository".
//
// This is the ADR 0017 bug shape shipped three times in other collectors: a
// question Nyrvo could not answer became a negative answer. Dirty is a bool
// with no "unknown" value (internal/snapshot/snapshot.go), so the unmeasured
// list is the only way the snapshot can say the question was never answered;
// without it a diff between two captures of one machine reports git as having
// vanished.
func TestTimedOutGitProbeIsUnmeasuredNotAbsent(t *testing.T) {
	// The deadline has to cover the probes this test needs to SUCCEED, and it
	// is competing with `go test ./...` compiling every other package: spawning
	// a small binary is milliseconds on an idle machine and occasionally whole
	// seconds on a saturated one. At two seconds this test failed intermittently
	// with sha and branch marked unmeasured too, because the probe that was
	// supposed to answer did not get scheduled in time — a flake that asserts
	// the wrong thing rather than a real regression. The sleeping probe sleeps a
	// full minute, so a generous deadline still separates the two cases cleanly
	// and only the timing-out probe pays for it.
	restore := collector.DefaultTimeout
	collector.DefaultTimeout = 10 * time.Second
	t.Cleanup(func() { collector.DefaultTimeout = restore })

	dir := fakeGit(t, "--porcelain")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	snap := snapshot.New("test", time.Now())
	err := (&git.Git{}).Collect(context.Background(), snap)

	// Still unavailable to the capture engine: there is no complete Git section
	// to record, so the capture must not fail over a probe that ran out of time.
	if !errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect() error = %v, want it to wrap ErrUnavailable", err)
	}
	if snap.Git != nil {
		t.Fatalf("snap.Git = %+v, want nil when a probe ran out of time", snap.Git)
	}
	// Only the dirty probe failed; sha and branch were answered. Marking dirty
	// unmeasured drops exactly the key the diff would otherwise report as
	// absent, and the whole-section absence is suppressed by the git.* prefix.
	if got, want := snap.Unmeasured, []string{"git.dirty"}; !slices.Equal(got, want) {
		t.Errorf("Unmeasured = %v, want %v", got, want)
	}
	// A timeout proves nothing about whether the repository is usable, so it
	// must never be recorded as unusable — a refusal is a fact, a timeout is a
	// question.
	if len(snap.Unusable) != 0 {
		t.Errorf("a probe that ran out of time was marked unusable: %v", snap.Unusable)
	}
}

// TestFirstGitProbeTimingOutMarksTheWholeSectionUnmeasured covers the other end
// of the same bug: when even the work-tree question runs out of time, nothing
// at all is known about the directory, so all three facts must be unmeasured or
// the diff reports the section as absent for a repository git simply did not
// answer about.
func TestFirstGitProbeTimingOutMarksTheWholeSectionUnmeasured(t *testing.T) {
	restore := collector.DefaultTimeout
	collector.DefaultTimeout = 10 * time.Second
	t.Cleanup(func() { collector.DefaultTimeout = restore })

	dir := fakeGit(t, "--is-inside-work-tree")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	snap := snapshot.New("test", time.Now())
	err := (&git.Git{}).Collect(context.Background(), snap)

	if !errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect() error = %v, want it to wrap ErrUnavailable", err)
	}
	if got, want := snap.Unmeasured, []string{"git.sha", "git.branch", "git.dirty"}; !slices.Equal(got, want) {
		t.Errorf("Unmeasured = %v, want %v", got, want)
	}
	if snap.Git != nil {
		t.Errorf("snap.Git = %+v, want nil", snap.Git)
	}
}

// TestTimedOutGitIsNotMarkedUnmeasuredForGenuineAbsence pins the boundary of
// the unmeasured mark: a timeout marks only the facts that did not answer. A
// directory that is not a work tree is a real observation and stays absent
// without any unmeasured key, exactly as before the fix.
func TestTimedOutGitIsNotMarkedUnmeasuredForGenuineAbsence(t *testing.T) {
	requireGit(t)

	dir := t.TempDir()
	snap := snapshot.New("test", time.Now())
	err := (&git.Git{Dir: dir}).Collect(context.Background(), snap)

	if !errors.Is(err, collector.ErrUnavailable) {
		t.Fatalf("Collect() error = %v, want it to wrap ErrUnavailable", err)
	}
	if len(snap.Unmeasured) != 0 {
		t.Errorf("a genuine absence was marked unmeasured: %v", snap.Unmeasured)
	}
}

// fakeGit puts a git-shaped binary on PATH that answers the probes the
// collector asks — a work tree, a HEAD sha, a branch — except for the last
// arguments named in sleepArgs, which sleep past the probe deadline.
//
// It is compiled Go rather than a shell script so it behaves identically on
// Windows, where shebangs mean nothing and the fakes never ran. The sleeping
// variant keeps the test short: a probe that outlives a 50ms deadline needs a
// real timeout, not a second of test time.
func fakeGit(t *testing.T, sleepArgs ...string) string {
	t.Helper()
	quoted := make([]string, len(sleepArgs))
	for i, s := range sleepArgs {
		quoted[i] = strconv.Quote(s)
	}
	source := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	args := os.Args[1:]
	last := args[len(args)-1]
	for _, s := range []string{%s} {
		if last == s {
			time.Sleep(time.Minute)
		}
	}
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "--is-inside-work-tree"):
		fmt.Print("true")
	case strings.Contains(joined, "--abbrev-ref"):
		fmt.Print("main")
	case joined == "rev-parse HEAD":
		fmt.Print("0123456789abcdef0123456789abcdef01234567")
	}
}
`, strings.Join(quoted, ", "))

	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(source), 0o600); err != nil {
		t.Fatalf("write fake git source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module nyrvofakegit\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write fake git module: %v", err)
	}
	name := "git"
	if goruntime.GOOS == "windows" {
		name += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", filepath.Join(dir, name), src).CombinedOutput(); err != nil {
		t.Fatalf("build fake git: %v: %s", err, out)
	}
	return dir
}

// initRepo creates a repository with a deterministic default branch so the
// assertions do not depend on each machine's git init.defaultBranch setting.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "-c", "init.defaultBranch=main", "init", "-q")
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitFile stages and commits one file. Identity and signing are passed per
// invocation so commits work on machines whose global git config lacks a
// user.email or has commit.gpgsign enabled.
func commitFile(t *testing.T, dir, name, content string) {
	t.Helper()
	writeFile(t, dir, name, content)
	runGit(t, dir, "add", name)
	runGit(t, dir,
		"-c", "user.email=collector@example.com",
		"-c", "user.name=Nyrvo Collector Test",
		"-c", "commit.gpgsign=false",
		"commit", "-q", "-m", "test commit",
	)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out.String())
	}
	return strings.TrimSpace(out.String())
}
