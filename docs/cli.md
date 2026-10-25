# CLI reference

`hades` sets up and tears down a git worktree for parallel development; `hades-port` prints a worktree's port for Paseo. Both are static binaries. They call `git` and `sh`; `caddy` when the host config sets a domain; `direnv` when present; `git secret` in a repository with `.gitsecret`; `join` calls `ssh`, `ssh-add`, `ssh-keygen` and `gh`.

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

## Host config

The registry is the user's own: `~/.local/state/hades/{ports,sites,manifests}`, created at the first
`up`. The host config is optional: the file at `HADES_HOST_CONFIG`, else `/etc/hades/host.toml` when
it exists. A `HADES_HOST_CONFIG` file that does not exist is an error, and so is an unknown key.

```toml
domain = "example.test"                    # per-worktree HTTPS addresses and Caddy site files
chrome_path = "/opt/hades/chrome/chrome"   # enables the chrome-devtools MCP entry

[rabbitmq]                                 # enables vhosts and the doctor check
management_url = "http://127.0.0.1:15672"
amqp_url = "amqp://guest:guest@127.0.0.1:5672"
user = "guest"
password = "guest"
```

| | Without a domain | With a domain |
|---|---|---|
| Label | `<project>-<name>` | `<user>-<project>-<name>` |
| `PHX_HOST`, `HADES_URL` | `localhost`, `http://localhost:<port>` | `<label>.<domain>`, `https://<label>.<domain>` |
| Caddy | not used | `up` writes the site file; `up`, `down` and `gc` run `caddy reload --config /etc/caddy/Caddyfile` after writing or removing one |

Ports and Postgres come from the environment:

| Variable | Default |
|---|---|
| `HADES_PORT_RANGE` | `4100-4999` |
| `HADES_DB_HOST`, `HADES_DB_PORT` | `127.0.0.1`, `5432` |
| `HADES_DB_USER`, `HADES_DB_PASSWORD` | `postgres`, `postgres` |

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
which ADE's variables to read.

