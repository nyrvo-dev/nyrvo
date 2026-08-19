- A capture is refused when `.nyrvo` or `.nyrvo/snapshots` is a symbolic link.
  A repository can commit such a link, and every write of a capture — the
  `.gitignore` and each snapshot — would otherwise follow it to a directory
  outside the repository that the user never consented to touch.

- Terminal control bytes are stripped from every string a snapshot carries, on
  the way out as well as the way in. Collectors already sanitized what they
  store, but a snapshot is also a document Nyrvo reads: one from an older build,
  hand-edited, or sent by somebody else to diff. The first pass covered notes,
  services, requirements and runtimes and missed `Source.Ref` — which is
  workflow-derived and reaches the terminal inside a `doctor` recommendation and
  the `--ai` request — and the environment variable names the diff prints as
  keys. Enumerating the risky fields is what left that gap, so the whole document
  is stripped now.
