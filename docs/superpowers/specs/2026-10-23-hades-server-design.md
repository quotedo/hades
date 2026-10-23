# Hades server: turning a Linux box into a dev server for ADEs

Status: revised after the builder check (2026-10-23); Roman approved continuing to plans and
implementation the same day. Companion of the CLI design
(`2026-10-23-hades-cli-design.md`). Ported and generalised from the GymNation infra design
`2026-10-20-tokyo-dev-server-design.md`, which Roman approved on 2026-10-20 and which becomes a thin
site document once this is approved. Decisions marked "(builder's call)" are mine.

## 1. Intent

Hades is one project with two deliverables:

1. the `hades` CLI (companion document): what every ADE's worktree hook calls;
2. **Hades server** (this document): an Ansible playbook that turns a plain Ubuntu or Debian
   x86_64 machine, VPS or dedicated, into a shared development server for agentic development
   environments (ADEs): one Linux user per developer, each running the ADE daemons they choose
   (Paseo, Orca), any number of projects and parallel worktrees per user, every web worktree on a
   public HTTPS URL, databases and brokers per worktree, resource limits so one runaway job cannot
   take the box down, and a protected floor for anything else already running on the machine.

The name: Hades, a pun on ADEs; the H is for Host.

Why: Roman's dedicated box `tokyo` is the first server (one user, nine projects, two ADEs), and
company servers with four or five developers each follow. The same playbook must provision both,
driven only by inventory: user list, domain, which ADEs, which shared services, what to protect.
Nothing about a company, a box, or a project is hard-coded; GymNation's infra repository keeps only
its inventory, secrets handling, and site-specific extras, and calls this playbook.

## 2. Goals

1. One idempotent playbook run on a fresh Ubuntu/Debian x86_64 host gives every listed developer a
   working account with toolchain, ADE daemons, and `hades`, surviving reboots and logouts.
2. Any project a developer clones works through `hades up` with no server-side change: Phoenix
   projects get a port, databases, secrets, and `https://<label>.<domain>`; others get what their
   preset gives.
3. Several developers, each with several ADEs and many worktrees, share one box without seeing or
   affecting each other: separate users, separate ADE daemons with authentication, separate
   databases, per-user resource ceilings.
4. Heavy parallel load (for jarl: two or three `mix precommit` and five or six `mix test` at once
   per user) does not kill the box, other users, or the protected workloads.
5. Dev-only paths (mailboxes, dashboards, Tidewave) are reachable by the developers from anywhere
   over HTTPS and by nobody else; everything else on a worktree URL is fully public.
6. A reboot brings everything back without anyone logging in.
7. Everything is in the playbook. Interactive one-time steps are listed in one place and are the
   only manual actions.

## 3. Non-goals

- Fairness tuning for five developers; the limits are parameters with documented starting values.
- Managing the projects themselves: cloning is offered as a convenience (`repos` per user) but
  onboarding a project is `hades init` in that repository, not a server task.
- Containers or VMs per developer. Isolation is Linux users plus cgroups; developers are trusted
  colleagues, their agents are not trusted with anything outside their user.
- Backups of dev data beyond a nightly tarball of ADE state and agent logins (section 6.10).
- High availability, monitoring stacks, Kubernetes.
- ARM servers, non-Debian distributions, Windows. Only Ubuntu LTS and Debian stable on x86_64 are
  supported; anything else is untested and gets no effort.
- Replacing an existing reverse proxy on the box. Ingress is a Cloudflare Tunnel to a loopback Caddy
  precisely so the box's ports 80 and 443 are never touched.
- Per-worktree framed shells or any project-specific second service. The registry and Caddy
  snippet are additive if a project needs a second hostname later.

## 4. Anti-goals

- An ADE daemon reachable from the public internet, or reachable by another local user without
  authentication. Every local user on a shared box, including CI runner accounts, is a potential
  caller of `127.0.0.1:<port>`.
- A secret in the repository or in a committed variable file. Secrets reach the playbook only from
  the environment of the person running it, and reach the box only as root-owned or user-owned
  files with mode 0600.
- A developer's agent able to reach the box's other workloads: their databases, the Docker socket,
  other users' homes.
