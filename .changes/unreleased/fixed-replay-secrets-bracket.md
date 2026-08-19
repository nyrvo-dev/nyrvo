- Replay redacts GitHub Actions secret references written in bracket form
  (`secrets['TOKEN']`, `secrets["TOKEN"]`) the same way it already redacted
  the dot form, so those values print as `<secret>`.
