# AGENTS.md

Instructions for agents and contributors working on Nyrvo. Read this before
changing code.

## What Nyrvo is

A local-first Go CLI that captures an execution environment as a versioned JSON
snapshot and compares snapshots semantically, so a developer can see why
something works locally and fails in CI.

It is not a CI platform, a container runtime, or an observability service.

## Validation

Run before proposing any change:

```
go test ./...
go test -race ./...
go vet ./...
gofmt -l .          # must print nothing
git diff --check
```

`make check` runs the first four. `make lint` runs golangci-lint when it is
installed. Minimum Go is 1.25; development and CI primary version is 1.26.

## Architecture

```
Collector -> Snapshot -> Diff -> (later) Finding -> Doctor
```

| Package | Role |
| --- | --- |
| `internal/snapshot` | the core contract: snapshot shape, canonical JSON, on-disk store |
| `internal/collector` | `Collector` interface, `ErrUnavailable`, the external-command helper |
| `internal/collector/*` | one collector per observed area (system, git, runtime, docker and its running containers, requirements, env) |
| `internal/capture` | runs collectors, tolerates unavailable ones, assembles the snapshot |
| `internal/ci/githubactions` | parses workflow files and derives the environment a job declares |
| `internal/finding` | what a diagnosis is: rule ids, severities, findings |
| `internal/diagnostic` | deterministic rules that turn differences — or a project's own declared requirements — into findings |
| `internal/diff` | semantic comparison of two snapshots |
| `internal/analysis` | builds the evidence document an AI agent is given, and the prompt it reads |
| `internal/agent` | runs the user's own agent CLI; takes a string, so it cannot send more than the prompt |
| `internal/output` | terminal and JSON rendering |
| `internal/cli` | commands, flags, exit codes |

Domain packages never print. Rendering happens only in `internal/output`.

## Rules that are not negotiable

1. **Never store environment variable values.** Names only — not truncated, not
   hashed, not in error messages. See `docs/adr/0003`.
2. **Never run a shell.** External tools go through `collector.Run`, which uses
   `exec.CommandContext` with an argument vector and a timeout.
3. **A missing optional tool is not a failure.** Return an error wrapping
   `collector.ErrUnavailable`; the section is recorded as absent and the capture
   continues.
4. **Snapshots are deterministic.** Two captures of an unchanged machine must
   serialize to identical bytes. Sort collections; never let timestamps or
   collector ordering create drift.
5. **Never claim knowledge a source does not have.** CI configuration is
   parsed, never executed: an expression or version range is reported as
   unknown, not guessed. An incomplete observation (`Environment.Partial`)
   cannot testify to absence, and any narrowing of a comparison must be
   visible in the output. See `docs/adr/0006` and `docs/adr/0008`.
6. **Deterministic behavior stays independent of AI.** No LLM, network call, or
   agent invocation may be part of `capture`, `diff`, or a future `doctor`
   without an explicit `--ai` opt-in.
7. **Shared contracts change deliberately.** CLI commands, flags, exit codes,
   JSON output, and the snapshot schema are public once released. Changing the
   snapshot's meaning requires bumping `snapshot.SchemaVersion` and an ADR.

## Testing expectations

A feature is implementation **plus** tests **plus** validation in the same
change. A bug fix adds the regression test that would have failed before it.

Prefer table-driven unit tests over mocks. Cover the boring failure paths: tool
unavailable, malformed output, invalid input, cancelled context, filesystem
error, missing optional field. No test may depend on the network or an LLM.

Diff behavior that must always hold (see `internal/diff/diff_test.go`):
identical snapshots produce nothing; differing timestamps produce nothing;
different collection order with equal content produces nothing; an absent
section never panics.

## Exit codes

`0` success (finding differences is success) · `1` operational error ·
`2` usage error.

## Adding a collector

1. New package under `internal/collector/<area>`.
2. Implement `Name()` and `Collect(ctx, *snapshot.Snapshot)`.
3. Fill only your own section; never clear another.
4. Return `ErrUnavailable` when there is nothing to observe here.
5. Add tests for available, unavailable, malformed output, and cancelled
   context.
6. Register it in `defaultCollectors()` in `internal/cli/cli.go`.

Adding a field to the snapshot is a contract change: justify it, keep it
optional, and update the diff and its tests in the same change.

## Adding a diagnostic rule

1. Pick or add a stable rule id in `internal/finding`. Ids appear in JSON and in
   scripts; renaming one is a breaking change.
2. Append a `Rule` to the right group in `internal/diagnostic/rules_*.go`.
3. Drive it from `Input.Diff` where the diff already has the answer; reach into
   the snapshots only for context the diff dropped (`Declared`, `Partial`,
   dirtiness).
4. Severity answers "how plausibly does this explain a failure?", not "how big
   is this difference?" — see `docs/adr/0009`. Anything above `low` needs a
   justification.
5. Never fire on a correct configuration. A declared version is a prefix:
   `go-version: "1.26"` is satisfied by `1.26.6`.
6. Test the firing case, the non-firing case, and the nil/empty case.

The requirement family (`rules_requirement.go`) is the exception to "compare
two snapshots": it judges one environment against what the project declared
(`RequirementRules`, always `high`), and a constraint it cannot fully parse must
stay silent rather than guess.

## Security review triggers

Environment variables, filesystem writes, process execution, Git, Docker, CI
configuration, network, credentials. Review for command injection, path
traversal, secret leakage, unsafe temp files, and sensitive logging.
