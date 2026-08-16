package githubactions

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// buildWorkflow wraps job in a workflow and returns both, mirroring how a parsed
// file would look. Tests build the model by hand because they exercise
// conversion, not parsing.
func buildWorkflow(job *Job) (*Workflow, *Job) {
	w := &Workflow{Path: ".github/workflows/ci.yml", Name: "CI"}
	w.Jobs = append(w.Jobs, *job)
	return w, &w.Jobs[0]
}

func containsNote(snap *snapshot.Snapshot, sub string) bool {
	for _, n := range snap.Source.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func TestSnapshotRunnerLabels(t *testing.T) {
	tests := []struct {
		label string
		os    string
		arch  string
	}{
		{"ubuntu-latest", "linux", "amd64"},
		{"ubuntu-22.04", "linux", "amd64"},
		// The arm runners are why labels are matched exactly: an "ubuntu-"
		// prefix rule would confidently report the wrong architecture here.
		{"ubuntu-24.04-arm", "linux", "arm64"},
		{"ubuntu-22.04-arm", "linux", "arm64"},
		{"windows-11-arm", "windows", "arm64"},
		{"UBUNTU-LATEST", "linux", "amd64"},
		{"windows-latest", "windows", "amd64"},
		{"macos-13", "darwin", "amd64"},
		{"macos-14", "darwin", "arm64"},
		{"macos-15", "darwin", "arm64"},
		{"macos-latest", "darwin", "arm64"},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			w, j := buildWorkflow(&Job{ID: "build", RunsOn: []string{tt.label}})
			snap, err := Snapshot(w, j, "n", time.Now())
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if snap.System == nil {
				t.Fatalf("System not set for runner %q", tt.label)
			}
			if snap.System.OS != tt.os || snap.System.Arch != tt.arch {
				t.Errorf("runner %q: System = %s/%s, want %s/%s",
					tt.label, snap.System.OS, snap.System.Arch, tt.os, tt.arch)
			}
		})
	}
}

func TestSnapshotRunnerUnknownOrExpression(t *testing.T) {
	tests := []struct {
		name  string
		label string
	}{
		{name: "unknown label", label: "self-hosted"},
		{name: "expression", label: "${{ matrix.os }}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, j := buildWorkflow(&Job{ID: "build", RunsOn: []string{tt.label}})
			snap, err := Snapshot(w, j, "n", time.Now())
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			// Guessing a platform for an unrecognized label would fabricate
			// evidence, so the system stays nil and the label is reported.
			if snap.System != nil {
				t.Errorf("System = %+v, want nil for label %q", snap.System, tt.label)
			}
			if !containsNote(snap, tt.label) {
				t.Errorf("expected a note naming label %q", tt.label)
			}
		})
	}
}

func TestSnapshotNoRunner(t *testing.T) {
	w, j := buildWorkflow(&Job{ID: "build"})
	snap, err := Snapshot(w, j, "n", time.Now())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.System != nil {
		t.Errorf("System = %+v, want nil when no runs-on is declared", snap.System)
	}
	if !containsNote(snap, "no runs-on") {
		t.Error("expected a note about the missing runs-on")
	}
}

func TestSnapshotContainerOverride(t *testing.T) {
	w, j := buildWorkflow(&Job{
		ID:        "build",
		RunsOn:    []string{"macos-14"},
		Container: "node:20-alpine",
	})
	snap, err := Snapshot(w, j, "n", time.Now())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.System == nil {
		t.Fatal("System not set")
	}
	// A container replaces the runner's OS (containers on GitHub-hosted runners
	// are always Linux); the architecture still comes from the runner mapping.
	if snap.System.OS != "linux" {
		t.Errorf("OS = %q, want linux", snap.System.OS)
	}
	if snap.System.Arch != "arm64" {
		t.Errorf("Arch = %q, want arm64 from the macos-14 runner", snap.System.Arch)
	}
	if !containsNote(snap, "node:20-alpine") {
		t.Error("container image should be noted")
	}
}

func TestSnapshotContainerWithUnknownRunner(t *testing.T) {
	w, j := buildWorkflow(&Job{
		ID:        "build",
		RunsOn:    []string{"self-hosted"},
		Container: "postgres:16",
	})
	snap, err := Snapshot(w, j, "n", time.Now())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// The container itself is evidence of the OS, so an unknown runner still
	// yields a system; only the architecture falls back to amd64.
	if snap.System == nil {
		t.Fatal("System not set for a container'd job with an unknown runner")
	}
	if snap.System.OS != "linux" || snap.System.Arch != "amd64" {
		t.Errorf("System = %+v, want linux/amd64", snap.System)
	}
	if !containsNote(snap, "postgres:16") {
		t.Error("container image should be noted")
	}
}

