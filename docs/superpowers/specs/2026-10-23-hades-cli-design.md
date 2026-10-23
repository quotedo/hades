# Hades CLI: the worktree hook every ADE calls

Status: revised after the builder check (2026-10-23), awaiting Roman's approval. Written from the
brainstorm in Roman's dev-server session; decisions marked "(builder's call)" are mine and open to
correction. Companion: `2026-10-23-hades-server-design.md` covers the second deliverable of the
project, the playbook that provisions a server.

## 1. Intent

Hades (a pun on ADEs; the H is for Host) is one project with two deliverables: this CLI, and the
server playbook described in the companion document. Together they turn a Linux box into a shared
development server for agentic development environments and make every project on it work with
every ADE.

The CLI is a single binary that turns a fresh git worktree of any project into a running
development environment, and tears it down again. It is the program every agentic development
environment (ADE) calls from its worktree hooks: Orca, Paseo, worktrunk, or a plain shell.

On a shared dev server it also does the server-only work: claim a port, publish a public URL per
worktree, give each worktree its own databases and (when asked) broker vhost, and keep a registry
so garbage collection and status work across users. On a laptop it does only the project part.

Why it exists. Roman, and later several developers per company server, work on many projects at
once (jarl, saga, runa, hati, tyrr, pops, infra, tokyo, PawsAndCo) with many parallel worktrees,
each through whichever ADE the person prefers. Without one tool, every repository grows its own copy
of the same hook logic in the ADE's own dialect (jarl already carries two: a worktrunk hook and a
server hook), the copies drift, and simple projects get no support at all. One binary with stack
presets gives zero-config to the common case and a few lines of config to the rest, and leaves the
ADE to do what it is for: creating, visualising, and archiving worktrees and running agents in them.

## 2. Goals

1. Zero configuration for common projects: detect the stack and do the right thing.
2. One behaviour everywhere: the same binary and the same result on a server through any ADE, on a
   laptop through worktrunk, or by hand in a shell.
3. Multi-user safe on shared servers: concurrent `up` runs from different users never collide; one
   user's `down` or `gc` cannot touch another user's worktree, databases, port, or URL.
4. `up` is idempotent and rolls back what it claimed when it fails; `down` is best effort and
   never blocks the ADE's archive.
5. Observable: `ls`, `status`, `doctor`, machine-readable with `--json`.
6. Deviations from the preset fit in a few lines of a committed `hades.toml`.
7. First-class on Apple Silicon macOS (darwin/arm64) and x86_64 Linux, Ubuntu/Debian
   (linux/amd64).

## 3. Non-goals

- Not a worktree manager. Hades never creates, lists, switches, or removes worktrees; the ADE or
  worktrunk does. It operates on a worktree that already exists.
- Not an installer. Caddy, the tunnel, Postgres, RabbitMQ, the ADEs, language toolchains, and the
  host configuration are installed by the Hades server playbook (companion document) or by hand
  on a laptop. The CLI checks for them (`doctor`) and uses them.
- No resource policy, no process supervision, no daemon, no network listener. It is a short-lived
  CLI.
- Not a build tool. Presets call the project's own setup (`mix setup`, `go mod download`,
  `npm ci`); hades does not know how to build anything.
- Other platforms. Windows is unsupported. linux/arm64 and darwin/amd64 are built only because the
  Go toolchain makes that free; they are untested and get no fixes of their own.
- No import of worktrees made before hades. Nothing was ever deployed to a server with the earlier
  jarl hook, so there is nothing to import there; laptop worktrees made by worktrunk keep working
  through `.config/wt.toml` until they are recreated. Hades does not read values an older hook left
  in `.envrc`.

## 4. Anti-goals

Even if everything technically works, it has failed if:

- a repository needs a config file before hades does something sensible for it;
- a failure is hidden: `down` may skip steps, but every skipped step is printed; `up` fails loudly;
- it destroys anything it does not own: no database, port, site, vhost, or manifest is ever touched
  unless the manifest of the worktree being operated on claims it;
