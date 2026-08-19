- Loading a snapshot is capped at 1 MiB, and a document whose
  `schema_version` is missing or whose `name` disagrees with the requested
  snapshot is refused rather than trusted as evidence.
