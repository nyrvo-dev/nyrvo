package system

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// OS and Arch are the reason this collector exists: they must describe the
// platform even when no external tool cooperates.
func TestCollectFillsOSAndArch(t *testing.T) {
	snap := snapshot.New("test", time.Now())
	if err := (System{}).Collect(context.Background(), snap); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if snap.System == nil {
		t.Fatal("snap.System is nil, want non-nil")
	}
	if snap.System.OS != runtime.GOOS {
		t.Errorf("OS = %q, want %q", snap.System.OS, runtime.GOOS)
	}
	if snap.System.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q, want %q", snap.System.Arch, runtime.GOARCH)
	}
}

// A cancelled context must not lose the runtime-derived fields, which never
// require external work and therefore never need to be cancelled.
func TestCollectCancelledContextStillFillsOSAndArch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	snap := snapshot.New("test", time.Now())
	if err := (System{}).Collect(ctx, snap); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if snap.System == nil {
		t.Fatal("snap.System is nil, want non-nil")
	}
	if snap.System.OS != runtime.GOOS {
		t.Errorf("OS = %q, want %q", snap.System.OS, runtime.GOOS)
	}
	if snap.System.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q, want %q", snap.System.Arch, runtime.GOARCH)
	}
}

// This collector must be the sole writer of its own section; other sections
// that capture recorded earlier must survive unchanged.
func TestCollectLeavesOtherSectionsUntouched(t *testing.T) {
	snap := snapshot.New("test", time.Now())
	snap.Git = &snapshot.Git{SHA: "abc123", Branch: "main", Dirty: true}

	if err := (System{}).Collect(context.Background(), snap); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	if snap.Git == nil {
		t.Fatal("snap.Git was cleared")
	}
	if snap.Git.SHA != "abc123" || snap.Git.Branch != "main" || !snap.Git.Dirty {
		t.Errorf("snap.Git changed: %+v", snap.Git)
	}
}

func TestName(t *testing.T) {
	if got := (System{}).Name(); got != "system" {
		t.Fatalf("Name() = %q, want %q", got, "system")
	}
}

// A missing uname binary is genuine absence of a kernel string, not a probe
// that failed to finish. The section must stay present with OS/arch and no
// unmeasured key.
func TestMissingUnameIsNotUnmeasured(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	snap := snapshot.New("test", time.Now())
	if err := (System{}).Collect(context.Background(), snap); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snap.System == nil || snap.System.OS == "" || snap.System.Arch == "" {
		t.Fatalf("OS/arch missing after a missing uname: %+v", snap.System)
	}
	if snap.System.Kernel != "" {
		t.Errorf("Kernel = %q, want empty when uname is absent", snap.System.Kernel)
	}
	if len(snap.Unmeasured) != 0 {
		t.Errorf("a missing uname was marked unmeasured: %v", snap.Unmeasured)
	}
}

// A uname that runs out of time must not look like a machine with no kernel.
func TestUnameTimeoutIsUnmeasured(t *testing.T) {
	restore := collector.DefaultTimeout
	collector.DefaultTimeout = 50 * time.Millisecond
	t.Cleanup(func() { collector.DefaultTimeout = restore })

	dir := fakeSleepingUname(t)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	snap := snapshot.New("test", time.Now())
	if err := (System{}).Collect(context.Background(), snap); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snap.System == nil || snap.System.OS == "" || snap.System.Arch == "" {
		t.Fatalf("OS/arch missing after a uname timeout: %+v", snap.System)
	}
	if snap.System.Kernel != "" {
		t.Errorf("Kernel = %q, want empty when uname timed out", snap.System.Kernel)
	}
	if got, want := snap.Unmeasured, []string{"system.kernel"}; !slices.Equal(got, want) {
		t.Errorf("Unmeasured = %v, want %v", got, want)
	}
}

func fakeSleepingUname(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	source := "package main\nimport \"time\"\nfunc main() { time.Sleep(time.Minute) }\n"
	if err := os.WriteFile(src, []byte(source), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	out := filepath.Join(dir, "uname")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	if b, err := exec.Command("go", "build", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("build sleeping uname: %v: %s", err, b)
	}
	return dir
}
