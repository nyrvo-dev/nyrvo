# 0013 — Replay prints a plan and never executes it

## Context

"Run your CI locally" is the feature every tool in this space eventually grows,
and it is where most of them start lying. Executing a workflow's steps on a
developer's machine reproduces the commands and almost nothing else: not the
runner image, not the actions the steps depend on, not the secrets, not the
service containers, not the matrix. The result is a run that passes or fails for
reasons unrelated to CI while carrying the authority of having "reproduced" it.

Nyrvo already refuses this trade elsewhere. ADR 0006 chose to parse CI
configuration rather than replay it, precisely because a partial execution
misrepresents itself. M12 does not reverse that decision; it asks what can be
delivered without it.

## Decision

`nyrvo ci replay <job>` prints a plan and executes nothing. The package that
builds it does not import `os/exec` and has no way to run anything.

A plan lists every step in declaration order — a step is never dropped — and
marks each one it cannot reproduce with a reason the reader can act on:

- an action, because Nyrvo does not fetch or run actions;
- a command containing a `${{ ... }}` expression, whose value the runner
  decides;
- a setup action whose version input is itself an expression, which names
  nothing installable.

Three things the plan states because omitting them would mislead:

- **The matrix.** A matrix job is not one job. Its steps describe one
  combination, and the one a reader happens to reproduce is rarely the one that
  failed, so the combinations are named up front.
- **Prerequisites.** Service containers and a job container are conditions that
  must already hold, not steps to run.
- **Checkout.** The reader is standing in the checkout, so that step is marked
  as needing no action rather than reported as work to redo.

Environment values are printed as the workflow file writes them — they are
already visible in the repository — with one exception: a value referencing a
secret is replaced with `<secret>`, in any spacing or casing the file may use.
A plan is meant to be read and pasted, and nothing that reads like a resolved
secret belongs in something a user pastes.

## Consequences

- Nyrvo cannot tell you whether a job would pass here. It tells you what the
  job does and what stands between you and doing it, which is the part a person
  can act on.
- The plan is a pure function of the parsed workflow, so it is deterministic and
  testable without a runner, a network, or a daemon.
- If executing steps is ever added, it must be a separate, explicit command that
  states its own divergences. It must not become a flag on this one: a plan that
  might have run something is no longer a plan you can trust to have run
  nothing.
