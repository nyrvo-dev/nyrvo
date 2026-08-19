// Package docker tests exercise the collector through a fake command executor,
// never the real docker binary: the daemon state of the machine running the
// tests is unknowable, and a test whose result depends on whether the
// developer's daemon happens to be running is worse than no test.
package docker

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// Command vectors, spelled out so the fake's lookup keys and the collector's
// invocations cannot drift apart silently.
const (
	clientFormat  = "docker version --format {{.Client.Version}}"
	serverFormat  = "docker version --format {{.Server.Version}}"
	legacyClient  = "docker --version"
	composeShort  = "docker compose version --short"
	legacyCompose = "docker-compose --version"
	psFormat      = "docker ps --format {{json .}}"
)

// fakeResult is one fake command invocation's answer.
type fakeResult struct {
	out string
	err error
}

// fakeRun returns a command executor that answers each invocation from stub,
// keyed by "name arg1 arg2 ...". A call with no stub entry panics so an
// unexpected command fails the test loudly instead of being silently misread.
// Every invocation honours ctx first, so a cancelled context surfaces as a
// context error exactly as the real collector.Run would.
func fakeRun(stub map[string]fakeResult) runFunc {
	return func(ctx context.Context, name string, args ...string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		r, ok := stub[strings.Join(append([]string{name}, args...), " ")]
		if !ok {
			panic(fmt.Sprintf("unexpected command: %s %s", name, strings.Join(args, " ")))
		}
		return r.out, r.err
	}
}

func TestName(t *testing.T) {
	if got := (&Docker{}).Name(); got != "docker" {
		t.Errorf("Name() = %q, want %q", got, "docker")
	}
}

