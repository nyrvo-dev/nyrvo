# 0009 — Severity is about plausible cause, not difference size

## Context

Once Nyrvo could compare environments it could also rank what it found, and the
tempting rule is "bigger difference, higher severity". That produces a tool
people stop reading. A developer on macOS whose CI runs Linux gets `os` flagged
in red on every single run — a difference that is real, expected, and almost
never the answer.

The opposite failure is worse. `actions/setup-go` with `go-version: "1.26"`
installs the latest 1.26.x. Comparing that literally against a machine running
1.26.6 reports a mismatch on a correctly configured project. A diagnostic that
cries wolf on a correct setup is not merely noisy; it trains its user to ignore
the one time it is right.

## Decision

Severity answers one question: **how plausibly does this difference explain a
failure?**

- **high** — something declares a requirement the other environment does not
  meet. A major runtime version gap belongs here.
- **medium** — the difference can change behaviour and deserves a look, but
  nothing observed proves it breaks anything: a missing runtime, a missing
  variable, two different commits.
- **low** — real, and usually just how things are: a different OS, a different
  architecture, a patch-level version gap, a dirty working tree.

Two rules follow from it:

1. A declared version is treated as a prefix. `Satisfies("1.26", "1.26.6")` is
   true, and no finding is produced. Matching is per segment, so `"2"` is not
   satisfied by `"20"`.
2. A rule that cannot distinguish "different" from "wrong" reports **low** and
   says so in its description, rather than inflating severity to be noticed.

A clean run states that no rule matched — never that the environments are
equivalent. Nyrvo's rule set is small, and the output must not imply coverage
it does not have.

## Consequences

- The common correct configuration produces a quiet report, which is what makes
  a loud one meaningful.
- Nyrvo will miss things a stricter tool would flag. That trade is deliberate:
  a false negative costs one missed hint, a false positive costs the user's
  trust in every future run.
- Severity is a judgement about causation, so it belongs to rules, not to the
  diff. The diff stays pure evidence and a user who disagrees with a rule can
  still trust the comparison underneath it.
- New rules must justify anything above low.
