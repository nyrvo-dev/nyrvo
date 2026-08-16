# 0015 — Adapters run the user's own agent, in an empty directory

## Context

ADR 0014 settled that `nyrvo doctor --ai` builds an analysis request and runs
nothing. The obvious next step — actually getting an answer — is where a tool
usually acquires an API key, a vendor, and a bill.

There is a better option available, and it is already installed: the user's own
agent CLI. They chose it, they authenticated it, they know what it costs, and
they already trust it with this repository. Nyrvo shelling out to it owns no
credentials, stores no keys, and picks no vendor.

That raises a different problem. These CLIs are not model endpoints; they are
coding agents with file and shell tools. Handing one a prompt inside the user's
repository invites exactly what the project's design forbids: an agent going off
to inspect the machine on its own, producing conclusions from evidence Nyrvo
never gathered, never sanitized, and never showed the user.

## Decision

`nyrvo doctor --ai --agent=<name>` runs the named CLI, with the request as an
argument vector, never through a shell.

The command lines are what each tool documents, verified by running them rather
than by assuming:

```
claude    claude -p <request>
codex     codex exec --skip-git-repo-check <request>
opencode  opencode run <request>
```

Three constraints hold for every adapter:

- **The child runs in an empty temporary directory.** This is the vendor-neutral
  enforcement of "Nyrvo gathers the evidence, your agent reasons about it": an
  agent that goes looking finds nothing, so the answer can only come from the
  request. It needs no per-vendor flag and cannot be defeated by a tool growing a
  new capability.
- **The adapter is given a string, not an `Input`.** The package that runs
  agents does not import the package that builds requests, so it has no access
  to a snapshot and cannot send anything but the prompt Nyrvo already showed.
- **Stdin is empty.** `codex exec` reads stdin when it is not a terminal; a child
  inheriting Nyrvo's stdin would consume input meant for something else.

Before anything runs, Nyrvo states which agent it will invoke, what data the
request carries, and the exact command — with the request argument elided,
because it was already available in full. It does not claim the exchange is
local or external: that depends on how the user configured their own tool, and
Nyrvo does not know.

The answer is printed under `AI ANALYSIS — <agent>`, verbatim, marked as model
inference. Nyrvo does not parse it, rank it, or merge it into its findings.

## Consequences

- Nyrvo works with agents that did not exist when it was written, and stops
  working with none of them when a vendor changes its pricing.
- The three command lines are a compatibility surface Nyrvo does not control. A
  CLI that changes its non-interactive invocation breaks that adapter, and the
  fix is a one-line table edit — but it must be re-verified against the tool, not
  guessed.
- An agent's answer costs the user's own quota, on their own account. That is
  the right place for it, and it is another reason the flag is explicit.
- Running in an empty directory means an agent cannot look at the code that
  failed. That is the trade: it answers from the evidence Nyrvo vouches for, or
  it says it cannot.
