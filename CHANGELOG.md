# Changelog

All notable changes to Nyrvo are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

What counts as a breaking change for a command line tool is spelled out in
[docs/RELEASING.md](docs/RELEASING.md): command names, flags, exit codes, rule
identifiers and the snapshot schema are the contract. Wording of a report is
not.

## [Unreleased]

Entries for the next release live in `.changes/unreleased/`; see docs/RELEASING.md.

## [0.6.0] — 2026-08-19

### Changed

- On Windows, AI agent prompts longer than the CreateProcess argument limit are
  passed on stdin with `-` as the final argv element instead of being truncated.

- GitHub Actions job logs now contribute observed versions for java, ruby, php,
  dotnet, pnpm, and rust when setup actions print the usual lines.

- Java 1.x ↔ 8 version matching in `Satisfies` now applies only when the
  runtime is `java`, so Go 1.8 no longer satisfies a declared `8`.

- The snapshot schema version is now 2. The `unusable` field added in v0.2.0 is
  optional, but it changed what an existing shape means: a runtime absent from
  `runtimes` used to mean "not observed", and can now mean "installed, and it
  refused to report a version". A build that predates the field would read a
  newer document, ignore the key it does not know, and report that runtime as
  missing — the untruth the field exists to prevent. Bumping the version makes
  an older Nyrvo refuse the document instead of quietly misreading it. Snapshots
  written with schema 1 are still read normally.

- `.tool-versions` aliases now include npm, pnpm, yarn, composer, and dotnet
  (including `dotnet-core`).

- Workflow YAML reads are capped at 1 MiB and fetched job logs at 10 MiB so a
  pathological repository cannot exhaust memory during import.

### Fixed

- The AI evidence document now carries `PartialRuntimes` and warns an
  agent the same way it already warned about a partial environment list.

- The failure excerpt of an imported job log is now bounded end to end. The
  excerpt rule capped the output lines before the first error, but then kept
  every `##[error]` line, so a step that printed one error per output line
  turned a 10MB log into 43,000 "Log:" notes and an 8MB snapshot. Only the
  first 50 error lines are kept now, and a note says how many more were
  dropped — a prefix that silently cut the rest would overstate what the log
  actually said.

- A capture now bounds its own total running time. Every external tool already
  had its own deadline, but nothing bounded their sum: a capture spawns up to 24
  processes, so a machine where each one hangs — a stalled network filesystem, a
  wedged Docker daemon — spent the total of every deadline before returning,
  roughly two minutes here and six on Windows, with a spinner turning and no
  indication it would ever stop. The budget is a minute (three on Windows, where
  every probe is allowed three times as long), far above a healthy capture's two
  seconds. A capture that exceeds it produces no snapshot and says which
  collector it gave up in: the collectors that never ran would leave their
  sections absent, and absence in a snapshot means "looked for and not found",
  so saving a partial capture would report the machine as lacking every tool the
  capture never reached.

- A job that runs in a container on an unknown runner still records Linux,
  but no longer guesses `amd64` for the architecture.

- A `docker ps` probe that ran out of time is marked unmeasured instead of
  leaving an empty service list that looked like no containers were
  running. Cancelling the capture still aborts.

- `nyrvo doctor --json` emits `"findings": []` for a clean diagnosis instead of
  `"findings": null`, matching `nyrvo diff --json`. A consumer reading
  `findings.length` no longer has to special-case the healthy answer.

- `nyrvo doctor <run>` now fails when a collector failed outright, the same
  way `nyrvo capture` does, instead of diagnosing a silently partial
  machine.

Fixed git status timeout preserving sha and branch in the snapshot while marking only dirty as unmeasured.

- A git probe that runs out of time is no longer reported as "not a repository".
  `git status` hanging on a cold filesystem made the collector return the raw
  deadline error, capture marked the section failed, and the snapshot carried no
  git section at all — so a diff reported "git described in A, not described in
  B" for a machine whose git probe merely timed out, the same invented drift
  ADR 0017 records for npm, docker compose, and dotnet. A timed-out probe now
  records the facts it could not read (`git.sha`, `git.branch`, `git.dirty`) in
  the snapshot's `unmeasured` list and leaves the section absent, so the diff
  drops those keys instead of treating silence as absence. A directory that is
  not a work tree and a repository with no commits are still genuine answers and
  are unchanged.

- A declared Java version of `8` now satisfies an observed `1.8.0`, which
  is how JDK 8 reports itself, without treating `2` as a prefix of `20`.

