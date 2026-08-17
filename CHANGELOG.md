# Changelog

All notable changes to Nyrvo are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

What counts as a breaking change for a command line tool is spelled out in
[docs/RELEASING.md](docs/RELEASING.md): command names, flags, exit codes, rule
identifiers and the snapshot schema are the contract. Wording of a report is
not.

## [Unreleased]

### Added

- .NET is observed: `dotnet --version` is captured as the `dotnet` runtime, and
  `global.json` is read as a requirement source. `actions/setup-dotnet` is
  recognized in a workflow, so a .NET job's declared SDK is comparable against
  the machine — without it the constraint could be stored but never checked.

  `sdk.version` is recorded as a floor, not a pin. The .NET resolver rolls
  forward to a newer SDK under every policy except `rollForward: "disable"`,
  which is the one spelling recorded as a pin. Treating it as a pin everywhere
  would report every machine with a newer SDK as broken, which is the normal
  arrangement rather than drift.

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

[Unreleased]: https://github.com/nyrvo-dev/nyrvo/compare/v0.1.2...HEAD
[0.1.2]: https://github.com/nyrvo-dev/nyrvo/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/nyrvo-dev/nyrvo/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/nyrvo-dev/nyrvo/releases/tag/v0.1.0
