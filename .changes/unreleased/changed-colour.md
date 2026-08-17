- `diff` and `doctor` use colour when they are writing to a terminal: bold
  headings, the severity word in red, yellow or dim, and the rule identifier in
  Nyrvo's purple. Nothing else is styled.

  The decision is made per writer, not per process. A pipe, a file, a CI log and
  a test all get exactly the bytes they got before, so anything parsing that
  output is unaffected. `NO_COLOR` and `TERM=dumb` disable it, and on Windows
  colour is offered only to terminals known to interpret escape sequences.

  Only lines that carry no tab are ever styled: the aligned columns are laid out
  by `text/tabwriter`, which counts raw bytes rather than display width, so an
  escape sequence on one of those lines would silently shift every column.