- Nested `env` and `with` maps in a workflow file are noted as unmodelled
  instead of disappearing silently.

- Released binaries report their version instead of `v0.5.0+dirty`. Go stamps a
  binary as dirty when `git status` is not clean, and the release workflow built
  into an untracked `dist/` inside the repository, which was enough to trigger
  it — so the v0.5.0 archives each claimed to be a modified build of the tag
  they were cut from. The build now happens outside the working tree. The
  release step is also re-runnable now: `gh release create` refuses to run
  twice, which defeated the `workflow_dispatch` retry that exists for exactly
  this situation, so an existing release is updated in place instead.

- Replay redacts GitHub Actions secret references written in bracket form
  (`secrets['TOKEN']`, `secrets["TOKEN"]`) the same way it already redacted
  the dot form, so those values print as `<secret>`.

- `nyrvo ci replay --json` serializes a job with no steps as `"steps": []`
  instead of `"steps": null`. The steps slice is part of the machine contract,
  and a consumer should not have to treat `null` and `[]` as the same thing:
  "no steps" is a fact, not an unknown.

- Snapshot directories are created `0700`, matching the documented contract
  and the user config store, instead of `0755`.

- Saving a snapshot refuses to follow a symlink at `.nyrvo`,
  `.nyrvo/snapshots`, or `.nyrvo/.gitignore`, so a planted link cannot
  redirect writes outside the repository.

Fixed snapshot Load refusing symlinked files and Save re-checking for symlinks after MkdirAll.

- Loading a snapshot is capped at 1 MiB, and a document whose
  `schema_version` is missing or whose `name` disagrees with the requested
  snapshot is refused rather than trusted as evidence.

- The snapshot loader now refuses documents too incomplete to describe an
  environment instead of answering confidently from them. `schema_version` must
  be present and positive, a snapshot must carry a name matching the file it is
  stored as, and the keyed collections (runtimes, requirements) must have their
  identity and no duplicate keys. `nyrvo diff empty empty` on a `{}` document —
  previously "No differences between  and ." — and `nyrvo doctor` against a
  schema-version-0 file now fail loudly. Snapshot files are also read through a
  1 MiB cap, so a hostile or generated file cannot exhaust memory at load.

- Terminal escape sequences can no longer reach a snapshot through CI metadata.
  A job log's output was already stripped of ANSI and other control bytes, but
  step names, job names, runs-on labels and container images were copied into
  `Source.Notes` verbatim — and those notes are printed by `nyrvo ci inspect`
  and embedded in the AI prompt. A workflow file could carry a container image
  like `node:20\x1b[2J` and emit a real clear-screen to the terminal. Every
  note a workflow file or a run's API contributes is sanitized now, so nothing
  a terminal would interpret instead of print survives into a snapshot.

Fixed textsafe stripping DCS, APC, and SOS terminal sequences in full.
 SOS is consumed too, in both its 7-bit (ESC X) and C1 (U+0098) forms: the case list carried 'p', which is not an introducer at all, so an SOS payload passed through as text a terminal would still act on.

- Terminal sanitization now consumes OSC sequences and 8-bit C1
  introducers, not only CSI, so a loaded snapshot cannot retain an escape
  sequence in fields that reach the terminal.

- A `uname` probe that ran out of time is recorded as unmeasured rather
  than as a machine with no kernel string. A missing uname binary still
  leaves the kernel field empty.

- A runtime that is installed but refuses to report a version is no longer
  reported as "missing". `nyrvo diff` prints "installed, not usable" for that
  side, `diff --json` carries `a_unusable`/`b_unusable` flags plus a top-level
  `unusable` so a consumer can tell "not installed" from "installed, refused",
  and the AI evidence document lists the refused runtime instead of letting an
  agent read absence while the finding below it says the opposite. This is the
  deterministic counterpart to `unmeasured` from ADR 0017: unlike an unmeasured
  probe, a refusal is not dropped — it is usually the very drift being sought.
  The kernel version is also compared symmetrically now: a workflow-derived
  snapshot cannot state one, so a one-sided kernel no longer prints "kernel: ci
  missing" on every local-vs-CI diff.

- A runtime listed as unusable is visible to requirement and
  `runtime.unusable` rules even when the other side is a partial runtime
  list, instead of disappearing because `MarkUnusable` does not append a
  Runtime entry.

### Security

