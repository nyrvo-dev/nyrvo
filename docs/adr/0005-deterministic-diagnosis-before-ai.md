# 0005 — Deterministic diagnosis before AI

## Context

"Why does CI fail when my laptop passes?" is a question an LLM answers
plausibly and unreliably. It is tempting to reach for one early, because the
deterministic path — collect evidence, normalize it, compare it, encode the
rules — is more work.

Nyrvo's value is the evidence. If the evidence is good, most of the common
answers (a Node version below what `engines.node` requires, a variable missing
in CI) follow from rules, not from inference.

## Decision

`capture`, `diff`, and the future `doctor` are deterministic and offline. They
make no network call and invoke no agent.

AI arrives later as an additive layer behind an explicit `--ai` opt-in, using an
agent the user already has installed, and its output is labelled distinctly from
deterministic analysis so inference is never presented as fact.

No empty abstractions are created now to prepare for it. The only requirement on
today's code is that it does not make that layer hard to add: evidence is
already collected, sanitized, and structured.

## Consequences

- Nyrvo works offline, without an account, and without an API key.
- Every diagnostic rule is testable and reproducible; no test may depend on an
  LLM.
- Nyrvo is not coupled to an AI vendor, and never owns AI credentials.
- The deterministic rule set has to be built even though an LLM could
  approximate it. That is the point.
