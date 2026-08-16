package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run executes a command line in an isolated working directory so snapshots
// never touch the developer's own .nyrvo directory.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestSplitFlags(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantFlags    []string
		wantOperands []string
	}{
		{"flags last", []string{"local", "ci", "--json"}, []string{"--json"}, []string{"local", "ci"}},
		{"flags first", []string{"--json", "local", "ci"}, []string{"--json"}, []string{"local", "ci"}},
		{"flags interleaved", []string{"local", "--json", "ci"}, []string{"--json"}, []string{"local", "ci"}},
		{"no flags", []string{"local", "ci"}, nil, []string{"local", "ci"}},
		{"double dash protects a dashed name", []string{"--", "-weird"}, nil, []string{"-weird"}},
		{"lone dash is an operand", []string{"-"}, nil, []string{"-"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, operands := splitFlags(tt.args)
			if strings.Join(flags, " ") != strings.Join(tt.wantFlags, " ") {
				t.Errorf("flags = %v, want %v", flags, tt.wantFlags)
			}
			if strings.Join(operands, " ") != strings.Join(tt.wantOperands, " ") {
				t.Errorf("operands = %v, want %v", operands, tt.wantOperands)
			}
		})
	}
}

// The documented workflow — capture two environments, then compare them — is
// the contract this milestone exists to deliver.
func TestCaptureDiffRoundTrip(t *testing.T) {
	t.Chdir(t.TempDir())

	if code, _, errOut := run(t, "capture", "local"); code != ExitOK {
		t.Fatalf("capture local: exit %d, stderr: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(".nyrvo", "snapshots", "local.json")); err != nil {
		t.Fatalf("snapshot file not written: %v", err)
	}

	if code, _, errOut := run(t, "capture", "other"); code != ExitOK {
		t.Fatalf("capture other: exit %d, stderr: %s", code, errOut)
	}

	// Two captures of the same machine seconds apart must not report drift:
	// this is the guarantee that makes a reported difference trustworthy.
	code, stdout, errOut := run(t, "diff", "local", "other")
	if code != ExitOK {
		t.Fatalf("diff: exit %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(stdout, "No differences") {
		t.Errorf("two captures of one machine differ:\n%s", stdout)
	}
}

// Flags must work where the documentation puts them: after the operands.
func TestDiffJSONFlagAfterOperands(t *testing.T) {
	t.Chdir(t.TempDir())
	mustCapture(t, "local")
	mustCapture(t, "other")

	code, stdout, errOut := run(t, "diff", "local", "other", "--json")
	if code != ExitOK {
		t.Fatalf("diff --json: exit %d, stderr: %s", code, errOut)
	}
	var payload struct {
		SchemaVersion int    `json:"schema_version"`
		A             string `json:"a"`
		B             string `json:"b"`
		Differences   []any  `json:"differences"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout)
	}
	if payload.A != "local" || payload.B != "other" || payload.SchemaVersion == 0 {
		t.Errorf("unexpected payload: %+v", payload)
	}
}

// With --json, stdout must carry nothing but the document, so it can be piped
// straight into another tool.
func TestCaptureJSONStdoutIsPureJSON(t *testing.T) {
	t.Chdir(t.TempDir())

	code, stdout, stderr := run(t, "capture", "local", "--json")
	if code != ExitOK {
		t.Fatalf("capture --json: exit %d, stderr: %s", code, stderr)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(stdout), &snap); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout)
	}
	if snap["name"] != "local" {
		t.Errorf("snapshot name = %v, want local", snap["name"])
	}
	// Progress belongs on stderr when stdout is a data stream.
	if !strings.Contains(stderr, "Capturing environment") {
		t.Errorf("collector status missing from stderr: %q", stderr)
	}
}

func TestListReportsCapturedNames(t *testing.T) {
	t.Chdir(t.TempDir())

	if _, stdout, _ := run(t, "list"); !strings.Contains(stdout, "No snapshots yet") {
		t.Errorf("empty store output = %q", stdout)
	}
	mustCapture(t, "beta")
	mustCapture(t, "alpha")

	code, stdout, errOut := run(t, "list")
	if code != ExitOK {
		t.Fatalf("list: exit %d, stderr: %s", code, errOut)
	}
	if got := strings.Fields(stdout); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("list = %q, want sorted alpha beta", stdout)
	}
}

func TestExitCodes(t *testing.T) {
	t.Chdir(t.TempDir())
	mustCapture(t, "local")

	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no arguments", nil, ExitUsage},
		{"unknown command", []string{"explode"}, ExitUsage},
		{"capture without a name", []string{"capture"}, ExitUsage},
		{"capture with two names", []string{"capture", "a", "b"}, ExitUsage},
		{"diff with one name", []string{"diff", "local"}, ExitUsage},
		{"list with arguments", []string{"list", "extra"}, ExitUsage},
		{"unknown flag", []string{"diff", "local", "local", "--nope"}, ExitUsage},
		{"missing snapshot", []string{"diff", "local", "absent"}, ExitError},
		{"invalid snapshot name", []string{"capture", "../escape"}, ExitError},
		{"help", []string{"help"}, ExitOK},
		{"version", []string{"version"}, ExitOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _, _ := run(t, tt.args...); code != tt.want {
				t.Errorf("exit = %d, want %d", code, tt.want)
			}
		})
	}
}

// A snapshot name is used to build a file path, so a traversal attempt must be
// rejected rather than sanitized, and must leave nothing behind.
func TestCaptureRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if code, _, _ := run(t, "capture", "../escape"); code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "escape.json")); err == nil {
		t.Fatal("capture wrote a file outside the snapshot directory")
	}
}

func mustCapture(t *testing.T, name string) {
	t.Helper()
	if code, _, errOut := run(t, "capture", name); code != ExitOK {
		t.Fatalf("capture %s: exit %d, stderr: %s", name, code, errOut)
	}
}
