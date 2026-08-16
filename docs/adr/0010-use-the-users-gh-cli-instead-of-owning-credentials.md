# 0010 — Use the user's gh CLI instead of owning credentials

## Context

Importing a workflow run needs the GitHub API, and the API needs a token. The
usual answer is to read `GITHUB_TOKEN` from the environment, or to add
`nyrvo auth login` and store a token somewhere under the user's home directory.

Both change what Nyrvo is. A local-first CLI that reads files and runs `git
--version` is something a developer can adopt without thinking. The moment it
holds a credential, it becomes software that has to be trusted differently:
where is the token kept, what scopes does it ask for, what happens on a shared
machine, what does it send and to whom.

There is also a plain fact: anyone importing a GitHub Actions run already has
`gh` installed and authenticated, with scopes they chose.

## Decision

Nyrvo shells out to the user's `gh` CLI. It never reads a token, never stores
one, and never asks for one.

- Run data is fetched with `gh api <path>`, executed as an argument vector with
  no shell.
- The repository is resolved by `gh`'s own `{owner}/{repo}` placeholder rather
  than by a second lookup.
- Everything interpolated into an API path is validated first: a run id must be
  digits, a run URL must match a strict pattern. A reference that does not match
  is rejected, never sanitized — quietly repairing a malformed reference would
  send a request the user did not ask for.
- When `gh` is missing, the error says how to install it. When `gh` fails, its
  own message is surfaced: "not authenticated" or "no such run" from `gh` is
  more useful than anything Nyrvo could infer from an exit code.

The same principle already governs the planned AI adapters: prefer the agent CLI
a user has configured over owning the integration.

## Consequences

- No credential storage, no scope negotiation, no token refresh, no secrets in
  Nyrvo's threat model.
- Importing a run requires `gh`. That is a real dependency, declared honestly at
  the point of use rather than hidden behind a token prompt.
- The API surface Nyrvo touches is whatever the user's own token can already
  reach; Nyrvo cannot widen it.
- Fetching and parsing stay separate, so every parser test runs against recorded
  API responses with no network and no token. The responses in
  `internal/ci/githubactions/testdata/runs` are real, recorded once, and are the
  contract the parser is tested against.
