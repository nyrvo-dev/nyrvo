# 0006 — Parse CI configuration before replaying it

## Context

The question Nyrvo exists to answer — "why does this pass locally and fail in
CI?" — invites an obvious answer: run the CI job locally and see. That is a
large, fragile project. Faithfully replaying GitHub Actions means implementing
expression evaluation, matrices, action resolution, caching, and container
semantics, and being wrong about any of them produces a confident, false
result.

Most of the value arrives long before that. A workflow file already states which
runner, which Node version, which services, and which environment variables a
job expects. Comparing those declarations against a local capture explains a
large share of real failures.

## Decision

Nyrvo reads `.github/workflows/*.yml` and converts a job into a snapshot of the
environment that job is *expected* to run in. It does not execute workflows,
resolve actions over the network, or evaluate expressions.

Consequently the CI-derived snapshot is marked `source.kind =
"github-actions"`, and anything recognized but not modelled is recorded in
`source.notes` and shown to the user. Unsupported constructs degrade visibly:
Nyrvo never implies it understood a workflow feature it skipped.

Two rules keep this honest:

- an expression such as `${{ matrix.node }}` is never guessed at;
- a version range or file reference (`>=20`, `20.x`, `.nvmrc`) is not recorded
  as a version, because a diff that reports `20.x` against `24.4.0` is a lie
  about what CI will actually install.

## Consequences

- `nyrvo diff local ci` works with no new comparison logic: the CI environment
  is just another snapshot.
- Nyrvo is useful offline, with no GitHub token and no API calls.
- Some drift is invisible: what an action installs at runtime, what a cache
  restores, what a range resolves to on the day the job runs. Importing real
  run metadata (roadmap M9) is the answer to that, not local replay.
- Partial CI replay stays possible later, but only on top of an accurate model
  of the declared environment.
