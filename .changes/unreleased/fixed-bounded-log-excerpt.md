- The failure excerpt of an imported job log is now bounded end to end. The
  excerpt rule capped the output lines before the first error, but then kept
  every `##[error]` line, so a step that printed one error per output line
  turned a 10MB log into 43,000 "Log:" notes and an 8MB snapshot. Only the
  first 50 error lines are kept now, and a note says how many more were
  dropped — a prefix that silently cut the rest would overstate what the log
  actually said.