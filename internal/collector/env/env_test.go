package env

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

func collect(t *testing.T, entries []string) *snapshot.Snapshot {
	t.Helper()
	snap := snapshot.New("test", time.Now())
	err := (Env{Environ: func() []string { return entries }}).Collect(context.Background(), snap)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return snap
}

// The snapshot is a file users paste into bug reports, so a value that slips
// through would leak credentials. This test marshals the whole document and
// asserts the secrets themselves are absent.
func TestCollectLeaksNoValuesIntoJSON(t *testing.T) {
	snap := collect(t, []string{
		"OPENAI_API_KEY=sk-live-abc123",
		"DATABASE_URL=postgres://user:hunter2@db/app",
		"PATH=/usr/bin",
	})
	snap.Normalize()

	js, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	for _, want := range []string{"OPENAI_API_KEY", "DATABASE_URL", "PATH"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("JSON missing expected variable name %q", want)
		}
	}
	for _, secret := range []string{"sk-live-abc123", "hunter2", "postgres://", "/usr/bin"} {
		if strings.Contains(string(js), secret) {
			t.Errorf("JSON leaked value substring %q", secret)
		}
	}
}

func TestCollectSkipsMalformedEntries(t *testing.T) {
	snap := collect(t, []string{"NOEQUALS", "=novalue", "", "KEEP=1"})
	snap.Normalize()

	if want := []string{"KEEP"}; !reflect.DeepEqual(snap.Environment.Names, want) {
		t.Fatalf("names = %v, want %v", snap.Environment.Names, want)
	}
}

func TestCollectNameBeforeFirstEquals(t *testing.T) {
	snap := collect(t, []string{"A=b=c"})
	snap.Normalize()

	if want := []string{"A"}; !reflect.DeepEqual(snap.Environment.Names, want) {
		t.Fatalf("names = %v, want %v", snap.Environment.Names, want)
	}
}

func TestCollectDeduplicatesAndSorts(t *testing.T) {
	snap := collect(t, []string{"B=1", "A=2", "A=3", "C=4", "b=5"})
	snap.Normalize()

	// ASCII byte order: uppercase sorts before lowercase.
	if want := []string{"A", "B", "C", "b"}; !reflect.DeepEqual(snap.Environment.Names, want) {
		t.Fatalf("names = %v, want %v", snap.Environment.Names, want)
	}
}

func TestCollectEmptyEnviron(t *testing.T) {
	snap := collect(t, nil)

	if snap.Environment == nil {
		t.Fatal("snap.Environment is nil, want non-nil")
	}
	// A non-nil empty slice serializes as "names":[] rather than "names":null,
	// keeping the document shape stable.
	if snap.Environment.Names == nil || len(snap.Environment.Names) != 0 {
		t.Fatalf("names = %v, want empty non-nil slice", snap.Environment.Names)
	}
}

func TestName(t *testing.T) {
	if got := (Env{}).Name(); got != "environment" {
		t.Fatalf("Name() = %q, want %q", got, "environment")
	}
}
