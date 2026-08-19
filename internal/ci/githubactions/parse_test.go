package githubactions

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseFile(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		jobs  int
		check func(t *testing.T, w *Workflow)
	}{
		{
			name: "setup-node",
			file: "setup-node.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				wantString(t, w.Name, "Node CI", "workflow name")
				j := jobByName(t, w, "test")
				wantString(t, j.Name, "Run node tests", "job name")
				wantStrings(t, j.RunsOn, []string{"ubuntu-latest"}, "runs-on")
				if len(j.Steps) != 2 {
					t.Fatalf("steps = %d, want 2", len(j.Steps))
				}
				wantString(t, j.Steps[1].Uses, "actions/setup-node@v4", "step uses")
				wantString(t, j.Steps[1].With["node-version"], "22", "node-version")
				if !hasNote(w.Notes, "on triggers") {
					t.Errorf("expected a note about on triggers, got %v", w.Notes)
				}
			},
		},
		{
			name: "setup-python",
			file: "setup-python.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				j := jobByName(t, w, "tests")
				wantStrings(t, j.RunsOn, []string{"ubuntu-latest", "windows-latest"}, "runs-on")
				if len(j.Steps) != 2 {
					t.Fatalf("steps = %d, want 2", len(j.Steps))
				}
				wantString(t, j.Steps[1].Uses, "actions/setup-python@v5", "step uses")
				wantString(t, j.Steps[1].With["python-version"], "3.12", "python-version")
			},
		},
		{
			name: "setup-go",
			file: "setup-go.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				j := jobByName(t, w, "build")
				wantString(t, j.Steps[0].Uses, "actions/setup-go@v5", "step uses")
				wantString(t, j.Steps[0].With["go-version"], "1.25", "go-version")
				wantString(t, j.Steps[1].Run, "go test ./...", "step run")
			},
		},
		{
			name: "services-postgres",
			file: "services-postgres.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				j := jobByName(t, w, "db")
				if len(j.Services) != 1 {
					t.Fatalf("services = %d, want 1", len(j.Services))
				}
				s := j.Services[0]
				wantString(t, s.ID, "postgres", "service id")
				wantString(t, s.Image, "postgres:16", "service image")
				wantString(t, s.Env["POSTGRES_USER"], "test", "service env POSTGRES_USER")
				wantString(t, s.Env["POSTGRES_PASSWORD"], "test", "service env POSTGRES_PASSWORD")
				wantString(t, s.Env["POSTGRES_DB"], "test", "service env POSTGRES_DB")
			},
		},
		{
			name: "services-redis",
			file: "services-redis.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				j := jobByName(t, w, "cache")
				if len(j.Services) != 1 {
					t.Fatalf("services = %d, want 1", len(j.Services))
				}
				s := j.Services[0]
				wantString(t, s.ID, "redis", "service id")
				wantString(t, s.Image, "redis:7", "service image")
				wantString(t, s.Env["REDIS_PORT"], "6379", "service env REDIS_PORT")
			},
		},
		{
			name: "job-container",
			file: "job-container.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				j := jobByName(t, w, "containerized")
				wantString(t, j.Container, "node:24", "container")
			},
		},
		{
			name: "container-object",
			file: "container-object.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				j := jobByName(t, w, "test")
				wantString(t, j.Container, "node:24", "container")
			},
		},
		{
			name: "job-env",
			file: "job-env.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				j := jobByName(t, w, "test")
				wantString(t, j.Env["NODE_ENV"], "test", "env NODE_ENV")
				wantString(t, j.Env["CI"], "true", "env CI")
				wantString(t, j.Env["NODE_OPTIONS"], "--max-old-space-size=4096", "env NODE_OPTIONS")
			},
		},
		{
			name: "step-env",
			file: "step-env.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				j := jobByName(t, w, "test")
				wantString(t, j.Steps[0].Env["FOO"], "bar", "step env FOO")
				wantString(t, j.Steps[0].Env["BAZ"], "qux", "step env BAZ")
			},
		},
		{
			name: "working-directory",
			file: "working-directory.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				j := jobByName(t, w, "test")
				wantString(t, j.Steps[0].Name, "Run in subdirectory", "step name")
				wantString(t, j.Steps[0].WorkingDirectory, "./src", "step working-directory")
				wantString(t, j.Steps[0].Run, "npm test", "step run")
			},
		},
		{
			name: "matrix-simple",
			file: "matrix-simple.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				j := jobByName(t, w, "test")
				wantStrings(t, j.RunsOn, []string{"${{ matrix.os }}"}, "runs-on expression kept verbatim")
				wantStrings(t, j.Matrix["os"], []string{"ubuntu-latest", "macos-latest"}, "matrix os")
				wantStrings(t, j.Matrix["node"], []string{"20", "22"}, "matrix node")
			},
		},
		{
			name: "matrix-include",
			file: "matrix-include.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				j := jobByName(t, w, "test")
				if len(j.Matrix) != 0 {
					t.Errorf("matrix = %v, want empty", j.Matrix)
				}
				if !hasNote(j.Notes, "include") {
					t.Errorf("expected a note about matrix include, got %v", j.Notes)
				}
			},
		},
		{
			name: "multiple-jobs",
			file: "multiple-jobs.yml",
			jobs: 3,
			check: func(t *testing.T, w *Workflow) {
				wantString(t, w.Name, "CI", "workflow name")
				ids := make([]string, 0, len(w.Jobs))
				for _, j := range w.Jobs {
					ids = append(ids, j.ID)
				}
				wantStrings(t, ids, []string{"deploy", "lint", "test"}, "job order")
				d := jobByName(t, w, "deploy")
				if !hasNote(d.Notes, "if conditions") {
					t.Errorf("expected a note about job if, got %v", d.Notes)
				}
			},
		},
		{
			name: "unsupported-action",
			file: "unsupported-action.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				j := jobByName(t, w, "test")
				if !hasNote(j.Notes, "if conditions") {
					t.Errorf("expected a note about job if, got %v", j.Notes)
				}
				if len(j.Steps) != 3 {
					t.Fatalf("steps = %d, want 3", len(j.Steps))
				}
				wantString(t, j.Steps[0].Uses, "actions/upload-artifact@v4", "step uses")
				wantString(t, j.Steps[2].Uses, "docker://alpine:3.20", "step uses")
				if !hasNote(j.Notes, `step "Conditional"`) {
					t.Errorf("expected a note about the step if, got %v", j.Notes)
				}
			},
		},
		{
			name: "no-runtime",
			file: "no-runtime.yml",
			jobs: 1,
			check: func(t *testing.T, w *Workflow) {
				j := jobByName(t, w, "test")
				wantString(t, j.Steps[0].Name, "Say hello", "step name")
				wantString(t, j.Steps[0].Run, `echo "no runtime here"`, "step run")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join("testdata", "github-actions", tt.file)
			w, err := ParseFile(path)
			if err != nil {
				t.Fatalf("ParseFile(%s): %v", tt.file, err)
			}
			if got := len(w.Jobs); got != tt.jobs {
				t.Fatalf("ParseFile(%s): jobs = %d, want %d", tt.file, got, tt.jobs)
			}
			if w.Path != path {
				t.Errorf("Path = %q, want %q", w.Path, path)
			}
			if tt.check != nil {
				tt.check(t, w)
			}
		})
	}
}

