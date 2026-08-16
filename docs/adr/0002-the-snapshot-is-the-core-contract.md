# 0002 — The snapshot is the core contract

## Context

Everything Nyrvo does is either producing an observation of an environment or
reasoning about one. If each command invented its own representation, diff,
doctor, CI import, and any external tooling would each need their own parsing
and their own idea of what "the same environment" means.

Snapshots are also written to disk and printed as JSON, so they outlive the
version of Nyrvo that produced them and get read by other programs.

## Decision

One versioned type, `snapshot.Snapshot`, is the contract between every stage of
the pipeline. It carries an explicit `schema_version`, optional sections, and a
canonical serialization.

Determinism is part of the contract:

- collections are sorted by `Normalize` before writing or comparing;
- `created_at` is recorded but never compared, so time alone cannot create
  drift;
- two captures of an unchanged machine serialize to identical bytes.

Renaming a field or changing what one means requires bumping `SchemaVersion`.
Reading a snapshot whose version is newer than the running build is refused
rather than guessed at.

## Consequences

- Collectors, diff, renderers, and future diagnostics all speak one vocabulary.
- Snapshot changes are deliberate contract changes, not incidental refactors.
- Golden tests over serialized snapshots are meaningful, because the bytes are
  stable.
- Purely additive optional fields stay compatible without a version bump.
