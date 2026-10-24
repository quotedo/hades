# Hades server v2: one administrator, a shared toolchain, intranet VPN, production access, Tailscale sharing

Date: 2026-10-24. Status: for the owner's approval. Amends `2026-10-23-hades-server-design.md`
(v1, branch `server`): sections 6.3 and 6.4 of v1 are replaced here; everything else in v1 stands.
Facts below marked *(found)* come from the owner's own machine, the GymNation repositories and the
clusters on 2026-10-24; facts marked *(builder)* must be verified by the builder before the plan
relies on them. Decisions marked *(my call)* were made by the designer, not the owner; section 10
lists them. Decisions marked *(owner)* were made by the owner in conversation.

## 1. Intent

v1 gives each developer their own Linux account, their own toolchain and ADE daemons, and no sudo.
Running it showed four things the owner wants that v1 does not do:

1. Most dev servers must reach the company intranet, which sits behind an AmneziaWG VPN.
2. Developers work on production from the dev server (kubectl, ssh) with their own, differing
   permissions; the server must support that without ever sharing production credentials.
3. Each developer wants the server in their own Tailscale network.
4. One developer per box is its administrator and needs root; the toolchain should be shared and
   identical for everyone on the box instead of one copy per user.

The owner also asked about encrypting home directories; this document records why it is not done.

## 2. Goals

- A host-wide AmneziaWG tunnel to the intranet, up at boot, split-tunnel only, configured from a
  secret that never touches the controller's disk, a command line or a world-readable place.
- Per-developer production identities that live only in that developer's home, with the tools to
  use them installed once for everyone.
- The server visible in every developer's own tailnet, revocable per developer.
- Exactly the software listed in the inventory, at exactly the listed versions, in one shared store,
  the same for every user, updated together by one inventory change and one apply.
- One optional administrator per box with real root (sudo), everyone else without.
- A written rule in the docs: the box is multi-tenant, root belongs to the administrator, software
  requests go through the inventory.

## 3. Non-goals

- Per-user VPN tunnels or network namespaces. The tunnel is host-wide: every account on the box,
  agents included, can reach the intranet once it is up. Identity on the intranet comes from each
  developer's own credentials, not from the tunnel.
- Managing the Amnezia VPN server, Kubernetes RBAC, client-certificate issuance or SSH key
  distribution. Those stay where they are (the infra repository and the operator).
- Running several `tailscaled` instances. Node sharing covers the requirement with zero code.
- Letting developers add software themselves. A missing tool or version is a one-line inventory
  change by the administrator, applied in a minute.
- Home-directory encryption (see 6.7).
- Keeping v1's per-user asdf and the self-service `hades-ade-update`. Both go.
- Custom error text for a tool version that is not in the store; mise's own message plus a docs
  paragraph is enough *(my call)*.
- Prompting on the laptop before an agent may use production credentials (see 6.5; *(owner)*:
  accepted for now, room left for hardening later).

## 4. Anti-goals

- Any account other than root and the administrator able to read another developer's home.
- Production credentials in the inventory, the registry, `/etc/hades`, or any archive that leaves
  the box.
