package collector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess re-executes the current test binary as a stand-in for an
// external tool. It is the standard subprocess pattern from the Go docs and
// keeps the command tests portable: no reliance on bash builtins, `/bin/echo`,
// or any tool beyond the binary `go test` is already running.
//
// The helper is only active when os.Args contains the "--" separator, which the
// tests below insert themselves. A normal `go test` run has no such argument,
// so the test just returns and passes.
func TestHelperProcess(t *testing.T) {
	sep := -1
	for i, a := range os.Args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 {
		return
	}
	args := os.Args[sep+1:]
	switch {
	case len(args) == 0:
		// No mode at all exercises the empty-stderr failure path.
		_, _ = fmt.Fprintln(os.Stderr, "boom on stderr")
		os.Exit(3)
	case args[0] == "echo":
		// Echo the remaining arguments joined by spaces, mirroring how a
		// shell would print them verbatim.
		_, _ = fmt.Fprintln(os.Stdout, strings.Join(args[1:], " "))
		os.Exit(0)
	case args[0] == "fail":
		// Print one line to stderr and exit non-zero.
		_, _ = fmt.Fprintln(os.Stderr, args[1])
		os.Exit(3)
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown helper mode: %s\n", args[0])
		os.Exit(2)
	}
}

// helperProcessArgs builds the argument vector that re-enters the test binary
// in TestHelperProcess mode. Run appends these after the binary name.
func helperProcessArgs(t *testing.T, mode string, args ...string) []string {
	t.Helper()
	out := []string{"-test.run=TestHelperProcess", "--", mode}
	return append(out, args...)
}

func TestRunReturnsTrimmedStdout(t *testing.T) {
	// Padding matters: a tool that left stray newlines or spaces would make
	// snapshot values non-deterministic, so Run must trim before returning.
	got, err := Run(t.Context(), os.Args[0], helperProcessArgs(t, "echo", "  padded  ")...)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := "padded"; got != want {
		t.Errorf("Run trimmed output = %q, want %q", got, want)
	}
}

func TestRunRealToolGoEnv(t *testing.T) {
	// A real-world success: `go` is guaranteed present while `go test` runs.
	got, err := Run(t.Context(), "go", "env", "GOOS")
	if err != nil {
		t.Fatalf("Run go env GOOS: %v", err)
	}
	if got == "" {
		t.Error("Run go env GOOS returned empty output")
	}
}

func TestRunMissingBinaryIsUnavailable(t *testing.T) {
	_, err := Run(t.Context(), "nyrvo-no-such-binary-9f3k2z")
	if err == nil {
		t.Fatal("Run on a missing binary returned nil error")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("Run on a missing binary = %v, want it to wrap ErrUnavailable", err)
	}
}

func TestRunNonZeroExitIncludesStderr(t *testing.T) {
	// A tool that exists but fails is evidence for the report, not an excuse
	// to pretend it is absent; the error must carry the first stderr line.
	_, err := Run(t.Context(), os.Args[0], helperProcessArgs(t, "fail", "boom on stderr")...)
	if err == nil {
		t.Fatal("Run on a failing command returned nil error")
	}
	if errors.Is(err, ErrUnavailable) {
		t.Errorf("Run failing command = %v, must not be ErrUnavailable", err)
	}
	if !strings.Contains(err.Error(), "boom on stderr") {
		t.Errorf("Run failing command error %q does not include first stderr line", err)
	}
}

// TestRunCancelledContext verifies that Run honours an already-cancelled
// context and returns promptly. A watchdog bounds the wait so a hang fails the
// test instead of blocking it forever.
func TestRunCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := Run(ctx, os.Args[0], helperProcessArgs(t, "echo", "never runs")...)
		done <- result{out: out, err: err}
	}()

	var res result
	select {
	case res = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run hung on an already-cancelled context")
	}
	if !errors.Is(res.err, context.Canceled) {
		t.Errorf("Run cancelled context = %v, want it to wrap context.Canceled", res.err)
	}
}

// TestRunDoesNotInterpretShellMetacharacters proves arguments are passed as a
// literal vector. If a shell were ever involved, `; rm -rf x` would be parsed
// as a command separator and the helper would misbehave; instead it must come
// back byte-for-byte as a single argument.
func TestRunDoesNotInterpretShellMetacharacters(t *testing.T) {
	got, err := Run(t.Context(), os.Args[0], helperProcessArgs(t, "echo", "hello", "; rm -rf x")...)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := "hello ; rm -rf x"; got != want {
		t.Errorf("Run metacharacter passthrough = %q, want %q", got, want)
	}
}

func TestLookPath(t *testing.T) {
	path, err := LookPath("go")
	if err != nil {
		t.Fatalf("LookPath(go): %v", err)
	}
	if path == "" {
		t.Error("LookPath(go) returned an empty path")
	}

	if _, err := LookPath("nyrvo-no-such-binary-9f3k2z"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("LookPath(missing) = %v, want it to wrap ErrUnavailable", err)
	}
}
