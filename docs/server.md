# Server reference

For the operator of a hades server: the Ansible collection `quotedo.hades` in `ansible/`. What developers see is in the [developer guide](developer.md); the CLI in the [CLI reference](cli.md).

## Rules the server enforces

- Multi-tenant: one Linux account per developer, own home, ports, ADE daemon and credentials.
- Among dev accounts, only those with `admin: true` are in group `sudo`, password required. The account Ansible connects with keeps its own sudo.
- Developers cannot install software: tools and packages come from the inventory.
- Agents run as the developer with the developer's own credentials.
- Secrets reach the host over a task's stdin, never through a file on the controller or a command line, except private Hex keys (see Known limits).
- The roles never remove packages, never edit `/etc/ssh`, never restart Docker, never touch workloads listed as protected, never delete inside a home, never delete anything in Cloudflare.

## Requirements

| | |
|---|---|
| Target | Ubuntu 24.04+ or Debian 13+, x86_64; tested on Ubuntu 26.04. VPN role: Ubuntu only. |
| Controller | `ansible-core` 2.17+, `ansible-galaxy collection install -r ansible/requirements.yml`; `gh` logged in with `gh auth login` and read access to `quotedo/hades`: the host role downloads the release with it and ignores `GH_TOKEN`. |
| Access | A sudo-capable account on the host, key-only SSH. |
| Cloudflare | The domain as a zone of your account. Zero Trust (free plan, One-time PIN login): the ingress role enables it through the API when the account allows, else enable it once in the dashboard. An API token with: Account › Cloudflare Tunnel Edit, Access: Apps and Policies Edit, Access: Organizations Edit; Zone › DNS Edit. |
| Optional | Tailscale (Orca pairing and node sharing), with a pre-authorised auth key; an AmneziaWG config for the intranet VPN. |

## Workflow

```mermaid
flowchart LR
  inv["inventory.yml<br/>(private)"] --> pre["preflight.yml<br/>validates, changes nothing"]
  env[".env<br/>(private secrets)"] --> site
  pre --> site["site.yml<br/>host → postgres → services → ingress → user → protected → vpn"]
  site --> ver["verify.yml<br/>read-only checks"]
  ver --> ok{"failed=0?"}
  ok -- no --> fix["hades-status, journalctl,<br/>fix inventory or host"] --> site
  ok -- yes --> tell["tell each new developer<br/>the host name"]
```

```sh
cd ansible
cp inventory.example.yml inventory.yml      # keep it outside git, or in a private site repository
cp .env.example .env && set -a && . ./.env && set +a
ansible-playbook -i inventory.yml playbooks/preflight.yml   # dry check; site.yml runs it again
ansible-playbook -i inventory.yml playbooks/site.yml
ansible-playbook -i inventory.yml playbooks/verify.yml
```

