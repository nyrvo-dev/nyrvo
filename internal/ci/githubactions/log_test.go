package githubactions

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// envPairRe matches one entry of a step's env: block — two-space indent,
// uppercase key, ": ", value. This is the configuration echo the failure
// excerpt must never quote.
var envPairRe = regexp.MustCompile(`^  ([A-Z0-9_]+): (.*)$`)

// logTimestampRe matches a line that still begins with a per-line timestamp,
// which no stored field may do.
var logTimestampRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`)

func readFixtureLog(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "logs", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

// TestJobLogLeaksNoEnvironmentValues is the security test, and it comes first
// on purpose. Before running a step the runner echoes the step's configuration,
// including its env: block with real values, and the naive "keep the last
// twenty lines before the error" approach captures that block exactly. The
// fixture log-failure.txt genuinely contains such values (GH_AW_GITHUB_ACTOR:
// sergiou87 among them), so this test proves the excerpt rule keeps every field
// of the parsed log free of each of them.
func TestJobLogLeaksNoEnvironmentValues(t *testing.T) {
	raw := readFixtureLog(t, "log-failure.txt")
	jl := ParseJobLog(raw)
	joined := joinedJobLog(jl)

	for _, pair := range fixtureEnvPairs(t, raw) {
		if strings.Contains(joined, pair.key) {
			t.Errorf("environment key %q leaked into the job log", pair.key)
		}
		if pair.value != "" && strings.Contains(joined, pair.value) {
			t.Errorf("environment value %q leaked into the job log", pair.value)
		}
	}
	if strings.Contains(joined, "GH_AW_GITHUB_ACTOR") || strings.Contains(joined, "sergiou87") {
		t.Error("step configuration env values leaked into the job log")
	}
}

// The same failing fixture must still surface the real error: the failing
// command's message and the exit-code line that followed it.
func TestJobLogFailureExcerpt(t *testing.T) {
	jl := ParseJobLog(readFixtureLog(t, "log-failure.txt"))

	joined := joinedJobLog(jl)
	if !strings.Contains(joined, "Process completed with exit code 127") {
		t.Errorf("missing the exit code error:\n%s", joined)
	}
	if !strings.Contains(joined, "No such file or directory") {
		t.Errorf("missing the failing command's error line:\n%s", joined)
	}

	// The excerpt is the one output line that preceded the error, plus the
	// error itself — nothing from the configuration block.
	if len(jl.FailureLines) != 2 {
		t.Fatalf("FailureLines = %d lines, want 2: %+v", len(jl.FailureLines), jl.FailureLines)
	}
	if !strings.Contains(jl.FailureLines[0], "No such file or directory") {
		t.Errorf("FailureLines[0] = %q, want the failing command's message", jl.FailureLines[0])
	}
	if jl.FailureLines[1] != "Process completed with exit code 127." {
		t.Errorf("FailureLines[1] = %q", jl.FailureLines[1])
	}
	if !strings.EqualFold(jl.Errors[0], "Process completed with exit code 127.") {
		t.Errorf("Errors = %v", jl.Errors)
	}
}

// ANSI escape sequences are terminal directives, not content: no field of a
// parsed log may contain an escape byte, for any fixture.
func TestJobLogNoAnsi(t *testing.T) {
	for _, name := range []string{"log-failure.txt", "log-go.txt", "log-node.txt", "log-python.txt"} {
		jl := ParseJobLog(readFixtureLog(t, name))
		if strings.Contains(joinedJobLog(jl), "\x1b") {
			t.Errorf("%s: an ANSI escape byte survived into the result", name)
		}
	}
}

// Every stored field is content, not the runner's transcript framing: none may
// start with a timestamp.
func TestJobLogNoTimestamps(t *testing.T) {
	for _, name := range []string{"log-failure.txt", "log-go.txt", "log-node.txt", "log-python.txt"} {
		jl := ParseJobLog(readFixtureLog(t, name))
		for _, field := range strings.Split(joinedJobLog(jl), "\n") {
			if strings.HasPrefix(field, "2026-") || logTimestampRe.MatchString(field) {
				t.Errorf("%s: a timestamp prefix survived into a field: %q", name, field)
			}
		}
	}
}

func TestJobLogRuntimes(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		wantName string
		wantVer  string
	}{
		{"go", "log-go.txt", "go", "1.26.6"},
		{"node", "log-node.txt", "node", "24.19.0"},
		{"python", "log-python.txt", "python", "3.10.20"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jl := ParseJobLog(readFixtureLog(t, tt.fixture))
			if len(jl.Runtimes) != 1 {
				t.Fatalf("runtimes = %+v, want exactly one", jl.Runtimes)
			}
			if jl.Runtimes[0].Name != tt.wantName || jl.Runtimes[0].Version != tt.wantVer {
				t.Errorf("runtime = %+v, want %s %s", jl.Runtimes[0], tt.wantName, tt.wantVer)
			}
		})
	}
}

// log-go is a standard hosted runner, which writes its header as separate
// "Operating System" and "Runner Image" groups and states no architecture at
// all. The architecture therefore stays empty rather than being assumed from
// the distribution.
func TestJobLogGoRunnerVersion(t *testing.T) {
	jl := ParseJobLog(readFixtureLog(t, "log-go.txt"))
	if jl.RunnerVersion != "2.336.0" {
		t.Errorf("runner version = %q, want 2.336.0", jl.RunnerVersion)
	}
	want := Image{OS: "linux", Name: "ubuntu-24.04", Version: "20260810.271.1"}
	if jl.Image != want {
		t.Errorf("image = %+v, want %+v", jl.Image, want)
	}
}

// log-failure is a container job, whose runner writes a single "VM Image" group
// carrying the architecture as well. Both header formats are real and both are
// read.
func TestJobLogImage(t *testing.T) {
	jl := ParseJobLog(readFixtureLog(t, "log-failure.txt"))
	want := Image{OS: "linux", Arch: "amd64", Name: "ubuntu:24.04", Version: "20260728.2.1"}
	if jl.Image != want {
		t.Errorf("image = %+v, want %+v", jl.Image, want)
	}
}

// A log the parser cannot learn anything from must yield an empty result, not
// an error or a panic: a job whose log is unreadable is not a failed import.
func TestJobLogEmptyInputs(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{"empty", []byte{}},
		{"only bom", []byte{0xEF, 0xBB, 0xBF}},
		{"no groups", []byte("just a line\nwithout any markers\n")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jl := ParseJobLog(tt.raw)
			if len(jl.Runtimes) != 0 || len(jl.Errors) != 0 || len(jl.FailureLines) != 0 || jl.RunnerVersion != "" {
				t.Errorf("expected an empty result, got %+v", jl)
			}
		})
	}
}

// A runaway output line must be cut before it can become a snapshot: 500 runes
// plus the ellipsis that marks the cut.
func TestJobLogLineTruncation(t *testing.T) {
	raw := []byte("2026-08-16T00:00:00.0000000Z " + strings.Repeat("x", 5000) + "\n" +
		"2026-08-16T00:00:00.0000000Z ##[error]boom\n")
	jl := ParseJobLog(raw)
	if len(jl.FailureLines) != 2 {
		t.Fatalf("FailureLines = %d lines, want 2 (excerpt + error): %+v", len(jl.FailureLines), jl.FailureLines)
	}
	if got := utf8.RuneCountInString(jl.FailureLines[0]); got != 501 {
		t.Errorf("truncated line = %d runes, want 501", got)
	}
	if !strings.HasSuffix(jl.FailureLines[0], "…") {
		t.Errorf("truncated line does not end with the ellipsis: %q", jl.FailureLines[0][:20])
	}
	if jl.FailureLines[1] != "boom" {
		t.Errorf("error line = %q, want boom", jl.FailureLines[1])
	}
}

// A runaway error line must be cut too. TestJobLogLineTruncation only walks the
// excerpt path, so without this test removing the truncation from the
// ##[error] path would leave the suite green.
func TestJobLogErrorLineTruncation(t *testing.T) {
	raw := []byte("2026-08-16T00:00:00.0000000Z ##[error]" + strings.Repeat("x", 5000) + "\n")
	jl := ParseJobLog(raw)
	if len(jl.Errors) != 1 {
		t.Fatalf("Errors = %d lines, want 1", len(jl.Errors))
	}
	if got := utf8.RuneCountInString(jl.Errors[0]); got != 501 {
		t.Errorf("error line = %d runes, want 501", got)
	}
	if !strings.HasSuffix(jl.Errors[0], "…") {
		t.Errorf("error line does not end with the ellipsis: %q", jl.Errors[0][:20])
	}
}

// The failure excerpt must be bounded end to end: excerptLimit before the
// error and errorLimit error lines, whatever the log contains. A step that
// prints one error per output line must not turn a log into a snapshot.
func TestJobLogErrorBounded(t *testing.T) {
	var b strings.Builder
	for i := 0; i < errorLimit+1000; i++ {
		fmt.Fprintf(&b, "##[error]err %d\n", i)
	}
	jl := ParseJobLog([]byte(b.String()))
	if len(jl.Errors) != errorLimit {
		t.Errorf("Errors = %d, want %d", len(jl.Errors), errorLimit)
	}
	if len(jl.FailureLines) != errorLimit {
		t.Errorf("FailureLines = %d, want %d", len(jl.FailureLines), errorLimit)
	}
	if jl.DroppedErrors != 1000 {
		t.Errorf("DroppedErrors = %d, want 1000", jl.DroppedErrors)
	}
}

// The go fallback line yields the version when no setup line exists.
func TestJobLogGoFallback(t *testing.T) {
	raw := []byte("2026-08-16T00:00:00.0000000Z go version go1.26.6 linux/amd64\n")
	jl := ParseJobLog(raw)
	if len(jl.Runtimes) != 1 || jl.Runtimes[0].Name != "go" || jl.Runtimes[0].Version != "1.26.6" {
		t.Errorf("runtimes = %+v, want go 1.26.6 from the fallback line", jl.Runtimes)
	}
}

// The setup line is authoritative over the fallback, whatever their order in
// the log.
func TestJobLogGoPrimaryWins(t *testing.T) {
	raw := []byte("2026-08-16T00:00:00.0000000Z go version go1.26.6 linux/amd64\n" +
		"2026-08-16T00:00:00.0000000Z Successfully set up Go version 1.26.9\n")
	jl := ParseJobLog(raw)
	if len(jl.Runtimes) != 1 || jl.Runtimes[0].Version != "1.26.9" {
		t.Errorf("runtimes = %+v, want go 1.26.9 from the setup line", jl.Runtimes)
	}
}

// A version that is not dotted digits is skipped, never guessed.
func TestJobLogSkipsNonDottedVersion(t *testing.T) {
	raw := []byte("2026-08-16T00:00:00.0000000Z Successfully set up Go version v1.26\n")
	jl := ParseJobLog(raw)
	if len(jl.Runtimes) != 0 {
		t.Errorf("runtimes = %+v, want none for a non-dotted version", jl.Runtimes)
	}
}

func TestJobLogExtraRuntimes(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		version string
	}{
		{"java setup line", "2026-08-16T00:00:00.0000000Z Installed Java version: 17.0.9.\n", "java", "17.0.9"},
		{"java env details", "2026-08-16T00:00:00.0000000Z ##[group]Environment details\n2026-08-16T00:00:00.0000000Z java: 11.0.22\n2026-08-16T00:00:00.0000000Z ##[endgroup]\n", "java", "11.0.22"},
		{"ruby setup line", "2026-08-16T00:00:00.0000000Z Using Ruby version: 3.2.2\n", "ruby", "3.2.2"},
		{"ruby env details", "2026-08-16T00:00:00.0000000Z ##[group]Environment details\n2026-08-16T00:00:00.0000000Z ruby: 3.3.0\n2026-08-16T00:00:00.0000000Z ##[endgroup]\n", "ruby", "3.3.0"},
		{"php version line", "2026-08-16T00:00:00.0000000Z PHP 8.2.12 (cli)\n", "php", "8.2.12"},
		{"dotnet sdk", "2026-08-16T00:00:00.0000000Z Installed .NET SDK version 8.0.100\n", "dotnet", "8.0.100"},
		{"pnpm", "2026-08-16T00:00:00.0000000Z Installed pnpm version 9.1.0\n", "pnpm", "9.1.0"},
		{"rust channel", "2026-08-16T00:00:00.0000000Z info: syncing channel updates for '1.78.0-x86_64-unknown-linux-gnu'\n", "rust", "1.78.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jl := ParseJobLog([]byte(tt.raw))
			if len(jl.Runtimes) != 1 || jl.Runtimes[0].Name != tt.want || jl.Runtimes[0].Version != tt.version {
				t.Errorf("runtimes = %+v, want %s %s", jl.Runtimes, tt.want, tt.version)
			}
		})
	}
}

// fixtureEnvPairs reads the env: block of a recorded log — the exact strings
// the leak test must keep out of the parsed result.
func fixtureEnvPairs(t *testing.T, raw []byte) []struct{ key, value string } {
	t.Helper()
	var pairs []struct{ key, value string }
	for _, line := range strings.Split(string(raw), "\n") {
		line = timestampPrefix.ReplaceAllString(line, "")
		if m := envPairRe.FindStringSubmatch(line); m != nil {
			pairs = append(pairs, struct{ key, value string }{m[1], m[2]})
		}
	}
	if len(pairs) == 0 {
		t.Fatal("no env pairs extracted from the fixture; the leak test cannot run")
	}
	return pairs
}

// joinedJobLog concatenates every field of a parsed log, so an assertion on
// the joined string covers the whole result.
func joinedJobLog(jl *JobLog) string {
	parts := []string{jl.RunnerVersion, jl.Image.OS, jl.Image.Arch, jl.Image.Name, jl.Image.Version}
	for _, r := range jl.Runtimes {
		parts = append(parts, r.Name, r.Version)
	}
	parts = append(parts, jl.Errors...)
	parts = append(parts, jl.FailureLines...)
	return strings.Join(parts, "\n")
}

// hasControlByte reports whether s carries any C0 control character other than
// tab. Nothing that enters a snapshot may: a control byte is a terminal
// directive, not content.
func hasControlByte(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 && s[i] != '\t' {
			return true
		}
	}
	return false
}
