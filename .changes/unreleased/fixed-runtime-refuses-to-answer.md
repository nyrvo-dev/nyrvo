- A runtime that is installed but will not report a version is no longer
  reported as a runtime that is missing. A project pinning a toolchain the
  machine does not have — a `global.json` naming an SDK that is not installed, a
  `rust-toolchain.toml` naming an unknown toolchain, an rbenv version with no
  matching install — makes `dotnet --version`, `rustc --version` and
  `ruby --version` exit without answering, even though the binary is on PATH and
  the runtime is genuinely there. `doctor` said "ci has dotnet installed, but
  local does not" about a machine carrying .NET, and recommended installing it;
  it now reports `runtime.unusable`, says the runtime is installed but would not
  report a version, and points at the pin.

  Snapshots record such runtimes in `unusable` as `component.key` only, never
  the tool's error text: the real messages embed the user's absolute paths, and
  a snapshot is pasted into bug reports. Unlike `unmeasured`, a refusal is
  deterministic and is reported rather than skipped.