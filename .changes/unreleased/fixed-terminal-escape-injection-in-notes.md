- Terminal escape sequences can no longer reach a snapshot through CI metadata.
  A job log's output was already stripped of ANSI and other control bytes, but
  step names, job names, runs-on labels and container images were copied into
  `Source.Notes` verbatim — and those notes are printed by `nyrvo ci inspect`
  and embedded in the AI prompt. A workflow file could carry a container image
  like `node:20\x1b[2J` and emit a real clear-screen to the terminal. Every
  note a workflow file or a run's API contributes is sanitized now, so nothing
  a terminal would interpret instead of print survives into a snapshot.