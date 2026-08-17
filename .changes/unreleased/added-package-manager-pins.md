- `packageManager` in package.json is read as a requirement source, so a
  project that pins the package manager corepack must run can be compared
  against what a machine actually has. The value is `npm`, `pnpm` or `yarn` at
  an exact version, and the `+sha512.…` integrity suffix corepack records is
  stripped before it is stored. It is recorded as a pin, not a floor: corepack
  downloads and runs precisely that version and refuses anything else, so a
  machine with any other version genuinely cannot build the project. A package
  manager name Nyrvo does not understand, or a malformed value, produces no
  requirement rather than a guess.

- `pnpm --version` and `yarn --version` are captured as the `pnpm` and `yarn`
  runtimes, and `pnpm/action-setup` is recognized in a workflow, so a CI job's
  pinned package manager is comparable against the machine — without the
  observations a packageManager requirement could be stored but never checked.
