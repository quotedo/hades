# Server reference

For the operator of a hades server: the Ansible collection `quotedo.hades` in `ansible/`. What developers see is in the [developer guide](developer.md); the CLI in the [CLI reference](cli.md).

## Rules the server enforces

- Multi-tenant: one Linux account per developer, own home, ports, ADE daemon and credentials.
- One administrator has sudo (password required); nobody else. Developers cannot install software: tools and packages come from the inventory.
- Agents run as the developer with the developer's own credentials.
- Secrets reach the host over a task's stdin, never through a file on the controller or a command line. Nothing in the repository names a host, an address or a secret; the inventory and `.env` are private.
- The roles never remove packages, never edit `/etc/ssh`, never restart Docker, never touch workloads listed as protected, never delete inside a home.

## Requirements

| | |
|---|---|
| Target | Ubuntu 24.04+ or Debian 13+, x86_64 (reference: Ubuntu 26.04). VPN role: Ubuntu only. |
| Controller | `ansible-core` 2.16+, `ansible-galaxy collection install -r ansible/requirements.yml`, `ansible-lint` optional. |
| Access | A sudo-capable account on the host, key-only SSH. |
| Cloudflare | The domain as a zone of your account; Zero Trust enabled once (free plan, One-time PIN login). An API token with: Account › Cloudflare Tunnel Edit, Access: Apps and Policies Edit, Access: Organizations Edit; Zone › DNS Edit. |
| Optional | Tailscale (Orca pairing and node sharing); an AmneziaWG config for the intranet VPN. |

## Workflow

```mermaid
flowchart LR
  inv["inventory.yml<br/>(private)"] --> pre["preflight.yml<br/>validates, changes nothing"]
  env[".env<br/>(private secrets)"] --> site
  pre --> site["site.yml --tags GROUP<br/>host → postgres → services → ingress → user → protected → vpn"]
  site --> ver["verify.yml --tags TAG<br/>read-only checks"]
  ver --> ok{"failed=0?"}
  ok -- no --> fix["hades-status, journalctl,<br/>fix inventory or host"] --> site
  ok -- yes --> steps["interactive steps<br/>passwd, tailscale up, Paseo passwords"]
```

```sh
cd ansible
cp inventory.example.yml inventory.yml    # keep it outside git, or in a private site repo
cp .env.example .env && set -a && . ./.env && set +a
ansible-playbook -i inventory.yml playbooks/preflight.yml
for g in host postgres services ingress user protected vpn; do
  ansible-playbook -i inventory.yml playbooks/site.yml --tags $g
done
ansible-playbook -i inventory.yml playbooks/verify.yml --tags host   # and so on per tag
```

Apply one tag group at a time: a full `site.yml` exceeds a 10-minute command timeout. Every group is idempotent; a second run reports `changed=0`. Without Cloudflare variables add `--skip-tags cloudflared`; without `vpn_enabled` the `vpn` group is skipped. From a site repository, install the collection with `ansible-galaxy collection install git+https://github.com/quotedo/hades.git#/ansible/` and `import_playbook` `quotedo.hades.site` and `quotedo.hades.verify`.

| Tag group | Installs | Seconds (reference host, no change) |
|---|---|---|
| `host` | packages, kernel settings, hades CLI and registry, tool store, agent CLIs, Paseo, Orca, Chrome for Testing, loopback isolation, backups, `hades-status` | 81 |
| `postgres` | dev cluster on loopback, `hades.slice` | 24 |
| `services` | RabbitMQ and LocalStack containers on loopback | 22 |
| `ingress` | Caddy, Cloudflare tunnel, DNS, Access, cloudflared, Tailscale | 41 |
| `user` | accounts, resource policy, ADE units, GPG keys, repositories; offboarding | 71 |
| `protected` | resource floor and caps for pre-existing workloads | 42 |
| `vpn` | AmneziaWG tunnel | 15 |

## Inventory

`ansible/inventory.example.yml` is the template. Host variables:

