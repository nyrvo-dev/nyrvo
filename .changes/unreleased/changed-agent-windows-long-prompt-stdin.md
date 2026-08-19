- On Windows, AI agent prompts longer than the CreateProcess argument limit are
  passed on stdin with `-` as the final argv element instead of being truncated.
