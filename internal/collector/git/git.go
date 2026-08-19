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
// than a failed collection. A probe that runs out of time is the one exception:
// nothing git said proves the directory is not a repository, so the facts that
// never answered are recorded as unmeasured instead, and a diff between two
// captures of one machine does not report git as having vanished. The section
// is assigned only once, at the end, so a failure anywhere leaves it untouched.
func (g *Git) Collect(ctx context.Context, snap *snapshot.Snapshot) error {
	if err := g.requireWorkTree(ctx); err != nil {
		// A timeout here means git never answered even "are we in a work
		// tree?", so not one of the three facts is known. A refusal is a
		// genuine "not a repository" and keeps its existing absent behaviour.
		return g.classify(ctx, snap, err, "sha", "branch", "dirty")
	}

	sha, err := g.run(ctx, "rev-parse", "HEAD")
	if err != nil {
		// rev-parse HEAD fails on a fresh repository that has no commits yet;
		// that is "nothing to observe here", not a broken capture. Only a probe
		// that ran out of time leaves the repository's state unknown.
		return g.classify(ctx, snap, err, "sha", "branch", "dirty")
	}

	branch, err := g.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return g.classify(ctx, snap, err, "branch", "dirty")
	}
	// A detached HEAD reports the literal "HEAD"; recording that string would
	// confuse diffs, which expect a branch name or nothing at all.
	if branch == "HEAD" {
		branch = ""
	}

	status, err := g.run(ctx, "status", "--porcelain")
	if err != nil {
		return g.classify(ctx, snap, err, "dirty")
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
		return err
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

// classify separates a failed git probe into the three outcomes ADR 0017
// distinguishes. A cancelled capture is the caller's own failure and must
// surface as itself. A probe that ran out of time did not observe absence, so
// the keys it could not read are marked unmeasured — inventing a clean
// checkout, or leaving git nil without that mark, would make the next diff
// report a repository as missing. Everything else is a genuine "nothing here".
//
// Only the keys still unknown at the point of failure are passed: a timeout on
// `git status` has already learned the sha and the branch, and marking those
// unmeasured too would decline to compare facts Nyrvo actually holds.
//
// The result wraps ErrUnavailable rather than being nil. There is no Git
// section to record either way, and returning nil would have capture report the
// section as "ok" — a collector announcing success for something it never
// observed, which is the same overstatement in the progress output that
// unmeasured exists to prevent in the snapshot.
func (g *Git) classify(ctx context.Context, snap *snapshot.Snapshot, err error, keys ...string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if collector.IsTimeout(err) {
		for _, k := range keys {
			snap.MarkUnmeasured("git", k)
		}
		return fmt.Errorf("git: %v: %w", err, collector.ErrUnavailable)
	}
	return unavailable(err)
}

// unavailable labels an absent-repository cause with ErrUnavailable so callers
// can classify it. It exists for the genuine refusals — no git binary, not a
// work tree, no commits yet — which collector.Run does not map to ErrUnavailable
// on its own. A probe deadline is handled by classify before this is reached,
// and a cancelled context is the caller's own failure that must surface as
// itself and never masquerade as "collector unavailable".
func unavailable(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("git unavailable: %w: %w", err, collector.ErrUnavailable)
}
