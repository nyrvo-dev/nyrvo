# Nyrvo

A Go CLI that captures and compares execution environments, answering why an
application behaves differently across local, CI, staging, and production.

## Status

Early development. The commands listed under [Usage](#usage) are what works
today: capture, diff, reading CI configuration, importing a run that happened,
and deterministic diagnosis. Not yet available: Docker and database collectors,
replaying CI locally, and the optional AI layer.

## Why

Code that passes on a laptop and fails in CI is usually not a code problem: it
is environment drift. Different Go, Node, or Python versions, a different
kernel, an uncommitted working tree, or a missing environment variable silently
change behavior between environments. Nyrvo snapshots each environment as a
small, shareable JSON document and diffs them so you can see exactly what
differs between local and CI, staging, and production.

## Install

```
go install github.com/nyrvo-dev/nyrvo/cmd/nyrvo@latest
```

or build the binary locally:

```
make build
```

## Usage

Capture two environments and diff them:

```
$ nyrvo capture local
$ nyrvo capture ci
$ nyrvo diff local ci
```

`--json` prints machine-readable output instead of human text:

```
$ nyrvo capture local --json
$ nyrvo diff local ci --json
```

Compare this machine against what CI declares:

```
$ nyrvo ci inspect            # what each workflow job says it needs
$ nyrvo ci capture lint       # save that job's declared environment as "ci"
$ nyrvo capture local
$ nyrvo diff local ci
```

`ci inspect` and `ci capture` read `.github/workflows/*.yml` and never run a
workflow, resolve an action, or call the GitHub API. Anything Nyrvo recognizes
but does not model is listed under "not modelled" instead of being silently
dropped, and a value it cannot know — `node-version: 20.x`, `runs-on: ${{
matrix.os }}` — is reported as unknown rather than guessed.

Or import a run that actually happened:

```
$ nyrvo ci import 31921289286
$ nyrvo ci import https://github.com/owner/repo/actions/runs/31921289286
$ nyrvo ci import <run> "test (ubuntu-latest)"     # pick a specific job
```

A run reports what a job *got* rather than what it asked for: the commit that
was checked out and the runner it landed on, so a `${{ matrix.os }}` that the
workflow file could only leave unknown becomes a real platform. It defaults to
the job that failed — the one you are asking about — and refuses to guess when
more than one did.

Importing uses your own `gh` CLI. Nyrvo never reads, stores, or asks for a
GitHub token. Run metadata does not include installed runtime versions (those
live in the job logs), and the snapshot says so rather than implying it knows.

Ask what the differences mean:

```
$ nyrvo doctor                                  # diagnoses local against ci
$ nyrvo doctor 31921289286                      # import a run and diagnose it, in one step
$ nyrvo doctor https://github.com/owner/repo/actions/runs/31921289286
$ nyrvo doctor local prod
$ nyrvo doctor --json
```

Given a run reference, `doctor` imports the run and captures this machine in
memory and diagnoses both — nothing is saved, so a one-shot question never
overwrites snapshots you captured deliberately. The report opens with what the
evidence itself reported: the run's conclusion, the step that failed, the error
line from the job log, and anything Nyrvo could not model.

`doctor` applies deterministic rules — no model, no network, no clock — and
ranks findings by how plausibly they explain a failure rather than by how large
the difference is. A version a workflow declares as a prefix (`go-version:
"1.26"`) is satisfied by a machine running `1.26.6`, so a correct setup stays
quiet. When nothing matches, Nyrvo says no rule matched; it does not claim the
environments are equivalent.

Other commands:

```
$ nyrvo list
$ nyrvo version
```

## What Nyrvo observes

- OS, CPU architecture, and kernel
- Git commit SHA, branch, and whether the working tree is dirty
- Go, Node.js, and Python versions (including install paths)
- Environment variable **names**
- From CI: the runner platform, setup-action runtime versions, declared
  environment variable names, and service containers a job asks for

## Snapshots

Snapshots are stored as plain JSON files in `.nyrvo/snapshots/<name>.json` in
the working directory, so they follow the repository they describe. Each file
carries a `schema_version` so an older Nyrvo can refuse to misread a newer
snapshot.

The first capture also writes `.nyrvo/.gitignore`, so capturing inside a
repository does not leave untracked files that would make Nyrvo report its own
working tree as dirty. Empty that file if you want to commit your snapshots;
Nyrvo will not recreate it.

## Security

Nyrvo **never stores environment variable values** — only their names.
Presence is enough to detect drift ("REDIS_URL is missing in CI"), and never
recording values means credentials never end up in a file you share in a bug
report or commit by accident. Snapshot files are written with 0600 permissions
so they remain owner-readable.

## Development

Run the full local checks with `make check` (test, race detector, vet, gofmt
and golangci-lint).
Go 1.25 is the minimum supported version; development runs on 1.26. CI verifies
both.

## License

[MIT](LICENSE)