func TestParseFileNoJobs(t *testing.T) {
	w, err := ParseFile(filepath.Join("testdata", "github-actions", "no-jobs.yml"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(w.Jobs) != 0 {
		t.Errorf("jobs = %d, want 0", len(w.Jobs))
	}
}

func TestParseFileInvalid(t *testing.T) {
	path := filepath.Join("testdata", "github-actions", "invalid.yml")
	_, err := ParseFile(path)
	if err == nil {
		t.Fatal("expected an error for invalid.yml")
	}
	if !strings.Contains(err.Error(), "invalid.yml") {
		t.Errorf("error should name the file, got %v", err)
	}
}

func TestParseDir(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "b.yaml"), "name: B\njobs: {}\n")
	writeFixture(t, filepath.Join(dir, "a.yml"), "name: A\njobs: {}\n")
	writeFixture(t, filepath.Join(dir, "skip.txt"), "not a workflow")

	workflows, err := ParseDir(dir)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(workflows) != 2 {
		t.Fatalf("workflows = %d, want 2", len(workflows))
	}
	got := []string{workflows[0].Path, workflows[1].Path}
	want := []string{filepath.Join(dir, "a.yml"), filepath.Join(dir, "b.yaml")}
	wantStrings(t, got, want, "paths")
}

func TestParseDirMissingDir(t *testing.T) {
	workflows, err := ParseDir(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("ParseDir on missing dir: %v", err)
	}
	if len(workflows) != 0 {
		t.Errorf("workflows = %d, want 0", len(workflows))
	}
}

func TestParseDirInvalidFile(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "good.yml"), "name: ok\njobs: {}\n")
	writeFixture(t, filepath.Join(dir, "bad.yml"), "jobs: [\n")

	_, err := ParseDir(dir)
	if err == nil {
		t.Fatal("expected an error for the invalid file")
	}
	if !strings.Contains(err.Error(), "bad.yml") {
		t.Errorf("error should name the bad file, got %v", err)
	}
}

func TestParseNotesNonScalarEnvAndWith(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested.yml")
	writeFixture(t, path, `
name: nested
jobs:
  test:
    runs-on: ubuntu-latest
    env:
      NODE_ENV: test
      COMPLEX:
        nested: true
    steps:
      - name: Setup
        uses: actions/setup-node@v4
        with:
          node-version: "20"
          cache-dependency-path:
            - package-lock.json
        env:
          TOKEN: x
          NESTED:
            inner: 1
`)
	w, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	j := jobByName(t, w, "test")
	if j.Env["NODE_ENV"] != "test" {
		t.Errorf("scalar env was dropped: %v", j.Env)
	}
	if _, ok := j.Env["COMPLEX"]; ok {
		t.Errorf("non-scalar env was kept: %v", j.Env)
	}
	if !hasNote(j.Notes, "env has a non-scalar entry") {
		t.Errorf("expected a note about non-scalar env, got %v", j.Notes)
	}
	if j.Steps[0].With["node-version"] != "20" {
		t.Errorf("scalar with was dropped: %v", j.Steps[0].With)
	}
	if !hasNote(j.Notes, "with has a non-scalar entry") {
		t.Errorf("expected a note about non-scalar with, got %v", j.Notes)
	}
	if !hasNote(j.Notes, "step \"Setup\" env has a non-scalar entry") {
		t.Errorf("expected a note about non-scalar step env, got %v", j.Notes)
	}
}

func jobByName(t *testing.T, w *Workflow, id string) *Job {
	t.Helper()
	j := w.Job(id)
	if j == nil {
		t.Fatalf("job %q not found", id)
	}
	return j
}

func wantString(t *testing.T, got, want, what string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

func wantStrings(t *testing.T, got, want []string, what string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func hasNote(notes []string, substr string) bool {
	for _, n := range notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
