# 0011 — Job logs are untrusted terminal output

## Context

A run's metadata says which job failed. Only the job's log says what actually
happened: the command that broke, the error it printed, and the runtime versions
the runner really installed — the gap ADR 0006 leaves open when it reads a
workflow file as a declaration.

Reading that log looked like the easy part of the milestone. Two facts found in
real logs say otherwise.

**Logs echo environment variable values.** Before running a step, the runner
prints the step's configuration, including its `env:` block:

```
2026-08-16T02:11:01.998Z   GH_AW_GITHUB_ACTOR: sergiou87
2026-08-16T02:11:01.998Z   GH_AW_GITHUB_REPOSITORY: cli/cli
2026-08-16T02:11:02.001Z bash: .../create_prompt_first.sh: No such file or directory
2026-08-16T02:11:02.003Z ##[error]Process completed with exit code 127.
```

Registered secrets are masked as `***`; everything else is printed in full. The
obvious implementation — keep the twenty lines before the error — captures that
block exactly, and would write environment values into a snapshot file, which
ADR 0003 exists to prevent.

**Logs are terminal output.** They carry ANSI escape sequences; `gh` refuses to
print them without `--allow-escape-sequences` precisely because a terminal
interprets them. Anything Nyrvo stores or prints from a log could otherwise move
a cursor, repaint a line, or hide text in a report a human is reading to make a
decision.

## Decision

A log is untrusted input, parsed rather than excerpted.

- **Structure decides what may be kept.** The runner's configuration echo lives
  between `##[group]` and `##[endgroup]`; command output lives outside those
  markers. A failure excerpt may only keep lines outside group blocks, plus
  `##[error]` lines. That rule is what keeps the `env:` block out, and it comes
  from the log's own structure rather than from pattern-matching for things that
  look like secrets.
- **Facts are extracted into fields, never copied as prose.** Runtime versions
  and the runner image are read from known lines into typed values. A handful of
  groups are allow-listed for this (`VM Image`, `Environment details`,
  `Installed versions`); their raw text is still never stored.
- **Everything is sanitized before it is stored or shown**: the byte-order mark,
  the per-line timestamp, and every ANSI escape sequence are stripped.
- **Excerpts are bounded**, so a runaway log cannot become a snapshot.

## Consequences

- `nyrvo ci import` can fill in what a run's metadata cannot: the versions that
  were really installed, and the error that actually stopped the job.
- The failure excerpt is smaller than what a human would copy out of the web UI.
  That is the trade: a smaller excerpt that cannot leak beats a fuller one that
  might.
- The parser is pinned to real recorded logs under `testdata/logs`, including
  one that genuinely contains environment values, so the leak test asserts
  against evidence rather than a hypothetical.
- Extraction depends on wording the Actions runner and the setup actions use
  today. When that wording changes Nyrvo will extract less, never something
  wrong — an unrecognized line is skipped, not guessed at.
