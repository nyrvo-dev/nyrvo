# 0007 — Depend on gopkg.in/yaml.v3 for workflow parsing

## Context

Reading GitHub Actions workflows requires a YAML parser. The standard library
has none, and YAML is not a format to hand-roll: anchors, block scalars, flow
collections, and implicit typing are exactly the kind of detail a partial parser
gets quietly wrong on someone else's workflow file.

Nyrvo's dependency policy asks whether the standard library can solve the
problem cleanly (it cannot), whether the dependency removes meaningful
complexity (it removes a parser), and whether it is mature and low-risk.

## Decision

Use `gopkg.in/yaml.v3` as Nyrvo's first and, so far, only third-party
dependency.

It was chosen over the alternatives because it has no transitive dependencies of
its own, is the most widely deployed YAML parser in the Go ecosystem, and parses
a stable, frozen specification — a package whose problem domain does not move is
a safe place to be in maintenance mode.

The dependency is confined to `internal/ci/githubactions/parse.go`. Everything
downstream works on Nyrvo's own model types, so replacing the parser later would
touch one file.

## Consequences

- Nyrvo is no longer dependency-free. That threshold is crossed once, for a
  reason, rather than drifting.
- Workflow parsing handles real-world YAML rather than the subset a hand-written
  parser would cover.
- Adding a second dependency requires the same justification; convenience is not
  one.
- If yaml.v3 is ever abandoned in a way that matters, the blast radius is a
  single file behind a stable internal model.