- A superuser login page or an MCP endpoint exposed to strangers through a public worktree URL.
- Any address, domain, port, interface, or user name as a constant in a role. `verify.yml` fails
  if an inventory literal appears in a template or task body.
- A hand-configured box. If it is not in the playbook, it does not exist.

## 5. Constraints and facts

### Target hosts

- Ubuntu LTS (24.04 or 26.04) or Debian stable, x86_64, systemd with cgroup v2 (cpu, memory, io,
  pids controllers), Docker CE optional but used for shared services. A VPS with 8 GB works for one
  light user; the numbers below are written for a 128 GB dedicated box and are inventory
  parameters.
- The playbook connects as an existing sudo-capable SSH user named in inventory and never changes
  that user, the SSH daemon, or the firewall default policy. It adds rules only for what it
  installs.
- The box may already run other workloads (on tokyo: three Kamal-deployed apps behind a proxy on
  ports 80 and 443, a Postgres cluster on 5432, six GitHub Actions runner users). Those are
  "protected workloads": the playbook gives them a floor and keeps dev users away from them, and
  changes nothing else about them.

### ADEs (verified 2026-10-20 for Orca 1.4.232, 2026-10-23 for Paseo 0.12.0)

| | Paseo | Orca |
|---|---|---|
| Server runtime | Node daemon, `npm install -g @getpaseo/cli`, one per user, `~/.paseo` | Linux AppImage per user, `orca-ide serve --supervise`, profile `~/.config/orca` |
| Foreground run for systemd | `paseo daemon run` (env `PASEO_LISTEN`, `PASEO_PASSWORD`, `PASEO_RELAY_ENABLED` apply in this mode) | `orca-ide serve --port <p> --pairing-address <addr>` or its generated user unit |
| Listens | `127.0.0.1:6767` by default, configurable | configured port, pairing address |
| Client connection | Desktop and CLI over **SSH** to the daemon's loopback port (`?daemonPort=`); phone via end-to-end encrypted **relay** (outbound only, `paseo daemon pair --relay`) or Tailscale | Paired clients over the pairing address; docs say use **Tailscale**, never expose the port |
| Local authentication | bcrypt password in `config.json` (`paseo daemon set-password` or `PASEO_PASSWORD`); without it, any local process can drive the daemon | pairing grants per client; local loopback trust not documented |
| Updates | `npm install -g @getpaseo/cli@<v>` plus unit restart; CLI on npm the same day as each stable release, roughly weekly; desktop auto-updates on a 36-hour rollout; client/daemon compatibility undocumented | self-update of a supervised headless host from a paired desktop client |
| Hook env and files | `PASEO_*`, `paseo.json` in the worktree | `ORCA_*`, `orca.yaml` from the primary checkout |
| Browser | tabs render in the desktop app; the daemon runs no browser | embedded browser renders on the client by default |
| Terminal caps | none | `ORCA_TERMINAL_MEMORY_MAX` etc. as systemd user scopes |
| Notes | 0.x, moving fast; Linux tested by early adopters; `node-pty` native addon (prebuilt usually) | needs the Electron/Xvfb library set for the runtime |

Both run the project's agents (Claude Code, Codex, others) as the user's own subprocesses with the
user's own credentials, installed and logged in once per user on the box.

### Ingress facts

- Cloudflare's free Universal SSL covers the apex and one label, so worktree hostnames are one
  label: `<label>.<domain>`. Cloudflare proxies WebSockets; request bodies are capped at 100 MB.
- A remotely managed Cloudflare Tunnel needs one token (created once in the dashboard); one
  ingress rule `*.<domain>` to a loopback origin; explicit DNS records keep precedence over the
  wildcard.
- Cloudflare Access (free plan, up to 50 seats) can gate paths on a wildcard application
  (`dev`, `dev/*`, `tidewave`, `tidewave/*`) with an email or domain policy. The origin being
  reachable only through the tunnel is what makes the edge decision authoritative.

### Projects and toolchains