- the project config speaks one ADE's vocabulary (Orca's or Paseo's names leak into `hades.toml`);
- a worktree that was fine last week stops working because a new hades version changed behaviour
  silently. The protection is light, not a compatibility layer: every manifest records the hades
  version and the concrete rules in effect (which databases, which vhost, which site), `down` and
  `gc` follow what the manifest recorded rather than the current defaults, and `doctor` lists
  manifests written by an older version together with the changelog lines that affect them. There
  is no behaviour pin in `hades.toml`.

## 5. Constraints and facts

### Platforms and dependencies

- Dev servers: Ubuntu (Debian family), x86_64. Laptops: Apple Silicon Macs. One static Go binary
  per platform, no CGO, no runtime dependencies.
- External programs hades may run, all optional and detected at run time: `git` (required),
  `direnv`, `mix`, `go`, `npm`/`pnpm`/`yarn`, `git-secret`/`gpg`, `sudo` (only for the host's
  Caddy reload wrapper). Postgres and RabbitMQ are reached over the network with Go clients, so
  `psql` and `rabbitmqctl` are not needed.
- Hooks run with the ADE daemon's environment: a non-login shell, no profile files, so `PATH` may
  lack the user's toolchain shims. Hades prepends `$HOME/.asdf/shims` and `$HOME/.local/bin` when
  they exist, plus any `tool_paths` from host config, before running project commands
  (builder's call).

### What the host provides (installed by infra, not by hades)

- Caddy on loopback with a `(worktree)` snippet and an include of `<caddy.live_dir>/*.caddy`, a
  root-owned directory that only `hades caddy-reload` writes; the developer-writable
  `<sites_dir>` is the staging area it validates and mirrors from. Each site file is exactly one
  line `import worktree <host> <port>`.
- A sudoers rule letting group `hades` run exactly `/usr/local/bin/hades caddy-reload` (and its
  `--check` form) as root without a password. The reload step is a hades subcommand (section 6.8),
  so the grammar that validates site files is the same code that writes them, tested in this
  repository; infra only installs the root-owned binary and the sudoers line.
- Registry directories `ports/`, `sites/`, `manifests/` owned `root:hades`, mode `3775` (setgid so
  files get the group, sticky so users cannot delete each other's files). Every developer is in
  group `hades`.
- A dev Postgres cluster (role, password, host, port in host config) where every developer may
  create and drop databases.
- Optionally a RabbitMQ management API (URL and credentials in host config).
- Optionally a Chrome binary path for agents' browser MCP servers.
- The host config file `/etc/hades/host.toml`, root-owned, world-readable, secrets excluded except
  dev-only credentials that are already public knowledge on the box (the dev Postgres role, the
  RabbitMQ dev user).

### ADE hook environments (verified against Orca 1.4.232 and Paseo 0.12.0)

| ADE | Root checkout | Worktree path | Name | Hook file, read from |
|---|---|---|---|---|
| Orca | `ORCA_ROOT_PATH` | `ORCA_WORKTREE_PATH` | `ORCA_WORKSPACE_NAME` | `orca.yaml`, primary checkout |
| Paseo | `PASEO_SOURCE_CHECKOUT_PATH` (alias `PASEO_ROOT_PATH`) | `PASEO_WORKTREE_PATH` | `PASEO_BRANCH_NAME` | `paseo.json`, the worktree (seeded from the source checkout) |
| worktrunk | cwd's common dir | cwd | `{{ branch }}` template | `.config/wt.toml` hooks |

Both ADEs run hooks with the worktree as working directory in `bash` without profile files.
Paseo aborts worktree removal when a teardown command exits non-zero and has no override; Orca
aborts unless the user passes `--allow-failed-archive-hook`. Both read the hook file from the repo,
so the hook must be a one-liner invoking a binary on `PATH`, not a script that an older branch may
lack.

Paseo specifics that matter here: `worktree.setup` runs once, right after the worktree is created;
a `servicePorts.portScript` is run later, only when a declared service script is started, without
a shell, with four positional arguments (script name, workspace id, branch, worktree path) and the
`PASEO_*` variables above in its environment; Paseo does not set `PORT` in any hook's environment
(a legacy `PASEO_WORKTREE_PORT` is present and ignored). So `hades port` is read-only: it reports
the port `up` claimed and fails if there is no manifest yet.

Because each caller derives the name differently (Orca: workspace name, which is the directory
basename; Paseo: branch; worktrunk and a shell: current branch), a worktree is identified by its
path, not by its name. The name, and therefore the label and hostname, is fixed at the first `up`
and recorded in the manifest; `down` and later `up` runs find the manifest by worktree path and
reuse it even when the branch was renamed or a different caller computes a different name. To get
a new label, `down` and then `up`.

### Naming limits

- A public hostname is one DNS label under the host's domain (Cloudflare Universal SSL covers one
  level only): `[a-z0-9-]`, at most 63 characters, so `<project>-<name>` must fit in 63.
- Postgres identifiers are at most 63 bytes. jarl additionally hashes a `DB_SUFFIX` longer than
  32 bytes in its own config; other Phoenix projects usually name databases `<app>_dev`.
- A generator-default Phoenix `config/dev.exs` reads `PORT` from the environment but hard-codes
  the database host, port, credentials and name (`<app>_dev`), and `config/test.exs` hard-codes
  `<app>_test` plus `MIX_TEST_PARTITION`. Such a project cannot reach the server's Postgres (not on
  5432) and every worktree would share one database. Hades cannot fix that from outside; the
  project's config must read a few environment variables (section 7). "Zero config" therefore means
  zero hades configuration, not zero changes to a project that hard-codes its dev database.