func TestSnapshotSetupActions(t *testing.T) {
	tests := []struct {
		name    string
		uses    string
		with    map[string]string
		runtime string
		version string
	}{
		{name: "node", uses: "actions/setup-node@v4", with: map[string]string{"node-version": "20.11.1"}, runtime: "node", version: "20.11.1"},
		{name: "node long pin", uses: "actions/setup-node@v4.0.0", with: map[string]string{"node-version": "v20.9.0"}, runtime: "node", version: "20.9.0"},
		{name: "node case-insensitive", uses: "Actions/Setup-Node@v4", with: map[string]string{"node-version": "22.2.0"}, runtime: "node", version: "22.2.0"},
		{name: "python", uses: "actions/setup-python@v5", with: map[string]string{"python-version": "3.13.1"}, runtime: "python", version: "3.13.1"},
		{name: "go", uses: "actions/setup-go@v5", with: map[string]string{"go-version": "1.22.4"}, runtime: "go", version: "1.22.4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, j := buildWorkflow(&Job{
				ID:    "build",
				Steps: []Step{{Uses: tt.uses, With: tt.with}},
			})
			snap, err := Snapshot(w, j, "n", time.Now())
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			rt := snap.Runtime(tt.runtime)
			if rt == nil {
				t.Fatalf("runtime %q not recorded", tt.runtime)
			}
			if rt.Version != tt.version {
				t.Errorf("runtime %q version = %q, want %q", tt.runtime, rt.Version, tt.version)
			}
		})
	}
}

func TestSnapshotSetupVersionNotConcrete(t *testing.T) {
	tests := []string{">=20", "^20", "20.x", "*", "lts/*", ".nvmrc", ".python-version", "go.mod", "latest", "${{ matrix.node }}"}
	for _, v := range tests {
		t.Run(v, func(t *testing.T) {
			w, j := buildWorkflow(&Job{
				ID:    "build",
				Steps: []Step{{Uses: "actions/setup-node@v4", With: map[string]string{"node-version": v}}},
			})
			snap, err := Snapshot(w, j, "n", time.Now())
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if rt := snap.Runtime("node"); rt != nil {
				t.Errorf("runtime recorded for %q: %+v", v, rt)
			}
			if !containsNote(snap, "node-version") {
				t.Errorf("expected a note explaining node-version %q", v)
			}
		})
	}
}

func TestSnapshotSetupVersionFile(t *testing.T) {
	tests := []struct {
		uses string
		key  string
		file string
	}{
		{"actions/setup-node@v4", "node-version-file", ".nvmrc"},
		{"actions/setup-python@v5", "python-version-file", ".python-version"},
		{"actions/setup-go@v5", "go-version-file", "go.mod"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			w, j := buildWorkflow(&Job{
				ID:    "build",
				Steps: []Step{{Uses: tt.uses, With: map[string]string{tt.key: tt.file}}},
			})
			snap, err := Snapshot(w, j, "n", time.Now())
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if len(snap.Runtimes) != 0 {
				t.Errorf("unexpected runtimes for %s: %+v", tt.key, snap.Runtimes)
			}
			if !containsNote(snap, tt.file) {
				t.Errorf("expected a note naming %q", tt.file)
			}
		})
	}
}

func TestSnapshotDuplicateRuntime(t *testing.T) {
	w, j := buildWorkflow(&Job{
		ID: "build",
		Steps: []Step{
			{Uses: "actions/setup-node@v4", With: map[string]string{"node-version": "20.11.1"}},
			{Uses: "actions/setup-node@v4", With: map[string]string{"node-version": "22.2.0"}},
		},
	})
	snap, err := Snapshot(w, j, "n", time.Now())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	rt := snap.Runtime("node")
	if rt == nil {
		t.Fatal("runtime node not recorded")
	}
	if rt.Version != "20.11.1" {
		t.Errorf("version = %q, want the first declaration 20.11.1", rt.Version)
	}
	if !containsNote(snap, "configured twice") {
		t.Error("expected a note about the duplicate setup action")
	}
}

func TestSnapshotEnvNames(t *testing.T) {
	w, j := buildWorkflow(&Job{
		ID:  "build",
		Env: map[string]string{"JOB_VAR": "1", "SHARED": "x"},
		Steps: []Step{{
			Env: map[string]string{"STEP_VAR": "2", "SHARED": "y"},
		}},
		Services: []Service{{
			ID:  "db",
			Env: map[string]string{"DB_VAR": "3"},
		}},
	})
	snap, err := Snapshot(w, j, "n", time.Now())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Environment == nil {
		t.Fatal("Environment not set")
	}
	want := []string{"DB_VAR", "JOB_VAR", "SHARED", "STEP_VAR"}
	if !reflect.DeepEqual(snap.Environment.Names, want) {
		t.Errorf("Names = %v, want %v", snap.Environment.Names, want)
	}
}

