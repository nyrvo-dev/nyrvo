- A runtime that is installed but refuses to report a version is no longer
  reported as "missing". `nyrvo diff` prints "installed, not usable" for that
  side, `diff --json` carries `a_unusable`/`b_unusable` flags plus a top-level
  `unusable` so a consumer can tell "not installed" from "installed, refused",
  and the AI evidence document lists the refused runtime instead of letting an
  agent read absence while the finding below it says the opposite. This is the
  deterministic counterpart to `unmeasured` from ADR 0017: unlike an unmeasured
  probe, a refusal is not dropped — it is usually the very drift being sought.
  The kernel version is also compared symmetrically now: a workflow-derived
  snapshot cannot state one, so a one-sided kernel no longer prints "kernel: ci
  missing" on every local-vs-CI diff.
