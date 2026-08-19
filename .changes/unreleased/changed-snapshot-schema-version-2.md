- The snapshot schema version is now 2. The `unusable` field added in v0.2.0 is
  optional, but it changed what an existing shape means: a runtime absent from
  `runtimes` used to mean "not observed", and can now mean "installed, and it
  refused to report a version". A build that predates the field would read a
  newer document, ignore the key it does not know, and report that runtime as
  missing — the untruth the field exists to prevent. Bumping the version makes
  an older Nyrvo refuse the document instead of quietly misreading it. Snapshots
  written with schema 1 are still read normally.
