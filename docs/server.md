# Server reference

For the administrator of a hades server: the Ansible collection `quotedo.hades` in `ansible/`. The
developer's side is in the [developer guide](developer.md); the CLI in the [CLI reference](cli.md).

## Rules the server enforces

- One Linux account per developer, with its own home, ports, ADE daemon and credentials.
- Developers cannot install software: tools and packages come from the inventory.
- Agents run as the developer, with the developer's own credentials.
- Secrets reach the box over a task's stdin, never through a file on the controller or a command line, except private Hex keys (see [Known limits](#known-limits)).
- The roles never remove packages, never restart Docker and never delete anything in Cloudflare.

## Requirements

| | |
|---|---|
| Target | Ubuntu 24.04+ or Debian 13+, x86_64. The VPN role is Ubuntu only. |
| Controller | `ansible-core` 2.17+ (for example `pipx install ansible-core`), then `ansible-galaxy collection install -r requirements.yml` in `ansible/`. `sha256sum` (GNU coreutils) on `PATH`: the host role checks the release with it. |
| GitHub | `gh` logged in on the controller with read access to the hades repository: the host role downloads the private release with it. It ignores `GH_TOKEN`; without a keyring login, set `hades_local_binary_dir`. |
| Access | A sudo-capable account on the box, key-only SSH. |
| sshd | `AllowAgentForwarding yes`; with an `admin: true` user also `PasswordAuthentication no` and `KbdInteractiveAuthentication no`. Set them in your own sshd drop-in; preflight checks them. |
| Cloudflare | The domain as a zone of your account. Zero Trust on the free plan; the role sets no login method, so enable One-time PIN under Zero Trust > Settings > Authentication. An API token with Account › Cloudflare Tunnel Edit, Access: Apps and Policies Edit, Access: Organizations Edit, and Zone › DNS Edit. |
| Optional | Tailscale for Orca, with a pre-authorised auth key; an AmneziaWG config for the intranet VPN. |

## Apply and verify

Run every command from `ansible/`: its `ansible.cfg` sets the inventory to `inventory.yml`.

```sh
cd ansible
cp inventory.example.yml inventory.yml     # keep it out of git, or in a private site repository
cp .env.example .env                       # fill in, keep out of git
set -a && . ./.env && set +a               # or direnv, with `dotenv` in .envrc
ansible-playbook playbooks/site.yml
ansible-playbook playbooks/verify.yml
```

`site.yml` runs preflight first under every tag. Preflight checks the inventory, `tools` included,
on the controller, then sshd on the box, so a wrong setup fails in seconds.

| Situation | What to do or expect |
|---|---|
| First apply | Tens of minutes per Erlang version in `tools`: each is built from source. |
| Rerun | Safe; a few minutes, and `changed=0` when nothing changed. |
| No Cloudflare variables | Add `--skip-tags cloudflared`. |
| No VPN | `vpn_enabled: false` skips the `vpn` role. |
| Part of the server | `--tags <tag>,...` from the table below. Preflight and loopback isolation run under any tag; `--skip-tags isolation` leaves isolation out. |
| Check without changing anything | `ansible-playbook playbooks/preflight.yml`: the inventory, then sshd on the box. `--skip-tags sshd` checks the inventory alone. |
| From a site repository | `ansible-galaxy collection install git+ssh://git@github.com/quotedo/hades.git#/ansible/`, then `import_playbook` `quotedo.hades.site` and `quotedo.hades.verify`, with `-i` naming your inventory. |

| Tag | Installs |
|---|---|
| `host` | packages, kernel settings, hades CLI and registry, tool store, agent CLIs, Paseo, Orca, Chrome for Testing, backups, `hades-status`, weekly `hades gc`, hourly `hades-keys`, `hades-onboard`, and the `protected` tasks |
| `protected` | resource floor and caps for pre-existing workloads; part of `host` |
| `postgres` | dev cluster on loopback from the PGDG repository, `hades.slice` |
| `services` | RabbitMQ (AMQP, MQTT, management) and LocalStack (S3) containers on loopback, Redis when listed; Docker log rotation in the compose file; Docker CE from Docker's repository when `docker compose` is missing |
| `ingress` | Caddy, Cloudflare tunnel, DNS and Access (tag `cloudflared`), Tailscale and its login (tag `tailscale`) |
| `user` | accounts, SSH keys, sudo passwords, resource policy, ADE units, GPG keys, repository lists, the sshd `AllowUsers` drop-in; offboarding |
| `vpn` | AmneziaWG tunnel |
| `isolation` | loopback isolation rules (nftables) |

