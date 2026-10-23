# Configuration

Hades reads two files: a host config, which says what the machine offers, and a project config, `hades.toml`, which says what the project needs. Precedence for behaviour: project config over preset defaults over built-ins. The host config decides only what the machine offers.

## Host config

`/etc/hades/host.toml`, written by the server playbook and never by hades. Its presence means server mode; its absence means laptop mode.

`HADES_HOST_CONFIG` overrides the path. If it names a file that does not exist, hades fails rather than falling back to laptop mode, so a typo cannot silently demote a server. The literal value `HADES_HOST_CONFIG=laptop` forces laptop mode, for tests or for a box that has a host config but where you want laptop behaviour.

```toml
domain = "example.test"             # public URLs are <label>.<domain>
label_template = "{{project}}-{{name}}"   # shared servers: "{{project}}-{{user}}-{{name}}"
port_range = "4100-4999"
ports_dir = "/srv/hades/ports"
sites_dir = "/srv/hades/sites"
manifests_dir = "/srv/hades/manifests"
tool_paths = []                      # extra PATH entries for project commands
chrome_path = ""                     # optional; enables the chrome-devtools MCP entry

[caddy]
config = "/etc/caddy/Caddyfile"      # validated by `hades caddy-reload` before reloading
live_dir = "/etc/caddy/hades-sites"  # root-owned; Caddy imports this, never sites_dir
reload = ["systemctl", "reload", "caddy"]

[postgres]
host = "127.0.0.1"
port = 5433
user = "postgres"
password = "postgres"

[rabbitmq]                           # optional block
management_url = "http://127.0.0.1:15672"
amqp_url = "amqp://guest:guest@127.0.0.1:5672"
user = "guest"
password = "guest"
```

- `domain`, `label_template`: the public hostname of a worktree is `<label>.<domain>`. The label is rendered from the template and cut to fit 63 characters. A template without `{{user}}` fails with "label taken" when another user already holds the label.
- `port_range`, `ports_dir`, `sites_dir`, `manifests_dir`: the registry. Each port is a symlink in `ports_dir` to the worktree path; each worktree has a manifest and a one-line Caddy site file.
- `tool_paths`: appended to the directories hades puts on `PATH` for project commands (see [ades](ades.md)).
- `chrome_path`: when set, the chrome-devtools MCP entry is written.
- `[caddy]`: `hades caddy-reload` validates every `*.caddy` in `sites_dir`, mirrors exactly the validated bytes into `live_dir`, runs `caddy validate --config <config>`, then runs `reload`. Caddy should import `live_dir`, never `sites_dir`. The command runs as root through sudo and, as root, reads only `/etc/hades/host.toml`, which must be a root-owned regular file without group or world write bits. `hades caddy-reload --check` validates and touches nothing.
- `[postgres]`: exported to project commands as `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`.
- `[rabbitmq]`: optional; needed when a project asks for a vhost.

In server mode hades refuses to start when a required key is missing, and `hades up` refuses to run when a registry directory is missing or has the wrong group or mode: each must be setgid and sticky (mode `3775` is typical), group-writable, not world-writable, and owned by a group the user belongs to. `hades doctor` says which directory lacks what.

### Laptop mode

No host config means these defaults:

- registry under `~/.local/state/hades/` (`ports`, `sites`, `manifests`);
- port range `4100-4999`;
- Postgres at `127.0.0.1:5432`, role `postgres`, password `postgres`;
- no domain, so `PHX_HOST` is `localhost` and `HADES_URL` is `http://localhost:<port>`; no site files, no sudo, no RabbitMQ.

Environment overrides: `HADES_PORT_RANGE`, `HADES_DB_HOST`, `HADES_DB_PORT`, `HADES_DB_USER`, `HADES_DB_PASSWORD`. There is no per-user config file.

## Project config

`hades.toml` at the repository root, committed. It is read from the worktree first, then from the root checkout if the branch predates the file, then preset defaults. Every key is optional; `hades init` writes a file with all of them commented out.

