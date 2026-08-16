# Contributing to Nyrvo

## Before you write code

Run `make check` — tests, race detector, vet, gofmt and golangci-lint. It must
pass before and after your change. Go 1.25 is the minimum supported version;
development runs on 1.26, and CI verifies both.

Nyrvo has one dependency (`gopkg.in/yaml.v3`) and intends to keep it that way. A
pull request that adds a dependency needs to explain what it does that the
standard library cannot.

## What Nyrvo will not do

These are decisions, not omissions, and each has an ADR in `docs/adr`:

- It never stores environment variable **values**, only names.
- It never runs a workflow's steps. `nyrvo ci replay` prints a plan.
- It never sends anything anywhere on its own. `nyrvo doctor --ai` prints a
  request; `--agent=` runs a CLI you already installed and authenticated.
- It never presents model output as observed fact.

A change that crosses one of these needs its own ADR arguing the reversal, not a
flag that quietly opts out of it.

## Comments

Comments say **why**, never what. A comment that restates the code it sits on
will be asked for removal. The ones worth writing explain a decision a later
reader would otherwise undo — a special case that looks wrong until you know
what broke without it.

## Commits

Conventional Commits, and every commit must build and test on its own. Squashing
a broken intermediate state into a working one is fine; pushing a series where
`git checkout <sha> && go test ./...` fails in the middle is not.

## Adding an AI agent adapter

Nyrvo supports `claude`, `codex` and `opencode`. Adding another is deliberately a
small change: a line in the table in `internal/agent/agent.go`.

```go
"yourtool": {name: "yourtool", argv: []string{"yourtool", "run", "--quiet"}},
```

The rules that make an adapter acceptable:

1. **Verify the invocation by running it, and say so in the pull request.** Paste
   the `--help` output or the command you ran. Every entry in that table was
   probed on a real machine, and two of the three behaved differently from what
   the documentation implied.
2. **It must be non-interactive.** The CLI has to accept a prompt as an argument
   and print an answer to stdout without a TTY, a pager, or a confirmation.
3. **It must need no credentials from Nyrvo.** The user authenticates their own
   tool. Nyrvo will not read an API key, store one, or ask for one.
4. **It must work from an empty directory.** Adapters run in an empty temporary
   directory so an agent with file tools cannot go inspect the user's machine
   instead of reading the evidence. If your tool refuses to start outside a
   repository, find its equivalent of `--skip-git-repo-check`; if it has none, it
   is not a candidate.
5. **Add it to `TestCommand`** in `internal/agent/agent_test.go` with its exact
   argument vector.

What will not be accepted is a way to configure an arbitrary command as an
agent. It reads as flexibility and works as "make Nyrvo run this program,"
configured from a file, in the one command that sends evidence off the machine.
