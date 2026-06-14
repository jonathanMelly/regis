# regis

> *régisseur* — the French backstage director who calls every cue.

One `regis.yml`, any environment. Deploy without drama.

## What it does

regis reads `regis.yml` and applies your environment state — binaries, configs, secrets, and
services — locally or over SSH, with optional service management built in.
No daemon. No agent. No cloud dependency.

## Install

    go install git.disroot.org/jmy/regis/cmd/regis@latest

Or build from source:

    git clone https://git.disroot.org/jmy/regis
    cd regis
    task build   # requires go-task

## Quick start

    cp .env.local.example .env.local   # edit with SSH credentials and paths
    regis env                          # verify variables resolve correctly
    regis score                        # visualise the deployment DAG
    regis full -n                      # dry run — show what would happen
    regis full                         # deploy

## Generate regis.yml with AI help

    regis ai > regis-ai.md
    # Give regis-ai.md + your project files (Taskfile.yml, deploy scripts, go.mod) to an AI

The AI reads the schema context and generates a `regis.yml` for your project.

## Per-target environments

    # .env.local       — shared defaults
    # .env.prod        — prod overrides (auto-discovered for target named "prod")
    # .env.staging     — staging overrides

    regis env --target prod       # show resolved variables for prod

## Commands

| Command | Description |
|---------|-------------|
| `regis <scenario>` | Run a named scenario |
| `regis full` | Run the built-in full-deploy scenario |
| `regis env` | Show dotenv files loaded and how variables resolve |
| `regis ai` | Output embedded schema context for AI-assisted authoring |
| `regis score` | Visualise the deployment DAG (ASCII + Mermaid) |
| `regis rdiff` | Show what would change on the target |
| `regis fetch` | Download files from the target |
| `regis ssh` | Open an interactive SSH session |
| `regis service` | Manage services on the target |
| `regis release` | Manage release directories |
| `regis config` | Show parsed and merged configuration |

## Flags

    -f, --file     config file (default: regis.yml)
        --target   target selector: name, comma-list, glob, or "all"
    -n, --dry-run  show what would happen without executing
    -y, --yes      skip confirmation prompts (CI mode)
    -v, --verbose  show all cues including unchanged
    -q, --quiet    errors and summary only

## I have git on prod, why just not use that?

You can — and for pure code deploys it works fine. But the honest answer depends on what your
deploy actually contains.

**Where git on prod holds up**

Git's object model gives you cheap hash-based change detection (`git status`, `git diff`) and
atomic checkouts for free. For a codebase where everything lives in the repo, `git pull` on the
target is a reasonable substitute for regis's file-sync machinery.

**Where regis earns its place**

*Rendered and generated artifacts.* Build output (webpack bundles, compiled templates, rendered
configs) doesn't belong in git. regis's `render` and `generate` natures build locally and upload
only what changed — no artifact commits polluting history, no separate artifact store to wire up.

*Secrets.* `.env` files can't go in the repo. regis handles them as first-class `secret` cues:
masked in all output, `preserve:` protects keys that must survive deploys (per-env passwords,
tokens). Coordinating secrets alongside a git-based deploy requires separate tooling regis replaces.

*Targets without repo access.* `pack` with `git: true` reads your local HEAD and pushes files over
SSH — the target needs no git, no credentials, no network path to the repo. Useful for locked-down
servers, air-gapped environments, or anywhere you don't want the repo cloned on prod.

*Rollback for non-code artifacts.* `git revert` rolls back code. It can't restore a secret file or
a rendered binary blob that was never committed. regis snapshots everything it deployed regardless
of origin, and `restore: true` on any cue wires that cue into the rollback sequence.

*Service lifecycle and post-action deduplication.* Git has nothing for systemd/crontab management,
health checks, or "five cues all want `reload:nginx` — run it exactly once."

**The pragmatic middle ground**

regis supports a `nature: git` cue (pull-based, SHA-aware) for when the target does have repo
access and you want git's delta efficiency. You get drift detection by SHA comparison, automatic
`{prev_sha}` in rollback hints, and `restore: true` wired to `git reset --hard <prev_sha>` —
while the same scenario also handles secrets, rendered output, and services that git can't touch.

In short: if your entire deploy is repo-tracked files, git pull is fine. The moment it also
involves secrets, build artifacts, or services, regis handles the parts git cannot.

## Docs

- [Getting started](docs/getting-started.md)
- [Full specification](docs/superpowers/specs/2026-05-22-regis-design.md)
