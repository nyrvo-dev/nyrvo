# 0016 — A configured agent runs, and the config file is user-level only

Amends [ADR 0014](0014-nyrvo-gathers-the-evidence-your-agent-reasons.md).

## Context

ADR 0014 said `nyrvo doctor --ai` builds an analysis request, prints it, and
runs nothing. That was written before adapters existed, when `--ai` had no
agent to run. ADR 0015 then added `--agent=<name>`.

Typing the agent on every invocation is friction, and the project has always
intended a configured default:

```bash
nyrvo config set ai.agent opencode
nyrvo doctor --ai
```

That reopens the question 0014 answered. If a stored setting makes `--ai`
execute something, then a user who configured an agent months ago types a flag
whose behaviour they cannot see in the command line, and evidence leaves their
machine.

## Decision

**A configured agent runs.** `--ai` with `ai.agent` set invokes that agent
exactly as `--agent=` would, disclosure included. Writing the config *is* the
opt-in; it is a deliberate act, and requiring it to be repeated on every command
would be asking the user to keep agreeing to a decision they already made.

What keeps this honest is the disclosure, which was already required before any
agent runs and now carries more weight: it names the agent, states the data, and
prints the exact command **before** execution. `nyrvo config list` shows the
setting and where it lives. An explicit `--agent=` on the command line overrides
the configured one.

**The config file is user-level only**, at `os.UserConfigDir()/nyrvo/config.json`.
There is deliberately no project-level file, and this is a security decision
rather than a simplification: this file selects a program Nyrvo will execute. A
config file living in a repository would let whoever opens a pull request choose
that program, which is the same argument that keeps an arbitrary agent command
out of the tool entirely (ADR 0015). Someone will eventually offer to add
per-project config as a convenience; the answer is here.

The document is decoded into a struct holding exactly the keys Nyrvo knows, and
dotted CLI keys map to fields through an explicit table. No reflection, no path
traversal, no map walked blindly — the same rule the collectors follow for
untrusted files, applied to a file that decides what runs.

A malformed config is an error rather than an empty config. This is the opposite
of how a malformed `package.json` is treated, and the difference is the point:
there the file is untrusted repository input and one broken source must not cost
the capture, while here the file is the user's own stated intent, and ignoring
it silently would run a different agent than they asked for.

## Consequences

- `--ai` no longer has one fixed meaning; it means "use the AI layer", and
  whether that prints or executes depends on a setting the user made. The
  disclosure is what makes that safe, so it must never become skippable — no
  `--quiet` may ever suppress it.
- 0014's core claim survives unchanged: Nyrvo still calls no model itself, owns
  no credentials, and picks no vendor. What changed is where the user says which
  of *their* tools to use.
- A user who wants the printed request while a default is configured passes
  `--agent=none`.
- Adding a second setting is a line in the key table. Adding a setting that
  changes what executes deserves its own ADR.
