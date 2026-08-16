# 0004 — Unavailable collectors do not fail a capture

## Context

Nyrvo observes things that are frequently absent: a machine without Python, a
directory that is not a Git repository, a container without `uname`. A capture
that failed whenever an optional tool was missing would be useless in exactly
the minimal environments that most need explaining.

The obvious interface is a two-step `Available()` then `Collect()`. That spawns
every probe twice, and the two calls can disagree between them.

## Decision

A collector is one method, `Collect(ctx, *snapshot.Snapshot)`. Its outcome is
expressed through the error:

- `nil` — the section was observed;
- an error wrapping `collector.ErrUnavailable` — nothing to observe here; the
  section stays absent and the capture continues;
- any other error — the tool exists but misbehaved; the capture records the
  failure and the CLI exits non-zero, while still saving what was collected.

Context cancellation is never reported as unavailability: an interrupted capture
must surface as interrupted, not as an environment without tools.

The distinction that matters: "not installed" is information about the
environment, "installed but broken" is information about a problem.

## Consequences

- A snapshot from a bare container is still valid and still useful.
- The capture is saved even on failure, because a partial capture is the
  evidence the user is trying to explain.
- Callers must use `errors.Is` and collectors must wrap with `%w`.
- Progress output distinguishes `ok`, `skipped`, and `FAILED`, so absence is
  visible rather than silent.
