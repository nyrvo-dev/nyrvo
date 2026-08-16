# Changelog

All notable changes to Nyrvo are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

What counts as a breaking change for a command line tool is spelled out in
[docs/RELEASING.md](docs/RELEASING.md): command names, flags, exit codes, rule
identifiers and the snapshot schema are the contract. Wording of a report is
not.

## [Unreleased]

Nothing yet.

## [0.1.0] — unreleased

First tagged version. Everything below is what `0.1.0` will contain; it is
listed as one release rather than as invented history, because there is no
earlier version anyone could be upgrading from.

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

[Unreleased]: https://github.com/nyrvo-dev/nyrvo/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/nyrvo-dev/nyrvo/releases/tag/v0.1.0
