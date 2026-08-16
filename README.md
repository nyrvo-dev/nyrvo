# Nyrvo

A Go CLI that captures and compares execution environments, answering why an
application behaves differently across local, CI, staging, and production.

## Status

Early development. The commands listed under [Usage](#usage) are what works
today; the roadmap (automated drift diagnosis, more collectors) is not yet
available.

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

Run the full local checks with `make check` (test, race, vet, and gofmt).
Go 1.25 is the minimum supported version; development runs on 1.26. CI verifies
both.

## License

[MIT](LICENSE)