- The VPN becoming the default route, or carrying developer traffic to GitHub, npm or Cloudflare.
- Agent CLIs or ADEs updating themselves per user so that versions drift across the box.
- A sudo rule an agent can use silently (NOPASSWD for anything beyond v1's `hades caddy-reload`).
- A kernel module build that leaves the VPN down after a kernel upgrade without anyone noticing
  (verify must catch it).
- A secret visible in `/proc/*/cmdline` on the host, even briefly.

## 5. Constraints and facts

### What the owner's machine shows *(found)*

- The VPN client is AmneziaVPN running the AmneziaWG protocol in userspace (`amneziawg-go`), as a
  split tunnel: the VPN subnet plus one management /24 and a few single hosts are routed through
  it; the default route is not. Two of the owner's SSH hosts live in that /24. The intranet is
  reached by IP; no VPN DNS is in use.
- Production is not behind the VPN: the three production k3s API endpoints, the staging one and the
  production/staging SSH hosts all have public addresses and answer without the tunnel.
- Production identity is per person: the kubeconfig holds client certificates (one production, one
  staging identity); SSH uses the 1Password agent plus a few per-host key files.
- The owner's Tailscale is a personal tailnet with three peers.
- In the infra repository the VPNs are documented as "primary and backup AmneziaWG VPNs" used for
  management of production, Postgres and web hosts; each runs as a Docker container
  (`amnezia-awg2`) on company hosts.
- Production Kubernetes is k3s v1.33.3; staging is k3s v1.32.6.

### What the projects pin *(found)*

| Project | Pins |
|---|---|
| jarl | erlang 29.1.2, elixir 1.20.4-otp-29 (`.tool-versions`), `elixir: "~> 1.20"` |
| saga | erlang 28.3.1, elixir 1.19.5-otp-28 |
| pops | erlang 28.5, elixir 1.19.5-otp-28 |
| infra | python 3.12.10 |
| tyrr-go | go 1.26.1 (`go.mod`) |
| runa | Expo SDK 58 (Node LTS) |

### Tailscale node sharing *(found, vendor docs)*

- A machine shared into another tailnet keeps its Tailscale IP, is visible only to the user it was
  shared with, can answer connections from that tailnet but cannot start connections into it, and
  is revoked from the admin console per recipient. Sharing is initiated by an admin of the machine's
  own tailnet; accepting it tells the sharer the recipient's email.
- Availability on every plan is stated by third parties, not confirmed on the vendor's pages *(builder)*.

### mise *(found, release notes v2026.3.9, experimental)*

- `mise install --system` installs a tool into `/usr/local/share/mise/installs` (or
  `MISE_SYSTEM_DATA_DIR`), where every user's mise finds it; `MISE_SHARED_INSTALL_DIRS` adds
  read-only lookup directories; `mise ls` labels such versions `(system)`.
- The per-user data directory stays `~/.local/share/mise`; cache and state are per user.
- mise reads `.tool-versions` files walking up from the working directory, including
  `~/.tool-versions`; a user's own `~/.tool-versions` therefore overrides the box default for that
  user. v1 wrote `~/.tool-versions` into every dev home (`asdf set --home`).
- Whether a system-wide config file (`/etc/mise/config.toml`) pins versions for every user, how
  shims behave for non-interactive shells and systemd user units, how `.tool-versions` in a project
  resolves against system installs, and how system installs are pruned are *(builder)* items; see
  section 8.

### AmneziaWG on Ubuntu *(found)*

- The vendor PPA publishes for Ubuntu 26.04 (resolute) and 24.04 (noble), among others, with the
  source packages `amneziawg` (the DKMS kernel module plus `amneziawg-tools`, i.e. `awg` and
  `awg-quick`) and `amneziawg-linux-kmod`. The userspace `amneziawg-go` is not packaged there.
- The vendor's documented Linux path is the kernel module via DKMS with the running kernel's
  headers; `awg-quick@<name>.service` reads `/etc/amnezia/amneziawg/<name>.conf`.

### How secrets travel in Ansible *(found, builder check)*

- A task's `environment:` is prefixed as `VAR=value` to the remote command line, so for the length
  of the task the value sits in `/proc/<pid>/cmdline`, readable by every account on the host.
- `copy: content:`, `template` and any module argument without pipelining pass through a temporary
  file on the controller; module arguments also land in the remote temporary directory (0700).
- With `pipelining = True` the module payload, including its arguments and a `stdin:` value, travels
  over the SSH session's stdin: no controller file, no remote file, nothing on a command line.
  Pipelining needs sudoers without `requiretty`, which is the Ubuntu and Debian default.
- v1 does not fully follow its own rule: `cloudflared.yml` writes the tunnel token with
  `copy: content:`, and `toolchain.yml` passes private Hex keys as `mix` arguments visible in `ps`.

### v1 state the change has to meet *(found)*

- `roles/user/tasks/toolchain.yml` installs asdf per user (`asdf_version`, `asdf_plugins`,
  `asdf_global_versions`, kerl options), `agents.yml` installs the agent CLIs per user with npm,
  `ade_paseo.yml` installs Paseo per user with npm, `ade_orca.yml` downloads the AppImage per user,
  `hades-ade-update.sh.j2` is the per-user update script, `env.sh.j2` puts `~/.asdf/shims` on PATH.
- The CLI's `internal/execx/execx.go` adds `~/.asdf/shims` to the PATH it uses for project commands.
  `hades doctor` only checks that `mix`, `go` or `npm` are on PATH.
- The reference host has an operator account (`admin`) with sudo that the roles never touch, and
  v1 verify asserts that dev users have no sudo. The reference host's sshd is key-only (Kamal
  lockdown drop-ins) and v1 forbids touching `/etc/ssh` there.
