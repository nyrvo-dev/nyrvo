- .NET is observed: `dotnet --version` is captured as the `dotnet` runtime, and
  `global.json` is read as a requirement source. `actions/setup-dotnet` is
  recognized in a workflow, so a .NET job's declared SDK is comparable against
  the machine — without it the constraint could be stored but never checked.

  `sdk.version` is recorded as a floor, not a pin. The .NET resolver rolls
  forward to a newer SDK under every policy except `rollForward: "disable"`,
  which is the one spelling recorded as a pin. Treating it as a pin everywhere
  would report every machine with a newer SDK as broken, which is the normal
  arrangement rather than drift.