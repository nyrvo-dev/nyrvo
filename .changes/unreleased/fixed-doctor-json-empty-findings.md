- `nyrvo doctor --json` emits `"findings": []` for a clean diagnosis instead of
  `"findings": null`, matching `nyrvo diff --json`. A consumer reading
  `findings.length` no longer has to special-case the healthy answer.