- v1's last task offboards the reference host's throwaway dev user `hadestest` for real.

## 6. Decisions

### 6.1 Administrator

- A `dev_users` entry may carry `admin: true`. The account's groups become `[hades, sudo]`
  (v1's `append: false` already rewrites groups, so dropping the flag removes sudo on the next run,
  and offboarding needs nothing extra). On the reference host `roman` is the administrator; the
  operator account `admin` stays untouched *(owner)*.
- sudo asks for the user's password, as the distribution's default sudoers does. Nothing is added
  to sudoers beyond v1's root-only `hades caddy-reload` rule. The operator sets the password once
  after the first apply (`sudo passwd <name>` on the host), listed with the other interactive steps.
  Reason: an agent running as the administrator cannot escalate without the person typing the
  password.
- A password on an account must not open SSH password logins. When any user is `admin: true`,
  preflight (host facts stage) and verify assert that `sshd -T` reports `passwordauthentication no`
  and fail with the instruction to disable it; the role never edits `/etc/ssh` *(my call)*.
- Verify: non-admin dev users have no sudo (v1 assertion, now exempting admins); admins are in
  `sudo`. Preflight accepts any number of admins, including none; the owner's practice is one.
- The administrator can read every home. That is the accepted trade for the one most-privileged
  person on the box; the docs say so.

### 6.2 Shared toolchain (replaces v1 6.4 and the per-user installs of v1 6.3)

- **Store.** One root-owned, user-read-only store on the host, managed by `mise`
  (binary pinned by `mise_version`, installed to `/usr/local/bin`). Inventory `tools` is a map of
  tool name to list of exact versions; the first version is the box default. For the reference host
  *(my call, from the project pins and "latest stable, lag allowed")*:

  ```yaml
  tools:
    node: ["<latest Active LTS patch at build time>"]
    erlang: ["29.1.2", "28.5", "28.3.1"]        # jarl default; pops; saga
    elixir: ["1.20.4-otp-29", "1.19.5-otp-28"]  # jarl default; saga and pops
    python: ["<latest 3.14 patch>", "3.12.10"]  # latest default; infra's pin
    golang: ["<latest 1.26 patch>"]             # tyrr-go needs >= 1.26.1
    kubectl: ["<latest 1.33 patch>"]            # production is 1.33; one minor above staging, within skew
    gh: ["<latest 2.x>"]                        # replaces v1's per-user gh install (v1 final review K16)
  ```

  The builder pins the `<latest …>` entries to exact versions at plan time. Versions are exact,
  never prefixes, so two boxes or two applies cannot land on different patch releases; preflight
  rejects a bare major or minor *(my call)*. The host role renders the default versions into mise's
  system-wide config, installs every listed version with `mise install --system` as root, and
  removes system versions no longer listed, so the store is exactly the inventory *(my call)*.
  Removing a Node version removes the npm globals under it; the role installs the agent CLIs and
  Paseo under the default Node on every apply, so a default-Node bump is: change the list, apply,
  units restart. A project pinned to a removed version fails until its `.tool-versions` moves; the
  docs say to keep old versions listed until projects have moved. Upgrades are the administrator's
  routine: bump the inventory to the latest stable, apply, everyone moves *(owner)*.
- **Resolution.** Several versions of one tool may be listed so that projects' `.tool-versions`
  resolve. A version a project asks for that is not in the store fails with mise's own
  "not installed" error; users cannot write to the store and mise's auto-install is off. A user's
  own `~/.tool-versions` overrides the box default for that user, which is mise's normal behaviour
  and harmless because it can only name installed versions *(my call)*.