`site.yml` is safe to rerun, and a second run reports `changed=0`. A first apply takes longer: every Erlang listed in `tools` is built from source. Without the Cloudflare variables add `--skip-tags cloudflared`; without `vpn_enabled` the `vpn` role is skipped. `verify.yml` runs every check except the opt-in `acceptance` and `reboot` tags (see [Verify](#verify)). `--tags` limits either playbook to the groups below, for example `--tags host,user` after a tool change; loopback isolation is recomputed after any run (`--skip-tags isolation` leaves it out). From a site repository, install the collection with `ansible-galaxy collection install git+ssh://git@github.com/quotedo/hades.git#/ansible/` and `import_playbook` `quotedo.hades.site` and `quotedo.hades.verify`.

| Tag group | Installs |
|---|---|
| `host` | packages, kernel settings, hades CLI (release) and registry, tool store, agent CLIs, Paseo, Orca, Chrome for Testing, backups, `hades-status`, weekly `hades gc`, hourly `hades-keys`, `hades-onboard` |
| `postgres` | dev cluster on loopback, `hades.slice` |
| `services` | RabbitMQ (AMQP, MQTT, management) and LocalStack (S3) containers on loopback; Redis when listed |
| `ingress` | Caddy, Cloudflare tunnel, DNS, Access organization, application and policy, cloudflared, Tailscale and its login |
| `user` | accounts, SSH keys, the welcome, the administrator's password, resource policy, ADE units, GPG keys, repositories; offboarding |
| `protected` | resource floor and caps for pre-existing workloads |
| `vpn` | AmneziaWG tunnel |

## Inventory

`ansible/inventory.example.yml` is the template. Host variables:

| Key | Type | Meaning |
|---|---|---|
| `ansible_host`, `ansible_user` | | How Ansible reaches the box: a sudo-capable account. |
| `hades_domain` | string | Worktree URLs are `https://<label>.<domain>`; a Cloudflare zone of the account, not a subdomain of one. |
| `hades_label_template` | `!unsafe` string | Label pattern, default `{{project}}-{{name}}`; use `{{project}}-{{user}}-{{name}}` when developers share projects, or two developers on one branch collide. |
| `hades_version` | string | A published release of the CLI, default `0.1.0`. The controller downloads `hades_<version>_linux_amd64.tar.gz` and `checksums.txt` with the operator's `gh`, checks the archive, and installs `hades` and `hades-port` when the host's version differs. |
| `hades_local_binary_dir` | path | Instead of the release (development): a controller directory holding `hades` and `hades-port` built for linux/amd64. |
| `dev_services` | list | Any of `rabbitmq`, `localstack`, `redis`; empty disables the stack. |
| `tools` | map | Tool → list of exact versions in the shared store; the first is the default. Keys are mise names (`node`, `erlang`, `elixir`, `python`, `go`, `kubectl`, `gh`). `erlang` before `elixir`. Keep `gh`, which the default lists: `hades-onboard` needs it. |
| `host_extra_packages` | list | Extra apt packages (for example `podman` for rootless containers). Never removed. |
| `agent_clis`, `paseo_version`, `orca_version` | | Installed in the store. `agent_clis` maps a command to an npm package, for example `claude: "@anthropic-ai/claude-code@2.1.310"`; a pinned entry moves everyone, an unpinned one is installed once. |
| `hex_repos` | list | `{name, url, key_env, fingerprint_env}` per private Hex repository; needs `elixir` in `tools`. |
| `access_allow` | list | Extra emails or `@domain` entries admitted by Cloudflare Access, besides every present user's `email`. |
| `access_session` | string | Access session length, default `720h`; set on the application and on the account's Access organization. |
| `cloudflare_team` | string | Zero Trust team name, used only when the role creates the Access organization: `<cloudflare_team>.cloudflareaccess.com`. Default `hades-<domain with dots replaced by dashes>`. |
| `tailscale_enabled` | bool | Run `tailscaled`. Default: true when a present user lists `orca`; the example sets `false`, so set it or delete the line. |
| `vpn_enabled`, `vpn_conf_env`, `vpn_probe` | | Intranet VPN: on/off, the environment variable holding this host's config, one `<ip>:<port>` verify must reach. |
| `capped_cpu_weight`, `capped_memory_max` | | Caps for `protected.capped_users` and `capped_units`. |
| `protected` | map | `floor` (guarantee `system.slice` resources), `capped_users`, `capped_units`, `blocked_ports` (ports dev accounts may not reach, such as a production Postgres), `private_homes` (made 0750). |
| `dev_users[]` | list | See below. |

Role defaults can be overridden per host: resource numbers, ports and paths in `roles/host/defaults/main.yml`; Postgres settings in `roles/postgres/defaults/main.yml`; container images and LocalStack buckets in `roles/services/defaults/main.yml`. Resource defaults assume a 128 GB host.

Per developer (`dev_users` entry):

| Key | Meaning |
|---|---|
| `name`, `uid` | Login (`^[a-z][a-z0-9_-]{0,31}$`, unique after dropping characters outside `[a-z0-9]`) and fixed uid ≥ 1000. The role never takes over an existing account with another uid. |
| `state` | `present` (default) or `absent` (offboard). |
| `admin` | `true` on the administrator: group `sudo`, password required. |
| `email` | The address they log in to Cloudflare Access with; required while the Cloudflare variables are set. |
| `github` | GitHub login. `~/.ssh/authorized_keys` is `ssh_keys` followed by `https://github.com/<github>.keys`, rewritten at every apply and hourly by `hades-keys`. Every present user needs `github` or a non-empty `ssh_keys`. |
| `ssh_keys` | Static public keys, written before the GitHub ones; the only keys when `github` is not set. |
| `ades`, `paseo_port`, `orca_port` | `paseo` and/or `orca`, each with a distinct loopback port. `orca` needs `tailscale_enabled`. |
| `gpg_key` | Generate a passphrase-less GPG key for git-secret; uid `<name>+<inventory host>@<domain>`, public key in `/etc/hades/keys/<name>.asc`. |
| `repos` | Git URLs cloned into the home: public ones at apply, private ones by `hades-onboard` after the user's GitHub login. Listed in `~/.config/hades/repos`. |

## Secrets

Exported in the controller's shell before any run (`.env.example` lists them):

| Variable | Used for |
|---|---|
| `CF_ACCOUNT_ID`, `CF_API_TOKEN` | Cloudflare tunnel, DNS, Access (ingress group). Without them: `--skip-tags cloudflared`. |
| `<vpn_conf_env>` (e.g. `AMNEZIA_WG_CONF_BOX`) | The host's AmneziaWG config, multi-line: `export AMNEZIA_WG_CONF_BOX="$(cat box.conf)"`. |
| `<key_env>`, `<fingerprint_env>` per `hex_repos` entry | Private Hex repository keys. |
| `TS_AUTHKEY` | Optional: a pre-authorised auth key from the Tailscale admin console. The ingress role logs the host in with it while Tailscale is not logged in (see [Tailscale](#tailscale)). |

Generated on the host: GPG keys; Paseo passwords in `~/.config/hades/paseo.env`, shown to their user by the welcome and never printed by an apply; the administrator's sudo password (see [Administrator](#administrator)). The dev Postgres credentials are `dev_pg_user`/`dev_pg_password`, default `postgres`/`postgres`.

## Cloudflare

With the two variables exported and every present user carrying an `email`, apply (`--tags ingress` is enough). The role reads first and writes only on difference; it never deletes:

| Object | Value |
|---|---|
| Tunnel | `hades-<inventory host name>`, routing `*.<domain>` to Caddy, 404 catch-all. |
| DNS | A proxied `*.<domain>` CNAME to the tunnel. Any other record on that name stops the run. |
| Token | `/etc/cloudflared/token`, root 0600, read by the connector with `--token-file`. |
| Access application | "hades dev sites" on `*.<domain>`, hidden from the launcher, straight to login, `access_session` long. |
| Access organization | Created when the account has none, which enables Zero Trust: name `hades`, team domain `<cloudflare_team>.cloudflareaccess.com`. If Cloudflare refuses, the run stops with `Zero Trust could not be created through the API: enable it once in the Cloudflare dashboard, then apply again`, followed by Cloudflare's reason. Session length set to `access_session` (account-wide). |
| Policy `team` | Allow exactly: present users' `email` + `access_allow`. Offboarding removes an email on the next apply. |

A developer opening a worktree URL enters their email and the PIN Cloudflare mails them. Behind Access, a host name no worktree claims redirects to the apex domain; the dev-tool paths (`/dev`, `/tidewave`, `/phoenix/live_reload`) also require the Access header at Caddy.

## Tailscale

Set `tailscale_enabled: true` (or list `orca` for a user and remove the example's `false`) and apply. While Tailscale is not logged in, the ingress role:

| `TS_AUTHKEY` | Result |
|---|---|
| exported | Writes the key over stdin to a root-only file, runs `tailscale up --auth-key=file:<file> --hostname <inventory host>`, removes the file. |
| not set | Prints `Tailscale is installed but not logged in: export TS_AUTHKEY and apply again, or run: sudo tailscale up`. |

The ingress role runs before the user role, so with the key one apply also sets up Orca. Without it, each Orca user is skipped with `<user>: Orca skipped, Tailscale is not logged in: set TS_AUTHKEY or run tailscale up, then apply again`, and the rest of the run completes. For each developer, share the machine from the Tailscale admin console with their account; they see the box in their own tailnet. Revoke the share at offboarding. Orca pairs over the Tailscale address; its ports are reachable only on `lo` and `tailscale0`.

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

- `admin: true` → group `sudo`; the distribution's rule asks for the user's password, so an agent cannot escalate.
- While the administrator's password is locked or unset, the apply generates one, stores it in `/etc/hades/admin-password.<name>` (root 0600) and prints it once: `<name>'s sudo password, stored only in /etc/hades/admin-password.<name>: <password>`. A set password is never touched, so later applies print nothing. The account Ansible connects with can read the file again.
- Change it with `passwd`, then delete `/etc/hades/admin-password.<name>`.
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
  a["Add the dev_users entry<br/>name, uid, github, email, ades, ports"] --> b["site.yml<br/>account, keys from GitHub, welcome, ADE, Access policy"]
  b --> c["Tell the developer the host name<br/>Orca users: share the Tailscale node"]
  c --> d["Developer, laptop:<br/>hades join HOST --as NAME"]
  d --> e["Developer, box:<br/>read the welcome, run hades-onboard"]
  e --> f["Projects with git-secret: a key holder runs git secret tell NAME+HOST@DOMAIN"]
```

No password changes hands: the welcome shows the developer their Paseo password. When `hades join` reports `not authorised yet`, the message names the GitHub login it found: compare it with the entry, apply, and the developer reruns the same command. A developer without GitHub sends the key `hades join` printed, which goes into `ssh_keys`. By hand per developer: the Tailscale share for Orca users; for projects using git-secret, a key holder runs `git secret tell <name>+<inventory host>@<hades_domain>` with the key from `/etc/hades/keys/<name>.asc`.

## Offboarding

```mermaid
flowchart TD
  a["state: absent"] --> b["site.yml"]
  b --> g["ingress: email leaves the Access policy"]
  b --> c["user: stop user manager, disable linger, kill processes"]
  c --> d["hades gc --owner: worktrees, ports, sites, databases released"]
  d --> e["home archived to backup_dir/NAME-offboard-TIMESTAMP.tar.gz"]
  e --> f["per-uid drop-ins, static keys, admin password file and GitHub login line removed;<br/>account and home deleted"]
  f --> h["By hand: revoke prod certificate and keys, GitHub, Tailscale share; delete the archives when no longer needed"]
```

Rules: `state: absent` acts only on accounts the role created (group `hades`, matching uid); the entry stays in the inventory (verify expects the archive); a run interrupted before the account is deleted archives again under a new time stamp; offboard archives and the user's nightly archives are never pruned. The archives hold the user's credentials (kubeconfig, GitHub token, agent logins): revoke those, then delete the archives when no longer needed.

## Verify

`verify.yml --tags <tag>`; every tag is read-only except its own probes.

| Tag | Proves |
|---|---|
| `preflight` | the inventory rules on the fixtures in `tests/inventories/` |
| `host` | registry, host config, sudoers, kernel settings, slices, the store holds exactly `tools`, CLIs at their versions, `hades-keys` and `hades-onboard` installed, the keys timer active, a failed GitHub fetch reported as `fetch failed, kept` |
| `isolation` | the nftables table at boot; a non-dev account is refused on the dev ports |
| `ops` | timers, a backup run, `hades-status`, log caps, Chrome headless |
| `postgres` | dev cluster on loopback, `pg_hba.conf`, credentials |
| `services` | containers on loopback in `hades.slice`, RabbitMQ and LocalStack answer |
| `ingress` | Caddy on loopback, apex redirect, gated paths, cloudflared, the edge redirects to the login, the Access policy equals the inventory |
| `user` | accounts, lingering, resource policy, tools in a login shell, sudo only for administrators, sshd settings, blocked ports, `hades doctor`, every GitHub and static key in `authorized_keys`, the login list, the welcome in interactive shells only, the administrator's stored password working with sudo |
| `ade` | each ADE unit and listener, Paseo's password, no stale units |
| `offboard` | absent users gone and archived, GPG keys exported |
| `protected` | floor, caps, private homes |
| `vpn` | tunnel unit, interface, no default route through it, handshake, DKMS module, probe |
| `acceptance` | with `-e acceptance_user=<throwaway dev user>`: a sample project end to end (`hades up`, URL routing, database, isolation, `hades down`, `gc`, a missing tool version fails loudly). Cleans up after itself. |

`verify.yml --tags reboot -e reboot_test=true` reboots the host and repeats the boot-sensitive tags.

## Operations

| Task | Do |
|---|---|
| Add a tool version | Append to `tools`, apply `--tags host`. |
| Move everyone to a new ADE or CLI version | Change `paseo_version`, `orca_version` or a pinned `agent_clis` entry; apply `--tags host,user`. |
| Upgrade the hades CLI | Change `hades_version` to a published release, or rebuild into `hades_local_binary_dir`; apply `--tags host`. |
| Refresh SSH keys now | `hades-keys.timer` runs `hades-keys` hourly. `sudo systemctl start hades-keys.service` runs it now; `sudo hades-keys <login>` for one user. Each line ends `updated`, `unchanged`, `fetch failed, kept`, `no home, skipped` or `write failed, kept`; `sudo hades-status` shows the last run. |
| Rotate the admin password | The administrator runs `passwd`, then deletes `/etc/hades/admin-password.<name>` (see [Administrator](#administrator)). |
| Add a system package | `host_extra_packages`, apply `--tags host`. |
| Vanished worktrees | `hades-gc.timer` runs `hades gc` as root weekly; `sudo hades gc` runs it now. |
| Restore a user's ADE state | Stop the user's ADE unit, `sudo tar -xzf <backup_dir>/<user>-<date>.tar.gz -C /home/<user>`, start it. |
| Remove an ADE from a user | Not automated: the user runs `systemctl --user disable --now <ade>.service` and removes `~/.config/systemd/user/<ade>.service`. |
| Roll back an apply | No automatic rollback: revert the inventory and apply again. Removed tool versions are reinstalled; packages the role installed stay. |
| Protected workloads | `protected.floor`; caps named `20-hades-cap.conf` so an operator's `systemctl set-property` (`50-*`) still wins; Docker never restarted; `blocked_ports` enforced in `table inet hades`. |
| Backups | Nightly at 03:30 per user: ADE state and agent logins in `backup_dir`, newest `host_backup_keep` (7) kept, root-only, on the box. |
| What is wrong | `sudo hades-status` (slices, pressure, OOM kills, disk, VPN handshake), `hades doctor` as a user, `verify.yml --tags <tag>`. |

## Known limits

- `AllowUsers` in sshd is not managed; add dev logins by hand.
- A changed open-files limit reaches a running user manager only after it restarts.
- A partly unpacked Chrome for Testing sticks behind its `creates` guard: remove `<chrome_root>/<version>/` and apply again.
- Private Hex keys are passed to `mix` as arguments and show in the process list while it runs.
- Homes are not encrypted; use full-disk encryption at provisioning.
- Isolation hooks the output path only: traffic from a container is not matched.
- `hades-status` embeds the dev Postgres password, which every local account can read anyway.
- The Orca unit has been verified with a headless smoke test, not in daily use.
- Orca is skipped, with a message, until Tailscale is logged in.
- A key removed from GitHub keeps working until the next hourly `hades-keys` run.
