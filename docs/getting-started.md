# Getting started with regis

regis is a single Go binary that reads `regis.yml` and deploys to remote servers over SSH.
No daemon. No agent on the target.

---

## 1. Installation

**From source (recommended while regis is pre-release):**

```
git clone https://git.disroot.org/jmy/regis
cd regis
go build -o regis ./cmd/regis
```

Move the binary somewhere on your `PATH` (e.g. `~/go/bin/` or `/usr/local/bin/`).

**Once published:**

```
go install git.disroot.org/jmy/regis/cmd/regis@latest
```

Verify:

```
regis --version
```

---

## 2. Creating regis.yml

Create `regis.yml` in your project root. The minimal structure is:

```yaml
targets:
  - name: prod
    host: ${NODE_HOST}
    user: ${NODE_USER}
    port: ${NODE_PORT}
    dir: /opt/myapp

scenarios:

  build:
    describe: "Build local binary"
    cues:
      - name: bin
        nature: action
        local: true
        cmd: go build -o bin/myapp ./cmd/myapp
        env:
          GOOS: linux
          GOARCH: amd64
          CGO_ENABLED: "0"

  deploy:
    describe: "Upload and restart"
    requires: [build]
    post: restart:myapp
    cues:
      - name: bin
        nature: binary
        src: bin/myapp
        dest: bin/myapp

  full:
    describe: "Full deployment"
    cues:
      - scenario: deploy

services:
  - name: myapp
    manager: crontab
    binary: myapp
```

Key concepts:

- `targets` — one or more SSH destinations. The first is the default.
- `scenarios` — named units of work. Each contains `cues` (the individual steps).
- `requires` — prerequisite scenarios that run once, deduplicated, before the current scenario.
- `nature` — what a cue does:
  - `binary` — upload a file to the remote, compare by MD5
  - `config` — upload a text file, show unified diff
  - `secret` — masked merge of a dotenv file to the remote
  - `action` — run a shell command (local or remote)
  - `generate` — run a local shell command to produce artifacts (always runs, even during `rdiff`; no upload)
  - `render` — regis injects `$ARTIFACT_PATH`, runs your shell locally, then diffs whatever the shell wrote to `$ARTIFACT_PATH` against the remote `dest` and uploads if different. By default `$ARTIFACT_PATH` is a temp file; set `local_dest:` to make it a persistent local path (populated by `regis fetch`). Add a `reverse:` shell to transform the fetched remote content back into your source format
- `local: true` — runs on your machine, not the remote target (action and generate only).

---

## 3. Setting up dotenv

regis never stores secrets in `regis.yml`. Use dotenv files instead.

**Create `.env.local`** for shared defaults (add to `.gitignore`):

```
# .env.local
NODE_HOST=prod.example.com
NODE_USER=deploy
NODE_PORT=22
```

**Auto-discovery:** when you run `regis --target prod`, regis automatically loads `.env.prod`
if it exists alongside `regis.yml`. No declaration required.

**Create `.env.prod`** for prod-specific overrides:

```
# .env.prod
NODE_HOST=prod.example.com
NODE_USER=root
APP_URL=https://app.example.com
```

**Create `.env.staging`** for staging:

```
# .env.staging
NODE_HOST=staging.example.com
NODE_USER=deployer
NODE_PORT=2222
APP_URL=https://staging.app.example.com
```

**Interpolation order** (highest priority first):

```
shell environment  >  target dotenv (.env.<name>)  >  .env.local
```

Shell environment wins — useful for CI where you inject secrets as environment variables.

**`.gitignore` additions:**

```
.env.local
.env.prod
.env.staging
.env.secrets
```

---

## 4. Verify variable resolution

Before deploying, confirm that variables resolve to the values you expect:

```
regis env
```

Shows which dotenv files were loaded and the final value of every variable used in `regis.yml`.

For a specific target:

```
regis env --target prod
regis env --target staging
```

Example output:

```
Loaded: .env.local
Loaded: .env.prod (target: prod)

NODE_HOST   = prod.example.com   (.env.prod)
NODE_USER   = root               (.env.prod)
NODE_PORT   = 22                 (.env.local)
APP_URL     = https://app.example.com  (.env.prod)
```

