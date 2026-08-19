# Releasing Nyrvo

## The release mechanism is a git tag

Nyrvo is a Go module, so a tag is a release:

```
go install github.com/nyrvo-dev/nyrvo/cmd/nyrvo@v0.1.0
```

There is no build pipeline, no artifact upload, and no version constant in the
source. `nyrvo version` reads the module version and VCS revision the toolchain
embeds at build time, so a binary always reports what it actually is. A release
process that rewrites a source file to say `0.2.0` can be wrong; this one
cannot.

Pushing a tag also runs `.github/workflows/release.yml`, which cross-compiles
the binary for macOS, Linux and Windows, packages each one with the LICENSE and
the README, writes a `SHA256SUMS` over the archives, and attaches everything to
a GitHub Release. The build uses the checked-out tag and no version flag, so
the archives report exactly the version the tag says — the same answer
`go install` produces from the same tag. The release is created with the
runner's own `gh` CLI and the workflow's `GITHUB_TOKEN`; no third-party action
is handed the token, and the workflow can also be started by hand to rebuild a
release that went wrong without inventing a new tag. Artifacts are checksummed,
not signed.

## What the version number promises

Semantic versioning, applied to the things people actually depend on. **The
contract is:**

- command names and their arguments;
- flag names, their spelling, and which take values;
- exit codes (`0` success, `1` error, `2` usage);
- rule identifiers such as `runtime.requirement_unsatisfied` — scripts filter on
  them and issue trackers quote them;
- the snapshot document: field names and their meaning;
- the shape of every `--json` document.

**Not the contract**, and free to change in a patch:

- the wording of a report, a description, or a recommendation;
- the order findings print in, beyond "most serious first";
- which severity a rule assigns, when the evidence for that judgement improves;
- internal package layout.

`internal/` is genuinely internal: Go itself forbids importing it, so nothing
under it can be part of anyone's contract.

### The snapshot schema is versioned separately

`snapshot.SchemaVersion` is not the release version and never tracks it. It is
bumped only when a change would make an older Nyrvo misread a newer snapshot —
a renamed field, changed units, changed semantics. Purely additive optional
fields do not bump it, which is why several releases can share one schema
version. A snapshot carries its own `schema_version` so an old binary can refuse
a document it would misread rather than quietly misreading it.

### Before 1.0

Below `1.0.0`, a minor bump may break the contract above. That is what pre-1.0
means and it is stated here so nobody has to guess. Every such break is listed
under **Changed** or **Removed** in the changelog with what to do instead.

Reaching `1.0.0` is a decision to stop doing that, not a measure of how finished
the tool feels. It should happen once the commands have been used by people who
did not write them.

## Changelog fragments

The changelog is assembled from fragments so that concurrent pull requests
never edit the same lines of `CHANGELOG.md`. A PR that changes behaviour adds
one Markdown file under `.changes/unreleased/` named `<type>-<slug>.md`, where
`<type>` is one of the Keep a Changelog section names — `added`, `changed`,
`deprecated`, `removed`, `fixed` or `security` — and `<slug>` describes the
change. The filename carries the type, so the file needs no front matter. Its
body is the entry exactly as it should read in the changelog, starting with
"- " and wrapped at 80 columns like every entry in `CHANGELOG.md`. Two branches
never write the same lines, because each change owns its own file.

The assembler places each entry by its filename's type prefix and reads files
in filename order within a section, so the assembled changelog is
deterministic no matter which order the fragments were added. A fragment whose
filename has an unknown type prefix is a typo and an error: the assembler
refuses to run rather than silently dropping an entry from the release.

`tools/changelog.sh` is the assembler. It is a maintainer command run by hand
at release time and is never run by CI. `tools/changelog.sh preview` prints the
sections the next release will contain without changing anything;
`tools/changelog.sh release <version> <date>` splices them into `CHANGELOG.md`
under a new `## [<version>] — <date>` heading and deletes the consumed
fragments. It deliberately leaves the link definitions at the bottom of
`CHANGELOG.md` alone; that block is edited by hand.

## Cutting a release

1. `make check` — tests, race detector, vet, gofmt, golangci-lint. It must be
   clean; there is no "known failure" allowance.
2. Verify each new commit builds on its own. Every commit is expected to, so
   this catches the one that does not:

   ```
   for c in $(git log --format=%h --reverse <last-tag>..HEAD); do
     git worktree add -q --detach /tmp/nyrvo-rel $c
     (cd /tmp/nyrvo-rel && go build ./... && go test ./... >/dev/null) \
       || echo "$c FAILS"
     git worktree remove --force /tmp/nyrvo-rel
   done
   ```

3. Run the suite on the platform CI uses, not only on your own:

   ```
   docker run --rm -v "$PWD":/src -w /src golang:1.26 go test ./...
   docker run --rm -v "$PWD":/src -w /src golang:1.25 go test ./...
   ```

   `make check` on a laptop is not enough and has already proved it. A test that
   diagnosed a Linux fixture against a live capture passed on macOS, where the
   platforms disagree and findings appear, and failed on the Linux runner, where
   they match and the findings vanish. Nothing but running it on Linux would
   have caught that.

4. Run the commands the README documents against a real repository. Every
   serious defect this project has found was found this way and none were found
   by reading a diff.
5. Assemble the changelog from the fragments:
   `tools/changelog.sh release vX.Y.Z <date>` writes the unreleased entries
   into a new `## [vX.Y.Z] — <date>` section and deletes the fragments. Then
   update the link definitions at the bottom by hand: add the `[vX.Y.Z]` link
   and repoint `[Unreleased]` at the new tag. The script deliberately leaves
   that block alone.
6. Commit the changelog: `docs: release vX.Y.Z`.
7. Tag and push:

   ```
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin main --follow-tags
   ```

8. Watch the release workflow. The tag push triggers
   `.github/workflows/release.yml`, which cross-compiles the six platform
   binaries, packages them with the LICENSE and README, writes `SHA256SUMS`,
   and attaches everything to a GitHub Release pointing at the changelog. If
   it fails partway, re-run it from the Actions tab (`workflow_dispatch`),
   naming the same tag — no new tag is invented.
9. Confirm the module is fetchable, from a directory that is not the repository:

   ```
   go install github.com/nyrvo-dev/nyrvo/cmd/nyrvo@vX.Y.Z
   nyrvo version
   ```

   The proxy can take a minute. If `nyrvo version` reports the tag, the release
   is real; until then it is only a tag.

## Tags are permanent

Do not move or delete a published tag. The Go module proxy caches by version,
so a moved tag means two people with the same version string have different
code — the exact confusion this tool exists to diagnose. A bad release is fixed
by publishing the next one.
