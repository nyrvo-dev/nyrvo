- A git probe that runs out of time is no longer reported as "not a repository".
  `git status` hanging on a cold filesystem made the collector return the raw
  deadline error, capture marked the section failed, and the snapshot carried no
  git section at all — so a diff reported "git described in A, not described in
  B" for a machine whose git probe merely timed out, the same invented drift
  ADR 0017 records for npm, docker compose, and dotnet. A timed-out probe now
  records the facts it could not read (`git.sha`, `git.branch`, `git.dirty`) in
  the snapshot's `unmeasured` list and leaves the section absent, so the diff
  drops those keys instead of treating silence as absence. A directory that is
  not a work tree and a repository with no commits are still genuine answers and
  are unchanged.