// A job that declares no variables still gets an environment section, marked
// partial. Leaving it absent would let a diff read every local variable as
// missing from CI, which is one noise line per shell variable and hides the
// real findings.
func TestSnapshotEnvEmptyIsStillPartial(t *testing.T) {
	w, j := buildWorkflow(&Job{ID: "build"})
	snap, err := Snapshot(w, j, "n", time.Now())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Environment == nil {
		t.Fatal("Environment is absent; want a present, empty, partial section")
	}
	if len(snap.Environment.Names) != 0 {
		t.Errorf("Names = %v, want empty", snap.Environment.Names)
	}
	if !snap.Environment.Partial {
		t.Error("Partial = false; a workflow never lists the variables the runner adds")
	}
}

// Every CI-derived environment is partial, declared variables or not.
func TestSnapshotEnvIsAlwaysPartial(t *testing.T) {
	w, j := buildWorkflow(&Job{ID: "build", Env: map[string]string{"CI": "true"}})
	snap, err := Snapshot(w, j, "n", time.Now())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !snap.Environment.Partial {
		t.Error("Partial = false for a CI-derived environment")
	}
}

// TestSnapshotNeverStoresEnvValues is the leak test: secret-looking values given
// to job, step, and service env must never appear anywhere in the serialized
// snapshot, while the names must.
func TestSnapshotNeverStoresEnvValues(t *testing.T) {
	secrets := map[string]string{
		"JOB_TOKEN":   "s3cr3t-JOB-t0ken",
		"STEP_TOKEN":  "s3cr3t-STEP-t0ken",
		"DB_PASSWORD": "sup3r-s3cr3t-db-pw",
	}
	w, j := buildWorkflow(&Job{
		ID:  "build",
		Env: map[string]string{"JOB_TOKEN": secrets["JOB_TOKEN"]},
		Steps: []Step{{
			Env: map[string]string{"STEP_TOKEN": secrets["STEP_TOKEN"]},
		}},
		Services: []Service{{
			ID:    "db",
			Image: "postgres:16",
			Env:   map[string]string{"DB_PASSWORD": secrets["DB_PASSWORD"]},
		}},
	})

	snap, err := Snapshot(w, j, "leak check", time.Now())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for name, value := range secrets {
		if !bytes.Contains(data, []byte(name)) {
			t.Errorf("env name %q missing from serialized snapshot", name)
		}
		if bytes.Contains(data, []byte(value)) {
			t.Errorf("env value %q leaked into serialized snapshot", value)
		}
	}
}

func TestSnapshotServicesNoted(t *testing.T) {
	suffix := "; services are not modelled as a snapshot section yet"
	tests := []struct {
		name     string
		services []Service
		want     []string // expected service notes, in order
	}{
		{name: "no services", services: nil, want: nil},
		{name: "service without image", services: []Service{{ID: "redis"}}, want: []string{`job declares service "redis"` + suffix}},
		{name: "one service with image", services: []Service{{ID: "postgres", Image: "postgres:16"}}, want: []string{`job declares service "postgres" (image postgres:16)` + suffix}},
		{
			name: "two services in ID order",
			services: []Service{
				{ID: "postgres", Image: "postgres:16"},
				{ID: "redis", Image: "redis:7"},
			},
			want: []string{
				`job declares service "postgres" (image postgres:16)` + suffix,
				`job declares service "redis" (image redis:7)` + suffix,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, j := buildWorkflow(&Job{ID: "build", Services: tt.services})
			snap, err := Snapshot(w, j, "n", time.Now())
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			var got []string
			for _, n := range snap.Source.Notes {
				if strings.HasPrefix(n, "job declares service") {
					got = append(got, n)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("service notes = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSnapshotSource(t *testing.T) {
	w, j := buildWorkflow(&Job{
		ID:     "build",
		RunsOn: []string{"ubuntu-latest"},
		Notes:  []string{"job note"},
	})
	w.Notes = []string{"workflow note"}

	snap, err := Snapshot(w, j, "build snapshot", time.Now())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Source == nil {
		t.Fatal("Source not set")
	}
	if snap.Source.Kind != snapshot.SourceGitHubActions {
		t.Errorf("Source.Kind = %q, want %q", snap.Source.Kind, snapshot.SourceGitHubActions)
	}
	if snap.Source.Ref != ".github/workflows/ci.yml#build" {
		t.Errorf("Source.Ref = %q, want %q", snap.Source.Ref, ".github/workflows/ci.yml#build")
	}
	// Workflow notes come first, then job notes, then conversion notes.
	joined := strings.Join(snap.Source.Notes, "\n")
	wIdx, jIdx := strings.Index(joined, "workflow note"), strings.Index(joined, "job note")
	if wIdx < 0 {
		t.Error("workflow note missing from Source.Notes")
	}
	if jIdx < 0 {
		t.Error("job note missing from Source.Notes")
	}
	if wIdx > jIdx {
		t.Error("workflow notes should precede job notes")
	}
}

func TestSnapshotNilInputs(t *testing.T) {
	if _, err := Snapshot(nil, &Job{ID: "build"}, "n", time.Now()); err == nil {
		t.Error("expected an error for a nil workflow")
	}
	if _, err := Snapshot(&Workflow{}, nil, "n", time.Now()); err == nil {
		t.Error("expected an error for a nil job")
	}
}
