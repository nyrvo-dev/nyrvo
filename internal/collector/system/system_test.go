package system

import (
	"context"
	"runtime"
	"testing"
	"time"

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
