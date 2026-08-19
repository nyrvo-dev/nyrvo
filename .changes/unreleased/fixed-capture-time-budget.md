- A capture now bounds its own total running time. Every external tool already
  had its own deadline, but nothing bounded their sum: a capture spawns up to 24
  processes, so a machine where each one hangs — a stalled network filesystem, a
  wedged Docker daemon — spent the total of every deadline before returning,
  roughly two minutes here and six on Windows, with a spinner turning and no
  indication it would ever stop. The budget is a minute (three on Windows, where
  every probe is allowed three times as long), far above a healthy capture's two
  seconds. A capture that exceeds it produces no snapshot and says which
  collector it gave up in: the collectors that never ran would leave their
  sections absent, and absence in a snapshot means "looked for and not found",
  so saving a partial capture would report the machine as lacking every tool the
  capture never reached.
