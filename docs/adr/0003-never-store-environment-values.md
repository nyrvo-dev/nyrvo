# 0003 — Never store environment variable values

## Context

Environment drift is one of the most common causes of "works locally, fails in
CI", so environment variables must be part of a snapshot. But the environment of
a real project holds `DATABASE_URL`, `OPENAI_API_KEY`, and session tokens.

Snapshots are files users commit, attach to issues, and paste into chat. A tool
that quietly writes credentials into such a file is a liability regardless of
how useful the data is.

## Decision

Nyrvo records **variable names only**. A value is never persisted, logged,
truncated, or included in an error message.

Nothing is hashed either. An HMAC of a value would allow comparing values across
environments, but it is still derived from a secret, and no diagnostic today
needs it. Hashing waits for a concrete use case and a written threat model.

Snapshot files are written with `0600` permissions, and the first capture writes
`.nyrvo/.gitignore` so snapshots are not committed by accident.

## Consequences

- Drift is expressed as presence: "REDIS_URL is missing in CI". That answers the
  common failure without holding a secret.
- Nyrvo cannot detect "same variable, different value" drift, e.g. a
  `DATABASE_URL` pointing at the wrong host. This is a known and accepted gap.
- Contributors will occasionally read this as missing functionality, so the
  restriction is stated in the code, in `AGENTS.md`, and here.
- The env collector's test suite asserts, against serialized output, that no
  value substring appears. That test is a requirement, not a nicety.
