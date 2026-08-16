# 0008 — Partial observations cannot testify to absence

## Context

The first working `nyrvo diff local ci` produced a correct report that was
unusable: sixty-odd lines reading `HOME … missing in ci`, `TERM … missing in
ci`, one per variable on the developer's shell, burying the three findings that
mattered.

Nothing was wrong with the comparison. The inputs were asymmetric. A local
capture enumerates the entire environment; a workflow file states only the
variables the job sets, and says nothing about the hundreds the runner adds. The
diff was reading "not in this list" as "not in that environment", which is only
valid when the list is complete.

The same asymmetry appeared per section: a CI snapshot has no git section at
all, so `sha`, `branch`, and `dirty` each rendered as missing — a report that
reads as if CI had been inspected and found on a different commit, when the
workflow file simply never mentions git.

## Decision

Absence is only evidence when the source could have reported presence.

Two mechanisms implement that:

- `Environment.Partial` marks a list known to be incomplete. Every CI-derived
  environment is partial — including an empty one, because a job that declares
  no variables still runs with plenty. When either side is partial, the diff
  suppresses absences that side could not have reported and sets
  `Result.PartialEnvironment` so the narrowing is visible.
- A section one side never described at all yields **one** difference with an
  empty key ("described in local, not described in ci") rather than one per
  field.

Narrowing is never silent. Renderers print the partial-environment note whether
or not any difference was found, because a report that looks exhaustive while
skipping variables is worse than one that is merely noisy.

## Consequences

- `nyrvo diff local ci` is readable: the real drift is the whole output.
- Nyrvo cannot tell you that CI lacks a variable your machine has. That is
  honest — the workflow file does not know either. Importing a real run's
  environment (roadmap M9) is what would answer it.
- Any future source that observes partially (a remote agent, an imported run,
  a container introspected from outside) sets the same flag and inherits the
  same treatment.
- `partial` and `partial_environment` are additive optional fields, so the
  snapshot schema version does not change.