| Key | Type | Meaning |
|---|---|---|
| `ansible_host`, `ansible_user` | | How Ansible reaches the box: a sudo-capable account. |
| `hades_domain` | string | Worktree URLs are `https://<label>.<domain>`; must be a Cloudflare zone of the account. |
| `hades_label_template` | `!unsafe` string | Label pattern, default `{{project}}-{{name}}`; add `{{user}}` when several users share projects. |
| `hades_version` | string | Release to install (`hades_local_binary_dir` installs a local build instead). |
| `dev_services` | list | Any of `rabbitmq`, `localstack`, `redis`; empty disables the stack. |
| `tools` | map | Tool → list of exact versions in the shared store; first is the default. Keys are mise names (`node`, `erlang`, `elixir`, `python`, `go`, `kubectl`, `gh`). `erlang` before `elixir`. |
| `host_extra_packages` | list | Extra apt packages (for example `podman` for rootless containers). Never removed. |
| `agent_clis`, `paseo_version`, `orca_version` | | Versions installed in the store; pin `@<version>` in `agent_clis` to move everyone, an unpinned entry is installed once. |
| `access_allow` | list | Extra emails or `@domain` entries admitted by Cloudflare Access, besides every present user's `email`. |
| `access_session` | string | Access session length, default `720h`. |
| `tailscale_enabled` | bool | Run `tailscaled`; implied when a user lists `orca`. |
| `vpn_enabled`, `vpn_conf_env`, `vpn_probe` | | Intranet VPN: on/off, the environment variable holding this host's config, one `<ip>:<port>` verify must reach. |
| `capped_cpu_weight`, `capped_memory_max` | | Caps for `protected.capped_users` and `capped_units`. |
| `protected` | map | `floor` (guarantee `system.slice` resources), `capped_users`, `capped_units`, `blocked_ports` (ports dev accounts may not reach, such as a production Postgres), `private_homes` (made 0750). |
| `dev_users[]` | list | See below. |

Any role default (resource numbers, ports, paths) can be overridden per host; they live in `roles/host/defaults/main.yml`.

Per developer (`dev_users` entry):

| Key | Meaning |
|---|---|
| `name`, `uid` | Login and fixed uid (≥ 1000; the role never takes over an existing account with another uid). Logins must stay unique after dropping characters outside `[a-z0-9]`. |
| `state` | `present` (default) or `absent` (offboard). |
| `admin` | `true` on the one administrator: group `sudo`, password required. |
| `email` | The address they log in to Cloudflare Access with; required while the Cloudflare variables are set. |
| `ssh_keys` | Public keys for `~/.ssh/authorized_keys`. |
| `ades`, `paseo_port`, `orca_port` | `paseo` and/or `orca`, each with a distinct loopback port. `orca` needs `tailscale_enabled`. |
| `gpg_key` | Generate a passphrase-less GPG key (for git-secret); the public key lands in `/etc/hades/keys/<name>.asc`. |
| `repos` | Git URLs cloned into the home; private ones after the user's `gh auth setup-git`. |

## Secrets

Exported in the controller's shell before any run (`.env.example` lists them):

| Variable | Used for |
|---|---|
| `CF_ACCOUNT_ID`, `CF_API_TOKEN` | Cloudflare tunnel, DNS, Access (ingress group). Without them: `--skip-tags cloudflared`. |
| `<vpn_conf_env>` (e.g. `AMNEZIA_WG_CONF_BOX`) | The host's AmneziaWG config, multi-line: `export AMNEZIA_WG_CONF_BOX="$(cat box.conf)"`. |
| `<key_env>`, `<fingerprint_env>` per `hex_repos` entry | Private Hex repository keys. |

Generated on the host and never leaving it: Paseo passwords (`~/.config/hades/paseo.env`, printed once at the first apply), GPG keys, the dev Postgres password (a shared dev default).

## Cloudflare

Once: enable Zero Trust on the account. Then, with the two variables exported, every present user carrying an `email`, apply `--tags ingress`. The role ensures, reading first and writing only on difference, never deleting:

| Object | Value |
|---|---|
| Tunnel | `hades-<inventory host name>`, routing `*.<domain>` to Caddy, 404 catch-all. |
| DNS | A proxied `*.<domain>` CNAME to the tunnel. Any other record on that name stops the run. |
| Token | `/etc/cloudflared/token`, root 0600, read by the connector with `--token-file`. |
| Access application | "hades dev sites" on `*.<domain>`, hidden from the launcher, straight to login, 30-day sessions. |
| Policy `team` | Allow exactly: present users' `email` + `access_allow`. Offboarding removes an email on the next apply. |

A developer opening a worktree URL enters their email and the PIN Cloudflare mails them. Behind Access, a host name no worktree claims redirects to the apex domain; the dev-tool paths (`/dev`, `/tidewave`, `/phoenix/live_reload`) also require the Access header at Caddy.

## Tailscale

