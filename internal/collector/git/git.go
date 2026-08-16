// Package git observes the checked-out state of a Git repository.
//
// The section is optional: a directory that is not a Git work tree still yields
// a valid snapshot with an absent git section, so a capture never fails just
// because it ran somewhere without a repository.
package git

import (
	"context"
	"errors"
	"fmt"

	"github.com/nyrvo-dev/nyrvo/internal/collector"
	"github.com/nyrvo-dev/nyrvo/internal/snapshot"
)

// Git fills the snapshot's Git section.
type Git struct {
	// Dir is the repository to inspect. Empty means the process working
	// directory, which keeps collecting the current tree without naming it.
	Dir string
}

// Name identifies this collector in progress output and errors.
func (g *Git) Name() string {
	return "git"
}

// Collect fills snap.Git with the repository's SHA, branch, and dirtiness.
//
// The section is left absent when git cannot observe a repository here: no git
// binary, a directory that is not a work tree, or a repository with no commits.
// Those cases return ErrUnavailable, which capture treats as "no data" rather
// than a failed collection. The section is assigned only once, at the end, so a
// failure anywhere leaves it untouched.
func (g *Git) Collect(ctx context.Context, snap *snapshot.Snapshot) error {
	if err := g.requireWorkTree(ctx); err != nil {
		return err
	}

	sha, err := g.run(ctx, "rev-parse", "HEAD")
	if err != nil {
		// rev-parse HEAD fails on a fresh repository that has no commits yet;
		// that is "nothing to observe here", not a broken capture.
		return unavailable(err)
	}

	branch, err := g.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	// A detached HEAD reports the literal "HEAD"; recording that string would
	// confuse diffs, which expect a branch name or nothing at all.
	if branch == "HEAD" {
		branch = ""
	}

	status, err := g.run(ctx, "status", "--porcelain")
	if err != nil {
		return err
	}

	snap.Git = &snapshot.Git{
		SHA:    sha,
		Branch: branch,
		// --porcelain prints nothing for a clean tree, so any output is dirt.
		// Untracked files count as dirty: they can change what a build produces
		// just as much as an uncommitted edit, so the SHA alone would not
		// describe what actually ran.
		Dirty: status != "",
	}
	return nil
}

// requireWorkTree verifies the directory is a Git work tree before probing it,
// so the later rev-parse HEAD failure is unambiguous as the "no commits" case
// rather than "not a repository".
func (g *Git) requireWorkTree(ctx context.Context) error {
	out, err := g.run(ctx, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return unavailable(err)
	}
	if out != "true" {
		return fmt.Errorf("not a git work tree: %w", collector.ErrUnavailable)
	}
	return nil
}

// run invokes git through collector.Run, which spawns the binary directly and
// never through a shell, so values that live in the repository cannot be
// interpreted as commands, and which enforces a timeout so a hung filesystem
// cannot stall a capture.
func (g *Git) run(ctx context.Context, args ...string) (string, error) {
	full := make([]string, 0, len(args)+2)
	if g.Dir != "" {
		full = append(full, "-C", g.Dir)
	}
	full = append(full, args...)
	return collector.Run(ctx, "git", full...)
}

// unavailable labels an absent-repository cause with ErrUnavailable so callers
// can classify it, except when the failure is a cancelled or timed-out context:
// that is a real collection failure that must surface as itself (nothing git
// could tell us on retry would change it) and never masquerade as
// "collector unavailable". collector.Run only maps a missing binary to
// ErrUnavailable, so every other "nothing here" failure needs it added here.
func unavailable(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("git unavailable: %w: %w", err, collector.ErrUnavailable)
}
