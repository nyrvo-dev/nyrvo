# Security Policy

## Reporting a vulnerability

Report privately through GitHub: open the **Security** tab of this repository and
choose **Report a vulnerability**. That creates a private advisory only the
maintainers can see.

Please do not open a public issue for a vulnerability, and do not disclose it
elsewhere until a fix is released.

Include what you would want if you were fixing it: what Nyrvo does that it
should not, the smallest input or environment that reproduces it, and the
version (`nyrvo version`).

You should get an acknowledgement within a week. If a report is valid, the fix
and the advisory are published together, and you are credited unless you ask not
to be.

## Supported versions

Nyrvo is before `1.0.0`. Only the latest released version is supported: fixes
land in the next tag rather than being backported. See
[docs/RELEASING.md](docs/RELEASING.md).

## What Nyrvo already refuses to do

These are not aspirations. Each is a decision with an ADR behind it, enforced in
code and covered by tests, and each is worth knowing before you go looking for a
weakness.

- **Environment variable values are never recorded.** Only names. A snapshot
  cannot leak a credential because it has no field able to hold one
  ([ADR 0003](docs/adr/0003-never-store-environment-values.md)).
- **No shell, ever.** External tools are run with an argument vector through
  `collector.Run`, so nothing read from a repository or an environment can be
  interpreted as a command.
- **No credentials of its own.** Importing a CI run uses your `gh` CLI; Nyrvo
  never reads, stores or asks for a GitHub token. The AI layer runs an agent CLI
  you already authenticated, so Nyrvo holds no API keys for any vendor.
- **Nothing leaves the machine unasked.** `nyrvo doctor` never contacts anything.
  The AI layer is opt-in, and states which agent it will run and the exact
  command before running it.
- **An analysis request is a chosen subset.** Home directory prefixes become
  `~`, and environment variable names are narrowed to the ones a finding or a
  difference actually refers to
  ([ADR 0014](docs/adr/0014-nyrvo-gathers-the-evidence-your-agent-reasons.md)).
- **Agents run in an empty temporary directory**, so an agent CLI with file tools
  cannot go reading the machine instead of the evidence it was given
  ([ADR 0015](docs/adr/0015-adapters-run-the-users-own-agent-in-an-empty-directory.md)).
- **Configuration is user-level only.** The config file selects a program Nyrvo
  executes, so there is deliberately no project-level config: a copy living in a
  repository would let whoever opens a pull request choose that program
  ([ADR 0016](docs/adr/0016-a-configured-agent-runs-and-the-config-is-user-level.md)).
  For the same reason an arbitrary command cannot be configured as an agent —
  only the known ones.
- **Container names and labels are never recorded.** `docker ps` reports the
  whole machine, and its labels carry the absolute paths of your unrelated
  repositories.
- **Secrets in a workflow are redacted.** A value referencing `secrets.*` prints
  as `<secret>` in a replay plan, in any spacing or casing the file uses.
- Snapshots are written `0600`, the config file `0600`, their directories `0700`.

## What is not a vulnerability

- **A snapshot contains environment variable names.** That is the design, and it
  is what makes "REDIS_URL is missing in CI" possible. Names are not values, but
  they do describe a machine — treat a snapshot as you would any diagnostic
  output before pasting it somewhere public.
- **Nyrvo runs the agent CLI you configured.** Executing your own installed tool
  is the feature. What would be a bug is executing one you did not choose, or
  executing it without saying so first.
- **Nyrvo runs `git`, `docker`, `gh` and language binaries found on `PATH`.** A
  `PATH` an attacker controls is a machine already compromised, and no tool that
  observes an environment can defend against its own environment lying to it.
- **A report is wrong about your setup.** That is a correctness bug and belongs
  in a normal issue. It is only a security matter if the wrongness leaks
  something.

## If you are contributing

The decisions above are load-bearing. A change that crosses one of them needs an
ADR arguing the reversal, not a flag that opts out of it — see
[CONTRIBUTING.md](CONTRIBUTING.md). The three that get proposed most often, and
the answer to each:

- *"Let people configure a custom agent command."* No. It reads as flexibility
  and works as "make Nyrvo run this program", in the one command that sends
  evidence off the machine.
- *"Support a per-project config file."* No, same reason: a pull request would
  then be able to choose what runs on your machine.
- *"Store environment values behind a flag."* No. A field that can hold a
  credential is a field that will end up in a bug report.
