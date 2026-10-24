# Server

The Ansible collection in `ansible/` turns one host into a multi-user dev server: the hades CLI and its registry, a shared tool store, a dev Postgres, shared services, Caddy behind a Cloudflare tunnel, Paseo or Orca per user, and an optional intranet VPN. The CLI side is in [install](install.md), [configuration](configuration.md) and [ADE integration](ades.md).

## The rules

- The box is multi-tenant. Every developer has their own account, home, ports, ADE units and credentials. No credential is shared.
- Root belongs to one administrator (see [Administrator](#administrator)). Nobody else has sudo.
- Developers do not install software. A missing tool or version is a request to the administrator, who adds it to `tools` or `host_extra_packages` and applies.
- Agents run as the developer, with the developer's own credentials: GitHub, agent CLI logins, kubeconfig, forwarded SSH agent.

## Targets

- Ubuntu 24.04+ and Debian 13+, x86_64. The reference host runs Ubuntu 26.04.
- Debian 12 is untested; some package names differ there.
- The VPN role runs on Ubuntu only.

## Inventory and secrets

Start from `ansible/inventory.example.yml`. Per host:

- `hades_domain`: worktree sites are `https://<label>.<domain>`. `hades_label_template` is a YAML `!unsafe` string so Ansible never expands the CLI's placeholders.
- `dev_services`: any of `rabbitmq`, `localstack`, `redis`; empty means no shared-services stack.
- `tools` and `host_extra_packages` ([Tools](#tools), [Software requests](#software-requests)); `access_allow` ([Cloudflare](#cloudflare)); `tailscale_enabled` ([Tailscale](#tailscale)); `vpn_enabled`, `vpn_conf_env` and `vpn_probe` ([VPN](#vpn)).
- `dev_users`: `name`, `uid`, `state` (`present` or `absent`), `admin`, `email`, `ssh_keys`, `ades` (`paseo`, `orca`), `paseo_port`, `orca_port`, `gpg_key`, `repos`. Logins must stay unique after dropping every character outside `[a-z0-9]`.
- `protected`, for workloads that were on the host before hades:
  - `floor`: guarantees `system.slice` memory and CPU weight, and gives `user.slice` a CPU weight.
  - `capped_users` and `capped_units`: CPU weight and memory caps, set by the `capped_cpu_weight` and `capped_memory_max` host vars.
  - `blocked_ports`: ports dev users may not connect to on this host, such as a production Postgres.
  - `private_homes`: these homes become mode 0750.
- Any role default can be overridden per host, for example the resource numbers in `roles/host/defaults/main.yml`.

Secrets never go into `ansible/`. Copy `.env.example` to `.env`, fill it in and export it with `set -a; . ./.env; set +a`.

- `CF_ACCOUNT_ID` and `CF_API_TOKEN`: see [Cloudflare](#cloudflare).
- The VPN config: in the variable that `vpn_conf_env` names; see [VPN](#vpn).
- Private Hex repository keys are read from the environment variables that `hex_repos` names.
- Paseo passwords are generated on the host and never leave it.
- Keep `pipelining = True` in `ansible.cfg`: secrets reach the host over a task's stdin, never as a file on the controller or a command-line argument.

## Running

From `ansible/`, with the collection's requirements installed (`ansible-galaxy collection install -r requirements.yml`):

```sh
ansible-playbook -i inventory.yml playbooks/preflight.yml
ansible-playbook -i inventory.yml playbooks/site.yml --tags host --skip-tags cloudflared
ansible-playbook -i inventory.yml playbooks/verify.yml --skip-tags cloudflared
```

- Run `preflight.yml` first on a new inventory; it changes nothing.
- On the reference host a full `site.yml` exceeds a 10-minute command timeout, so it is applied one tag group at a time, in order: `host`, `postgres`, `services`, `ingress`, `user`, `protected`, `vpn`. The loopback isolation rules are recomputed after every tagged run.
- From a site repository, install the collection with `ansible-galaxy collection install git+https://github.com/quotedo/hades.git#/ansible/` and wrap `quotedo.hades.site` and `quotedo.hades.verify` with `import_playbook`.

Apply times on the reference host with nothing to change:

| Tag group | Seconds |
|---|---|
| `host` | 81 |
| `postgres` | 24 |
| `services` | 22 |
| `ingress` | 41 |
| `user` | 71 (two dev users) |
| `protected` | 42 |
| `vpn` | 15 |

## Cloudflare

Once per Cloudflare account, enable Zero Trust (the free plan works) with One-time PIN as its login method. `hades_domain` must be a zone of that account, not a subdomain of one.

1. Export `CF_ACCOUNT_ID` and `CF_API_TOKEN`. The token needs the permissions listed in `.env.example`.
2. Give every present dev user an `email`: the address they log in with. Preflight requires it while `CF_API_TOKEN` is set.
3. Optionally list extra emails or `@domain` entries in `access_allow`. An `@domain` entry admits a whole mail domain.
4. Apply `--tags ingress`.

The role reads each object first and writes only what is missing or different. It never deletes anything in Cloudflare. It ensures:

- a tunnel `hades-<inventory host name>` routing `*.<domain>` to Caddy, with a 404 catch-all. An existing tunnel of that name is reused;
- a proxied `*.<domain>` CNAME to the tunnel. Any other record on that name stops the run; fix it in the dashboard;
- the tunnel token in `/etc/cloudflared/token` (root, 0600);
- a self-hosted Access application `hades dev sites` on `*.<domain>`, hidden from the app launcher, going straight to the login, with 30-day sessions (`access_session: 720h`, also set on the organisation);
- one allow policy, `team`, admitting exactly the present dev users' emails plus `access_allow`.

A developer opens a worktree URL, enters their email and types the PIN Cloudflare mails them. Behind Access, a host name no worktree claims redirects to the apex domain. An offboarded user's email leaves the policy on the next `--tags ingress` apply. Without the two variables, run every playbook with `--skip-tags cloudflared`.

## Administrator

- `admin: true` on a dev user puts the account in group `sudo`. Dropping the flag removes sudo on the next `--tags user` apply.
- sudo asks for the user's password, so an agent running as the administrator cannot escalate. After the first apply the operator sets it once: `sudo passwd <name>` on the host.
- sshd must allow agent forwarding (`AllowAgentForwarding yes`, the default) and, while any user is an administrator, refuse password logins (`PasswordAuthentication no` and `KbdInteractiveAuthentication no`). Otherwise the `user` tag stops. Set them in an sshd drop-in yourself; the role never edits `/etc/ssh`.
- The administrator can read every home.

## Tools

The host role installs every version listed in `tools` with mise into one root-owned store, `/usr/local/share/mise`, that every user reads.

- Keys are mise registry names: `node`, `erlang`, `elixir`, `python`, `go` (never `golang`), `kubectl`, `gh`.
- Versions are exact and quoted, with at least two numeric parts: `"28.5"`, `"1.20.4-otp-29"`. Preflight rejects a bare major.
- The first version of each tool is the box default. List more versions of a tool for projects whose `.tool-versions` pin them. List `erlang` before `elixir`.
- The store is exactly the list: an apply removes versions no longer listed. Keep an old version listed until every project has moved off it.
- A project that asks for a version the store lacks fails with mise's `Tool not installed for shim: <tool>`.
- `MIX_HOME` is per user (`~/.mix`), set in the login environment and the ADE units, because mise's Elixir would otherwise point it into the read-only store. Anything that runs `mix` with an environment of its own, such as a cron job or `sudo -u`, must set `MIX_HOME` and `MIX_ARCHIVES` too.
- To upgrade, change the version and apply `--tags host,user`. A new default Node reinstalls the agent CLIs and Paseo under it and restarts every `paseo.service`.

## Software requests

System packages go in `host_extra_packages`; the host role installs them and never removes them. Example: `host_extra_packages: [podman]` gives developers rootless containers, since dev users stay out of group `docker`. Ubuntu's `useradd` already gives each account the subordinate ids rootless podman needs. Language toolchains go in `tools`.

## VPN

A host that must reach the intranet runs one AmneziaWG tunnel, `intranet`.

1. Export the host's config from the Amnezia client in AmneziaWG format.
2. Edit `AllowedIPs` to the intranet subnets, because the export routes everything, and delete any `DNS` line. Preflight rejects `0.0.0.0/0`, `::/0` and a `DNS =` line.
3. Put the config in the environment variable that `vpn_conf_env` names, for example `export AMNEZIA_WG_CONF_BOX="$(cat intranet.conf)"`.
4. Set `vpn_enabled: true`, `vpn_conf_env: AMNEZIA_WG_CONF_BOX` and optionally `vpn_probe: "<ip>:<port>"`, one intranet TCP endpoint that verify must reach.
5. Apply `--tags vpn`.

The role adds the vendor PPA `ppa:amnezia/ppa` and installs the running kernel's headers, `amneziawg` (a DKMS kernel module) and `amneziawg-tools`. It writes the config to `/etc/amnezia/amneziawg/intranet.conf` (root, 0600) and enables `awg-quick@intranet`. A changed config restarts the tunnel.

- The tunnel is host-wide: every account on the box, agents included, reaches the intranet. Identity on the intranet comes from each developer's own credentials.
- `verify.yml --tags vpn` checks that the unit is active, the `intranet` interface exists, the default route does not use it, the last handshake is under 180 seconds old, DKMS lists `amneziawg` as installed for the running kernel, and `vpn_probe` answers. `sudo hades-status` prints the handshake age.
- DKMS builds the module only for kernels whose headers are installed. After rebooting into a new kernel, apply `--tags vpn` and run `verify.yml --tags vpn`; verify fails while the module is missing.
- Rollback: `sudo systemctl disable --now awg-quick@intranet`, `sudo apt remove amneziawg amneziawg-dkms amneziawg-tools dkms` plus the `linux-headers-*` packages the role added, delete `/etc/apt/sources.list.d/amnezia-ppa.sources` and `/etc/apt/keyrings/amnezia-ppa.asc`, and set `vpn_enabled: false`. `hades-status` keeps its `vpn:` section until `--tags host` runs again. The role never removes packages.

## Production access

The box provides `kubectl` through `tools`, never credentials.

- **Kubernetes.** Ask the operator for a kubeconfig with your own client certificate. Put it in `~/.kube/config` on the box and run `chmod 600 ~/.kube/config`.
- **SSH.** Your keys stay on your laptop, in any SSH agent; 1Password asks before each use. Forward the agent from your laptop's `~/.ssh/config`:

  ```
  Host <box>
    HostName <box address>
    User <name>
    ForwardAgent yes
  ```

- **Exposure.** Every agent your ADE runs can use your kubeconfig at any hour, with your full production rights and no prompt. The forwarded SSH agent works only while your session is open, and an agent that prompts shows every use. To get a prompt for Kubernetes too, keep the production kubeconfig on your laptop.
- **Hardening**, not in place yet: an exec credential plugin that asks the laptop, or short-lived certificates.
- Nightly backups and offboard archives contain the kubeconfig. They are root-only and stay on the box.

## Accounts each developer needs

- GitHub, with access to the team's repositories.
- An SSH agent on the laptop holding their keys, such as 1Password.
- Tailscale, to accept the node share.
- An email address that receives Cloudflare's PIN: their `email` in the inventory.
- An account for each agent CLI they use.

## Tailscale

The box runs one `tailscaled` in the administrator's tailnet. Set `tailscale_enabled: true` (implied when a present user lists `orca`), apply `--tags ingress`, and run `sudo tailscale up` on the host once.

For each developer, a tailnet admin shares the machine from the Tailscale admin console with the developer's Tailscale account. The developer then sees the box in their own tailnet at its one Tailscale address. At offboarding, revoke the share in the admin console.

## Interactive steps, in order

1. Cloudflare: enable Zero Trust once, export the two variables, list the emails and apply `--tags ingress` (see [Cloudflare](#cloudflare)).
2. After the first `--tags user` apply, set the administrator's password: `sudo passwd <name>`.
3. Run `sudo tailscale up` on the host and share the node with each developer (see [Tailscale](#tailscale)). Do this before any user lists `orca`: preflight refuses `orca` when `tailscale_enabled` is false, and the Orca tasks fail on a host that is not logged in to Tailscale.
4. Each user runs `gh auth login`, `gh auth setup-git` and each agent CLI's own login. Private `repos` clone only after that; until then a run prints "could not clone" and goes on.
5. Each user who needs production puts their kubeconfig in place (see [Production access](#production-access)).
6. Paseo Desktop connects with "Remote SSH". The first apply prints each user's password once. It stays in `~/.config/hades/paseo.env` (0600; root can read it). For a phone, the user runs `paseo daemon pair --relay`.
7. Orca: the user reads the pairing URL from the unit's journal with `journalctl --user -u orca.service`.
8. For a project that uses git-secret, a current key holder runs `git secret tell` for each new user.

## ADEs

Each listed ADE runs as a user unit, `~/.config/systemd/user/<ade>.service`, under the user's lingering manager. The role starts `user@<uid>.service` right after enabling lingering, so the first user unit never races the user bus.

- Paseo, Orca and the agent CLIs are installed once in the store, at `paseo_version` and `orca_version`. Pin `@<version>` in `agent_clis` to move everyone; an unpinned entry is installed once at the latest and left alone. A changed version plus `--tags host,user` moves every user and restarts their units. Users cannot update them.
- **Paseo** listens on loopback only. The listen address, relay setting and `PASEO_PASSWORD` come from the unit and its 0600 environment file. Hades writes no Paseo config file.
- **Orca** binds every address and has no option to narrow it. The `input` chain of `table inet hades` rejects its ports on every interface except `lo` and `tailscale0`.
- Orca serves headless on orcad, so there is no Xvfb.
- On first start, Orca writes hooks into `~/.claude/settings.json`, `~/.codex/hooks.json` and `~/.codex/config.toml`.
- Orca terminals do not survive a unit restart, because the unit stops its whole control group.
- The Orca path was verified by a headless smoke test only, never as a unit on the reference host.

Removing an ADE from a user's `ades` is not automated. Verify flags the leftover unit. The user removes it:

```sh
systemctl --user disable --now <ade>.service
rm ~/.config/systemd/user/<ade>.service
```

## Isolation and resources

- `table inet hades` is loaded by its own `hades-nftables.service`. The role never enables `nftables.service` and never edits `/etc/nftables.conf`, which may flush ufw, Docker and other rules at boot.
- Its `output` chain rejects new TCP connections to the dev ports from any uid outside root, the present dev users and the `caddy`, `cloudflared` and `hades-postgres` accounts. The dev ports are the worktree range, the dev Postgres, the shared services, Caddy and the ADE ports. A second rule keeps dev users off `protected.blocked_ports`. Both rules match only connections to this host's own addresses.
- Isolation hooks `output` only, so traffic from a container is not matched: root and members of group `docker` can reach host-bound dev ports through one.
- Per dev user, the role writes `user-<uid>.slice.d/20-hades.conf` (memory, CPU, tasks) and `user@<uid>.service.d/50-hades.conf` (open files). Caps on protected units are `20-hades-cap.conf`. The `20-` names sort before the `50-*.conf` files of `systemctl set-property`, so an operator's own values still win.
- Docker is never restarted by the role (`docker_restart_on_change: false`). New log options reach containers created after the operator's next Docker restart. A log driver other than `json-file` or `local` is left alone, with a notice in the run output.
- Backups run nightly. Each user's ADE state and agent logins go to `backup_dir`, and the newest `host_backup_keep` archives per user are kept.

## Offboarding checklist

Set the user's `state: absent` and apply `--tags user`, then `--tags ingress`. The user role:

1. Stops `user@<uid>.service`, disables lingering and kills the user's remaining processes.
2. Releases the user's worktrees with `hades gc --owner <name>`.
3. Archives the home to `backup_dir/<name>-offboard-<YYYYMMDDTHHMMSS>.tar.gz`. Every run archives again under its own time stamp. Offboard archives are never pruned. The user's nightly archives, `<name>-YYYY-MM-DD.tar.gz`, are not pruned either: delete them by hand.
4. Removes the per-uid drop-in directories and reloads systemd.
5. Deletes the account and its home last, so the next run finishes an interrupted one.

The `--tags ingress` apply removes the user's email from the Access allow-list. By hand, because the archives keep the user's credentials:

- Revoke the user's production client certificate and the SSH keys production trusts for them.
- Remove their GitHub access.
- Revoke their Tailscale share.

Keep the `state: absent` entry, because `verify --tags offboard` expects an archive for every absent entry; remove entries for logins that never existed. `state: absent` applies only to accounts hades created: the run fails for an existing account that is not in group `hades` or whose uid differs from the inventory.

## Encryption

Homes are not encrypted. The ADE daemons run around the clock, so every home would stay unlocked anyway, and root reads everything. For protection at rest, use full-disk encryption when provisioning the box; the playbooks do not set it up.

## Verify

`verify.yml` selects its checks by tag:

| Tag | Proves | Time on the reference host |
|---|---|---|
| `preflight` | preflight rejects the bad fixtures in `tests/inventories/` and accepts the valid one; no inventory literal appears in the roles or playbooks | 6 s |
| `host` | registry, host config, sudoers rule, kernel settings, `hades.slice`, set-property caps still effective, CPU governor, the store holds exactly `tools`, mise's settings, agent CLIs, Paseo and Orca at their versions | 33 s |
| `isolation` | the rule table is loaded at boot; a non-dev account (`nobody`) is refused on the dev ports while root and ssh still connect | 10 s |
| `ops` | gc and backup timers, a backup run, `hades-status`, journald and Docker log caps, Chrome headless with its sandbox | 78 s |
| `postgres` | dev cluster on loopback in `hades.slice`, `pg_hba.conf`, credentials, wrong password refused | 6 s |
| `services` | containers running in `hades.slice` on loopback, no dev user in group docker, RabbitMQ and LocalStack answer | 9 s |
| `ingress` | Caddy on loopback only, API on its unix socket, unknown host names redirect to the apex domain, gated paths 403 without the Access header; with the Cloudflare variables: cloudflared active, the edge redirects to the team domain, the Access policy equals the inventory's list | 21 s |
| `user` | accounts, lingering, resource policy, the toolchain from the store in a login shell, `MIX_HOME`, sudo only for administrators, sshd settings, blocked ports refused, `hades doctor` | 15 s |
| `ade` | each ADE's unit and listeners, Paseo's password, Orca's port in the input chain, no stale units | 9 s |
| `offboard` | absent users gone and archived, GPG keys exported | 5 s |
| `protected` | floor, caps and private homes | 13 s |
| `vpn` | the tunnel unit and interface, no default route through it, a recent handshake, the DKMS module for the running kernel, `vpn_probe` | 5 s |
| `acceptance` | the sample project end to end, described below | 47 s |

The `acceptance` check runs only with `-e acceptance_user=<a throwaway dev user>` and is skipped otherwise. It works in that user's home with the sample project in `tests/sample-project/`:

1. It commits the project and adds a worktree.
2. It runs `hades up`.
3. It checks the `hades ls` entry, both site files, Caddy routing to the worktree port, the dev database and loopback isolation, and that `hades doctor` passes.
4. It runs `hades down` and checks that the host name redirects to the apex again and the sites, the port claim and the database are gone.
5. It runs `hades gc` as root.
6. The same worktree then pins a version the store lacks; `hades up` must fail with mise's not-installed error and install nothing.

It cleans up after itself even when a check fails.

`-e reboot_test=true` reboots the host after the other checks and repeats the `host`, `isolation`, `postgres`, `services`, `ingress` and `ade` checks. It has not been exercised on the reference host.

## Known limits

- Hosts whose sshd uses `AllowUsers` need a drop-in that lists the dev users. This is not automated.
- A changed open-files limit reaches an already running user manager only after it restarts.
- A partly unpacked Chrome for Testing sticks behind its `creates:` guard. Remove `<chrome_root>/<version>/` and run again.
- Private Hex repository keys are passed to `mix` as arguments, because Hex takes them no other way, so they show in the process list while it runs. They are read-only repository keys.
- On a host that had no Postgres, the role sets `create_main_cluster = false` in `/etc/postgresql-common/createcluster.d/hades.conf`, so no package creates a `<major>/main` cluster there.
- The design's load test (acceptance 5) is not implemented. The resource numbers are the design's starting values.
- `hades-status` embeds the dev Postgres password. That password is readable by every local user anyway, by design of the dev cluster.

## Migrating from v1

1. In each dev home, remove `~/.asdf`, `~/.tool-versions`, `~/.local/opt/orca`, `~/.local/bin/hades-ade-update` and `~/.local/bin/orca-ide`. The roles never delete inside a home. A leftover `~/.tool-versions` pins a Node the store lacks, and verify's toolchain check fails on it.
2. Apply `--tags host,user`.
3. Replace the v1 tunnel token in the environment with `CF_ACCOUNT_ID` and `CF_API_TOKEN`, then apply `--tags ingress`. Rename the v1 tunnel to `hades-<inventory host name>` first; otherwise the role creates a new tunnel and stops on the wildcard record that points to the old one.
4. The roles never remove these host leftovers; delete them by hand if wanted: `/etc/apt/sources.list.d/nodesource.sources`, `/etc/apt/keyrings/nodesource.asc`, `/etc/apt/preferences.d/nodesource`, `/var/cache/hades/npm` and `/etc/hades/ingress.md`.
5. Remove NodeSource's `nodejs` package too (`sudo apt remove nodejs`): a shell without the login profile finds its `/usr/bin/node` before the store. Verify covers the login shell and the ADE units; ssh commands get the store through `~/.bashrc`.
