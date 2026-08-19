- A runtime listed as unusable is visible to requirement and
  `runtime.unusable` rules even when the other side is a partial runtime
  list, instead of disappearing because `MarkUnusable` does not append a
  Runtime entry.