## Inventory

`ansible/inventory.example.yml` is the template. Host variables:

| Key | Meaning |
|---|---|
| `ansible_host`, `ansible_user` | How Ansible reaches the box: a sudo-capable account. |
| `hades_domain` | Worktree URLs are `https://<label>.<domain>`. A Cloudflare zone of the account, not a subdomain of one. |
| `hades_label_template` | `!unsafe` string, default `{{project}}-{{name}}`. Use `{{project}}-{{user}}-{{name}}` when developers share projects, or two developers on one branch collide. |
| `hades_version` | A published CLI release, default `0.1.0`, tagged `v<version>`. The controller downloads `hades_<version>_linux_amd64.tar.gz` and `checksums.txt`, checks the archive, and installs `hades` and `hades-port` when the box's version differs. |
| `hades_local_binary_dir` | Instead of the release: a controller directory holding `hades` and `hades-port` built for linux/amd64. |
| `dev_services`, `localstack_buckets` | Any of `rabbitmq`, `localstack`, `redis`; empty disables the stack. S3 buckets created at every LocalStack start, default `[]`. |
| `dev_pg_major` | Dev Postgres major, default 18. A box whose PGDG suite lacks it needs the newest major it has. |
| `dev_repos` | Clone URLs every developer gets, default `[]`; `hades-onboard` clones them after the developer's GitHub login. The apply clones nothing. |
| `tools` | Tool to a list of exact versions; the first is the default. Keys are mise names (`node`, `erlang`, `elixir`, `python`, `go`, `kubectl`, `gh`); `erlang` before `elixir`. Must include `node` (the agent CLIs) and `gh` (`hades-onboard`). Default: `node` and `gh` only. |
| `host_extra_packages` | Extra apt packages, for example `podman`. Never removed. |
| `agent_clis`, `paseo_version`, `orca_version` | Installed in the store. `agent_clis` maps a command to an npm package, for example `claude: "@anthropic-ai/claude-code@2.1.310"`; a pinned entry moves everyone, an unpinned one is installed once. |
| `hex_repos` | `{name, url, key_env, fingerprint_env}` per private Hex repository; needs `elixir` in `tools`. |
| `access_allow` | Extra emails or `@domain` entries admitted by Cloudflare Access, besides every present user's `email`. |
| `access_session` | Access session length, default `720h`, set on the application and on the account's Access organization. |
| `cloudflare_team` | Zero Trust team name, used only when the role creates the Access organization. Default `hades-<domain with dots replaced by dashes>`. |
| `tailscale_enabled` | Run `tailscaled`. Default: true exactly when a present user lists `orca`. |
| `vpn_enabled`, `vpn_conf_env`, `vpn_probe` | Intranet VPN: on or off, the environment variable holding this box's config, one `<ip>:<port>` verify must reach. |
| `capped_cpu_weight`, `capped_memory_max` | Caps for `protected.capped_users` and `capped_units`. |
| `protected` | `floor` (guarantee `system.slice` resources), `capped_users`, `capped_units`, `blocked_ports` (ports dev accounts may not reach, such as a production Postgres), `private_homes` (made 0750). Optional. |
| `dev_users` | The developers; see below. |

Resource numbers and ports can be overridden per host: `roles/host/defaults/main.yml`,
`roles/postgres/defaults/main.yml` and `roles/services/defaults/main.yml`. Resource defaults assume
a 128 GB box.

A developer entry needs four keys:

```yaml
dev_users:
  - name: alice
    uid: 2001
    github: alice
    email: alice@example.test
```

| Key | Default | Meaning |
|---|---|---|
| `name` | required | Login, `^[a-z][a-z0-9_-]{0,31}$`, unique after dropping characters outside `[a-z0-9]`. |
| `uid` | required | Fixed uid, at least 1000. The role never takes over an existing account with another uid. |
| `github` | required unless `ssh_keys` is set | `authorized_keys` follows `https://github.com/<github>.keys`. An apply fails with `GitHub served no keys for <github>` on a wrong GitHub login. |
| `email` | required with Cloudflare | Their Cloudflare Access login. |
| `ades` | `[paseo]` | `paseo`, `orca` or both. `orca` needs Tailscale. Removing an ADE stops and removes its unit at the next apply. |
| `paseo_port` | uid + 10000 | The Paseo port, on loopback. Listed ports must be unique. |
| `orca_port` | uid + 20000 | The Orca port, reachable on `lo` and `tailscale0` only. |
| `repos` | `dev_repos` | Replaces `dev_repos` for this developer. |
| `state` | `present` | `absent` offboards the developer. |
| `admin` | `false` | `true`: group `sudo`, password required. |
| `ssh_keys` | `[]` | Static public keys, written before the GitHub ones; the only keys without `github`. |
| `gpg_key` | `false` | `true`: a passphrase-less GPG key for git-secret, uid `<name>+<inventory host>@<domain>`, public key in `/etc/hades/keys/<name>.asc`. |

