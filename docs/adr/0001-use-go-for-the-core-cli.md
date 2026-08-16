# 0001 — Use Go for the core CLI

## Context

Nyrvo inspects environments: it shells out to `git`, `uname`, and language
runtimes, reads files, and must run on a developer laptop, inside a CI runner,
and inside minimal containers. Users install it before they trust it, so the
install step has to be a single binary rather than a runtime plus dependencies.

Namespaces are reserved on npm and crates.io, which invites building the core
elsewhere. That would be a distribution decision driving an implementation
decision.

## Decision

The core is a single-binary Go CLI. The standard library covers process
execution, JSON, and filesystem work, so the dependency surface stays near zero.

Reserved npm and crates.io namespaces stay reserved. They may later host an
installer, an integration package, or a types crate, but the core is not
duplicated in another language to justify them.

## Consequences

- Cross-compiled static binaries ship through GitHub Releases; no runtime to
  install.
- Contributors need only a Go toolchain (1.25 minimum).
- Platform-specific behavior must be isolated, since one binary now targets
  Linux and macOS (Windows progressively).
- A Node or Rust integration, if ever needed, wraps the binary or its JSON
  output rather than reimplementing it.
