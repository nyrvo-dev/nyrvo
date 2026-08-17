- `nyrvo capture` animates a spinner beside the collector it is currently
  running, so a slow external probe no longer looks like a hang. It appears only
  when the destination is a terminal: a pipe, a file and a CI log receive exactly
  the bytes they received before, down to the last carriage return, and
  `NO_COLOR`, `TERM=dumb` and the legacy Windows console all turn it off through
  the same check that governs colour. With `--json` it follows the progress
  stream to stderr, so the document on stdout stays parseable.
