- `nyrvo doctor` and `nyrvo ci import` animate a spinner while they wait on the
  network or on an AI agent, so a fetch that takes minutes no longer looks like
  a hang. The label says what the wait is for — `importing run 12345`, or the
  agent's name — and a spinner that has been running longer than two seconds
  shows how long: `⠹ claude · 47s`. It appears only when the destination is a
  terminal: a pipe, a file and a CI log receive exactly the bytes they received
  before, down to the last carriage return, and the `--json` document on stdout
  stays parseable.