### The projects this must serve on day one

jarl (Phoenix, git-secret, Tidewave, multi-repo databases, RabbitMQ, LocalStack), PawsAndCo (a
jarl clone), pops and tokyo (Phoenix), saga (Elixir daemon, no web UI), tyrr (Go), runa
(React Native with Expo), infra (Ansible, nothing to set up), hati (stack unknown; must at least
not break).

## 6. Decisions

### 6.1 Commands

```
hades up        [--dry-run] [--skip-setup]      prepare the worktree (idempotent)
hades down      [--strict]                       tear it down (best effort, exit 0 unless --strict)
hades ls        [--mine] [--json]                 worktrees known on this machine
hades status    [--json]                          host summary: config, services, ports used/free
hades doctor    [--json]                          what is misconfigured, exit 1 if anything is
hades gc        [--dry-run] [--owner <user>]      clean up worktrees that vanished (root on servers);
                                                   --owner releases everything that user owns (offboarding)
hades init      [--ade paseo,orca,wt] [--force]   write hades.toml and ADE hook files into a repo
hades env       [--shell|--json]                  print the worktree's hades environment
hades port                                         print the worktree's port (for ADE port scripts)
hades caddy-reload [--check]                       root only, via sudo: validate site files, reload
hades version
```

Global flags: `--root <dir>`, `--worktree <dir>`, `--name <name>`, `--ade <orca|paseo|wt|none>`,
`-v/--verbose`, `--json` where listed. Exit codes: 0 success, 1 failure, 2 usage.

Human output goes to stderr; `--json` results go to stdout. Every `down` step that is skipped or
fails prints one line saying what was skipped and why.

### 6.2 Context resolution

Root checkout, worktree path, and name are resolved in this order; the first source that provides
a value wins, per value:

1. explicit flags `--root`, `--worktree`, `--name`;
2. ADE environment: Paseo's variables, then Orca's (`--ade` restricts to one);
3. git: worktree is `git rev-parse --show-toplevel` of the current directory; root is the parent
   of `git rev-parse --git-common-dir`; name is the current branch.

The name is sanitised to a hostname fragment: lower-cased, every run outside `[a-z0-9]` becomes one
`-`, leading and trailing `-` stripped. The project slug comes from `hades.toml` `project`, else
the root checkout's directory name; either source is sanitised the same way and must be at most
30 characters, else `up` fails naming the value.

The label is rendered from the host's `label_template` (section 6.3), by default
`{{project}}-{{name}}`, which is what Roman chose for tokyo (`jarl-<worktree>.romanc.io`). On a
server shared by several developers the infra playbook sets `{{project}}-{{user}}-{{name}}`, so two
people on the same branch of the same project cannot collide (builder's call; the template keeps
the choice per host). The name is cut so the label fits 63 characters, then a trailing `-` is
stripped. When a template without `{{user}}` produces a label that another user's or another
worktree's manifest already holds, `up` fails with "label taken by <user> for <path>"; that is the
accepted behaviour on a single-user box.