---

## 5. Visualise the DAG

Before running anything, inspect the deployment graph:

```
regis score
```

This prints an ASCII diagram of every scenario, its cues, and their dependencies.

```
full
└── deploy   post: restart:myapp
    ├── [requires] build
    │   └── bin  (action, local)
    └── bin  (binary)
```

To emit Mermaid for documentation or pull request descriptions:

```
regis score --mermaid
```

---

## 6. Dry run

See exactly what regis would do without touching the server:

```
regis full -n
```

regis connects to the target, computes MD5 hashes for binary cues and text diffs for config
cues, then prints what would change — without uploading or executing anything.

Example output:

```
[full] deploy
  bin        CHANGED  local bin/myapp -> remote bin/myapp (1.4 MB)
  env        UNCHANGED
  post       WOULD RUN  systemctl restart myapp

Summary: 1 changed, 1 unchanged — 1 post-action queued
```

---

## 7. Deploy

Run the full deployment:

```
regis full
```

For a named scenario other than `full`:

```
regis deploy
regis build
```

Target a specific server:

```
regis full --target staging
regis full --target prod
```

Useful flags:

```
-v, --verbose   show all cues, including unchanged ones
-y, --yes       skip confirmation prompts (use in CI)
-q, --quiet     errors and summary only
```

---

## 8. AI-assisted authoring

If you have an existing project with a `Taskfile.yml`, shell deploy scripts, or `go.mod`,
you can ask an AI to generate a `regis.yml` for you.

Dump the schema context:

```
regis ai > regis-ai.md
```

Then give the AI:
1. `regis-ai.md` (the schema reference)
2. Your project files: `Taskfile.yml`, `Makefile`, deploy scripts, `go.mod`, directory layout

Example prompt for Claude Code or Cursor:

```
I want to create a regis.yml for my project.
The schema reference is in regis-ai.md.
My current deploy process is in Taskfile.yml.
Please generate a regis.yml that replicates my deploy workflow.
```

The AI understands the cue types, scenario composition, `requires` deduplication,
and dotenv interpolation from the embedded schema context.

---

## 9. Multi-target workflow

Use the same variable names in every target dotenv file so `regis.yml` stays identical
across environments.

**`regis.yml` (target definitions):**

```yaml
targets:
  - name: prod
    host: ${NODE_HOST}
    user: ${NODE_USER}
    port: ${NODE_PORT}
    dir: /opt/myapp

  - name: staging
    host: ${NODE_HOST}
    user: ${NODE_USER}
    port: ${NODE_PORT}
    dir: /opt/myapp-staging
```

**`.env.prod`:**

```
NODE_HOST=prod.example.com
NODE_USER=root
NODE_PORT=22
ENV_SERVER_FILE=.env.server.prod
```

**`.env.staging`:**

```
NODE_HOST=staging.example.com
NODE_USER=deployer
NODE_PORT=2222
ENV_SERVER_FILE=.env.server.staging
```

Now a config cue referencing `${ENV_SERVER_FILE}` automatically picks up the right file
depending on the active target — no branching logic in `regis.yml`.

**Typical multi-target commands:**

```
regis env --target staging          # verify staging resolves correctly
regis score --target staging        # inspect staging DAG
regis full -n --target staging      # dry run against staging
regis full --target staging         # deploy to staging
regis full --target prod            # deploy to prod
```

To deploy to all targets:

```
regis full --target all
```

---

## 10. Secret management

Secrets are never stored in `regis.yml`. They flow in through three mechanisms,
tried in order — first match wins.

### Option A: .env.secrets file (persistent, gitignored)

```
cp .env.secrets.example .env.secrets
# edit .env.secrets with real values
```

Add to `.gitignore`:

```
.env.secrets
```

regis loads `.env.secrets` automatically. Reference values in `regis.yml` via `${VAR_NAME}`.

A `nature: secret` cue uploads a dotenv file to the target, masking values in output,
and supports a `preserve:` list of keys that are never overwritten during merge:

```yaml
- name: env
  nature: secret
  src: ${ENV_SERVER_FILE}
  dest: .env
  preserve: [API_KEY, LEGACY_TOKEN]
```