```toml
project = "jarl"                 # URL slug; default: root checkout directory name
preset = "phoenix"               # default: detected
web = true                       # default: per preset
setup = ["mix setup"]            # replaces the preset's setup steps
teardown = ["mix some.cleanup"]  # extra steps, run before hades' own teardown
databases = ["gym_nation_dev", "gym_nation_internal_dev", "gym_nation_test"]

[env]
JARL_ENDPOINT = "{{host}}"

[services.rabbitmq]
vhost = true                     # create vhost <label>; export RABBITMQ_VHOST and AMQP_URL

[mcp]
tidewave = true                  # default: detected from mix.lock
```

- `project`: at most 30 characters after sanitising (lower-case, runs outside `[a-z0-9]` become `-`).
- `preset`: `phoenix`, `elixir`, `go`, `node` or `none`. Detected in the worktree in that order: `mix.exs` with a `phoenix` dep, any other `mix.exs`, `go.mod`, `package.json`, otherwise `none`. `none` does nothing, exits 0 and writes no manifest.
- `web`: decides only whether the worktree gets a port and URL. `true` by default for `phoenix`, `false` otherwise. A non-web project gets no `PORT`, `PHX_HOST` or `HADES_URL` and no site.
- `setup`: replaces the preset steps (phoenix and elixir: `mix setup` if the alias exists, else `mix deps.get`; go: `go mod download`; node: the install command for the lockfile found). They run last in `hades up`, and a failure there exits 1 but keeps the manifest and env files.
- `teardown`: extra commands run by `hades down` before the environment is removed.
- `databases`: database **base names**, the part before the worktree suffix. Default: `<app>_dev` and `<app>_test` from the Mix app name. On `down`, hades drops only databases named `<base><suffix>` or `<base><suffix><digits>` (the digits are a test partition), using the bases and suffix recorded in the manifest at `up` time. A later `up` with a changed list adds the new bases to the recorded ones and never drops any, and a vhost an earlier `up` created stays recorded after `vhost = false`, so `down` still releases it. When a `down` step fails, the manifest is kept (`down: manifest kept (failed: <steps>); gc will retry`) so `ls` still shows it and `gc` retries.
- `[env]`: extra variables. Values may use the placeholders `{{host}}`, `{{port}}`, `{{url}}`, `{{suffix}}`, `{{name}}`, `{{project}}` and `{{label}}`.
- `[services.rabbitmq] vhost`: default `false`. Server mode only; needs a `[rabbitmq]` block in the host config.
- `[mcp] tidewave`: expose the Tidewave MCP for the worktree.

## What `hades up` writes

Besides the manifest and registry entries, `up` writes two files in the worktree, both added to `.gitignore` by `hades init`:

- `.env.worktree`: `KEY=VALUE` lines, fully owned by hades and rewritten on each run.
- `.envrc`: a block between `# hades:begin` and `# hades:end` lines, for direnv. Anything outside the block is left alone. `direnv allow` is run if direnv is installed.

Exported variables:

| Variable | Value |
|---|---|
| `PORT` | the claimed port (web projects) |
| `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD` | from the host Postgres block |
| `DB_SUFFIX` | `_<user>_<name>`, at most 32 bytes, never ending in a digit |
| `DATABASE_URL`, `TEST_DATABASE_URL` | `<app>_dev<suffix>` and `<app>_test<suffix>` (Mix projects) |
| `SECRET_KEY_BASE` | random, kept across re-runs |
| `PHX_HOST`, `HADES_URL` | hostname and URL (web projects) |
| `HADES_LABEL` | the worktree label |
| `RABBITMQ_VHOST`, `AMQP_URL` | when a vhost is requested |
| the `[env]` entries | rendered |

Print them again with `hades env` (`--shell` or `--json`).

For Phoenix, `config/dev.exs` and `config/test.exs` should read `PORT`, `DB_*` and `DB_SUFFIX`; `hades init` prints a snippet for that.