Elixir/Phoenix (Erlang and Elixir pinned per repository in `.tool-versions`; Oban Pro from a
private hex repository needs a per-user licence key; some deps need make and gcc), Go, Node (Expo),
Python, Ansible. Each developer wants their own toolchain versions, like on a laptop.

## 6. Decisions

### 6.1 Topology: direct on the host, one Linux user per developer

No container or VM wall. Each developer is a local Linux user (in `/etc/passwd`; hades resolves
owners without cgo) in group `hades`, no sudo, no docker group, lingering enabled, home on the
root filesystem. Logins must stay unique after stripping everything outside `[a-z0-9]`
(`j-smith` and `jsmith` cannot both exist), because hades derives database suffixes from that
projection; the playbook asserts it over `dev_users`. All of a developer's processes (ADE daemons,
terminals, agents, builds) live in their `user-<uid>.slice`. Dev system services live in
`hades.slice`. Protected workloads keep their own slices with a floor.

### 6.2 Resource policy (cgroup v2 via systemd drop-ins, all values inventory parameters)

| Scope | Starting values for a 128 GB box |
|---|---|
| each dev user's slice | `MemoryHigh=88G MemoryMax=96G MemorySwapMax=2G CPUWeight=900 TasksMax=infinity` |
| `hades.slice` (dev Postgres, shared services, Caddy, cloudflared) | `MemoryMax=16G CPUWeight=300` |
| protected floor (`system.slice`) | `MemoryLow=10G CPUWeight=300`; `user.slice CPUWeight=700` |
| capped users (inventory list, e.g. CI runner accounts running as user services) | `CPUWeight=100 MemoryMax=6G` each, as `user-<uid>.slice` drop-ins |
| capped units (inventory list of system units, for runners installed as system services) | same values, as drop-ins on the named units |
| Orca terminals (if Orca enabled) | `ORCA_TERMINAL_MEMORY_MAX=28G`, swap 0 |

Plus `vm.swappiness=10`, governor `performance` when the host exposes one,
`fs.inotify.max_user_instances=4096`, `fs.inotify.max_user_watches=2097152`,
`DefaultLimitNOFILE=1048576` for user managers, and `ERL_FLAGS="+sbwt none +sbwtdcpu none
+sbwtdio none"` in every dev user's environment so idle BEAMs do not spin. The numbers are verified
under the acceptance load and adjusted in inventory, never by hand.

### 6.3 ADE daemons per user

Inventory lists `ades: [paseo, orca]` per user; each is a thin tasks file with the same four jobs:
install a pinned version, run it as a systemd user unit, authenticate it, and provide an update
script. Host components switch on by need: the Electron/Xvfb library set and Tailscale only when
some user has Orca. Adding an ADE later is one tasks file plus one row in the acceptance list.