Set `tailscale_enabled: true` (or list `orca` for a user), apply `--tags ingress`, run `sudo tailscale up` on the host once. For each developer, share the machine from the Tailscale admin console with their account; they see the box in their own tailnet. Revoke the share at offboarding. Orca pairs over the Tailscale address; its ports are reachable only on `lo` and `tailscale0`.

## VPN (intranet)

```sh
# 1. export the box's AmneziaWG config from the Amnezia client, edit AllowedIPs to the intranet subnets, remove any DNS line
export AMNEZIA_WG_CONF_BOX="$(cat box.conf)"
# 2. inventory: vpn_enabled: true, vpn_conf_env: AMNEZIA_WG_CONF_BOX, vpn_probe: "<intranet ip>:<port>"
ansible-playbook -i inventory.yml playbooks/site.yml --tags vpn
ansible-playbook -i inventory.yml playbooks/verify.yml --tags vpn
```

| | |
|---|---|
| Installs | `ppa:amnezia/ppa`; `amneziawg` (DKMS kernel module), `amneziawg-tools`, headers for the running kernel. AmneziaWG 2.0 configs are accepted. |
| Writes | `/etc/amnezia/amneziawg/intranet.conf` (root 0600) over stdin; unit `awg-quick@intranet`. A changed config restarts the tunnel. |
| Guards | Preflight and the role reject `0.0.0.0/0`, `::/0`, the `/1` halves and any `DNS =` line. Verify asserts no default route uses the tunnel, a handshake under 180 s, the DKMS module for the running kernel, and `vpn_probe` answering. `sudo hades-status` prints the handshake age. |
| Scope | Host-wide: every account reaches the intranet; identity there comes from each developer's own credentials. |
| Kernel upgrade | The module is built for the running kernel only: after a reboot into a new kernel, apply `--tags vpn` again (verify fails until then). |
| Rollback | `systemctl disable --now awg-quick@intranet`; `apt remove amneziawg amneziawg-dkms amneziawg-tools dkms` and the added `linux-headers-*`; delete `/etc/apt/sources.list.d/amnezia-ppa.sources` and `/etc/apt/keyrings/amnezia-ppa.asc`; `vpn_enabled: false`. |

## Administrator

- `admin: true` → group `sudo`; the distribution's rule asks for the user's password, so an agent cannot escalate. Set it once after the first `--tags user` apply: `sudo passwd <name>` on the host.
- sshd must have `AllowAgentForwarding yes` and, while any administrator exists, `PasswordAuthentication no` and `KbdInteractiveAuthentication no`; otherwise the `user` group stops. Set them in your own sshd drop-in. Hosts using `AllowUsers` need the dev logins added by hand.
- The administrator can read every home.

## Tools

- The store is `/usr/local/share/mise`, root-owned; every user's PATH has its shims. `/etc/mise/config.toml` pins the defaults and turns off auto-install and the fall-through to other binaries.
- The store is exactly `tools`: an apply installs what is listed and removes what is not. Keep an old version listed until every project has moved.
- A project pin the store lacks fails with `Tool not installed for shim: <tool>`.
- `MIX_HOME`/`MIX_ARCHIVES` are set to `~/.mix` in login shells and ADE units; anything else running `mix` (cron, `sudo -u`) must set them too.
- Upgrade: change the version, apply `--tags host,user`. A new default Node reinstalls the agent CLIs and Paseo under it and restarts every Paseo.

## Onboarding

```mermaid
flowchart TD
  a["Add dev_users entry<br/>name, uid, email, ssh_keys, ades, ports"] --> b["site.yml --tags user<br/>account, units, Paseo password printed once"]
  b --> c["site.yml --tags ingress<br/>email joins the Access policy"]
  c --> d["Hand over: Paseo password, SSH host, Tailscale share"]
  d --> e["Developer: gh auth login, agent logins, kubeconfig"]
```

Interactive steps, once per box: `sudo passwd <admin>`; `sudo tailscale up` (before any user lists `orca`); per developer: Paseo Desktop "Remote SSH" with the printed password (phone: `paseo daemon pair --relay`), Orca pairing URL from `journalctl --user -u orca.service`; `git secret tell` by a key holder for projects using git-secret.

## Offboarding

```mermaid
flowchart TD
  a["state: absent"] --> b["site.yml --tags user"]
  b --> c["stop user manager, disable linger, kill processes"]
  c --> d["hades gc --owner: worktrees, ports, sites, databases released"]
  d --> e["home archived to backup_dir/NAME-offboard-TIMESTAMP.tar.gz"]
  e --> f["per-uid drop-ins removed, account and home deleted"]
  f --> g["site.yml --tags ingress<br/>email leaves the Access policy"]
  g --> h["By hand: revoke prod certificate and keys, GitHub, Tailscale share; delete nightly archives"]
```

