# CLI reference

`hades` sets up and tears down a git worktree for parallel development; `hades-port` prints a worktree's port for Paseo. Both are static binaries. They call `git` and `sh`; on a server also `sudo` and `caddy`; `direnv` is used when present.

## Install

| Platform | Status |
|---|---|
| darwin/arm64, linux/amd64 | tested in CI |
| darwin/amd64, linux/arm64 | built, untested |

The repository and its releases are private: the download needs `gh` logged in with read access to
the repository. Replace `<os>_<arch>` with `darwin_arm64`, `darwin_amd64`, `linux_amd64` or
`linux_arm64`. `~/.local/bin` must be on your `PATH`.

```sh
gh release download --repo quotedo/hades --pattern '*_<os>_<arch>.tar.gz' --pattern checksums.txt
shasum -a 256 --check --ignore-missing checksums.txt
tar -xzf hades_*_<os>_<arch>.tar.gz hades hades-port
install -d ~/.local/bin && install -m 0755 hades hades-port ~/.local/bin/
hades version
```

Without a tag, `gh release download` takes the latest release. From source (Go 1.25+, with git
credentials for the private repository):

```sh
GOPRIVATE=github.com/quotedo/* go install github.com/quotedo/hades/cmd/...@latest   # both binaries into $(go env GOPATH)/bin
```

