# 0012 — A constraint Nyrvo cannot read produces no finding

## Context

Reading what a project declares it needs — `engines.node`, a `go` directive,
`.nvmrc`, `.tool-versions` — gave Nyrvo something no comparison could: the
ability to say an environment is *wrong*, not merely different from another
one. That is the most valuable finding the tool can produce, and the most
dangerous.

It is dangerous because constraint syntax is an open world. npm alone accepts
comparators, caret and tilde ranges, hyphen ranges, wildcards, `||`
alternatives, `workspace:*`, git URLs and tags. `.nvmrc` accepts `lts/iron` and
`system`. Nyrvo reads these files from repositories it did not write, and every
new package manager invents another form. Any evaluator will meet input it does
not understand.

There are two ways to be wrong about such input, and they are not symmetric. A
constraint Nyrvo skips costs one missed hint. A constraint Nyrvo misreads
produces a **high** severity finding telling a developer their correct setup is
broken — and one of those is enough to make the whole tool something to be
ignored.

The same asymmetry appears with imprecise versions. A workflow asking for node
`"20"` does not name a version; it names a range the runner resolves. Judging
`"20"` against `^20.1` by padding it to `20.0.0` would convict a job that will
very likely install `20.11` and pass.

## Decision

Constraint evaluation answers **two** questions, and callers must respect both:

```go
func SatisfiesConstraint(constraint, observed string) (met, decided bool)
```

`decided` false means Nyrvo does not fully understand the constraint. The rule
then reports nothing at all. It never falls back to a string comparison, a
partial parse, or a "probably fine".

Understood: comparators (`>=`, `>`, `<=`, `<`, `=`), caret and tilde ranges,
wildcards (`20.x`), bare prefixes (`20`, `3.11.2`), conjunctions separated by
commas or spaces, and `||` alternatives. Declined: hyphen ranges, `lts/*`,
`workspace:*`, git references, and anything else.

Two consequences of that shape are deliberate:

- An unreadable alternative poisons a whole `||` constraint unless another
  branch is satisfied, because the unreadable branch might have been the
  satisfied one.
- An unreadable term does **not** rescue a conjunction whose other term
  decisively failed: nothing a second condition says can make `>=24` true for
  node 20.

A version that was declared rather than observed is evaluated across the entire
range it stands for — the constraint is checked at both ends of that range, and
disagreement between the ends means undecided. So a workflow asking for node
`"20"` is convicted by `>=24`, which fails at every 20.x, and acquitted by
`^20.1`, which some 20.x satisfies.

## Consequences

- Nyrvo will stay silent about real violations expressed in syntax it declines.
  That is the intended trade, and it is the same one ADR 0009 made about
  severity.
- Adding a syntax is a small, testable change: teach `satisfiesTerm` one more
  form and the finding appears. Nothing has to be un-learned first, because
  nothing was guessed.
- Constraints are read by a collector and judged by a rule, in separate
  packages. The reader stores them verbatim and never interprets them, so a
  change to what Nyrvo understands never invalidates a stored snapshot.
- Project requirements are never compared between environments. Both sides
  normally read the same checkout, so a diff of them would manufacture drift
  out of provenance; they exist to be enforced, not compared.