- A capture is refused when `.nyrvo` or `.nyrvo/snapshots` is a symbolic link.
  A repository can commit such a link, and every write of a capture — the
  `.gitignore` and each snapshot — would otherwise follow it to a directory
  outside the repository that the user never consented to touch.

- Terminal control bytes are stripped from every string a snapshot carries, on
  the way out as well as the way in. Collectors already sanitized what they
  store, but a snapshot is also a document Nyrvo reads: one from an older build,
  hand-edited, or sent by somebody else to diff. The first pass covered notes,
  services, requirements and runtimes and missed `Source.Ref` — which is
  workflow-derived and reaches the terminal inside a `doctor` recommendation and
  the `--ai` request — and the environment variable names the diff prints as
  keys. Enumerating the risky fields is what left that gap, so the whole document
  is stripped now.

## [0.5.0] — 2026-08-19

### Added

- Every release now carries prebuilt binaries. Pushing a `v*` tag runs a
  release workflow that cross-compiles Nyrvo for macOS, Linux and Windows
  (amd64 and arm64), packages each binary with the LICENSE and the README, and
  attaches the archives and a SHA256SUMS to a GitHub Release. People without a
  Go toolchain — the JavaScript, Python and PHP developers most likely to hit
  environment drift — can download a binary instead of being bounced by the
  first `go install` in the README.

### Fixed

- External tools get longer to answer on Windows. The per-probe deadline was
  five seconds everywhere, and on Windows that is not enough: every observation
  the public runner feed has ever failed to measure was on `windows-latest` —
  `rustc --version` one day, `docker compose version` the next — while the Linux
  and macOS runners have never missed it once. Spawning a process is dearer
  there, and a cold image pays for it on the first call. The deadline is now
  fifteen seconds on Windows and unchanged elsewhere. Nothing was ever reported
  wrongly, because an expired probe is recorded as unmeasured rather than as
  absence, but a snapshot that keeps saying "I do not know" is worth less than
  one that waits a moment longer and finds out.

## [0.4.0] — 2026-08-18

### Added

- `composer --version` is captured as the `composer` runtime, so a machine's
  installed Composer can be compared across snapshots. Composer's version is
  independent of PHP's, and its resolver changed behaviour across majors: the
  same composer.json can install different dependency trees under Composer 1
  and Composer 2. Nothing a project declares pins the installed Composer, so
  the observation is the only record there is of the drift.

- `requires-python` in pyproject.toml's `[project]` table is read as a
  requirement source, so a project's declared Python floor is comparable
  against what a machine actually runs. PEP 621 puts it there, and it is where
  the standard, most-starred Python projects declare their floor — pyenv's
  `.python-version`, the file Nyrvo already reads, appears in almost none of
  them. The constraint is kept verbatim, including its own operators (">=3.11"
  or ">=3.11,<3.14"); it is recorded as a pin, not a floor, because it already
  says exactly what it means and an implicit "or newer" would silently discard
  an upper bound. The table match is exact, so `[project.optional-dependencies]`
  and `[project.urls]` are never mistaken for `[project]`. A project carrying
  both `.python-version` and pyproject.toml records both, since two files that
  disagree is itself worth seeing. No `[project]` table, no `requires-python`
  key, an empty value, or an unquoted value produces no requirement rather than
  a guess.

## [0.3.0] — 2026-08-18

### Added

- `packageManager` in package.json is read as a requirement source, so a
  project that pins the package manager corepack must run can be compared
  against what a machine actually has. The value is `npm`, `pnpm` or `yarn` at
  an exact version, and the `+sha512.…` integrity suffix corepack records is
  stripped before it is stored. It is recorded as a pin, not a floor: corepack
  downloads and runs precisely that version and refuses anything else, so a
  machine with any other version genuinely cannot build the project. A package
  manager name Nyrvo does not understand, or a malformed value, produces no
  requirement rather than a guess.

- `pnpm --version` and `yarn --version` are captured as the `pnpm` and `yarn`
  runtimes, and `pnpm/action-setup` is recognized in a workflow, so a CI job's
  pinned package manager is comparable against the machine — without the
  observations a packageManager requirement could be stored but never checked.

## [0.2.0] — 2026-08-17

### Added

- `nyrvo capture` animates a spinner beside the collector it is currently
  running, so a slow external probe no longer looks like a hang. It appears only
  when the destination is a terminal: a pipe, a file and a CI log receive exactly
  the bytes they received before, down to the last carriage return, and
  `NO_COLOR`, `TERM=dumb` and the legacy Windows console all turn it off through
  the same check that governs colour. With `--json` it follows the progress
  stream to stderr, so the document on stdout stays parseable.