On a server the Ansible collection installs the CLI; see the [server reference](server.md#inventory).

## Modes

| Mode | When | Registry | Postgres | URL |
|---|---|---|---|---|
| Server | `/etc/hades/host.toml` exists | `/srv/hades/{ports,sites,manifests}` | the host's dev cluster | `https://<label>.<domain>` through Caddy |
| Laptop | no host config | `~/.local/state/hades/`, created at the first `up` | `127.0.0.1:5432`, `postgres`/`postgres` | `http://localhost:<port>`; `PHX_HOST=localhost` |

`HADES_HOST_CONFIG=<path>` overrides the host config path and fails if the file is missing (a typo
cannot demote a server); `HADES_HOST_CONFIG=laptop` forces laptop mode. Laptop overrides:
`HADES_PORT_RANGE`, `HADES_DB_HOST`, `HADES_DB_PORT`, `HADES_DB_USER`, `HADES_DB_PASSWORD`.

On a laptop:

1. Start Postgres on `127.0.0.1:5432`, or set the `HADES_DB_*` overrides.
2. In the repository, run `hades init` and add the config snippet it prints.
3. Commit `hades.toml`, `paseo.json`, `orca.yaml` and `.gitignore`.
4. Open a worktree from your ADE: `hades up` claims a port; the app, once started, is at `http://localhost:<port>`.

## Commands

```
hades [--root DIR] [--worktree DIR] [--name NAME] [--ade paseo|orca|wt|shell] [--json] <command> [args]
```

`--root` is the main checkout, `--worktree` the worktree path, `--name` the worktree name, `--ade`
which ADE's variables to read. All are usually detected.

| Command | Does | Flags |
|---|---|---|
| `init` | Writes `hades.toml` (project name set, other keys commented out), the hook files for `--ade` (default `paseo,orca`) and `.gitignore` entries; prints the Phoenix config snippet. Existing files are left alone unless `--force`; a hook file that lacks hades gets the lines to add printed. Run once per project and commit the files. | `--ade paseo,orca,wt`, `--force` |
| `up` | Claims a port, writes the manifest, the site file, `.env.worktree` and `.envrc` (`direnv allow` if direnv is installed), runs `git secret reveal -f` when `.gitsecret` exists, ensures the RabbitMQ vhost, publishes the site and reloads Caddy, points the MCP configs at this worktree, then runs the project's setup steps. It names the databases (`DB_SUFFIX`, `DATABASE_URL`); the setup steps create them, for example `mix setup`. Idempotent: a second run reuses the label and port. | `--dry-run`, `--skip-setup` |
| `down` | Runs the project's teardown steps, drops the databases the manifest lists and its vhost, removes the site and reloads Caddy, releases the port, deletes the manifest. Best effort: every step is attempted, exit 0 unless `--strict`. | `--strict` |
| `ls` | Every worktree known on this machine: label, port, host, path, owner. | `--mine`, `--json` |
| `status` | Host summary: mode, port range and free ports, worktrees, Postgres and Caddy reachability. | `--json` |
| `env` | The worktree's variables as export lines, in file order. | `--json` |
| `port` | The worktree's port and nothing else (what `hades-port` runs). | |
| `doctor` | Checks host config, registry directories and permissions, Postgres, Caddy, RabbitMQ, git, tools, ADE hooks and manifests. Prints `ok`, `WARN` or `FAIL` per check; every FAIL ends with its fix. A repository without `paseo.json` or `orca.yaml` is a WARN. Exit 0 when nothing fails. | `--json` |
| `gc` | Releases the claims of worktrees whose directory vanished, or everything owned by a user. On a server it runs as root (a weekly timer runs it too); on a laptop as the user. | `--dry-run`, `--owner USER` |
| `caddy-reload` | As root through sudo: validates every site file, mirrors them into Caddy's live directory, validates the Caddyfile, reloads Caddy. | `--check` |
| `version` | Prints the version. | |
| `join` | On a laptop: `hades join <host-or-ip>` makes the host reachable over SSH, then runs `ssh -t <alias> hades-onboard <alias>`. Writes a block between `# hades:begin <alias>` and `# hades:end <alias>` in `~/.ssh/config`, generates `~/.ssh/id_ed25519` when no key exists, and may upload a key with `gh ssh-key add`. The steps are in the [developer guide](developer.md#join). | `--as LOGIN` (default `$USER`), `--alias NAME` (default: the host's first DNS label; required for an IP), `--github LOGIN` (default: `gh`'s login) |

How a worktree is identified: `--worktree`/`--name` flags, else Paseo's `PASEO_*` variables, else
Orca's `ORCA_*` (the label is the workspace name), else git. The name, label and hostname are fixed
at the first `up` and recorded in the manifest; a renamed branch keeps them. `down` then `up` gives a
fresh label. A label another user already holds fails `up` with `label <l> taken by <user> for <path>`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success. `--help` and `-h` print the usage on stdout and exit 0, for `hades` and every command. |
| 1 | Failure, usually as `hades: <command>: <reason>` on stderr. |
| 2 | Usage error: an unknown command, flag or argument. |

`join` differs:

| Code | When |
|---|---|
| hades-onboard's code | The login worked: 0 when onboarding completed. A box without `hades-onboard` gives 0 and prints `next: ssh <alias>`. |
| 2 | `not authorised yet`: the box refused the key. Also a usage error. |
| 1 | Any other failure, as `join: <reason>`; an unreachable host or a refused host key is `join: ssh: <ssh's message>`. |

## Server scripts and files

The Ansible collection installs these beside `hades`.

| Script or file | What it is |
|---|---|
| `hades-onboard [HOST]` | The developer's first-login steps, listed in the [developer guide](developer.md#first-login-steps). Needs a terminal: exit 2 without one. HOST is the name in the printed Paseo address, default the box's hostname. Exit 0 and `onboarding complete` when every step passed, else 1. |
| `~/.config/hades/onboarded` | Written by `hades-onboard` on success; while it is missing, interactive shells print `Run hades-onboard to finish setting up your account.` |
| `~/.config/hades/repos` | The repositories `hades-onboard` clones, one URL per line (owner, 0600). |
| `~/.config/hades/paseo.env` | `PASEO_PASSWORD=<password>`, generated once per Paseo user (owner, 0600) and kept, also when edited by hand. |
| `hades-keys [LOGIN]` | Root, hourly through `hades-keys.timer`, and by the user role at apply. For each `login:github` line of `/etc/hades/github-logins` (or LOGIN alone), writes `~/.ssh/authorized_keys` as that user: `/etc/hades/ssh-keys/<login>` followed by `https://github.com/<github>.keys`, or the static keys alone when github is empty. Writes only when the content differs; a failed or empty fetch keeps the file. Prints `<login>: updated`, `unchanged`, `fetch failed, kept`, `no home, skipped` or `write failed, kept`. |

## What `up` writes

| File | Content |
|---|---|
| `<manifests_dir>/<label>.toml` | Owner, worktree path, port, label, host, database bases and suffix, vhost: the record `down` and `gc` act on. |
| `<ports_dir>/<port>` | Symlink to the worktree: the port claim. |
| `<sites_dir>/<label>.caddy` | `import worktree <host> <port>`; `caddy-reload` mirrors it into Caddy's live directory. |
| `.env.worktree` (in the worktree) | `KEY=VALUE` lines, fully owned by hades, rewritten each run. |
| `.envrc` (in the worktree) | A block between `# hades:begin` and `# hades:end` for direnv; the rest of the file is left alone. |
| MCP configs | In whichever of `.mcp.json`, `.codex/config.toml`, `.gemini/settings.json`, `.cursor/mcp.json`, `.grok/settings.json` exist: the chrome-devtools entry (server) and Tidewave at this worktree's port (web projects). Marked `skip-worktree`, so `git status` hides the edit; undo with `git update-index --no-skip-worktree <file>`. |

Variables (web projects get all of them; others get no `PORT`, `PHX_HOST`, `HADES_URL`):

| Variable | Value |
|---|---|
| `PORT` | the claimed port |
| `PHX_HOST`, `HADES_URL`, `HADES_LABEL` | `<label>.<domain>`, `https://<label>.<domain>`, `<label>` |
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD` | the host's Postgres block |
| `DB_SUFFIX` | `_<user>_<name>`, at most 32 bytes, never ending in a digit |
| `DATABASE_URL`, `TEST_DATABASE_URL` | `<app>_dev<suffix>` and `<app>_test<suffix>` from the Mix app name (Mix projects only) |
| `SECRET_KEY_BASE` | random, kept across runs |
| `RABBITMQ_VHOST`, `AMQP_URL` | when the project asks for a vhost |
| `[env]` entries | rendered |

Phoenix projects read `PORT`, `DB_*` and `DB_SUFFIX` in `config/dev.exs` and `config/test.exs`; `hades init` prints the snippet.

## `hades.toml` (project)

At the repository root, committed; read from the worktree first, then the root checkout, then preset defaults. Every key is optional; unknown keys are an error.

```toml
project = "shop"                 # URL slug, max 30 chars; default: root directory name
preset = "phoenix"               # phoenix | elixir | go | node | none; default: detected
web = true                       # port and URL; default true for phoenix, else false
setup = ["mix setup"]            # replaces the preset's setup steps
teardown = ["mix some.cleanup"]  # extra steps before hades' own teardown
databases = ["shop_dev", "shop_test"]   # base names; default from the Mix app name

[env]
SHOP_ENDPOINT = "{{host}}"       # placeholders: host, port, url, suffix, name, project, label

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
| `node` | `package.json` | pnpm or yarn when their lockfile exists, else `npm ci` |
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
tool_paths = []                           # extra PATH entries for project commands; the collection writes []
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

`hades init --ade paseo,orca,wt` writes them; they call the binary on `PATH`.

| ADE | File | Content |
|---|---|---|
| Paseo | `paseo.json` | `worktree.setup: ["hades up"]`, `worktree.teardown: ["hades down"]`, `servicePorts.portScript: "hades-port"` (Paseo runs it to learn the port of a declared service). A failing teardown aborts the removal; `down` exits 0 unless `--strict`. |
| Orca | `orca.yaml` | `scripts.setup: hades up`, `scripts.archive: hades down`, `setupAgentStartupPolicy: wait-for-setup`. Orca aborts archiving on a failed hook unless `--allow-failed-archive-hook`. |
| worktrunk | `.config/wt.toml` | `pre-start = "hades up --name {{ branch }}"`, `pre-remove = "hades down --name {{ branch }}"` (printed, not written). |

Hooks run in `bash` without profile files, so hades prepends to `PATH`, when present: `~/.asdf/shims`, `~/.local/share/mise/shims`, `/usr/local/share/mise/shims` (the server's store), `~/.local/bin`, then the host's `tool_paths`.

## Safety rules

- `hades` never drops a database, releases a port, removes a site or deletes a vhost that the manifest it acts on does not list, and never acts on another user's manifest, except `gc` as root on vanished worktrees.
- It modifies no tracked file other than the MCP configs, and never commits, pushes or changes branches.
- `up` rolls back what it claimed in the failing run, nothing older; a failing setup step keeps the manifest and env files.
- Credentials reach project commands only as the documented variables, written nowhere but the two env files in the worktree.
