# CLI reference

`hades` sets up and tears down a git worktree for parallel development; `hades-port` prints a worktree's port for Paseo. Both are static binaries with no runtime dependencies.

## Install

| Platform | Status |
|---|---|
| darwin/arm64, linux/amd64 | tested in CI |
| darwin/amd64, linux/arm64 | built, untested |

```sh
go install github.com/quotedo/hades/cmd/...@latest          # Go 1.25+, both binaries into $(go env GOPATH)/bin
# or: download hades_<version>_<os>_<arch>.tar.gz and checksums.txt from GitHub Releases
tar xzf hades_<version>_darwin_arm64.tar.gz && install -m 0755 hades hades-port ~/.local/bin/
hades version
```

On a server the Ansible collection installs a pinned release and the host config; see the [server reference](server.md). Releases are cut by pushing a `v*` tag (goreleaser); `make release-snapshot` builds one locally into `dist/`.

## Modes

| Mode | When | Registry | Postgres | URL |
|---|---|---|---|---|
| Server | `/etc/hades/host.toml` exists | `/srv/hades/{ports,sites,manifests}` | the host's dev cluster | `https://<label>.<domain>` through Caddy |
| Laptop | no host config | `~/.local/state/hades/` | `127.0.0.1:5432`, `postgres`/`postgres` | none; `PHX_HOST=localhost`, `HADES_URL=http://localhost:<port>` |

`HADES_HOST_CONFIG=<path>` overrides the host config path and fails if the file is missing (a typo cannot demote a server); `HADES_HOST_CONFIG=laptop` forces laptop mode. Laptop overrides: `HADES_PORT_RANGE`, `HADES_DB_HOST`, `HADES_DB_PORT`, `HADES_DB_USER`, `HADES_DB_PASSWORD`.

## Commands

```
hades [--root DIR] [--worktree DIR] [--name NAME] [--ade ADE] [--json] [-v] <command> [args]
```

| Command | Does | Flags |
|---|---|---|
| `init` | Writes `hades.toml` (every key commented out), the ADE hook files and `.gitignore` entries; prints a Phoenix config snippet. Never overwrites an existing hook file without `--force`. | `--ade paseo,orca,wt`, `--force` |
| `up` | Claims a port, creates the databases, writes the manifest, the site file, `.env.worktree` and `.envrc`, wires MCP servers, reloads Caddy, runs the setup steps. Idempotent; a second run reuses the label and port. | `--dry-run`, `--skip-setup` |
| `down` | Runs the project's teardown steps, drops the worktree's databases and vhost, removes the site, releases the port, deletes the manifest. Best effort, exit 0 unless `--strict`. | `--strict` |
| `ls` | Lists worktrees known on this machine: label, port, host, path, owner. | `--mine`, `--json` |
| `status` | Host summary: mode, port range and free ports, worktrees, Postgres and Caddy reachability. | `--json` |
| `env` | The worktree's exported variables. | `--shell`, `--json` |
| `port` | The worktree's port and nothing else (what `hades-port` runs). | |
| `doctor` | Checks host config, registry directories and permissions, Postgres, Caddy, tools; prints `ok`/`WARN`/`FAIL` lines with the fix. Exit 0 when nothing fails. | `--json` |
| `gc` | As root: releases claims of worktrees whose directory vanished, or everything owned by a user. | `--dry-run`, `--owner USER` |
| `caddy-reload` | As root through sudo: validates every site file, mirrors them into Caddy's live directory, validates the Caddyfile, reloads Caddy. | `--check` |
| `version` | Prints the version. | |

How a worktree is identified: `--worktree`/`--name` flags, else Paseo's `PASEO_*` variables, else Orca's `ORCA_*`, else git (`--ade` restricts the choice). The name, label and hostname are fixed at the first `up` and recorded in the manifest; a renamed branch keeps them. `down` then `up` gives a fresh label.

## What `up` writes

| File | Content |
|---|---|
| `<manifests_dir>/<label>.toml` | Owner, worktree path, port, label, host, databases, vhost: the record `down` and `gc` act on. |
| `<ports_dir>/<port>` | Symlink to the worktree: the port claim. |
| `<sites_dir>/<label>.caddy` | `import worktree <host> <port>`; `caddy-reload` mirrors it into Caddy's live directory. |
| `.env.worktree` (in the worktree) | `KEY=VALUE` lines, fully owned by hades, rewritten each run. |
| `.envrc` (in the worktree) | A block between `# hades:begin` and `# hades:end` for direnv; the rest of the file is left alone; `direnv allow` runs if direnv is installed. |
| MCP configs | The chrome-devtools and Tidewave entries, when enabled. |

Exported variables:

| Variable | Value |
|---|---|
| `PORT` | the claimed port (web projects) |
| `PHX_HOST`, `HADES_URL`, `HADES_LABEL` | `<label>.<domain>`, `https://<label>.<domain>`, `<label>` |
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD` | the host's Postgres block |
| `DB_SUFFIX` | `_<user>_<name>`, at most 32 bytes, never ending in a digit |
| `DATABASE_URL`, `TEST_DATABASE_URL` | `<app>_dev<suffix>` and `<app>_test<suffix>` (Mix projects) |
| `SECRET_KEY_BASE` | random, kept across runs |
| `RABBITMQ_VHOST`, `AMQP_URL` | when the project asks for a vhost |
| `[env]` entries | rendered |

Phoenix projects read `PORT`, `DB_*` and `DB_SUFFIX` in `config/dev.exs` and `config/test.exs`; `hades init` prints the snippet.

## `hades.toml` (project)

At the repository root, committed; read from the worktree first, then the root checkout, then preset defaults. Every key is optional.

```toml
project = "jarl"                 # URL slug, max 30 chars; default: root directory name
preset = "phoenix"               # phoenix | elixir | go | node | none; default: detected
web = true                       # port and URL; default true for phoenix, else false
setup = ["mix setup"]            # replaces the preset's setup steps
teardown = ["mix some.cleanup"]  # extra steps before hades' own teardown
databases = ["app_dev", "app_test"]   # base names; default from the Mix app name

[env]
JARL_ENDPOINT = "{{host}}"       # placeholders: host, port, url, suffix, name, project, label

[services.rabbitmq]
vhost = true                     # server mode: a vhost <label>, exported as RABBITMQ_VHOST and AMQP_URL

[mcp]
tidewave = true                  # default: detected from mix.lock
```

| Preset | Detected by | Default setup |
|---|---|---|
| `phoenix` | `mix.exs` with a `phoenix` dep | `mix setup` if the alias exists, else `mix deps.get` |
| `elixir` | any other `mix.exs` | same |
| `go` | `go.mod` | `go mod download` |
| `node` | `package.json` | the install command for the lockfile found, else `npm ci` |
| `none` | nothing of the above | nothing; `up` exits 0 and writes no manifest |

Database rules: `down` drops only `<base><suffix>` and `<base><suffix><digits>` for the bases recorded at `up`. A later `up` with more bases adds them and never drops any; a vhost once created stays recorded until `down`. When a `down` step fails, the manifest is kept minus the released claims (`down: manifest kept (failed: ...); gc will retry`).

## Host config (server)

`/etc/hades/host.toml`, root-owned 0644, written by the Ansible collection, never by hades.

```toml
domain = "example.test"                  # URLs are <label>.<domain>
label_template = "{{project}}-{{name}}"  # shared servers may use "{{project}}-{{user}}-{{name}}"
port_range = "4100-4999"
ports_dir = "/srv/hades/ports"
sites_dir = "/srv/hades/sites"
manifests_dir = "/srv/hades/manifests"
tool_paths = []                           # extra PATH entries for project commands
chrome_path = "/opt/chrome-for-testing/chrome"   # enables the chrome-devtools MCP entry

[caddy]
config = "/etc/caddy/Caddyfile"
live_dir = "/etc/caddy/hades-sites"       # root-owned; Caddy imports this, never sites_dir
reload = ["systemctl", "reload", "caddy"]

[postgres]
host = "127.0.0.1"
port = 5433
user = "postgres"
password = "postgres"

[rabbitmq]                                # optional
management_url = "http://127.0.0.1:15672"
amqp_url = "amqp://guest:guest@127.0.0.1:5672"
user = "guest"
password = "guest"
```

Registry directories must be root-owned, setgid and sticky, group-writable by a group the user belongs to (`root:hades`, mode `3775`), never world-writable; `hades up` refuses otherwise and `hades doctor` says which one lacks what. `caddy-reload` accepts a site file only when it is one `import worktree <host> <port>` line, the host is exactly `<label>.<domain>` for `<label>.caddy`, and a manifest with the same owner exists.

## ADE hooks

`hades init --ade paseo,orca,wt` writes them. They call the binary on `PATH`, because the ADEs read the hook file from the branch being opened.

| ADE | File | Content |
|---|---|---|
| Paseo | `paseo.json` | `worktree.setup: ["hades up"]`, `worktree.teardown: ["hades down"]`, `servicePorts.portScript: "hades-port"` (Paseo runs it to learn the port of a declared service). A failing teardown aborts the removal; `down` exits 0 unless `--strict`. |
| Orca | `orca.yaml` | `scripts.setup: hades up`, `scripts.archive: hades down`, `setupAgentStartupPolicy: wait-for-setup`. Orca aborts archiving on a failed hook unless `--allow-failed-archive-hook`. |
| worktrunk | `.config/wt.toml` | `pre-start = "hades up --name {{ branch }}"`, `pre-remove = "hades down --name {{ branch }}"` (printed, not written). |

Hooks run in `bash` without profile files, so hades prepends to `PATH`, when present: `~/.asdf/shims`, `~/.local/share/mise/shims`, `/usr/local/share/mise/shims` (the server's store), `~/.local/bin`, then the host's `tool_paths`.
