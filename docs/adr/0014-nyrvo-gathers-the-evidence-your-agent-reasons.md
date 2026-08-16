# 0014 — Nyrvo gathers the evidence, your agent reasons about it

## Context

Every tool in this space eventually grows an "explain it with AI" button, and
the button usually does one of two things: it ships the user's machine to a
vendor the tool chose, or it prints model output next to observed facts in the
same typeface. Both are the same mistake in different clothes — the tool
spending trust it did not earn.

Nyrvo's whole claim is that its answers are checkable. A rule has an identifier,
a severity, and evidence behind it, and a user who disagrees can read the diff
underneath. A model's answer has none of that. Putting the two under one heading
would not improve the model's answer; it would demote the deterministic one.

## Decision

`nyrvo doctor --ai` builds an analysis request, prints it, and runs nothing.

No agent is selected, no command is executed, no network call is made, and the
package that builds the request cannot make one. The user pastes the request
into whatever agent they already use and trust.

Three rules follow from that and are enforced in code rather than in
documentation:

- **Opt-in, always.** `nyrvo doctor` alone must never hand an environment to
  anything. `--ai` is the flag that makes the choice visible in the command the
  user typed.
- **The request is a chosen subset, not a dump.** A snapshot has no field able
  to hold an environment variable's value, so a request cannot carry one. On top
  of that, the home directory prefix of a runtime path is replaced with `~`, and
  environment variable names are reduced to the ones a difference or a finding
  actually refers to. A machine's full variable list describes what is installed
  on it and who works there; the handful behind a finding is the evidence.
- **The deterministic report comes first, in full, under its own heading.** The
  AI section announces itself as a *request* — nothing has been analysed by a
  model — and says in the same breath that no agent ran and nothing left the
  machine.

The request is also what a future adapter will send. Building it as a document
first means the adapters, when they arrive, choose a transport and nothing else:
what they may send is already decided here.

## Consequences

- `--ai` today produces text, not an answer. That is less than the flag name
  promises and more than Nyrvo can honestly deliver on its own.
- Nyrvo owns no API credentials and is coupled to no vendor. It also cannot be
  blamed for a model's answer, because it never asked one.
- Under `--json`, the request document replaces the diagnosis document rather
  than following it: two JSON documents on one stream is not something a
  consumer can parse, and the request already carries the findings.
- When adapters arrive, they must state which agent runs, what command is
  invoked, and whether it is local or external, *before* running it. A flag that
  quietly executes something is a different feature from this one and must be
  spelled differently.