The label names everything public about a worktree: hostname `<label>.<domain>`, manifest file,
site file, vhost. The worktree's identity, used to find its manifest, is its path (section 5).

### 6.3 Configuration layers

**Host config** `/etc/hades/host.toml` (override with `HADES_HOST_CONFIG`). Its presence means
server mode; its absence means laptop mode. An explicit `HADES_HOST_CONFIG` that names a missing
file is an error, not laptop mode, so a typo cannot silently demote a server; the literal
`HADES_HOST_CONFIG=laptop` forces laptop mode (tests, or a developer who wants laptop behaviour on
a box that has a host config). Written by the server playbook, never by hades.

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

Hades refuses to start in server mode when a required key is missing or a registry directory is
missing, lacks the setgid or sticky bit, is not group-writable, is world-writable, or belongs to
a group the process is not in; `doctor` says which directory and which property.

**Project config** `hades.toml` at the repository root, committed. Read from the worktree first,
then from the root checkout if the worktree's branch predates the file, then preset defaults.
Every key is optional:

```toml
project = "jarl"                 # URL slug; default: root checkout directory name
preset = "phoenix"               # default: detected (section 6.4)
web = true                       # default: per preset
setup = ["mix setup"]            # replaces the preset's setup steps
teardown = ["mix some.cleanup"]  # extra steps, run before hades' own teardown
databases = ["gym_nation_dev", "gym_nation_internal_dev", "gym_nation_test"]  # base names (6.4)

[env]                            # extra variables, templated with {{host}} {{port}} {{url}}
JARL_ENDPOINT = "{{host}}"       # {{suffix}} {{name}} {{project}} {{label}}

[services.rabbitmq]
vhost = true                     # create vhost <label>; export RABBITMQ_VHOST and AMQP_URL

[mcp]
tidewave = true                  # default: detected from mix.lock
```