### Option B: KeePassXC (ephemeral — secrets never sit on disk)

Unlock once and inject for the current shell session only:

```bash
# bash / zsh
export DB_PASSWORD=$(keepassxc-cli show -q --attributes Password keys.kdbx "MyApp/prod")
export API_KEY=$(keepassxc-cli show -q --attributes Password keys.kdbx "MyApp/api")
regis full -s      # push only secret cues
unset DB_PASSWORD API_KEY
```

```powershell
# PowerShell
$env:DB_PASSWORD = keepassxc-cli show -q --attributes Password keys.kdbx "MyApp/prod"
$env:API_KEY     = keepassxc-cli show -q --attributes Password keys.kdbx "MyApp/api"
regis full -s
Remove-Item Env:DB_PASSWORD, Env:API_KEY
```

### Option C: 1Password CLI (cross-platform)

```
op run -- regis full -s
```

`op run` injects secrets from 1Password as environment variables for the duration of the
command. Nothing is written to disk.

### Typical first-deploy workflow

```
regis setup              # run your setup scenario (create dirs, install dependencies)
regis full -s            # push only secret cues (from env, file, or interactive prompt)
regis full               # run the full deployment
```

The `-s` flag (`--secrets-only`) limits execution to cues with `nature: secret`.
Useful for rotating credentials without triggering a full deploy.

### Interactive prompt fallback

If a secret variable is missing from both the environment and dotenv files, and regis
is running in an interactive terminal (TTY), it prompts for the value. The entered value
is used for the current run only and never written to disk.

---

## 11. Service management

Define services once in `services:` and reference them by name anywhere a `post:` field appears.

### Defining a service

```yaml
services:
  - name: app
    manager: crontab    # built-in: crontab | systemd; or any string (pm2, etc.)
    binary: app
    health: curl -sf http://localhost:8080/health

  - name: nginx-front
    manager: systemd
    sudo: true
```

Built-in managers (`systemd`, `crontab`) provide default commands automatically:

| action | systemd default | crontab default |
|--------|----------------|-----------------|
| start | `systemctl start {name}` | `nohup {dir}/{binary} …` |
| stop | `systemctl stop {name}` | `pkill -f {dir}/{binary}` |
| restart | `systemctl restart {name}` | stop + start |
| reload | `systemctl reload {name}` | `pkill -HUP -f {dir}/{binary}` |
| status | `systemctl is-active {name}` | health check or `pgrep` |

### restart: and reload: shorthands

Use `restart:<name>` or `reload:<name>` as the `post:` value on any cue — regis looks
up the service, expands the manager command, and inherits the service's `sudo:` flag:

```yaml
- name: bin
  nature: binary
  src: bin/app
  dest: app
  post: restart:app    # expands to the crontab restart command; no sudo needed
```

### Scenario-level post

`post:` on a **scenario** fires once if any remote cue in that scenario changed.
Local cues (`local: true`) never trigger it.

```yaml
deploy:
  post: restart:app     # fires once if env or bin changed; skipped if only local build changed
  cues:
    - { name: build, nature: action, local: true, cmd: go build … }
    - { name: env,   nature: secret, … }
    - { name: bin,   nature: binary, … }
```

This avoids repeating `post: restart:app` on every cue that might need the service restarted.
Scenario-level and cue-level posts are pooled and deduplicated — the restart runs exactly once
no matter how many cues changed.

### Overriding commands

Use `commands:` to add or replace individual actions. Template variables available:
`{name}`, `{binary}`, `{dir}`, `{service_file}`.

Action references `{restart}`, `{reload}`, `{start}`, `{stop}` etc. expand to the
**pre-override** base command, enabling a call-super pattern:

```yaml
services:
  - name: nginx-front
    manager: systemd
    sudo: true
    commands:
      reload: nginx -t && {reload}   # config test first, then original systemctl reload nginx-front
```

If you override `reload` with `nginx -t && {reload}`, then `post: reload:nginx-front` will:
1. Run `nginx -t` — abort if config is invalid
2. Run `systemctl reload nginx-front` — graceful in-place reload

### Managing services manually

```bash
regis service restart app        # restart on the default target
regis service reload nginx-front --target prod
regis service logs app           # tail the service log
```
