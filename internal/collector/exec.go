package collector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// DefaultTimeout bounds a single external tool invocation. Version probes
// answer in milliseconds; anything slower is a hung tool (an unreachable
// Docker daemon, a network filesystem) that must not stall a capture.
//
// It is longer on Windows, and that is not a guess. The public runner feed at
// runners.nyrvo.dev captures six GitHub-hosted images every day, and every
// observation it has ever failed to measure was on windows-latest — `rustc
// --version` one day, `docker compose version` the next — while the Linux and
// macOS runners have never missed the deadline once. Five seconds is enough
// time to answer on those platforms and is not on Windows, where spawning a
// process is dearer and a cold image pays for it on the first call.
//
// The consequence of getting this wrong is not a wrong answer — an expired
// probe is recorded as unmeasured, never as absence, per ADR 0017 — but a
// snapshot that keeps saying "I do not know" is worth less than one that waits
// a moment longer and finds out.
//
// A variable rather than a constant so a test can shorten it: what happens when
// a probe runs out of time is now a behaviour worth asserting, and asserting it
// against the real deadline would cost that long on every CI job. The package
// is internal, so nothing outside this module can reach it.
var DefaultTimeout = defaultTimeout(runtime.GOOS)

// defaultTimeout takes the operating system as an argument rather than reading
// runtime.GOOS itself, so both branches can be exercised from a test on any
// platform. A branch that only runs on one operating system is a branch that is
// only tested when CI happens to be on it.
func defaultTimeout(goos string) time.Duration {
	if goos == "windows" {
		return 15 * time.Second
	}
	return 5 * time.Second
}

// Run executes an external tool and returns its trimmed stdout.
//
// Arguments are passed as a vector and never through a shell, so values taken
// from the environment or repository cannot be interpreted as commands. Callers
// get ErrUnavailable when the tool is not installed, which lets an optional
// collector opt out instead of failing the capture.
func Run(ctx context.Context, name string, args ...string) (string, error) {
	out, _, err := RunOutput(ctx, name, args...)
	return out, err
}

// RunOutput is Run with the tool's stderr as well.
//
// It exists for one honest reason: a few tools answer on stderr. `java
// -version` is the case that forced it — JDK 8 has no --version, so a machine
// with Java 8 was being reported as having no Java at all. Callers must ask for
// stderr deliberately, per tool; a general "use stderr when stdout is empty"
// rule would read any tool's warning as a version.
func RunOutput(ctx context.Context, name string, args ...string) (stdout, stderr string, err error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	// Tools localize their output; a fixed locale keeps parsing deterministic
	// across developer machines and CI images.
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")

	runErr := cmd.Run()
	stdout = strings.TrimSpace(outBuf.String())
	stderr = strings.TrimSpace(errBuf.String())
	if runErr == nil {
		return stdout, stderr, nil
	}
	if errors.Is(runErr, exec.ErrNotFound) {
		return "", "", fmt.Errorf("%s not found: %w", name, ErrUnavailable)
	}
	// A cancelled or timed-out context is a real failure for the caller to
	// report, not a reason to pretend the tool is absent.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), ctxErr)
	}
	msg := stderr
	if msg == "" {
		msg = stdout
	}
	if msg != "" {
		return "", "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), runErr, firstLine(msg))
	}
	return "", "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), runErr)
}

// IsTimeout reports whether a tool was still running when its deadline expired.
//
// The distinction matters more than it looks. A tool that is absent and a tool
// that was too slow both leave a collector with no version to record, and
// recording either as "not installed" states something Nyrvo never observed. A
// cold Windows runner needs longer than DefaultTimeout to answer `npm
// --version`, and the machine plainly has npm.
//
// Callers must rule out their own cancellation first: when the caller's context
// is already done, the inner deadline is a symptom of that and not a slow tool.
func IsTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

// LookPath reports where a tool lives, or ErrUnavailable when it is not
// installed.
func LookPath(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found: %w", name, ErrUnavailable)
	}
	return path, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
