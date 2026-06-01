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

## Docs

- [Getting started](docs/getting-started.md)
- [Full specification](docs/superpowers/specs/2026-05-22-regis-design.md)
