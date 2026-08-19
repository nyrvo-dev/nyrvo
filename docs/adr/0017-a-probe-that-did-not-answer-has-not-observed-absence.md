# 0017 — A probe that did not answer has not observed absence

## Context

ADR 0008 established that absence is only evidence when the source could have
reported presence. It is about sources that are incomplete by nature — a
workflow file states the variables it sets and says nothing about the runner's
hundreds. It does not cover probes, and the same mistake has shipped three times
in the probe path:

- In v0.1.2, `npm --version` exceeded its deadline on a cold windows-latest
  runner. The collector recorded no version, and Nyrvo published "npm is not
  installed". Two captures of one unchanged machine disagreed about whether it
  had npm.
- The runners feed showed windows-latest as carrying no Docker compose when it
  carries 2.40.3 — the same timeout, published to a public page.
- A project with `global.json` `{"sdk":{"version":"9.0.100","rollForward":"disable"}}`
  on a machine whose only SDK is 10.0.400. `dotnet --version` exits 155 and
  refuses to answer; the binary is on PATH. Nyrvo said "ci has dotnet installed,
  but local does not" and recommended installing .NET on a machine that had
  .NET.

The shape is identical every time: a question Nyrvo could not answer became a
negative answer.

## Decision

A collector that did not get an answer must record why it did not. Absence is
recorded only when something was looked for and found not there. There are three
outcomes, not two, and the runtime collector already distinguishes them:

- **Nothing was found to ask.** `LookPath` found no binary. This is genuine
  absence, and the only case recorded as absent.
- **Asked, did not finish.** A deadline expired. The snapshot's `unmeasured`
  list records `runtime.npm` or `docker.compose_version`. Nondeterministic — the
  diff skips the key and the renderer prints "Unmeasured is not missing: run the
  capture again to settle it" — because asking again may answer.
- **Asked, answered by refusing.** The binary was found and exited non-zero,
  usually because a pinned toolchain (`global.json`, `rust-toolchain.toml`, an
  rbenv version) names something this machine does not have. The snapshot's
  `unusable` list records `runtime.dotnet`. Deterministic, so it is reported,
  not skipped: it is usually the very drift the user is running Nyrvo to find.
  It surfaces as the rule `runtime.unusable`, whose finding says "installed, not
  usable" and recommends changing the pin, never installing the tool.

The three outcomes map onto distinct code: `snapshot.MarkUnmeasured` and
`snapshot.MarkUnusable` write the two lists, `dropUnmeasured` in the diff
removes only the keys a side could not read while setting `Result.Unmeasured` so
the narrowing is announced, and `runtimeUnusableFinding` keeps a refusal from
ever being rendered as a missing runtime.

## Consequences

- Both lists store only `"<component>.<key>"`, never the tool's error text,
  because real refusals embed the user's absolute paths and snapshots end up
  pasted into bug reports. This is the same reasoning as ADR 0003, which keeps
  environment variable values out for the same reason.
- `unmeasured` and `unusable` are additive optional fields, so `SchemaVersion`
  stayed 1. See ADR 0002.

  **Superseded before 1.0.0. `SchemaVersion` is 2.** The reasoning above was
  wrong, and it was wrong in a way this ADR is specifically about. The *fields*
  are additive, but adding `unusable` changed what an existing shape *means*:
  before it, a runtime absent from `runtimes` meant "not observed here"; after
  it, the same absence can mean "installed, and it refused to report a version",
  with the fact recorded in `unusable` instead. A build that predates the field
  reads the newer document, ignores the key it does not understand, sees no
  entry, and reports the runtime as missing — the exact untruth the field was
  added to prevent, reintroduced by a reader instead of a writer. ADR 0002's
  rule is about what a field *means*, not about whether it is optional.
  RELEASING.md promises an old binary can refuse a document it would misread
  rather than quietly misreading it, and refusing requires a version it does not
  recognize. Below 1.0.0 the bump is free; after it, schema 1 would be a
  permanent population of files old binaries can misread.
- The cost is real: a snapshot can now say "I do not know" about a runtime, and
  every consumer has to handle a third state rather than a boolean. The diff,
  the renderer, and the diagnostic rule each had to be taught it.

  **This was written as though it had been done. Only the diagnostic rule had
  been.** `diff`, the terminal renderer and the AI evidence document went on
  reporting an unusable runtime as `missing` for three releases, so `doctor`
  said "installed, not usable" while the evidence directly above it said the
  runtime was absent. That is the fourth shipment of the bug this ADR names, and
  it shipped inside the ADR that names it. The lesson kept here on purpose:
  writing down that every consumer was taught is not the same as teaching them,
  and a claim in a design document is not evidence. Verify each consumer against
  running code.

  It is worth
  paying because a snapshot that cannot distinguish "not installed" from "did
  not answer" invents drift out of silence, and a diagnosis that tells a user to
  install software they already have destroys trust in every other finding in
  the report.
- This does not make the class of bug impossible — a probe can still be wrong
  about why it failed. It makes the failure nameable and reviewable: the
  snapshot says what was attempted and what happened, and a reader can judge the
  claim instead of trusting a boolean.