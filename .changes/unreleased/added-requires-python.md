- `requires-python` in pyproject.toml's `[project]` table is read as a
  requirement source, so a project's declared Python floor is comparable
  against what a machine actually runs. PEP 621 puts it there, and it is where
  the standard, most-starred Python projects declare their floor — pyenv's
  `.python-version`, the file Nyrvo already reads, appears in almost none of
  them. The constraint is kept verbatim, including its own operators (">=3.11"
  or ">=3.11,<3.14"); it is recorded as a pin, not a floor, because it already
  says exactly what it means and an implicit "or newer" would silently discard
  an upper bound. The table match is exact, so `[project.optional-dependencies]`
  and `[project.urls]` are never mistaken for `[project]`. A project carrying
  both `.python-version` and pyproject.toml records both, since two files that
  disagree is itself worth seeing. No `[project]` table, no `requires-python`
  key, an empty value, or an unquoted value produces no requirement rather than
  a guess.