- .NET is observed: `dotnet --version` is captured as the `dotnet` runtime, and
  `global.json` is read as a requirement source. `actions/setup-dotnet` is
  recognized in a workflow, so a .NET job's declared SDK is comparable against
  the machine — without it the constraint could be stored but never checked.

  `sdk.version` is recorded as a floor, not a pin. The .NET resolver rolls
  forward to a newer SDK under every policy except `rollForward: "disable"`,
  which is the one spelling recorded as a pin. Treating it as a pin everywhere
  would report every machine with a newer SDK as broken, which is the normal
  arrangement rather than drift.

- `nyrvo doctor` and `nyrvo ci import` animate a spinner while they wait on the
  network or on an AI agent, so a fetch that takes minutes no longer looks like
  a hang. The label says what the wait is for — `importing run 12345`, or the
  agent's name — and a spinner that has been running longer than two seconds
  shows how long: `⠹ claude · 47s`. It appears only when the destination is a
  terminal: a pipe, a file and a CI log receive exactly the bytes they received
  before, down to the last carriage return, and the `--json` document on stdout
  stays parseable.

### Changed

- `diff` and `doctor` use colour when they are writing to a terminal: bold
  headings, the severity word in red, yellow or dim, and the rule identifier in
  Nyrvo's purple. Nothing else is styled.

  The decision is made per writer, not per process. A pipe, a file, a CI log and
  a test all get exactly the bytes they got before, so anything parsing that
  output is unaffected. `NO_COLOR` and `TERM=dumb` disable it, and on Windows
  colour is offered only to terminals known to interpret escape sequences.

  Only lines that carry no tab are ever styled: the aligned columns are laid out
  by `text/tabwriter`, which counts raw bytes rather than display width, so an
  escape sequence on one of those lines would silently shift every column.

- A runtime that is installed but refuses to report a version now reports
  `runtime.unusable` where it previously reported `runtime.missing`. Rule
  identifiers are part of the contract, so this is called out rather than filed
  as a fix: a script filtering on `runtime.missing` will see fewer findings, and
  one filtering on the new identifier sees them instead. The old identifier is
  unchanged in meaning and still reports a runtime that is genuinely absent.

### Fixed

- A runtime that is installed but will not report a version is no longer
  reported as a runtime that is missing. A project pinning a toolchain the
  machine does not have — a `global.json` naming an SDK that is not installed, a
  `rust-toolchain.toml` naming an unknown toolchain, an rbenv version with no
  matching install — makes `dotnet --version`, `rustc --version` and
  `ruby --version` exit without answering, even though the binary is on PATH and
  the runtime is genuinely there. `doctor` said "ci has dotnet installed, but
  local does not" about a machine carrying .NET, and recommended installing it;
  it now reports `runtime.unusable`, says the runtime is installed but would not
  report a version, and points at the pin.

  Snapshots record such runtimes in `unusable` as `component.key` only, never
  the tool's error text: the real messages embed the user's absolute paths, and
  a snapshot is pasted into bug reports. Unlike `unmeasured`, a refusal is
  deterministic and is reported rather than skipped.

## [0.1.2] — 2026-08-17

### Fixed

- A tool that ran out of time is no longer recorded as a tool that is absent.
  A probe exceeding its deadline and a binary that is not installed both leave a
  collector with no version, and Nyrvo published the second answer for the first.
  On a cold windows-latest runner `npm --version` takes longer than the probe
  deadline, so two captures of one unchanged machine disagreed about whether it
  had npm, and the daily runner feed recorded windows-latest as carrying no
  Docker compose when it carries 2.40.3.

  Snapshots now list such observations in `unmeasured`, the comparison skips
  them, and `diff` says so rather than narrowing the report in silence.
  Unmeasured is not missing: it is a question this capture could not answer.
  `doctor` inherits the fix and no longer raises `runtime.missing` — a finding
  whose recommendation was to install software the machine already had.

### Added

- `unmeasured` on the snapshot document: an optional list of `component.key`
  observations that were attempted and did not complete. Additive and optional,
  so the schema version is unchanged and older documents remain valid.

### Changed