**Paseo.** Per user: Node via asdf, `npm install -g @getpaseo/cli@<paseo_version>`;
`~/.paseo/config.json` with `daemon.listen = 127.0.0.1:<paseo_port>` (distinct per user),
`daemon.relay.enabled = false`, `daemon.mcp.injectIntoAgents` as inventory says; a systemd user
unit `paseo.service` running `paseo daemon run` in the foreground with a drop-in setting
`Environment=PATH=%h/.local/bin:%h/.asdf/shims:/usr/local/bin:/usr/bin:/bin`, `ERL_FLAGS`,
`CHROME_PATH`, and `EnvironmentFile=%h/.config/hades/paseo.env` (mode 0600) carrying
`PASEO_PASSWORD`, generated by the playbook once per user and printed to the operator for entry
in the desktop client. Connection from desktops is **SSH** (nothing opened, no Tailscale); the phone
uses the relay, which the user enables themselves with `paseo daemon pair --relay` (one-time,
interactive; a relay is outbound only and end-to-end encrypted) unless inventory enables Tailscale.
Update: `hades-ade-update paseo` (a script the playbook installs per user) runs the npm install
of the pinned or latest version and restarts the unit; the inventory pin is what a playbook run
installs when nothing is installed, and a re-run never downgrades a newer self-installed version
(builder's call, mirrors the Orca rule below).

**Orca.** As in the 2026-10-20 design: the AppImage per user at `~/.local/opt/orca/orca.AppImage`,
CLI `~/.local/bin/orca-ide`, `orca-ide serve service install --port <orca_port>` as the user plus a
drop-in with the terminal caps, PATH, `ERL_FLAGS`, `CHROME_PATH`; pairing address the Tailscale
IP; pairing by stopping the unit and running `serve` in the foreground once per client; the
playbook never downgrades a self-updated AppImage. Tailscale is installed and `tailscale up` is
a one-time interactive step when any user has Orca or `tailscale_enabled` is set.

**Agents.** Claude Code and Codex CLIs per user (others optional), logged in once by the user from
the server shell; `gh auth login` per user with a token scoped to their repositories. Browser for
agents: Chrome for Testing (stable) system-wide under `/opt/chrome-for-testing` via
`@puppeteer/browsers`, exported as `CHROME_PATH`, so `hades up` can wire a `chrome-devtools` MCP
server into agent configs. Neither ADE needs a server-side browser for its own browser pane.

### 6.4 Toolchain per user

asdf-vm (Go binary) per user with plugins `erlang`, `elixir`, `nodejs`, `golang`, `python`
(inventory `asdf_plugins`); global versions from inventory (`asdf_global_versions`, e.g. Node LTS
for the ADE daemons and agent CLIs); project versions come from each repository's `.tool-versions`
and `hades up` runs `asdf install` for them. Erlang built with
`KERL_CONFIGURE_OPTIONS="--without-wx --without-javac --without-odbc"` and no docs; host build deps
installed by the playbook (`autoconf m4 libssl-dev libncurses-dev libxml2-utils xsltproc
inotify-tools build-essential git curl unzip jq`). `corepack enable` so `packageManager` fields
resolve. `mix local.hex`, `mix local.rebar`; private hex repositories from inventory
(`hex_repos: [{name, url, key_env, fingerprint_env}]`) with keys taken from the playbook runner's
environment. `direnv` installed and hooked into the users' shells. `git-secret` installed; a GPG
key per user (`<email>+<host>` convention, no passphrase, user keyring only) generated when
`gpg_key: true`, public key printed for the project's key holders to `git secret tell`.

Per user because it matches laptops and lets one developer test a version bump alone; the
one-time OTP build per user is accepted (a kerl build cache under `/srv/hades/cache` is a
builder option).

### 6.5 Hades CLI on the host

The playbook installs the pinned `hades_version` release to `/usr/local/bin/hades` (root, 0755),
the `hades-port` companion, group `hades`, the registry `/srv/hades/{ports,sites,manifests}`
(`root:hades`, mode 3775), `/etc/hades/host.toml` rendered from inventory (domain, label
template, port range, registry paths, Postgres and RabbitMQ blocks, `chrome_path`, `tool_paths`,
Caddy config path, reload command and `live_dir`), the root-owned Caddy import directory
`/etc/caddy/hades-sites` (0755; only `hades caddy-reload` writes it), a sudoers file allowing
group `hades` to run exactly `/usr/local/bin/hades caddy-reload` and
`/usr/local/bin/hades caddy-reload --check` as root (with sudo's default `env_reset`; no
`env_keep` for `HADES_*`, `HOME` or `PATH`), a weekly root timer running `hades gc`, and
`/usr/local/bin/hades-status` for the operator (section 6.10). `/etc/hades/host.toml` is
root-owned, mode 0644: `caddy-reload` refuses a group- or world-writable one. `verify.yml` runs
`hades doctor` as every dev user and requires exit 0.

### 6.6 Dev Postgres

A dedicated cluster (`pg_createcluster <major> dev`) on `127.0.0.1:<dev_pg_port>` (5433 by
default), under `hades.slice`, tuned for throwaway data: `fsync=off`, `synchronous_commit=off`,
`full_page_writes=off`, `shared_buffers`, `work_mem`, `max_connections=1000`, `max_wal_size=4GB`,
`log_min_messages=warning` from inventory.

The cluster runs as its **own OS user** `hades-postgres` (`pg_createcluster --user`), not as the
distribution's `postgres` user. The login role is `postgres`/`postgres`, a superuser of this
cluster only (dev convention; jarl, pops and stock Phoenix configs use it). Superuser is required
because projects create extensions Postgres does not mark trusted: jarl's migrations need
`postgis` (plus `citext`, `pg_trgm`, `unaccent`, which are trusted). What a superuser can do beyond
SQL, read files and run programs as the cluster's OS user, is therefore confined to
`hades-postgres`, which owns nothing but this cluster and can read neither dev homes (0750) nor a
protected cluster's data (0700, owned by `postgres`). Accepted residual: developers on one box
share the role, so an agent could drop or read a colleague's dev databases; dev data is
reproducible and the people are colleagues. Per-user roles become possible when no project on the
box needs an untrusted extension (builder's call to leave that as an inventory switch, not built).

`pg_hba`: scram on loopback only. Dev users may create and drop databases; names carry hades'
per-user suffix so users do not collide. A pre-existing Postgres on the box is a protected
workload: the loopback isolation rules (6.12) reject TCP from gid `hades` to
`dev_group_blocked_ports` (e.g. 5432) on every local address, including Docker bridge addresses
discovered at run time.

### 6.7 Shared dev services

One Docker Compose stack per host, root-owned unit `hades-services.service` under `hades.slice`,
rendered from inventory `dev_services`: `rabbitmq` (management plus MQTT plugins, management API on
loopback so `hades up` can create a vhost per worktree), `localstack` (S3, buckets from a list),
`redis`, each only if listed, every port bound to `127.0.0.1`. Dev users have no Docker access.
Known limitation carried over: fixed LocalStack bucket names mean worktrees of one project share
S3 objects.

### 6.8 Public worktree URLs

Cloudflare Tunnel (`cloudflared`, remotely managed, token from the operator's environment) with one
ingress rule `*.<domain>` to `http://127.0.0.1:<caddy_port>`; TLS at Cloudflare's edge, plain HTTP
on loopback, no firewall change. Caddy as a system service in `hades.slice`, `auto_https off`,
listening on `127.0.0.1:<caddy_port>`, importing `/etc/caddy/hades-sites/*.caddy` (one line each,
`import worktree <host> <port>`; `hades up` writes the developer-owned staging copy under
`/srv/hades/sites/`, and `hades caddy-reload`, as root, validates and mirrors it into the import
directory so Caddy never reads a developer-writable file), a `(worktree)` snippet mapping the host
to `127.0.0.1:<port>`, and a 404 page for unknown hosts naming the convention. Dev-only paths are **gated at Cloudflare's edge, not blocked**: a
Cloudflare Access application on `*` of the zone for paths `dev`, `dev/*`, `tidewave`,
`tidewave/*` with the developers' email or domain policy, so the people on the box reach
`/dev/mailbox` or Tidewave Web from any device after one login per hostname while everything else
stays public. `/phoenix/live_reload/*` is gated the same way rather than refused, so a developer
browsing the public URL keeps live reload while anonymous clients get nothing. Caddy additionally
requires the `Cf-Access-Jwt-Assertion` header on those paths as a tripwire, by presence only; the
tunnel-only origin plus the loopback isolation of 6.12 are the real boundary, so stock Caddy from
apt is enough and no JWT validation module is needed. `cloudflare_team_domain` is optional and used
only by `verify.yml`, which expects an anonymous request to a gated path to redirect to that team
domain; no audience tag is needed anywhere.

Zone constraint: wildcard mode needs a zone whose wildcard record is dedicated to this server, and
whose other hostnames tolerate the Access gating of `/dev*`, `/tidewave*` and
`/phoenix/live_reload*` (the Access application applies zone-wide). On tokyo that is `romanc.io`:
its other hostnames (the apex, the hex registry) have no such paths. Two servers cannot share one
zone's wildcard, and a production zone should not carry this gating, so each company server gets a
dedicated, cheap domain of its own (builder's call; Roman decides the names). Wildcard DNS over
per-worktree records because it needs no API token on the box and nothing to clean up; a
per-record mode for shared zones stays an inventory switch that is not built now.

### 6.9 Protected workloads (optional role, inventory `protected`)

For a box that already runs something: the `system.slice` floor and `user.slice` weight from 6.2;
`capped_users` and `capped_units` drop-ins (on tokyo the six GitHub runner accounts are lingering
user sessions running the runner as a user service, verified 2026-10-23, so `capped_users` is the
right one there; runners installed as system services take `capped_units`);
`dev_group_blocked_ports` (6.6); `private_homes` (home directories made 0750 so dev users cannot
read them). Anything beyond that (on tokyo: shrinking the existing Postgres cluster's
`shared_buffers`) is a site task in the caller's repository, not in hades.

### 6.10 Ops

- `hades gc` weekly as root (6.5).
- Nightly tarball per dev user of ADE state (`~/.paseo` minus logs, `~/.config/orca`, `~/.orca`)
  and agent logins (`~/.claude`, `~/.codex`, `~/.config/gh`) to `backup_dir`, keep 7. Dev data is
  reproducible; this is insurance against an accidental `rm`.
- `hades-status`: slice memory and CPU pressure, live worktrees with ports and URLs (from
  `hades ls --json`), dev Postgres connection count, last OOM kills from the journal, filesystem
  usage with the largest dev homes. It answers "why did my test die" and "who is filling the disk".
- `hades-ade-update <ade>` per user (6.3).
- Disk space is an accepted risk in version 1: homes, the dev cluster and Docker share the root
  filesystem, and per-user quotas would mean fstab changes and remounts on a live box with
  protected workloads. The playbook caps what it can without that (journald size, Docker log
  rotation) and `hades-status` makes the rest visible. Quotas are a later option behind an
  inventory switch, not built.
- Offboarding: a user with `state: absent` in `dev_users` is converged away: their ADE units are
  stopped and disabled, `hades gc --owner <user>` (root) releases every manifest they own
  (databases, vhosts, sites, ports), linger is disabled and their processes killed, the home is
  tarred into `backup_dir` once, then the account and home are removed. Users are never inferred
  from group membership; only inventory says who exists.
- `unattended-upgrades` left as is; reboots are manual; everything returns on boot.

### 6.12 Loopback isolation

Loopback is not private on a shared box: every local account, CI runner users included, can open
`127.0.0.1:<port>`. A worktree's port carries Tidewave, which runs code as that developer; Caddy
on loopback would accept a forged tripwire header; the dev Postgres password is known. So the
`host` role installs an owner-match rule set (nftables, or iptables under ufw where that is what
the box runs; builder's call) rejecting TCP to the dev port set from any uid outside the allowed
set, on every local address: the set is `port_range`, the dev Postgres port, the shared services'
ports, the Caddy port and every ADE daemon port; allowed are uids in group `hades`, root, and the
service accounts that must reach those ports (`caddy`, `cloudflared`, `hades-postgres`). The same
mechanism carries `dev_group_blocked_ports` (6.6). Accepted residual for version 1: developers on
the same box can reach each other's loopback apps and Tidewave, because group-level rules cannot
tell them apart and the CLI writes no firewall rules. The documented extension is one loopback
address per developer (inventory-driven static rules, `hades up` exporting a bind address); it is
an inventory switch that is not built now.

### 6.11 Provisioning layout

The repository ships an Ansible collection under `ansible/` (installable with `ansible-galaxy
collection install git+https://github.com/quotedo/hades.git#/ansible/`), with roles:

- `host`: packages, sysctl, governor, slices, group and registry, hades binary and host config,
  sudoers, loopback isolation rules, direnv, Chrome, gc and backup timers, status script;
- `postgres`: the dev cluster;
- `services`: the compose stack;
- `ingress`: Caddy, cloudflared, Access expectations, optional Tailscale;
- `user` (looped over `dev_users`): account, linger, slice drop-in, toolchain, hex repos, gh,
  agent CLIs, GPG key, `repos` clones, ADE tasks files per listed ADE;
- `protected`: section 6.9;

plus `playbooks/site.yml`, `playbooks/verify.yml`, an `inventory.example.yml` with placeholders
only, `scripts/check-no-literals.sh` (reads the inventory through `ansible-inventory --list` and
greps the roles), and a README for a site repository showing a wrapper playbook, inventory, and
`.env.example`. Required variables are asserted at the start of the role that consumes them, so
partial runs (`--tags host`) work before the Cloudflare checkpoint, and a full run still fails
before touching the host when a tunnel token or Access variable is missing.

Interactive one-time steps, listed in `site.yml`'s header: Cloudflare tunnel and Access
application in the dashboard; per user `gh auth login`, agent CLI logins, entering the Paseo
password in the desktop client, `paseo daemon pair --relay` for a phone; `tailscale up` and Orca
client pairing when Orca is enabled; `git secret tell` by a project's key holder.

Inventory shape (every value a placeholder in the example):

```yaml
dev_servers:
  hosts:
    box:
      ansible_host: <IP>
      ansible_user: <sudo user>
      hades_domain: <domain>
      hades_label_template: "{{project}}-{{name}}"      # shared boxes: "{{project}}-{{user}}-{{name}}"
      cloudflare_team_domain: <team>.cloudflareaccess.com     # optional, verify only
      dev_services: [rabbitmq, localstack]
      protected: { floor: true, capped_users: [...], capped_units: [...], blocked_ports: [5432],
                   private_homes: [...] }
      dev_users:
        - { name: roman, uid: 2001, state: present, ssh_keys: [...], ades: [paseo, orca],
            paseo_port: 6767, orca_port: 6768, gpg_key: true, repos: [...] }
```

Secrets (`CLOUDFLARE_TUNNEL_TOKEN`, hex repository keys) come only from the operator's
environment; `.env.example` names them.

## 7. Acceptance (generic; a site adds its own project-specific checks)

1. `site.yml` twice on a fresh host: the second run reports zero changes; `verify.yml` passes,
   including `hades doctor` as every dev user and the no-literals scan.
2. For each dev user and each enabled ADE: the daemon is active after a reboot without login; a
   desktop client connects (Paseo over SSH with the password; Orca paired over Tailscale); a
   second local user on the box cannot drive the daemon (Paseo without the password is refused).
3. Creating a worktree of a sample Phoenix project through each enabled ADE results, with no
   manual step, in `https://<label>.<domain>` serving the app to an anonymous browser; `/dev/*`
   on that host redirects anonymous browsers to Cloudflare Access and works after login; the
   live-reload socket is refused anonymously; an agent in the worktree reaches the app's MCP
   endpoint on loopback and opens the page through the `chrome-devtools` MCP.
4. Archiving the worktree from the ADE drops its databases, removes its vhost, frees its port and
   makes the URL 404; `hades ls` no longer lists it; a worktree directory deleted by hand is
   cleaned by `hades gc`.
5. Load: the inventory's load profile (for a jarl box: six worktrees, three `mix precommit` and six
   `mix test` started within a minute) completes; protected workloads keep answering (HTTP 200,
   p95 under the inventory threshold, sampled every 5 s); no OOM kill outside the dev users'
   slices; dev Postgres never refuses a connection.
6. Isolation from a dev user's shell: `docker ps` fails; TCP to each blocked port is refused on
   loopback, bridge, and public addresses; private homes are unreadable; ADE ports are not
   reachable from the public address. From a non-dev local account (a capped user or a throwaway
   test user): TCP to a worktree port, the dev Postgres port, the Caddy port and an ADE port is
   refused.
7. Offboarding: setting a test user to `state: absent` and re-running the playbook leaves no
   account, home, unit, manifest, database, site or port of theirs, and one archive in
   `backup_dir`.

## 8. Left to the builder

- Role internals, handlers, tags, exact Caddy and compose templates, the 404 and status pages.
- How `verify.yml` measures the load test and reads the protected workloads' health URL from
  inventory.
- Whether Orca's generated user unit takes a drop-in cleanly or the role ships its own unit.
- Kerl build caching, Node version pin default, Paseo relay versus Tailscale default for phones
  (both are inventory switches).
- Final resource numbers after the load test.

## 9. Open questions

None blocking. The tokyo-specific items (Tokyo Postgres `shared_buffers`, the runner accounts, home
permissions, jarl's own acceptance checks) move to the GymNation infra site document.
