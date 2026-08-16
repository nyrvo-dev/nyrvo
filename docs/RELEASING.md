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

Prebuilt binaries for people without a Go toolchain are a later decision. They
are worth adding when someone asks, and not before — every artifact published is
an artifact that has to be signed, hosted and kept in step with the tags.

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

3. Run the commands the README documents against a real repository. Every
   serious defect this project has found was found this way and none were found
   by reading a diff.
4. Move `[Unreleased]` in `CHANGELOG.md` into a version section with today's
   date, and update the link definitions at the bottom.
5. Commit the changelog: `docs: release vX.Y.Z`.
6. Tag and push:

   ```
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin main --follow-tags
   ```

7. Confirm the module is fetchable, from a directory that is not the repository:

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