| Command | Does | Flags |
|---|---|---|
| `init` | Writes `hades.toml` (project name set, other keys commented out), the hook files for `--ade` (default `paseo,orca`) and `.gitignore` entries; prints the config snippet for `phoenix` and `elixir` projects and the worktrunk snippet for `--ade wt`. Existing files are left alone unless `--force`; a hook file that lacks hades gets the lines to add printed. Run once per project and commit the files. | `--ade paseo,orca,wt`, `--force` |
| `up` | Claims a port (web projects), writes the manifest, `.env.worktree` and `.envrc` (`direnv allow` if direnv is installed), runs `git secret reveal -f` when `.gitsecret` exists, ensures the RabbitMQ vhost, writes the site file and reloads Caddy (with a domain), points the MCP configs at this worktree, then runs the project's setup steps. It names the databases (`DB_SUFFIX`, `DATABASE_URL`); the setup steps create them, for example `mix setup`. Idempotent: a second run reuses the label and port. | `--dry-run`, `--skip-setup` |
| `down` | Runs the project's teardown steps, drops the databases the manifest lists and its vhost, removes the site file and reloads Caddy (with a domain), releases the port, deletes the manifest. Best effort: every step is attempted, exit 0 unless `--strict`. | `--strict` |
| `ls` | Every worktree in the registry: label, port, host, path, age. | `--json` |
| `status` | Host config path, domain, port range and free ports, worktrees, Postgres reachability. | `--json` |
| `env` | The worktree's variables as export lines, in file order. Before `up`: `hades: env: no .env.worktree in <path>; run hades up`. | `--json` |
| `port` | The worktree's port and nothing else (what `hades-port` runs). | |
| `doctor` | Checks host config (`loaded <path> (domain <domain or none>)` or `no host config: laptop defaults`), registry directories, Postgres, RabbitMQ, git, tools, direnv, ADE hooks, manifests, and manifests written by another hades version. Prints `ok`, `WARN` or `FAIL` per check; every FAIL ends with its fix. A repository without `paseo.json` or `orca.yaml` is a WARN. Exit 0 when nothing fails. | `--json` |
| `gc` | Releases the claims of worktrees whose directory vanished, and port links to vanished directories; reloads Caddy once when a site file went. | `--dry-run` |
| `version` | Prints the version. | |
| `join` | On a laptop: `hades join <host>` writes the SSH aliases `<alias>-via-host` (through the box) and `<alias>` (over your tailnet when this laptop reaches your VM there) and runs `hades-onboard` in your VM. See [`join`](#join). | `--as LOGIN` (default `$USER`), `--alias NAME` (default: the host's first DNS label; required for an IP or a host without a dot), `--github LOGIN` (default: `gh`'s login) |

How a worktree is identified: `--root`/`--worktree`/`--name` flags, else Paseo's `PASEO_*`
variables, else Orca's `ORCA_*`, else git. The name is the branch (Paseo, git; the directory name on
a detached HEAD) or the workspace name (Orca). The label is in the [host config](#host-config) table,
at most 63 characters (the name is shortened to fit). The name, label and hostname are fixed at the
first `up` and recorded in the manifest; a renamed branch keeps them. `down` then `up` gives a fresh
label. A label held by the manifest of another worktree path fails `up` with
`label <l> taken by <user> for <path>`.

## `join`

The alias must differ from the host, compared without case, or ssh would take the alias's own block for the box: `hades join shop` exits 2 with `join: shop: the alias must differ from the host; pass --alias NAME` and the usage.

Steps, in order. Every ssh that `join` runs, except the hand-off, has `-o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10`; `<key>` is the private key file, for a key on disk.

| Step | Action |
|---|---|
| Key, GitHub | As in the [developer guide](developer.md#join). |
| Address | `ssh [-i <key>] -l hades <host>`: the box's jump account prints your VM's address. |
| `<alias>-via-host` | Writes the alias, then logs in once: `ssh <alias>-via-host true`. |
| Hand-off | `ssh -t <alias>-via-host hades-onboard <alias>` on your terminal. |
| `<alias>` | Only after `hades-onboard` exits 0. Reads `tailscale status --json` in the VM; with `BackendState` `Running`, logs in once over the tailnet with the VM's host key from `<alias>-via-host`: `ssh -o HostKeyAlias=<alias> -l <login> [-i <key>] <name> true`, `<name>` being `Self.DNSName` without the trailing dot, then, if that fails, the first of `Self.TailscaleIPs` (for a laptop without Tailscale's DNS). Then writes the alias. |

| Alias | `HostName` | `ProxyCommand` | Written when |
|---|---|---|---|
| `<alias>-via-host` | the VM's address | `ssh -W %h:%p -l hades -o StrictHostKeyChecking=accept-new [-i <key>] <host>` | every run |
| `<alias>` | `<name>` or the address | none | the tailnet login worked; prints `daily: ssh <alias> (tailnet); fallback: ssh <alias>-via-host` |
| `<alias>` | the VM's address | as `<alias>-via-host` | otherwise; with Tailscale `Running`, prints `<alias> goes through the box: this laptop does not reach <name> on your tailnet` |

Each alias is a block between `# hades:begin <name>` and `# hades:end <name>` in `~/.ssh/config`, also holding `User <login>`, `HostKeyAlias <alias>` (both aliases store the VM's host key under `<alias>`, whatever the `HostName`), `ForwardAgent yes`, `StrictHostKeyChecking accept-new` and `IdentityFile <key>` for a key on disk. `wrote Host <name> to ~/.ssh/config` is printed only when a block changed.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success. `--help` and `-h` print the usage on stdout and exit 0, for `hades` and every command. |
| 1 | Failure, as `hades: <command>: <reason>` on stderr. |
| 2 | Usage error: an unknown command, flag or argument. |

`join` differs:

| Code | When |
|---|---|
| hades-onboard's code | The login through `<alias>-via-host` worked: 0 when onboarding completed. A VM without `hades-onboard` gives 0 and prints `next: ssh <alias>-via-host`. |
| 2 | `not authorised yet`: the jump account or the VM refused the key. Also a usage error. |
| 1 | Any other failure, as `join: <reason>`; an unreachable host or a refused host key is `join: ssh: <ssh's message>`, jump-account output that is no address `join: ssh -l hades <host> printed "<output>", not an address`. |

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
| `manifests/<label>.toml` (in the registry) | User, worktree path, port, label, host, database bases and suffix, vhost: the record `down` and `gc` act on. |
| `ports/<port>` (in the registry) | Symlink to the worktree: the port claim. |
| `sites/<label>.caddy` (in the registry) | With a domain, web projects only: `import worktree <label>.<domain> <port>`. |
| `.env.worktree` (in the worktree) | `KEY=VALUE` lines, fully owned by hades, rewritten each run. |
| `.envrc` (in the worktree) | A block between `# hades:begin` and `# hades:end` for direnv; the rest of the file is left alone. |
| MCP configs | In whichever of `.mcp.json`, `.codex/config.toml`, `.gemini/settings.json`, `.cursor/mcp.json`, `.grok/settings.json` exist: a `tidewave` entry at this worktree's port, for web projects with Tidewave. `.mcp.json` also gets a `chrome-devtools` entry when the host config sets `chrome_path`. Marked `skip-worktree`, so `git status` hides the edit; undo with `git update-index --no-skip-worktree <file>`. |

Variables (web projects get all of them; others get no `PORT`, `PHX_HOST`, `HADES_URL`):

| Variable | Value |
|---|---|
| `PORT` | the claimed port |
| `PHX_HOST`, `HADES_URL` | in the [host config](#host-config) table |
| `HADES_LABEL` | `<label>` |
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD` | the `HADES_DB_*` values |
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
vhost = true                     # a vhost <label>, exported as RABBITMQ_VHOST and AMQP_URL; skipped with a warning without [rabbitmq] in the host config

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

## ADE hooks

`hades init --ade paseo,orca,wt` writes them; they call the binary on `PATH`.

| ADE | File | Content |
|---|---|---|
| Paseo | `paseo.json` | `worktree.setup: ["hades up"]`, `worktree.teardown: ["hades down"]`, `worktree.servicePorts.portScript: "hades-port"` (Paseo runs it to learn the port of a declared service). A failing teardown aborts the removal; `down` exits 0 unless `--strict`. |
| Orca | `orca.yaml` | `scripts.setup: hades up`, `scripts.archive: hades down`, `setupAgentStartupPolicy: wait-for-setup`. Orca aborts archiving on a failed hook unless `--allow-failed-archive-hook`. |
| worktrunk | `.config/wt.toml` | `pre-start = "hades up --name {{ branch }}"`, `pre-remove = "hades down --name {{ branch }}"` (printed, not written). |

Hooks run in `bash` without profile files, so hades prepends to `PATH`, when present: `~/.asdf/shims`, `~/.local/share/mise/shims`, `/usr/local/share/mise/shims` (mise's system store), `~/.local/bin`.

## Safety rules

- `hades` never drops a database, releases a port, removes a site or deletes a vhost that the manifest it acts on does not list.
- It modifies no tracked file other than the MCP configs, and never commits, pushes or changes branches.
- `up` rolls back what it claimed in the failing run, nothing older; a failing setup step keeps the manifest and env files.
- Credentials reach project commands only as the documented variables, written nowhere but the two env files in the worktree.