**Laptop defaults** without any host config: registry under `~/.local/state/hades/` on both
platforms (builder's call: one predictable path beats platform conventions here), port range
4100-4999, Postgres at `127.0.0.1:5432` as role `postgres`/`postgres`, overridable with
`HADES_PORT_RANGE`, `HADES_DB_PORT`, `HADES_DB_USER`, `HADES_DB_PASSWORD`. No URL, no sites, no
sudo. A `~/.config/hades/config.toml` is explicitly not part of version 1.

Precedence for behaviour: project config over preset defaults over built-ins. Host config decides
only what the machine offers.

### 6.4 Presets

Detection runs in the worktree, in this order, first match wins; `preset = "..."` overrides.

| Preset | Detected by | `web` default | Setup steps | Databases |
|---|---|---|---|---|
| `phoenix` | `mix.exs` whose deps include `phoenix` | true | `mix setup` if the alias exists, else `mix deps.get` | rule below if `ecto_sql` is a dep |
| `elixir` | any other `mix.exs` | false | same as phoenix | same rule if `ecto_sql` is a dep |
| `go` | `go.mod` | false | `go mod download` | none |
| `node` | `package.json` | false | `npm ci`, or `pnpm install --frozen-lockfile` / `yarn install --frozen-lockfile` by lockfile | none |
| `none` | anything else | false | nothing | none |

`none` does nothing and exits 0 with one line saying so; it touches no registry and writes no
manifest. Every other preset gets a manifest (so `down`, `ls` and `gc` know about its databases
and vhost), env files, secrets, and its setup steps. `web` decides only the port and the URL: a
preset with `web = true` claims a port and, in server mode, publishes a site; a non-web preset
claims no port, publishes nothing, exports no `PORT`, `PHX_HOST` or `HADES_URL`, and skips the
Tidewave MCP rewiring (it needs a port). A Go or Expo project that wants a URL sets `web = true`
in `hades.toml` (Expo's Metro server is the expected case).

What `up` does, in order, for a web preset in server mode. Each step is idempotent. A failure in
steps 1 to 9 releases everything claimed in this run and exits 1: nothing outside hades' own
registry has happened yet. A failure from step 10 on (the project's own setup) exits 1 but keeps
the manifest, port, URL and env files, because `mix setup` may already have created databases that
only the manifest knows how to find; the user re-runs `up` after fixing the cause, or runs `down`.

1. Resolve context, label, preset, config. If a manifest for the label exists and belongs to this
   user and this worktree, this is a re-run: reuse its port and continue. If it belongs to another
   user or another worktree: fail with "label taken by <user> for <path>".
2. Claim the lowest free port in the range by creating `ports/<port>` as a symlink to the worktree
   path; symlink creation is atomic, so two racing claims cannot both win a port.
3. Write the manifest (section 6.5) before doing anything else that leaves traces.
4. Compute the environment: `PORT`; `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD` from the
   host's Postgres block; `DB_SUFFIX`; `DATABASE_URL` and `TEST_DATABASE_URL`
   (`postgres://<user>:<password>@<host>:<port>/<app>_dev<suffix>` and the `_test` twin, for
   projects with a single repo that prefer one variable); `SECRET_KEY_BASE` (64 random bytes,
   base64); `PHX_HOST`, `HADES_URL`, `HADES_LABEL`; the `[env]` entries; and, when a RabbitMQ vhost
   is requested, `RABBITMQ_VHOST` and `AMQP_URL`. These are the documented variables of the
   credentials safety rule in 6.9.
   `DB_SUFFIX` is `_<user>_<name>` where the user part keeps only `[a-z0-9]` (so the first
   `_`-delimited segment is always the owner's login) and the name has `-` turned into `_` and
   everything outside `[a-z0-9_]` dropped, guaranteed at most 32 bytes: when longer, the first 22 bytes followed by `_` and the
   first 8 hex characters of the SHA-256 of the full string. A suffix never ends in a digit (a `_`
   is appended when it would), so a database name ending in digits after the suffix can only be
   this worktree's `MIX_TEST_PARTITION`, never a sibling worktree whose name ends in a digit. A
   suffix that is already short never triggers jarl's own hashing, so the names hades expects are
   the names the project creates.
5. Write the environment to two files in the worktree, both of which `init` adds to
   `.gitignore`: `.env.worktree` (KEY=VALUE, fully owned by hades, read by projects such as jarl
   through a loader) and `.envrc` (a managed block between `# hades:begin` and `# hades:end`
   lines, anything outside the block untouched, for terminals with direnv). Run `direnv allow` if
   direnv is installed.
6. Secrets: if `.gitsecret/` exists, run `git secret reveal -f`. Failure is fatal with the remedy
   printed: "this machine's GPG key must be added by a current key holder: `git secret tell
   <email>`".
7. Services: if `[services.rabbitmq] vhost = true` and the host offers RabbitMQ, create vhost
   `<label>` and grant the configured user full permissions on it through the management API.
8. Publish the URL: write `sites/<label>.caddy` and run `sudo -n /usr/local/bin/hades
   caddy-reload`. A reload failure fails `up` (an unreachable URL is a failed `up`; the
   validation inside `caddy-reload` keeps one user's bad file from breaking Caddy for everyone).
9. MCP wiring (section 6.6).
10. Setup steps, with the environment from step 4 and the extended `PATH`, from the worktree,
    unless `--skip-setup`.

Laptop mode runs the same steps minus 7 and 8, with a per-user registry.

What `down` does: read the manifest; if none exists or it is not this user's or not this
worktree's, print why and exit 0 (the ADE must still be allowed to archive). Then, each step
independent of the others' success: project `teardown` steps (only when the manifest is the
invoking user's own; root does not run another user's commands); drop the databases matched by
the project's rule with `DROP DATABASE ... WITH (FORCE)`; delete the vhost; remove the site file
and reload; remove the port link. The manifest itself is removed only when every step succeeded;
otherwise it is rewritten without the claims that were released (port, vhost, databases) and kept
so the leak stays visible to `ls` and `gc` retries exactly what failed, and `down` prints
`manifest kept (failed: <steps>)`. A later `up` on that worktree claims a fresh port. Exit 0
unless `--strict`.

A re-run of `up` on a worktree whose `hades.toml` changed keeps what the earlier run recorded:
the database base names become the union of the recorded and the newly configured ones, and a
recorded vhost is kept when the configuration no longer asks for one, so `down` can still release
what the first run created.

Database rule (phoenix and elixir): `databases` in `hades.toml` lists database **base names**,
everything before the suffix (`gym_nation_dev`, `gym_nation_internal_dev`, `gym_nation_test`,
…); when the key is absent the bases are `<app>_dev` and `<app>_test` from the Mix application
name. hades lists the cluster's database names and keeps a name only when, for some base, it
equals `<base><suffix>` or `<base><suffix><digits>` (regex `^<base><suffix>[0-9]*$` with both
parts escaped literally), `<suffix>` being the worktree's `DB_SUFFIX`. Base and suffix are
adjacent: nothing may sit between them, so another user's or another project's database cannot
match, and because a suffix never ends in a digit the trailing digits can only be this
worktree's `MIX_TEST_PARTITION`. There is no SQL LIKE and no wildcard for the author to get
wrong; an empty suffix or an empty base matches nothing. The concrete bases and suffix are
recorded in the manifest at `up` time, and `down` and `gc` match against the recorded values,
not against the current config.

### 6.5 Manifests and the registry

`manifests/<label>.toml`, written by the user running `up`, so owned by that user; the sticky
registry directories mean nobody else can remove it. Contents: hades version, created-at, user,
uid, project, name, label, host, port, worktree path, root path, preset, `DB_SUFFIX`, the
database base names in effect, services created (vhost name), ADE that invoked `up`.

Lookup is by worktree path: `down`, `env`, `port` and a repeated `up` find the manifest whose
recorded worktree path equals the resolved worktree path (paths compared after resolving
symlinks), considering only manifests owned by the invoking user (root sees all). The
implementation may keep a reverse index (for example `paths/<hash of path>` as a symlink to the
manifest) or scan the manifests directory; both are small (builder's call).

**The registry is untrusted input.** Every developer in group `hades` can write files into the
registry directories, so nothing in a manifest is believed without checking it against what the
file system guarantees (the file's owner, its name, its type):

- A manifest is read only if it is a regular file (no symlinks) and its file name is
  `<label>.toml` for the label it records; labels are plain names (never a path). A manifest that
  is misnamed, not a regular file, or fails to parse is skipped and reported as
  `ignored manifest <path>: <reason>` by `ls`, `gc` and `doctor` (a FAIL for `doctor`), and never
  stops `up`, `down`, `ls` or `port` for anyone else.
- Each manifest carries, in memory only, the uid of the file's owner. `down` acts only on the
  invoking user's manifests; `up` treats a label as taken when a manifest of that label exists
  that is not the invoking user's or not for this worktree.
- `gc` (root) releases a manifest only after verifying its claims against the file owner: the
  recorded `DB_SUFFIX` equals the suffix hades would compute for that owner's login and the
  recorded name (so a forged manifest can only name databases carrying its author's own suffix),
  the vhost is either empty or the label, a port link that points at the recorded worktree is
  owned by the same uid (a link that is missing or points elsewhere is simply not this manifest's
  to release), and the site file is `<label>.caddy`. The owner's login must resolve through the
  system account database (hades is built without cgo, so on Linux that means `/etc/passwd`; the
  server playbook creates local accounts); an unresolvable owner is a refusal, never a fallback to
  the manifest's `user` field. A manifest that fails verification is reported and left alone.
  `gc --owner <user>` selects by file owner, never by the `user` field.
- Accepted residuals of this model: a verified manifest may still name another worktree's
  database by choosing a base name that itself embeds that worktree's suffix, and two logins that
  differ only in punctuation produce the same suffix; both are bounded by the shared dev Postgres
  role (every developer can already drop every dev database) and by the server playbook keeping
  logins unique after stripping non-alphanumerics.
- The `user` and `uid` fields inside a manifest are informational (shown by `ls`), never a basis
  for a decision.

Ownership rule: `down` acts only on a manifest owned by the invoking user whose worktree path is
the worktree being torn down. `gc` as root acts on any manifest whose worktree path no longer
exists, and on `ports/` links whose target no longer exists, releasing exactly what the manifest
lists, then reloads Caddy once. `gc` never touches a manifest whose worktree still exists, with
one exception: `gc --owner <user>`, root only, releases every manifest of that user whether or not
the worktree exists, for offboarding by the server playbook. `ls` reads all manifests; `--mine`
filters by user.

Laptop mode uses the same files under `~/.local/state/hades/`.

### 6.6 MCP wiring

When the project uses Tidewave (`tidewave` present in `mix.lock`, or `[mcp] tidewave = true`),
`up` rewrites the Tidewave server URL to `http://localhost:<port>/tidewave/mcp` in each of these
files that exists in the worktree, and marks each `git update-index --skip-worktree` so the edit
never shows up in a commit: `.mcp.json` (Claude Code), `.codex/config.toml`,
`.gemini/settings.json`, `.cursor/mcp.json`, `.grok/settings.json`. The trigger is the file
existing, not the agent CLI being installed (jarl's earlier hook skipped silently when the CLI was
not on the hook's `PATH`). When `chrome_path` is set in host config, `.mcp.json` also gets a
`chrome-devtools` entry pointing at that binary. The exact per-file edit shapes are the ones in
jarl's `.config/wt.toml` today and move into hades verbatim.

### 6.7 ADE adapters

Hades ships the hook one-liners; `init` writes them.

- Paseo, `paseo.json`: `{"worktree": {"setup": ["hades up"], "teardown": ["hades down"],
  "servicePorts": {"portScript": "hades-port"}}}`. Paseo runs the port script without a shell and
  with four positional arguments, so the release ships a `hades-port` executable (a symlink or
  tiny wrapper to `hades port`, builder's call) that ignores its arguments and reads the port from
  the manifest found by `PASEO_WORKTREE_PATH`. This lets a declared Paseo service such as
  `mix phx.server` run on the same port hades published, and show up in Paseo's UI with its proxy
  URL and health. The service declaration itself is the project's choice and not written by
  `init`.
- Orca, `orca.yaml`: `scripts.setup: hades up`, `scripts.archive: hades down`,
  `setupAgentStartupPolicy: wait-for-setup`.
- worktrunk, `.config/wt.toml`: `pre-start = "hades up --name {{ branch }}"`,
  `pre-remove = "hades down --name {{ branch }}"`; `init --ade wt` prints the snippet and does
  not edit an existing `wt.toml`.
- Shell: `hades up` inside any worktree.

`init` never overwrites an existing `orca.yaml`, `paseo.json`, or `hades.toml` without `--force`;
when a hook file exists without hades in it, it prints the lines to add.

### 6.8 Distribution and platforms

- Releases on GitHub (quotedo/hades, private until Roman opens it) built by goreleaser:
  darwin/arm64 and linux/amd64 are first-class and tested in CI on macOS arm64 and Ubuntu runners;
  linux/arm64 and darwin/amd64 are built because the matrix makes it free and are labelled
  untested. Tarballs plus a checksums file. The version is embedded at build time and printed by
  `hades version` and recorded in manifests.
- Install: the infra playbook downloads a pinned release into `/usr/local/bin/hades`
  (root-owned, mode 0755) and installs the sudoers line for `hades caddy-reload`. Laptops: the
  release tarball or `go install`. A Homebrew tap only if goreleaser gives it for free (builder's
  call).
- `hades caddy-reload` is the one subcommand meant to run as root. It reads every `*.caddy` in
  `sites_dir` (at most 256 bytes each) and validates the bytes against the grammar
  `^import worktree [a-z0-9.-]+ [0-9]+$` (one line, newline terminated), requires the host of
  `<label>.caddy` to be exactly `<label>.<domain>` and a regular `manifests/<label>.toml` owned by
  the same uid as the site file (so a developer can publish only hostnames of worktrees they own),
  refusing to continue if any file fails and naming it; then mirrors exactly those validated bytes into the root-owned
  `caddy.live_dir` (temp file and rename per site, stale files removed), so a file swapped after
  validation never reaches Caddy; then runs `caddy validate --config <caddy.config>`; then the
  configured reload command. With `--check` it stops after validation and touches nothing. When
  it runs as root it ignores `HADES_HOST_CONFIG` and `HOME`, reads `/etc/hades/host.toml` only,
  and refuses a host config that is not a root-owned regular file free of group and world write
  bits. Every command it runs gets a fixed `PATH` (`/usr/local/sbin:/usr/local/bin:/usr/sbin:
  /usr/bin:/sbin:/bin`), never the caller's. It accepts no positional arguments. So the sudoers
  rule exposes nothing beyond validating, mirroring and reloading.
- Go 1.25, modules, no CGO. Dependencies kept to a TOML parser, a Postgres driver, and at most one
  CLI helper library; everything else from the standard library.

### 6.9 Safety rules

- Never drop a database, release a port, remove a site, or delete a vhost that the manifest being
  acted on does not list or match.
- Never act on another user's manifest, except `gc` running as root on vanished worktrees.
- Never modify tracked files other than the MCP configs in 6.6, and never run `git commit`,
  `git push`, or change branches.
- `up` rolls back everything claimed in the current run on failure, and nothing claimed earlier.
- `down` is best effort and exits 0 unless `--strict`; each skipped step is printed.
- Credentials from host config are passed to project commands only as the documented environment
  variables; hades never writes them anywhere but the two env files inside the worktree.

### 6.10 Testing

- Unit tests: name and label derivation, suffix hashing, config parsing and layering, preset
  detection, managed `.envrc` block, `.env.worktree` writing, manifest round trip, context
  resolution from each ADE environment, MCP file edits, database rule matching.
- Integration tests with temporary directories: a host config pointing at temp registry
  directories, a fake reload wrapper that records calls and can be told to fail, stub `mix`,
  `go`, `npm` on `PATH` that record their invocation and environment, two users simulated by two
  manifests with different owners. Postgres-backed tests run against a real cluster when
  `HADES_TEST_PG` points at one, and are skipped otherwise; CI provides one on both platforms.
- Acceptance on the real dev server (through Orca and Paseo, with jarl) belongs to the infra plan,
  not to this repository.

## 7. Consequences for the other repositories

- jarl: `hades.toml` with `project = "jarl"`, `[env] JARL_ENDPOINT = "{{host}}"`, and
  `[services.rabbitmq] vhost = true` once jarl reads `RABBITMQ_VHOST`/`AMQP_URL`; `orca.yaml` and
  `paseo.json` one-liners; `bin/dev-worktree` and its tests are removed from the hooks branch, the
  `.env.worktree` loader and the `DB_PORT`/`SECRET_KEY_BASE`/`JARL_ENDPOINT` config commits stay;
  `.config/wt.toml` hooks shrink to the two hades lines when laptops adopt it.
- Hades server playbook (companion document): installs the pinned binary, writes `host.toml`,
  installs the sudoers line, creates group `hades` and the registry directories, runs `hades gc`
  from a timer, and uses `hades doctor` and `hades status` in verification. GymNation's infra
  repository keeps only its inventory and site extras.
- Phoenix projects with generator-default config (pops, tokyo, PawsAndCo if it did not inherit
  jarl's config): `config/dev.exs` and `config/test.exs` read `PORT`, `DB_HOST`, `DB_PORT`,
  `DB_USER`, `DB_PASSWORD` and `DB_SUFFIX` with the generator's values as defaults (or
  `DATABASE_URL`/`TEST_DATABASE_URL` for a single repo), about ten lines per project.
  `hades init` prints the exact snippet for the detected preset and names the files to edit; it
  does not edit them.
- Other projects: `hades init` in each, commit, done; PawsAndCo copies jarl's `hades.toml`.

## 8. Left to the builder

- CLI and TOML libraries, output formatting, log wording.
- The exact port scan (lowest free first is required; locking strategy for same-user re-runs is
  not).
- Node package manager detection details beyond lockfile presence.
- Whether `hades-port` is a symlink or a wrapper, and whether manifest lookup by path uses a
  reverse index or a scan.
- The internal layout of the Go module and the test fixtures.

## 9. Open questions

None blocking. Licence is Roman's choice when the repository goes public.
