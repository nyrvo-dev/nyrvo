- The snapshot loader now refuses documents too incomplete to describe an
  environment instead of answering confidently from them. `schema_version` must
  be present and positive, a snapshot must carry a name matching the file it is
  stored as, and the keyed collections (runtimes, requirements) must have their
  identity and no duplicate keys. `nyrvo diff empty empty` on a `{}` document —
  previously "No differences between  and ." — and `nyrvo doctor` against a
  schema-version-0 file now fail loudly. Snapshot files are also read through a
  1 MiB cap, so a hostile or generated file cannot exhaust memory at load.