func TestCollect(t *testing.T) {
	tests := []struct {
		name    string
		stub    map[string]fakeResult
		want    *snapshot.Docker
		wantErr error
	}{
		{
			name: "client and daemon healthy",
			stub: map[string]fakeResult{
				clientFormat: {out: "29.4.0"},
				serverFormat: {out: "29.4.0"},
				psFormat:     {out: ""},
				composeShort: {out: "v5.1.2"},
			},
			want: &snapshot.Docker{
				ClientVersion:  "29.4.0",
				ServerVersion:  "29.4.0",
				DaemonRunning:  true,
				ComposeVersion: "5.1.2",
			},
		},
		{
			// The case this collector exists for: a working CLI whose daemon
			// does not answer. The section must be present, with the client
			// version and DaemonRunning false, and the capture must not fail.
			name: "CLI present, daemon down",
			stub: map[string]fakeResult{
				clientFormat: {out: "29.4.0"},
				serverFormat: {err: fmt.Errorf("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?")},
				composeShort: {out: "5.1.2"},
			},
			want: &snapshot.Docker{
				ClientVersion:  "29.4.0",
				ComposeVersion: "5.1.2",
			},
		},
		{
			name: "docker binary absent",
			stub: map[string]fakeResult{
				clientFormat: {err: fmt.Errorf("docker not found: %w", collector.ErrUnavailable)},
			},
			wantErr: collector.ErrUnavailable,
		},
		{
			name: "compose absent",
			stub: map[string]fakeResult{
				clientFormat:  {out: "29.4.0"},
				serverFormat:  {out: "29.4.0"},
				psFormat:      {out: ""},
				composeShort:  {err: fmt.Errorf("docker: unknown command")},
				legacyCompose: {err: fmt.Errorf("docker-compose not found: %w", collector.ErrUnavailable)},
			},
			want: &snapshot.Docker{
				ClientVersion: "29.4.0",
				ServerVersion: "29.4.0",
				DaemonRunning: true,
			},
		},
		{
			// A deadline on the plugin probe is not a verdict on compose: the
			// legacy binary is still asked before concluding it is absent.
			name: "compose probe deadline degrades to empty version",
			stub: map[string]fakeResult{
				clientFormat:  {out: "29.4.0"},
				serverFormat:  {out: "29.4.0"},
				psFormat:      {out: ""},
				composeShort:  {err: context.DeadlineExceeded},
				legacyCompose: {err: fmt.Errorf("docker-compose not found: %w", collector.ErrUnavailable)},
			},
			want: &snapshot.Docker{
				ClientVersion: "29.4.0",
				ServerVersion: "29.4.0",
				DaemonRunning: true,
			},
		},
		{
			name: "server probe deadline marks daemon not running",
			stub: map[string]fakeResult{
				clientFormat: {out: "29.4.0"},
				serverFormat: {err: context.DeadlineExceeded},
				composeShort: {out: "5.1.2"},
			},
			want: &snapshot.Docker{
				ClientVersion:  "29.4.0",
				ComposeVersion: "5.1.2",
			},
		},
		{
			// The bug the Windows runner found, in the probe most likely to hit
			// it: the format probe talks to the daemon, so a sick daemon is
			// exactly when it drags past the deadline.
			name: "client probe deadline falls through to the fallback",
			stub: map[string]fakeResult{
				clientFormat: {err: context.DeadlineExceeded},
				legacyClient: {out: "Docker version 29.4.0, build 9d7ad9f"},
				serverFormat: {out: "29.4.0"},
				psFormat:     {out: ""},
				composeShort: {out: "5.1.2"},
			},
			want: &snapshot.Docker{
				ClientVersion:  "29.4.0",
				ServerVersion:  "29.4.0",
				DaemonRunning:  true,
				ComposeVersion: "5.1.2",
			},
		},
		{
			name: "docker --version fallback when the format probe fails",
			stub: map[string]fakeResult{
				clientFormat: {err: fmt.Errorf("docker version: boom")},
				legacyClient: {out: "Docker version 29.4.0, build 9d7ad9f"},
				serverFormat: {out: "29.4.0"},
				psFormat:     {out: ""},
				composeShort: {out: "5.1.2"},
			},
			want: &snapshot.Docker{
				ClientVersion:  "29.4.0",
				ServerVersion:  "29.4.0",
				DaemonRunning:  true,
				ComposeVersion: "5.1.2",
			},
		},
		{
			name: "docker-compose --version fallback",
			stub: map[string]fakeResult{
				clientFormat:  {out: "29.4.0"},
				serverFormat:  {out: "29.4.0"},
				psFormat:      {out: ""},
				composeShort:  {err: fmt.Errorf("docker: unknown command")},
				legacyCompose: {out: "Docker Compose version v5.1.2"},
			},
			want: &snapshot.Docker{
				ClientVersion:  "29.4.0",
				ServerVersion:  "29.4.0",
				DaemonRunning:  true,
				ComposeVersion: "5.1.2",
			},
		},
		{
			// Every version source answering with garbage must yield an empty
			// field, never a wrong one, and never a failed capture.
			name: "unparseable versions degrade to empty fields",
			stub: map[string]fakeResult{
				clientFormat: {out: "garbage"},
				legacyClient: {out: "garbage"},
				serverFormat: {out: "garbage"},
				psFormat:     {out: ""},
				composeShort: {out: "garbage"},
			},
			want: &snapshot.Docker{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Docker{run: fakeRun(tt.stub)}
			snap := snapshot.New("test", time.Now())
			err := d.Collect(context.Background(), snap)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Collect() error = %v, want errors.Is(err, %v)", err, tt.wantErr)
				}
				if snap.Docker != nil {
					t.Fatalf("snap.Docker = %+v, want nil when docker is unavailable", snap.Docker)
				}
				return
			}

			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			if snap.Docker == nil {
				t.Fatal("snap.Docker = nil, want a Docker section")
			}
			if *snap.Docker != *tt.want {
				t.Errorf("snap.Docker = %+v, want %+v", *snap.Docker, *tt.want)
			}
		})
	}
}

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "29.4.0", want: "29.4.0"},
		{in: "v5.1.2", want: "5.1.2"},
		{in: "Docker version 29.4.0, build 9d7ad9f", want: "29.4.0"},
		{in: "Docker Compose version v5.1.2", want: "5.1.2"},
		{in: "2.32", want: "2.32"},
		{in: "garbage", want: ""},
		{in: "", want: ""},
		{in: "not a version at all", want: ""},
		// A build hash alone must not be read as a version.
		{in: "9d7ad9f", want: ""},
	}

	for _, tt := range tests {
		if got := normalizeVersion(tt.in); got != tt.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestCollectCancelledContext proves a cancelled context surfaces as a real
// error instead of ErrUnavailable and returns without hanging.
func TestCollectCancelledContext(t *testing.T) {
	// The watchdog outer context fails this test if Collect ever stalls past
	// its deadline, turning an endless hang into a bounded assertion.
	watchdog, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx, cancelCtx := context.WithCancel(watchdog)
	cancelCtx()

	d := &Docker{run: fakeRun(map[string]fakeResult{
		clientFormat: {out: "29.4.0"},
	})}
	snap := snapshot.New("test", time.Now())
	err := d.Collect(ctx, snap)

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
	if snap.Docker != nil {
		t.Errorf("snap.Docker = %+v, want nil after cancellation", snap.Docker)
	}
}

// TestCollectLeavesOtherSectionsUntouched proves the collector fills only its
// own section, so a concurrent sibling can never be clobbered.
func TestCollectLeavesOtherSectionsUntouched(t *testing.T) {
	d := &Docker{run: fakeRun(map[string]fakeResult{
		clientFormat: {out: "29.4.0"},
		serverFormat: {out: "29.4.0"},
		psFormat:     {out: ""},
		composeShort: {out: "5.1.2"},
	})}
	snap := snapshot.New("test", time.Now())
	snap.Git = &snapshot.Git{SHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Branch: "main", Dirty: true}
	snap.Environment = &snapshot.Environment{Names: []string{"REDIS_URL"}, Partial: true}
	gitBefore := *snap.Git
	envBefore := *snap.Environment

	err := d.Collect(context.Background(), snap)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if snap.Docker == nil {
		t.Fatal("snap.Docker = nil, want a Docker section")
	}
	if !reflect.DeepEqual(*snap.Git, gitBefore) {
		t.Errorf("snap.Git changed: got %+v, want %+v", *snap.Git, gitBefore)
	}
	if !reflect.DeepEqual(*snap.Environment, envBefore) {
		t.Errorf("snap.Environment changed: got %+v, want %+v", *snap.Environment, envBefore)
	}
}

func TestCollectRecordsRunningServicesOnlyWhenTheDaemonAnswers(t *testing.T) {
	up := fakeRun(map[string]fakeResult{
		clientFormat: {out: "29.4.0"},
		serverFormat: {out: "29.4.0"},
		composeShort: {out: "5.1.2"},
		psFormat:     {out: `{"Image":"postgres:16","Ports":"0.0.0.0:5432->5432/tcp"}`},
	})
	snap := snapshot.New("local", time.Time{})
	if err := (&Docker{run: up}).Collect(context.Background(), snap); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := []snapshot.Service{{Image: "postgres:16", Ports: []string{"5432"}}}
	if !reflect.DeepEqual(snap.Services, want) {
		t.Fatalf("services = %+v, want %+v", snap.Services, want)
	}

	// With the daemon down there is nothing to ask. Reporting no services then
	// would be answering "none" to a question that was never put, and a rule
	// downstream would read it as evidence the machine runs nothing.
	down := fakeRun(map[string]fakeResult{
		clientFormat: {out: "29.4.0"},
		serverFormat: {err: fmt.Errorf("Cannot connect to the Docker daemon")},
		composeShort: {out: "5.1.2"},
	})
	snap = snapshot.New("local", time.Time{})
	if err := (&Docker{run: down}).Collect(context.Background(), snap); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(snap.Services) != 0 {
		t.Fatalf("services = %+v, want none observed", snap.Services)
	}
}

// The published untruth this fixes: windows-latest carries compose, a cold
// probe ran out of time, and the daily feed recorded the runner as having none.
// A tool that was too slow to answer is unknown, not absent.
func TestTimedOutProbesAreUnmeasuredNotAbsent(t *testing.T) {
	timeout := func(args ...string) error {
		return fmt.Errorf("docker %v: %w", args, context.DeadlineExceeded)
	}
	d := &Docker{run: func(_ context.Context, _ string, args ...string) (string, error) {
		switch {
		case len(args) > 1 && args[1] == "--format" && strings.Contains(args[2], "Client"):
			return "29.1.5", nil
		case len(args) > 1 && args[1] == "--format":
			// The daemon probe: too slow to answer says nothing about whether
			// the daemon is up.
			return "", timeout(args...)
		case args[0] == "compose":
			return "", timeout(args...)
		}
		return "", timeout(args...)
	}}

	snap := snapshot.New("local", time.Time{})
	if err := d.Collect(context.Background(), snap); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	want := []string{"docker.compose_version", "docker.daemon_running", "docker.server_version"}
	got := append([]string(nil), snap.Unmeasured...)
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Errorf("Unmeasured = %v, want %v", got, want)
	}
	// DaemonRunning still records false, because a bool has no third state. That
	// is exactly why the key is listed above: the diff must refuse to compare it
	// rather than read the zero value as an observation.
	if snap.Docker == nil || snap.Docker.DaemonRunning {
		t.Errorf("docker section = %+v, want the section present with an unread daemon", snap.Docker)
	}
}