- `nyrvo capture` reports each collector as it finishes instead of printing the
  whole list once the run is over. A capture spawns a dozen external tools and
  takes seconds; until now the terminal stayed silent for all of it and then
  printed "Capturing environment..." above work that had already happened. The
  lines themselves are unchanged, so anything reading that output still reads
  the same bytes.

## [0.1.1] — 2026-08-16

### Fixed

- A slow external probe no longer fails a whole capture. `collector.Run` applies
  its own deadline, and the docker collector treated that expiring as if the
  caller had cancelled — so one sluggish `docker compose version` discarded every
  other observation. Only genuine cancellation now aborts a capture.
- The capture error names the collectors that failed instead of saying "some
  collectors failed", which was equally useless in a CI log and on a laptop.

### Added

- `SECURITY.md`: how to report a vulnerability privately, what Nyrvo already
  refuses to do, and what is deliberately not a vulnerability.
- Windows is verified. CI runs the whole suite on Linux, macOS and Windows,
  across Go 1.25 and 1.26, with nothing skipped to make a platform green except
  the POSIX permission assertion, which does not apply there.

## [0.1.0] — 2026-08-16

First tagged version. Everything below is listed as one release rather than as
invented history, because there is no earlier version anyone could be upgrading
from.

Verified on Linux and macOS. It builds for Windows and has never been run there,
so Windows is not claimed as supported.

### Added

- `nyrvo capture <name>` and `nyrvo diff <a> <b>`: snapshot an execution
  environment and compare two of them semantically. Snapshots are versioned
  JSON in `.nyrvo/snapshots/`, written 0600.
- Observations: OS, architecture and kernel; git SHA, branch and dirty state;
  Go, npm, Node.js, Python, Ruby, PHP, Rust and Java versions with install
  paths; Docker client, server and compose versions and whether the daemon
  answers; container images running locally; the version constraints the
  checked-out project declares; and environment variable **names**.
- `nyrvo ci inspect`, `nyrvo ci capture <job>`: read `.github/workflows/*.yml`
  and derive the environment a job declares, without running or resolving
  anything.
- `nyrvo ci import <run> [job]`: import a run that actually happened through the
  user's own `gh` CLI, including the failing job's log. Nyrvo never reads,
  stores or asks for a GitHub token.
- `nyrvo ci replay <job>`: print what a job does, step by step, marking what
  cannot be reproduced locally and why. It executes nothing.
- `nyrvo doctor`: deterministic diagnosis with stable rule identifiers —
  `runtime.version_mismatch`, `runtime.missing`,
  `runtime.requirement_unsatisfied`, `env.missing`, `system.os_mismatch`,
  `system.arch_mismatch`, `git.sha_mismatch`, `git.dirty`, `service.missing`,
  `service.image_mismatch`. No model, no network, no clock.
- `--fail-on=<severity>` to opt a CI job into a non-zero exit on findings.
- `nyrvo doctor --ai`: build an analysis request from the diagnosis and print
  it. `--agent=claude|codex|opencode` hands it to an agent CLI the user already
  installed, run in an empty temporary directory.
- `--json` on every command that produces a document.
- `nyrvo config set|unset|list`: a user-level preference file at
  `os.UserConfigDir()/nyrvo/config.json`. `ai.agent` sets the default agent, so
  `--ai` alone runs it. There is deliberately no project-level config.
- `nyrvo list`, `nyrvo version`.

### Notes

- `golangci-lint-action` is pinned to v7, the first release able to install
  golangci-lint v2.

### Security

- Environment variable **values** are never recorded, only names. An analysis
  request narrows even the names to those a difference or a finding refers to,
  and replaces the home directory prefix of a path with `~`.
- Container names and labels are never recorded: `docker ps` reports the whole
  machine, and its labels carry the absolute paths of a user's unrelated
  repositories.
- A value referencing a secret in a workflow is printed as `<secret>` in a
  replay plan.
- Nyrvo owns no API credentials for any AI vendor and never asks for one.
- Configuration is user-level only. A repository-level config file would let the
  author of a pull request choose which program Nyrvo executes.

[Unreleased]: https://github.com/nyrvo-dev/nyrvo/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/nyrvo-dev/nyrvo/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/nyrvo-dev/nyrvo/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/nyrvo-dev/nyrvo/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/nyrvo-dev/nyrvo/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/nyrvo-dev/nyrvo/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/nyrvo-dev/nyrvo/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/nyrvo-dev/nyrvo/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/nyrvo-dev/nyrvo/releases/tag/v0.1.0
