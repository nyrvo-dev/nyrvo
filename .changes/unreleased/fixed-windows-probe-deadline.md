- External tools get longer to answer on Windows. The per-probe deadline was
  five seconds everywhere, and on Windows that is not enough: every observation
  the public runner feed has ever failed to measure was on `windows-latest` —
  `rustc --version` one day, `docker compose version` the next — while the Linux
  and macOS runners have never missed it once. Spawning a process is dearer
  there, and a cold image pays for it on the first call. The deadline is now
  fifteen seconds on Windows and unchanged elsewhere. Nothing was ever reported
  wrongly, because an expired probe is recorded as unmeasured rather than as
  absence, but a snapshot that keeps saying "I do not know" is worth less than
  one that waits a moment longer and finds out.