// A daemon that answers "not running" is a real observation and must not be
// suppressed: that is the very state this collector exists to distinguish.
func TestDownDaemonIsNotMarkedUnmeasured(t *testing.T) {
	d := &Docker{run: func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) > 1 && args[1] == "--format" && strings.Contains(args[2], "Client") {
			return "29.1.5", nil
		}
		return "", errors.New("Cannot connect to the Docker daemon")
	}}

	snap := snapshot.New("local", time.Time{})
	if err := d.Collect(context.Background(), snap); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(snap.Unmeasured) != 0 {
		t.Errorf("a daemon that answered was marked unmeasured: %v", snap.Unmeasured)
	}
}

// docker ps running out of time is not an observation of no containers. The
// daemon answered, so the section stays, but services must be unmeasured
// rather than an empty list that looks like "none running".
func TestDockerPsTimeoutIsUnmeasuredNotEmpty(t *testing.T) {
	d := &Docker{run: fakeRun(map[string]fakeResult{
		clientFormat: {out: "29.4.0"},
		serverFormat: {out: "29.4.0"},
		composeShort: {out: "5.1.2"},
		psFormat:     {err: fmt.Errorf("docker ps: %w", context.DeadlineExceeded)},
	})}
	snap := snapshot.New("local", time.Time{})
	if err := d.Collect(context.Background(), snap); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if snap.Docker == nil || !snap.Docker.DaemonRunning {
		t.Fatalf("docker section = %+v, want a running daemon", snap.Docker)
	}
	if len(snap.Services) != 0 {
		t.Errorf("Services = %+v, want none recorded after a timeout", snap.Services)
	}
	if !slices.Contains(snap.Unmeasured, "docker.services") {
		t.Errorf("Unmeasured = %v, want docker.services", snap.Unmeasured)
	}
}

// Parent cancellation during docker ps must still abort the capture.
func TestDockerPsCancelledContextReturnsCtxErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Docker{run: func(ctx context.Context, name string, args ...string) (string, error) {
		key := strings.Join(append([]string{name}, args...), " ")
		if key == psFormat {
			cancel()
			return "", ctx.Err()
		}
		switch key {
		case clientFormat, serverFormat:
			return "29.4.0", nil
		case composeShort:
			return "5.1.2", nil
		}
		panic("unexpected command: " + key)
	}}
	snap := snapshot.New("local", time.Time{})
	err := d.Collect(ctx, snap)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect() error = %v, want context.Canceled", err)
	}
}