Rules: `state: absent` acts only on accounts the role created (group `hades`, matching uid); the entry stays in the inventory (verify expects the archive); every run archives again under a new time stamp; offboard archives are never pruned.

## Verify

`verify.yml --tags <tag>`; every tag is read-only except its own probes.

| Tag | Proves |
|---|---|
| `preflight` | the inventory rules on the fixtures in `tests/inventories/`; no inventory literal in the roles |
| `host` | registry, host config, sudoers, kernel settings, slices, the store holds exactly `tools`, CLIs at their versions |
| `isolation` | the nftables table at boot; a non-dev account is refused on the dev ports |
| `ops` | timers, a backup run, `hades-status`, log caps, Chrome headless |
| `postgres` | dev cluster on loopback, `pg_hba.conf`, credentials |
| `services` | containers on loopback in `hades.slice`, RabbitMQ and LocalStack answer |
| `ingress` | Caddy on loopback, apex redirect, gated paths, cloudflared, the edge redirects to the login, the Access policy equals the inventory |
| `user` | accounts, lingering, resource policy, tools in a login shell, sudo only for the administrator, sshd settings, blocked ports, `hades doctor` |
| `ade` | each ADE unit and listener, Paseo's password, no stale units |
| `offboard` | absent users gone and archived, GPG keys exported |
| `protected` | floor, caps, private homes |
| `vpn` | tunnel unit, interface, no default route through it, handshake, DKMS module, probe |
| `acceptance` | with `-e acceptance_user=<throwaway dev user>`: a sample project end to end (`hades up`, URL routing, database, isolation, `hades down`, `gc`, a missing tool version fails loudly). Cleans up after itself. |

`-e reboot_test=true` reboots and repeats the boot-sensitive tags (not exercised on the reference host).

## Operations

| Task | Do |
|---|---|
| Add a tool version | Append to `tools`, apply `--tags host`. |
| Move everyone to a new ADE or CLI version | Change `paseo_version`, `orca_version` or a pinned `agent_clis` entry; apply `--tags host,user`. |
| Add a system package | `host_extra_packages`, apply `--tags host`. |
| Remove an ADE from a user | Not automated: the user runs `systemctl --user disable --now <ade>.service` and removes `~/.config/systemd/user/<ade>.service`. |
| Protected workloads | `protected.floor`, caps named `20-hades-cap.conf` so an operator's `systemctl set-property` (50-*) still wins; Docker never restarted; `blocked_ports` enforced in `table inet hades`. |
| Backups | Nightly per-user archives of ADE state and agent logins in `backup_dir`, newest `host_backup_keep` kept, root-only, on the box. |
| What is wrong | `sudo hades-status` (slices, pressure, OOM kills, disk, VPN handshake), `hades doctor` as a user, `verify.yml --tags <tag>`. |

## Known limits

- `AllowUsers` in sshd is not managed; add dev logins by hand.
- A changed open-files limit reaches a running user manager only after it restarts.
- A partly unpacked Chrome for Testing sticks behind its `creates` guard: remove `<chrome_root>/<version>/` and apply again.
- Private Hex keys are passed to `mix` as arguments and show in the process list while it runs.
- Homes are not encrypted (daemons keep them unlocked anyway); use full-disk encryption at provisioning.
- Isolation hooks the output path only: traffic from a container is not matched.
- `hades-status` embeds the dev Postgres password, which every local account can read anyway.
- No load test backs the resource numbers; they are the design's starting values.

## Migrating from v1

1. In each dev home remove `~/.asdf`, `~/.tool-versions`, `~/.local/opt/orca`, `~/.local/bin/hades-ade-update`, `~/.local/bin/orca-ide`.
2. Apply `--tags host,user`.
3. Replace `CLOUDFLARE_TUNNEL_TOKEN` with `CF_ACCOUNT_ID` and `CF_API_TOKEN`; rename the v1 tunnel to `hades-<host name>`; apply `--tags ingress`.
4. Optional cleanup the roles never do: `apt remove nodejs`, `/etc/apt/sources.list.d/nodesource.sources`, `/etc/apt/keyrings/nodesource.asc`, `/etc/apt/preferences.d/nodesource`, `/var/cache/hades/npm`, `/etc/hades/ingress.md`.