- **PATH.** One `/etc/profile.d` snippet activates mise for members of group `hades` (v1's
  `profile-hades.sh.j2` already scopes exports to that group); `env.sh.j2` loses the asdf lines.
  ADE user units and the CLI get the store's shim directory in their PATH instead of `~/.asdf/shims`
  (`execx.go` change, one line, a CLI PR to `main`). Builder verifies the three contexts: login
  shell, non-interactive shell, systemd user unit.
- **Agent CLIs and Paseo** are installed once as root with `npm install -g` under the store's
  default Node (v1's `agent_clis` map and `paseo_version`, now host-level), with auto-update
  disabled (`DISABLE_AUTOUPDATER=1` for Claude Code in the profile snippet; builder checks the
  Codex and Paseo CLIs for equivalents). A version bump in the inventory plus `--tags host,user`
  moves everyone; the user role restarts an ADE unit when its binary changed (v1 already restarts
  on install change).
- **Orca** is one AppImage under `/opt/orca` (root, 0755) with the `orca-ide` wrapper in
  `/usr/local/bin`; user units point there. Orca's in-place self-update cannot write a root-owned
  file; the builder checks whether Orca has a flag to disable update checks and uses it, otherwise
  the failed update is accepted as a log line. Version pinned by `orca_version`, moved by the
  inventory like everything else.
- **Per-user state stays per user and small:** `~/.mix` (hex, rebar, private hex repositories),
  `~/.cache/mise`, `~/.local/state/mise`, agent logins, GPG keys.
- **Removed:** per-user asdf install, plugins and global versions; per-user npm installs;
  `hades-ade-update`; the "never downgrade a self-installed version" rules of v1 6.3.
- **Migration on the reference host** is by hand, listed in the docs: per dev user remove
  `~/.asdf`, `~/.tool-versions` (v1 wrote it; it pins a Node that will not be in the store) and
  `~/.local/opt/orca`, then apply. The roles never delete anything in a home; verify's resolution
  check runs in each user's home and fails loudly on a leftover `~/.tool-versions` *(my call)*.
- **Tool choice** is mise because it has a system-wide install mode and needs no per-user plugin
  setup. It is experimental in mise; if the builder's spike (section 8) fails on any of the three
  PATH contexts or on `.tool-versions` resolution, the fallback is asdf in a shared `ASDF_DATA_DIR`
  with the same inventory shape. The inventory and docs do not change between the two.

### 6.3 Software requests

- `host_extra_packages: []` per host: apt packages installed by the host role and never removed by
  it. This is the path for anything system-level a developer asks for, including rootless `podman`
  for developers who need their own containers (Ubuntu's `useradd` allocates subordinate ids by
  default; the builder confirms on the reference host).
- Everything language-level goes through `tools`. The docs state the rule in one paragraph.

### 6.4 Intranet VPN (new optional role `vpn`)

- On when `vpn_enabled: true`. Installs `amneziawg` and `amneziawg-tools` from the vendor PPA with
  the running kernel's headers (the vendor's documented path; the PPA covers Ubuntu 24.04 and
  26.04). Debian: the PPA does not apply; the role refuses with a message until a Debian path is
  needed. The kernel-module path is chosen because it is what the vendor supports; the userspace
  `amneziawg-go` would have to be built and shipped by us. If DKMS fails to build on the reference
  host's kernel, the builder reports it and we switch to building `amneziawg-go` once on the
  controller *(builder)*. The reference host gets the VPN in this round; the owner needs it to
  work from the box, and accepts the kernel-module install on the live host *(owner)*. The plan
  schedules that install as its own step with a check-mode run first and a rollback note (remove
  the module and packages).
- **Config and secret path.** Each host has its own server peer. The per-host inventory key
  `vpn_conf_env` names the operator's environment variable holding that host's exported AmneziaWG
  config (for example `AMNEZIA_WG_CONF_BOX1`), the way `hex_repos` names `key_env` *(my call)*.
  The config is written on the host to `/etc/amnezia/amneziawg/intranet.conf` (root, 0600) by a
  host-side command that reads it from the task's `stdin:`, with `no_log`, and the collection turns
  on `pipelining` in `ansible.cfg`, so the secret never exists as a controller file, a remote file
  outside its destination, or a command-line argument *(my call)*. `awg-quick@intranet` is enabled
  and started; a changed config restarts it.
