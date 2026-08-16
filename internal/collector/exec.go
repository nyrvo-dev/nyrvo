package collector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout bounds a single external tool invocation. Version probes
// answer in milliseconds; anything slower is a hung tool (an unreachable
// Docker daemon, a network filesystem) that must not stall a capture.
const DefaultTimeout = 5 * time.Second

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
