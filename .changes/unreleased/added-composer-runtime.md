- `composer --version` is captured as the `composer` runtime, so a machine's
  installed Composer can be compared across snapshots. Composer's version is
  independent of PHP's, and its resolver changed behaviour across majors: the
  same composer.json can install different dependency trees under Composer 1
  and Composer 2. Nothing a project declares pins the installed Composer, so
  the observation is the only record there is of the drift.
