# Nyrvo

A Go CLI that captures and compares execution environments, answering why an
application behaves differently across local, CI, staging, and production.

## Status

Early development. The commands listed under [Usage](#usage) are what works
today: capture, diff, reading CI configuration, importing a run that happened,
and deterministic diagnosis. `nyrvo ci replay` prints what a job does; it never
runs it. Not yet available: database collectors, executing anything on your
behalf, and the optional AI layer.

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

`doctor` exits `0` even when it reports findings: a diagnosis is an answer, not
a failure, and a CI job opts in when it wants the exit code to depend on them.
`--fail-on=high` (or `medium`, `low`) exits `1` when a finding at that severity
or worse exists:

```
$ nyrvo doctor local ci --fail-on=high     # exit 0 unless a high finding exists
```

It must be written with an equals sign. Every other nyrvo flag is boolean and
may appear anywhere on the line (`nyrvo diff local ci --json`), so the separate
form `--fail-on high` cannot be told apart from a snapshot named `high` and is
rejected with a usage error. A job that wants an unsound environment to fail the
build runs it against the defaults:

```
$ nyrvo capture local
$ nyrvo doctor --fail-on=high
```

One rule never compares two environments at all.
`runtime.requirement_unsatisfied` (always high) judges an environment against
what the checked-out project declares it needs, and reports when it does not
meet it — the only rule that can call an environment wrong rather than merely
different. It understands comparators (`>=`, `>`, `<=`, `<`, `=`), caret and
tilde ranges, wildcards (`"20.x"`), bare prefixes, and comma- or space-separated
conjunctions with `||` alternatives. A constraint it cannot fully parse —
`lts/iron`, `workspace:*`, a hyphen range — produces no finding rather than a
guess.

### Reproducing a CI job

`nyrvo ci replay <job>` prints what a job does, in order, and marks every step
it cannot reproduce here with the reason:

```
$ nyrvo ci replay test
REPLAY test

PREREQUISITES
  this job runs once per matrix combination (go-version=[1.25 1.26], os=[ubuntu-latest macos-latest]); the steps below describe one of them

STEPS

1. Checkout
   uses              actions/checkout@v4
   not reproducible  uses actions/checkout@v4; Nyrvo does not run actions, but your working tree is already the checkout, so there is nothing to do

2. Set up Go
   uses              actions/setup-go@v5
   not reproducible  actions/setup-go@v5 installs the go named by ${{ matrix.go-version }}, which the runner decides; install it yourself

3. Build
   run
     go build ./...
```

It executes nothing, by design. A tool that runs a workflow's steps outside the
runner diverges from CI in ways it cannot see — no runner image, no actions, no
secrets, no service containers — and produces a run that proves nothing while
looking authoritative. A plan you read and paste stays honest about the gap.
Environment values come from the workflow file and are printed as written, but a
value referencing a secret is replaced with `<secret>`.

Other commands:

```
$ nyrvo list
$ nyrvo version
```

## What Nyrvo observes

- OS, CPU architecture, and kernel
- Git commit SHA, branch, and whether the working tree is dirty
- Go, Node.js, and Python versions (including install paths)
- Docker client, server, and compose versions, and whether the daemon answers
- The version constraints the checked-out project declares
- Environment variable **names**
- From CI: the runner platform, setup-action runtime versions, declared
  environment variable names, and service containers a job asks for

An absent Docker section means Docker is not installed; a present section with
`daemon_running` false means the CLI is there but the daemon is not answering.
Those are different facts, and the second is a common reason a compose-backed
suite passes in CI and fails on a laptop. Docker versions are compared by
`nyrvo diff` like everything else.

The declared constraints come from `engines.node` and `engines.npm` in
package.json, the `go` directive in go.mod, `.nvmrc`, `.python-version`, and
`.tool-versions`, stored verbatim. They are never compared between environments:
both sides normally read the same repository, so diffing them would manufacture
drift out of provenance. They exist so a rule can call a version wrong, which is
what `runtime.requirement_unsatisfied` does.

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
