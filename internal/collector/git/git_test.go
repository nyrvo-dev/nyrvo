package git_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