- v1's two deviations from the secrets rule are fixed in the same way in v2: the cloudflared token
  write moves from `copy: content:` to the stdin pattern; the private Hex keys stay as `mix`
  arguments because Hex offers no other input, and the docs name that as a known limit (the keys
  are read-only repository keys) *(my call)*.
- Split tunnel is enforced, not assumed: preflight fails if the config's `AllowedIPs` contains
  `0.0.0.0/0` or `::/0` (the Amnezia client exports a full-tunnel config by default; the operator
  edits `AllowedIPs` to the intranet subnets before exporting). No DNS changes; the intranet is
  addressed by IP today. `vpn_probe: "<ip>:<port>"` (optional, inventory) names one intranet TCP
  endpoint verify must reach.
- Interaction with v1: the isolation table matches only connections to the host's own addresses,
  so tunnel traffic is unaffected; ufw and nft are not touched; the tunnel interface is `intranet`.
- Verify: unit active; interface present; default route not via it; last handshake under three
  minutes; the probe endpoint answers when configured; the DKMS module is built for the running
  kernel (so a kernel upgrade that broke it is caught at the next verify, and `hades-status` shows
  the tunnel state).

### 6.5 Production access from the dev server

- The server provides tools, never credentials. `kubectl` (and `helm`, `k9s`, whatever a box lists)
  come from `tools`. Each developer places their own kubeconfig with their own client certificate in
  `~/.kube/config` (0600; the docs say how to get one from the operator) and reaches production SSH
  through agent forwarding from their laptop (`ForwardAgent yes` on the laptop's entry for the dev
  server; keys stay on the laptop; any SSH agent works, 1Password's prompts per use). Verify asserts
  sshd allows agent forwarding (`sshd -T` reports `allowagentforwarding yes`, the default; no
  `/etc/ssh` change).
- A kubeconfig in the home is usable by every agent that developer's ADE runs, around the clock,
  with that developer's full production RBAC and no prompt. SSH does not have this exposure because
  the laptop's agent prompts per use. Accepted for now *(owner)*: it matches how agents already work
  on laptops. The docs say it plainly, advise developers who want a prompt to keep the production
  kubeconfig on the laptop, and list the hardening options (an exec credential plugin that asks the
  laptop, short-lived certificates) as a later step; nothing in this design blocks them.
- Each developer needs their own accounts for the services behind this: GitHub (`gh auth login`,
  their own repository access) and whatever holds their SSH keys on the laptop (1Password or any
  agent). The docs list both in the onboarding checklist *(owner)*.
- Homes are 0750 (v1), no one but root and the administrator reads them. Backups and offboard
  archives are root-only and stay on the box (v1), so they expose nothing beyond the home itself;
  the offboarding checklist in the docs adds "revoke the developer's production certificate, keys,
  GitHub access and Tailscale share" because the archive keeps the credentials.

### 6.6 Tailscale: one node, shared

- Unchanged from v1 on the host: one `tailscaled`, `tailscale up` once. The tailnet admin shares
  the machine with each developer from the admin console; the developer accepts and sees the server
  in their own tailnet under its one IP; nobody else in their tailnet does; the server cannot
  initiate connections into their tailnet (not needed). Offboarding checklist: revoke the share.
  Orca's input-chain rule (`lo` and `tailscale0` only) is unaffected. Docs only; no role change.

### 6.7 Home encryption: not done

- The ADE daemons run as each user around the clock, so every home must stay unlocked all the
  time; the key is always loaded and root reads everything anyway. Per-user encryption would add an
  unlock step after every reboot before any daemon starts and would protect nothing against the
  stated concern. Protection at rest for the whole box, if wanted, is full-disk encryption at
  provisioning time (outside the playbook); the docs recommend it in one line.

### 6.8 Inventory, docs, test user

- New keys: `admin` (per user), `tools`, `host_extra_packages`, `vpn_enabled`, `vpn_conf_env`,
  `vpn_probe`, `mise_version`; `agent_clis`, `paseo_version`, `orca_version` become host-level (they
  were role defaults already). Removed: `asdf_version`, `asdf_plugins`, `asdf_global_versions`.
  `ansible.cfg` gains `pipelining = True`.
- `docs/server.md` gains: the multi-tenant rule, the administrator (password, sshd check), `tools`
  (exact versions, pruning, keep old versions until projects move) and `host_extra_packages`, the
  VPN (config export, `AllowedIPs`, the per-host env variable), production access (kubeconfig,
  agent forwarding, the agent exposure and hardening options, per-developer accounts, the
  offboarding checklist), Tailscale sharing, the encryption note, the Hex-key limit, and the
  one-time migration from v1 on the reference host.
- The reference host's throwaway dev user `hadestest` is re-created (`state: present`, same uid)
  for v2's acceptance runs and offboarded again at the end *(my call; owner indifferent)*.

## 7. Acceptance (added to v1's verify tags)

- `--tags user`: an admin user is in `sudo` and sudo needs a password; a non-admin has no sudo;
  with any admin present, `sshd -T` reports `passwordauthentication no`; `allowagentforwarding yes`.
- `--tags host` (toolchain): the store holds exactly the `tools` versions (no more, no fewer);
  every default resolves for a dev user in a login shell, a non-interactive shell and from a
  systemd user unit, run in that user's home; `claude`, `codex`, `paseo`, `orca-ide` resolve to the
  store; `~/.asdf` is not on PATH.
- `--tags vpn`: as in 6.4.
- Acceptance (v1's `acceptance` tag) still passes with the shared toolchain, since `hades up`
  runs project commands through the store.
- Preflight: rejects a non-exact `tools` version; rejects a VPN config with a full-tunnel
  `AllowedIPs`; rejects `admin: true` on a host whose sshd allows password logins.
- Idempotence: second apply of every tag group `changed=0`.

## 8. Left to the builder

- **mise spike first** (throwaway, before the plan): system installs for Node, Erlang and Elixir on
  the reference host; the three PATH contexts; `.tool-versions` resolution against system
  installs; behaviour for a missing version with auto-install off; how system installs are listed
  and removed; whether `/etc/mise/config.toml` exists as system config or the defaults live in
  `MISE_GLOBAL_CONFIG_FILE`. Report before Task 1.
- Exact versions for the `<latest …>` entries in `tools`, pinned at plan time.
- AmneziaWG DKMS build on the reference host's kernel; the exact package names from the PPA on
  26.04; whether `awg-quick` needs `resolvconf` for a config without DNS.
- Orca's update-check flag; Codex and Paseo auto-update behaviour under a read-only install.
- Rootless podman's subordinate-id state on the reference host.
- Pipelining: confirm no `requiretty` on the reference host and that every existing task still
  passes with it on.
- Role internals, handlers, tags; the exact migration list for the reference host.

## 9. Open questions

None. The owner's answers of 2026-10-24: agents may use the developer's production kubeconfig
unattended for now; tool versions are the designer's call from the project pins, latest stable
where upgradable; the throwaway test user is the designer's call; `roman` is the reference host's
administrator; the VPN is installed on the reference host in this round.

## 10. Calls made by the designer *(my call, each)*

1. Secrets travel by pipelined `stdin:` into a host-side command; `pipelining = True`; the
   cloudflared token write is converted; Hex keys stay as arguments and are documented as a limit.
2. `tools` versions are exact; the store is pruned to the list; a default-Node bump reinstalls the
   agent CLIs and Paseo under the new default; the reference host's list is in 6.2.
3. v1 leftovers in homes (`~/.asdf`, `~/.tool-versions`, `~/.local/opt/orca`) are removed by the
   operator by hand, never by the roles; verify fails loudly while they are there.
4. One VPN config per host, named by `vpn_conf_env`.
5. No custom "ask the administrator" message; mise's error plus a docs paragraph.
6. `admin: true` requires `passwordauthentication no` in sshd, asserted in preflight and verify,
   never changed by the role.
7. `hadestest` is re-created for v2 acceptance and offboarded again at the end.
8. Tool choice mise, with asdf-in-a-shared-store as the fallback if the spike fails.
