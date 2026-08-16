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
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Tools localize their output; a fixed locale keeps parsing deterministic
	// across developer machines and CI images.
	cmd.Env = append(cmd.Environ(), "LC_ALL=C")

	err := cmd.Run()
	if err == nil {
		return strings.TrimSpace(stdout.String()), nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("%s not found: %w", name, ErrUnavailable)
	}
	// A cancelled or timed-out context is a real failure for the caller to
	// report, not a reason to pretend the tool is absent.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), ctxErr)
	}
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = strings.TrimSpace(stdout.String())
	}
	if msg != "" {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, firstLine(msg))
	}
	return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
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