## Secrets

Exported in the controller's shell before any run; `.env.example` lists them.

| Variable | Used for |
|---|---|
| `CF_ACCOUNT_ID`, `CF_API_TOKEN` | Cloudflare tunnel, DNS and Access. |
| The variable `vpn_conf_env` names, such as `AMNEZIA_WG_CONF_BOX` | The box's AmneziaWG config, multi-line (see [VPN](#vpn-intranet)). |
| `key_env`, `fingerprint_env` of each `hex_repos` entry | Private Hex repository keys. |
| `TS_AUTHKEY` | Optional: a pre-authorised Tailscale auth key (see [Tailscale](#tailscale)). |

Generated on the box and never printed by an apply: GPG keys, and each Paseo password in
`~/.config/hades/paseo.env`, which `hades-onboard` shows its developer. A sudo password is printed
once (see [Sudo and sshd](#sudo-and-sshd)). The dev Postgres credentials are
`dev_pg_user`/`dev_pg_password`, default `postgres`/`postgres`.

## Cloudflare

With the two variables exported and every present user carrying an `email`, apply
(`--tags ingress` is enough). The role reads first, writes only on difference, and never deletes.

| Object | Value |
|---|---|
| Tunnel | `hades-<inventory host name>`, routing `*.<domain>` to Caddy, 404 catch-all. |
| DNS | A proxied `*.<domain>` CNAME to the tunnel. Any other record on that name stops the run. |
| Token | `/etc/cloudflared/token`, root 0600, read by the connector with `--token-file`. |
| Access application | "hades dev sites" on `*.<domain>`, hidden from the launcher, straight to login, `access_session` long. |
| Access organization | Created when the account has none, which enables Zero Trust; team domain `<cloudflare_team>.cloudflareaccess.com`. If Cloudflare refuses: `Zero Trust could not be created through the API: enable it once in the Cloudflare dashboard, then apply again`, with Cloudflare's reason. |
| Policy `team` | Allows exactly the present users' `email` plus `access_allow`. |

A wrong zone name or a token without Zone permissions stops the run with
`the API token sees no Cloudflare zone named hades_domain`. Any other API failure prints the task
and Cloudflare's errors. Behind Access, a host name no worktree claims redirects to the apex domain;
the dev-tool paths (`/dev`, `/tidewave`, `/phoenix/live_reload`) also require the Access header at
Caddy.

## Tailscale

Orca pairs over Tailscale; Tailscale runs when a present user lists `orca`, or with
`tailscale_enabled: true`.

1. Export `TS_AUTHKEY`, a pre-authorised key from the Tailscale admin console. Without it, the run prints `Tailscale is installed but not logged in: export TS_AUTHKEY and apply again, or run: sudo tailscale up`.
2. Apply. While Tailscale is not logged in, the ingress role runs `tailscale up` with the key, read from a root-only file it then removes, and `--hostname <inventory host>`.
3. The user role runs after ingress, so one apply also sets up Orca. Until Tailscale is logged in, each Orca user is skipped with `<user>: Orca skipped, Tailscale is not logged in: set TS_AUTHKEY or run tailscale up, then apply again`, and the rest of the run completes.
4. For each Orca user, share the machine with their account from the Tailscale admin console.

## VPN (intranet)

1. Export the box's AmneziaWG config from the Amnezia client. Edit `AllowedIPs` to the intranet subnets and remove any `DNS` line.
2. `export AMNEZIA_WG_CONF_BOX="$(cat box.conf)"`.
3. In the inventory set `vpn_enabled: true`, `vpn_conf_env: AMNEZIA_WG_CONF_BOX` and `vpn_probe: "<intranet ip>:<port>"`.
4. `ansible-playbook playbooks/site.yml --tags vpn`, then `ansible-playbook playbooks/verify.yml --tags vpn`.

| | |
|---|---|
| Installs | `ppa:amnezia/ppa`; `amneziawg` (DKMS kernel module), `amneziawg-tools`, the running kernel's headers. |
| Writes | `/etc/amnezia/amneziawg/intranet.conf` (root 0600) over stdin; unit `awg-quick@intranet`. A changed config restarts the tunnel. |
| Guards | Preflight rejects `0.0.0.0/0`, `::/0`, their `/1` halves and any `DNS =` line. Verify asserts no default route uses the tunnel, a handshake under 180 s, the DKMS module for the running kernel, and `vpn_probe` answering. `sudo hades-status` prints the handshake age. |
| Scope | Host-wide: every account reaches the intranet; identity there comes from each developer's own credentials. |
| Kernel upgrade | The module is built for the running kernel only. After a reboot into a new kernel, apply `--tags vpn` again; verify fails until then. A kernel the PPA's module does not build for: boot the previous kernel. |
| Rollback | `systemctl disable --now awg-quick@intranet`; `apt remove amneziawg amneziawg-dkms amneziawg-tools dkms` and the added `linux-headers-*`; delete `/etc/apt/sources.list.d/amnezia-ppa.sources` and `/etc/apt/keyrings/amnezia-ppa.asc`; set `vpn_enabled: false`. |

## Sudo and sshd

- Among dev accounts, only those with `admin: true` are in group `sudo`; the distribution's rule asks for the user's password, so an agent cannot escalate. The account Ansible connects with keeps its own sudo.
- While such a user's password is locked or unset, the apply generates one and prints it once: `<name>'s sudo password, shown only now: <password>`. Nothing stores it.
- A set password is never touched. The user changes it with `passwd`.
- A missed print: `sudo passwd -l <name>`, then apply `--tags user` again.
- An `admin: true` user can read every home.
- In `/etc/ssh` the roles write one file. Where your own sshd files restrict logins with `AllowUsers`, the user role writes `/etc/ssh/sshd_config.d/hades-allowusers.conf` with `AllowUsers <present dev users>`; sshd adds it to yours. Elsewhere it removes that file.
- A box that restricts logins with `AllowGroups` needs group `hades` added by you.

## Tools

- The store is `/usr/local/share/mise`, root-owned; every user's `PATH` has its shims. `/etc/mise/config.toml` pins the defaults and turns off auto-install.
- The store is exactly `tools`: an apply installs what is listed and removes what is not. Keep an old version listed until every project has moved.
- `MIX_HOME`/`MIX_ARCHIVES` are set to `~/.mix` in shells and ADE units; anything else running `mix` (cron, `sudo -u`) must set them too.
- A new default Node reinstalls the agent CLIs and Paseo under it and restarts every Paseo.

## Onboarding a developer

1. Ask for their GitHub login and the email they will log in with.
2. Add the four-key entry to `dev_users`, with `ades: [orca]` or `[paseo, orca]` for Orca.
3. Apply `--tags user`, plus `ingress` when the entry has an `email` or is the box's first Orca user, plus `host` for the first Orca user.
4. Verify `--tags user,ade,ingress`.
5. Give them read access to the hades GitHub repository, so they can download the CLI.
6. Send them the host name. They run `hades join <host>`; the [developer guide](developer.md) has the rest.
7. Orca users: share the Tailscale machine with their account.
8. Projects using git-secret: a key holder runs `git secret tell <name>+<inventory host>@<hades_domain>` with the key from `/etc/hades/keys/<name>.asc` (needs `gpg_key: true`).

## Offboarding a developer

1. Set `state: absent` on the entry.
2. Apply `--tags user,ingress`. The role:
   1. stops the user manager, disables lingering and kills the user's processes;
   2. runs `hades gc --owner <name>`: worktrees, ports, sites and databases released;
   3. archives the home to `<backup_dir>/<name>-offboard-<timestamp>.tar.gz`;
   4. removes the per-uid drop-ins, static keys and GitHub login line, then the account and home;
   5. drops the email from the Access policy and the name from the `AllowUsers` drop-in.
3. Verify `--tags offboard`.
4. Revoke by hand: production certificate and keys, GitHub access, the Tailscale share.
5. The archives hold the user's credentials (kubeconfig, GitHub token, agent logins) and are never pruned. Verify expects one per absent entry: delete the entry, then the archives, when you no longer need them.

`state: absent` acts only on accounts the role created (group `hades`, matching uid). A run
interrupted before the account is deleted archives again under a new time stamp.

## Verify

`verify.yml` with no tags runs every set below; `--tags <tag>` runs one. Every set is read-only
except its own probes.

| Tag | Proves |
|---|---|
| `host` | group, registry and Caddy live directories, hades version and `hades-port`, kernel settings, `hades.slice` memory, agent CLIs and Paseo in the store, Orca, the keys timer, `hades-keys` keeping a file on a failed fetch, `hades-onboard` refusing to run without a terminal |
| `isolation` | the nftables table and its boot unit; a non-dev account is refused on the dev ports |
| `ops` | gc and backup timers, a backup run, the backup directory, `hades-status`, Chrome headless with its sandbox |
| `postgres` | dev cluster online on loopback, owner, unit in `hades.slice`, credentials, a wrong password refused |
| `services` | containers on loopback in `hades.slice`, RabbitMQ and LocalStack answer, buckets exist |
| `ingress` | Caddy on loopback, its API socket, apex redirect, gated paths, cloudflared, the edge redirects to the login, the Access policy equals the inventory, Tailscale running with `TS_AUTHKEY` |
| `user` | accounts, lingering, resource policy, tools in a login shell, sudo only for `admin: true` users and each of them has a password, blocked ports, `hades doctor`, every GitHub and static key in `authorized_keys`, the login list, the onboarding reminder in interactive shells only, sshd admits every present user |
| `ade` | each listed ADE unit and listener, the Paseo password, a unit exactly for each listed ADE |
| `offboard` | absent users gone, nothing left behind, archived; GPG keys exported |
| `protected` | floor, caps, private homes |
| `vpn` | tunnel unit, no default route through it, handshake, DKMS module, probe |
| `acceptance` | only with `-e acceptance_user=<throwaway dev user>`: a sample project end to end (`hades up`, routing, database, isolation, `hades down`, `gc`, a missing tool version failing loudly), cleaned up after |

After a reboot, run `verify.yml`.

## Operations

| Task | Do |
|---|---|
| Add or remove a developer | [Onboarding](#onboarding-a-developer), [Offboarding](#offboarding-a-developer). |
| Add or remove an ADE for a developer | Edit `ades`, apply with the tags of onboarding step 3. A removed ADE's unit is stopped and deleted. |
| Add a tool version | Append to `tools`, apply `--tags host`; `--tags host,user` when the default Node changes. |
| Move everyone to a new ADE or agent CLI version | Change `paseo_version`, `orca_version` or a pinned `agent_clis` entry; apply `--tags host,user`. |
| Upgrade the hades CLI | Change `hades_version` to a published release, or rebuild into `hades_local_binary_dir`; apply `--tags host`. Developers reinstall the laptop CLI themselves. |
| Refresh SSH keys now | `sudo hades-keys <name>` for one user, `sudo systemctl start hades-keys.service` for all. The timer runs hourly; `sudo hades-status` shows the last run. |
| Add a system package | `host_extra_packages`, apply `--tags host`. |
| Vanished worktrees | `hades-gc.timer` runs `hades gc` as root weekly; `sudo hades gc` runs it now. |
| Restore a user's ADE state | Stop the user's ADE unit, `sudo tar -xzf <backup_dir>/<name>-<date>.tar.gz -C /home/<name>`, start it. |
| Roll back an apply | Revert the inventory and apply again. Removed tool versions are reinstalled; packages the role installed stay. |
| Protected workloads | `protected.floor`; caps in drop-ins named `20-hades-cap.conf`, so your `systemctl set-property` (`50-*`) still wins; `blocked_ports` enforced in `table inet hades`. |
| Backups | Nightly between 03:30 and 04:00 per user: ADE state and agent logins in `backup_dir`, newest `host_backup_keep` (7) kept, root-only, on the box. |
| Reboot | By hand; then run `verify.yml`. |
| What is wrong | `sudo hades-status` (slices, pressure, OOM kills, disk, VPN handshake), `hades doctor` as a user, `verify.yml --tags <tag>`. |

## Known limits

- A changed open-files limit reaches a running user manager only after it restarts.
- A partly unpacked Chrome for Testing sticks behind its `creates` guard: remove `<chrome_root>/<version>/` and apply again.
- Private Hex keys are passed to `mix` as arguments and show in the process list while it runs.
- Homes are not encrypted; use full-disk encryption at provisioning.
- Isolation hooks the output path only: traffic from a container is not matched.
- `hades-status` embeds the dev Postgres password, which every local account can read anyway.
