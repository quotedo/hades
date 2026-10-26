# Server reference

For the administrator of a hades box: the Ansible collection `quotedo.hades` in `ansible/`. The
developer's side is in the [developer guide](developer.md); the CLI in the [CLI reference](cli.md).

## What the box enforces

- One Incus VM per developer, who is root in it; the administrator is root in every VM.
- The shared software (`/opt/hades`, `/usr/local/share/mise`, `/etc/mise`) is mounted read-only in every VM, also for root.
- A VM reaches the internet, and the intranet when the VPN is on; never another VM; the box only for DHCP and DNS.
- An apply never restarts, rebuilds, recreates or resizes a running VM.
- Secrets reach the box over a task's stdin, never through a file on the controller or a command line, except private Hex keys (see [Known limits](#known-limits)).
- The roles never remove packages and never delete anything in Cloudflare.

## Requirements

| | |
|---|---|
| Box | Ubuntu, tested on 26.04; x86_64 with KVM (`/dev/kvm`). Every VM runs the box's release (`images:ubuntu/<release>/cloud`): the shared tools link the box's libraries. |
| Controller | `ansible-core` 2.17+ (for example `pipx install ansible-core`), then `ansible-galaxy collection install -r requirements.yml` in `ansible/`. |
| Access | A sudo-capable account on the box (`ansible_user`), key-only SSH. Its `authorized_keys` become root's in every VM. |
| Cloudflare | The domain as a zone of your account, one zone per box. Zero Trust on the free plan. An API token with Account › Cloudflare Tunnel Edit, Access: Apps and Policies Edit, Access: Organizations, Identity Providers, and Groups Edit, and Zone › DNS Edit. |
| Optional | An AmneziaWG config, for a team that needs intranet hosts ([VPN](#vpn-intranet)). |

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

`site.yml` runs preflight first under every tag: it checks the inventory and the VMs' memory against
the box's, so a wrong setup fails in seconds. Then it configures the box, then each VM as root through the box.

| Situation | What to do or expect |
|---|---|
| First apply | Tens of minutes per Erlang version in `tools`: each is built from source. |
| Rerun | Safe; `changed=0` when nothing changed. |
| No Cloudflare variables | Add `--skip-tags cloudflared`. |
| Part of the setup | `--tags <tag>,...` from the table below. |
| Check without changing anything | `ansible-playbook playbooks/preflight.yml`. |
| From a site repository | `ansible-galaxy collection install git+ssh://git@github.com/quotedo/hades.git#/ansible/`, then `import_playbook` `quotedo.hades.site` and `quotedo.hades.verify`, with `-i` naming your inventory. |

| Tag | Configures |
|---|---|
| `host` | packages, the shared store (tools, agent CLIs, Paseo, Orca, `hades`, `hades-port`, `hades-onboard`), Incus with its pool and bridge, the bridge firewall, the jump account and `hades-keys`, the VMs and their sizes, offboarding |
| `ingress` | Caddy; Cloudflare tunnel, DNS and Access (tag `cloudflared`) |
| `vpn` | AmneziaWG tunnel, when `vpn_enabled` |
| `vm` | inside each VM: packages, Docker, Tailscale, Postgres, `dev_services`, Chrome for Testing, the account and its keys, shell environment, GPG key, Hex, Caddy, `/etc/hades/host.toml`, Paseo and Orca, the daily cleanup |

## Inventory

`ansible/inventory.example.yml` is the template. Host variables:

| Key | Default | Meaning |
|---|---|---|
| `ansible_host`, `ansible_user` | required | How Ansible reaches the box. |
| `hades_domain` | required | Worktree URLs are `https://<name>-<project>-<worktree>.<domain>`. A Cloudflare zone of the account, not a subdomain of one. |
| `hades_version` | `0.2.0` | A published CLI release, tagged `v<version>`. The box downloads `hades_<version>_linux_amd64.tar.gz` from the public release, checks it against the release's `checksums.txt`, and installs `hades` and `hades-port` into `/opt/hades/bin` when the version differs. |
| `hades_local_binary_dir` | none | Instead of the release: a controller directory holding `hades` and `hades-port` built for linux/amd64. |
| `vm_memory` | `28GiB` | Each VM's memory: a size or a share of the box's memory (`50%`). Preflight keeps 8 GiB for the box. |
| `vm_cpus` | all box threads | Each VM's CPUs. |
| `vm_disk` | `200GiB` | Each VM's root disk, a sparse file on the box's root filesystem. Preflight warns when the present VMs' disks add up to more than that filesystem. |
| `vm_subnet` | `10.88.0.0/16` | `<a>.<b>.0.0/16` for the bridge `hadesbr0`. A VM's address is `<a>.<b>.<uid // 256>.<uid % 256>`. Preflight refuses an overlap with the VPN's `AllowedIPs`. Fixed once the bridge exists. |
| `tools` | `node`, `gh` | Tool to a list of exact versions; the first is the default. Keys are mise names (`node`, `erlang`, `elixir`, `python`, `go`, `kubectl`, `gh`); `erlang` before `elixir`. Must include `node` (the agent CLIs) and `gh` (`hades-onboard`). |
| `agent_clis`, `paseo_version`, `orca_version` | `claude`, `codex`; set versions | Installed in the store. `agent_clis` maps a command to an npm package, for example `claude: "@anthropic-ai/claude-code@2.1.310"`; a pinned entry moves everyone, an unpinned one is installed once. |
| `extra_packages` | `[]` | Apt packages every VM gets. Never removed. |
| `dev_services`, `localstack_buckets` | `[]` | Any of `rabbitmq`, `localstack`, `redis`, run by Docker in every VM; S3 buckets created at every LocalStack start. |
| `dev_repos` | `[]` | Clone URLs every developer gets; `hades-onboard` clones them after the developer's GitHub login. |
| `hex_repos` | `[]` | `{name, url, key_env, fingerprint_env}` per private Hex repository; needs `elixir` in `tools`. |
| `access_allow` | `[]` | Emails or `@domain` entries Cloudflare Access admits besides every present developer's `email`. |
| `access_session` | `720h` | Access session length, on the application and on the account's Access organization. |
| `cloudflare_team` | `hades-<domain, dots as dashes>` | Zero Trust team name, used only when the role creates the Access organization. |
| `vpn_enabled`, `vpn_conf_env`, `vpn_probe` | off | Intranet VPN: on or off, the environment variable holding this box's config, one `<ip>:<port>` verify must reach. |
| `dev_users` | required | The developers, one entry each; see below. |

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
| `name` | required | `^[a-z][a-z0-9]*$`, unique: the VM's name, the login in it, and the start of the developer's web labels. |
| `uid` | required | 1001 to 65534, unique: the uid in the VM, and its address. |
| `github` | required unless `ssh_keys` is set | Keys from `https://github.com/<github>.keys`. A failed fetch keeps their keys (`<name>: fetch failed, kept`); an empty answer removes them. |
| `email` | required with Cloudflare | Their Cloudflare Access login. |
| `ades` | `[paseo]` | `paseo`, `orca` or both. |
| `ssh_keys` | `[]` | Static public keys, before the GitHub ones; the only keys without `github`. A key that already belongs to an earlier entry is skipped: `<name>: key <type> <comment> also belongs to <other>, skipped`. |
| `gpg_key` | `false` | `true`: a passphrase-less GPG key in the VM for git-secret, uid `<name>+<inventory host>@<domain>`. The apply that creates it prints the public key; the VM keeps it in `/etc/hades/keys/<name>.asc`. |
| `repos` | `dev_repos` | Replaces `dev_repos` for this developer. |
| `memory`, `cpus`, `disk` | `vm_memory`, `vm_cpus`, `vm_disk` | This developer's VM size: a whole number with a unit (`28GiB`, `500GB`), for memory also a share (`80%`); `cpus` a count. Preflight checks the formats. |
| `state` | `present` | `absent` offboards the developer. |

## Secrets

Exported in the controller's shell before any run; `.env.example` lists them.

| Variable | Used for |
|---|---|
| `CF_ACCOUNT_ID`, `CF_API_TOKEN` | Cloudflare tunnel, DNS and Access. |
| The variable `vpn_conf_env` names, such as `AMNEZIA_WG_CONF_BOX` | The box's AmneziaWG config, multi-line (see [VPN](#vpn-intranet)). |
| `key_env`, `fingerprint_env` of each `hex_repos` entry | Private Hex repository keys. |

Generated in each VM and never printed by an apply: GPG private keys, and the Paseo password in
`~/.config/hades/paseo.env`, which `hades-onboard` shows its developer.

## VMs

- Each present developer gets a VM named after them, from `images:ubuntu/<box release>/cloud`, on bridge `hadesbr0` with a fixed address, port isolation and IPv4 filtering (it sends from its own address only), started with the box (`boot.autostart`) and given 300 s to shut down with it. Its hostname is the box's inventory name.
- Shares, read-only at the same path as on the box: `/opt/hades` (`bin/` with `hades`, `hades-port`, `hades-onboard`, `mise`; `orca-ide` and `orca/` while a developer lists `orca`), `/usr/local/share/mise`, `/etc/mise`.
- Sizes: a stopped VM gets the inventory's sizes and stays stopped; the apply leaves its inside alone (`<name>: not running, not configured`). A running VM whose sizes differ prints `<name>: pending: incus stop <name>, apply, incus start <name>`; you choose when.
- Root in a VM: `sudo incus exec <name> -- bash` on the box, or `ssh -J <ansible_user>@<box> root@<address>`.
- State and use: `incus list`, `incus info <name>`.

## Network

| From → to | Allowed |
|---|---|
| VM → internet; VM → intranet with the VPN on | yes, NAT through the box |
| VM → another VM | no |
| VM → box, its Docker networks and published ports | DHCP and DNS on the bridge only |
| box → VM | yes (Caddy, the jump account, the administrator) |
| a developer's tailnet → their VM | yes (Tailscale in the VM; the box has none) |

The rules live in table `inet hades` (`/etc/nftables.d/hades.nft`, applied by
`hades-nftables.service`); the roles never enable `nftables.service`. With ufw active, the host
role adds the bridge rules: DHCP (67/udp) and DNS (53) in on `hadesbr0`, routed traffic in and out
on `hadesbr0`. A box with Docker but without ufw drops the VMs' forwarded traffic; add these rules
and restore them at boot with your own firewall tooling:

```sh
iptables -I DOCKER-USER -i hadesbr0 -j ACCEPT
iptables -I DOCKER-USER -o hadesbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
```

## SSH entry

- Developers reach their VM through one jump account, `hades`. Each key in its `authorized_keys` may only open `<own VM address>:22` and prints `<login>@<address>` when used for a session, which is how `hades join` learns both.
- `/etc/ssh/sshd_config.d/hades.conf` holds `AllowUsers hades` only when another sshd file uses `AllowUsers`, and a `Match User hades` block that refuses remote and Unix-socket forwards, a terminal, X11 and agent forwarding. The roles write nothing else in `/etc/ssh`.
- `hades-keys [LOGIN]` (root, `/usr/local/sbin/hades-keys`; hourly through `hades-keys.timer` and at every apply) writes the jump account's keys and each VM's `~/.ssh/authorized_keys` for its developer: `/etc/hades/ssh-keys/<name>`, then `https://github.com/<github>.keys`. It prints `<name>: updated`, `unchanged`, `fetch failed, kept`, `no account yet, skipped` or `write failed, kept`, then `jump file: updated` or `unchanged`. A failed fetch keeps that developer's keys; an empty answer removes their GitHub keys. A run that waits 600 s for another prints `locked, skipped`.
- Refresh now: `sudo hades-keys <name>` for one developer, `sudo systemctl start hades-keys.service` for all.

## Tools

- mise installs every `tools` version once, on the box, into `/usr/local/share/mise`; every VM sees it read-only. `/etc/mise/config.toml` sets the defaults (the first version of each tool) and turns off auto-install, so a project pinning a version outside the store fails.
- The store is exactly `tools`. To move a project to a new version:
  1. Add the version to `tools` and apply `--tags host`. Every VM has it at once; nothing restarts.
  2. The project pins it in `.tool-versions` in git; each worktree moves when it is rebased.
  3. Remove the old version once nothing pins it, and apply `--tags host`. A version a process in a running VM still uses stays, with `<tool>@<version> kept: in use in <name>`; a later apply removes it.
- The first `tools.node` entry is the default Node of every directory without a pin. Changing it installs the agent CLIs and Paseo again under it and makes each running ADE's restart pending ([Operations](#operations)). A new pinned `agent_clis` version restarts nothing: agents started afterwards run it.
- Installs run at CPU and IO weight 20, below the VMs; mise's and npm's caches are cleared after them.

## Cloudflare

With the two variables exported and every present developer carrying an `email`, apply
(`--tags ingress` is enough). The role reads first, writes only on difference, and never deletes.

| Object | Value |
|---|---|
| Tunnel | `hades-<inventory host name>`, routing `*.<domain>` to the box's Caddy, 404 catch-all. |
| DNS | A proxied `*.<domain>` CNAME to the tunnel. Any other record on that name stops the run. |
| Token | `/etc/cloudflared/token`, root 0600, read by the connector with `--token-file`. |
| Access application | "hades dev sites" on `*.<domain>`, hidden from the launcher, its login page listing every login method, `access_session` long. |
| One-time PIN | The login by a PIN mailed to the email, created when the account has none; other login methods are left as they are. |
| Access organization | Created when the account has none, which enables Zero Trust; team domain `<cloudflare_team>.cloudflareaccess.com`. If Cloudflare refuses: `Zero Trust could not be created through the API: enable it once in the Cloudflare dashboard, then apply again`, with Cloudflare's reason. |
| Policy `team` | Allows exactly the present developers' `email` plus `access_allow`. |

A wrong zone name or a token without Zone permissions stops the run with
`the API token sees no Cloudflare zone named hades_domain`. Any other API failure prints the task
and Cloudflare's errors.

The box's Caddy listens on loopback port 8090 and changes only when developers come or go. It sends
a host name starting with `<name>-` to that developer's VM, whose own Caddy maps each worktree to its
port, and redirects any other name to the apex domain. On a developer's names, `/dev*`, `/tidewave*`
and `/phoenix/live_reload*` answer 403 unless Access' token and that developer's `email` come with
the request; other paths are open to every allowed email.

## VPN (intranet)

1. Export the box's AmneziaWG config from the Amnezia client. Edit `AllowedIPs` to the intranet subnets and remove any `DNS` line.
2. `export AMNEZIA_WG_CONF_BOX="$(cat box.conf)"`.
3. In the inventory set `vpn_enabled: true`, `vpn_conf_env: AMNEZIA_WG_CONF_BOX` and `vpn_probe: "<intranet ip>:<port>"`.
4. `ansible-playbook playbooks/site.yml --tags vpn`, then `ansible-playbook playbooks/verify.yml --tags vpn`.

| | |
|---|---|
| Installs | `ppa:amnezia/ppa`; `amneziawg` (DKMS kernel module), `amneziawg-tools`, the running kernel's headers. Ubuntu only. |
| Writes | `/etc/amnezia/amneziawg/intranet.conf` (root 0600) over stdin; unit `awg-quick@intranet`. A changed config restarts the tunnel. |
| Guards | Preflight rejects `0.0.0.0/0`, `::/0`, their `/1` halves, any `DNS =` line and an overlap with `vm_subnet`. Verify asserts no default route uses the tunnel, a handshake under 180 s, the DKMS module for the running kernel, and `vpn_probe` answering from the box and from a VM. |
| Scope | Every VM reaches the intranet through the box; identity there comes from each developer's own credentials. |
| Kernel upgrade | The module is built for the running kernel only. After a reboot into a new kernel, apply `--tags vpn` again; verify fails until then. A kernel the PPA's module does not build for: boot the previous kernel. |
| Rollback | `systemctl disable --now awg-quick@intranet`; `apt remove amneziawg amneziawg-dkms amneziawg-tools dkms` and the added `linux-headers-*`; delete `/etc/apt/sources.list.d/amnezia-ppa.sources` and `/etc/apt/keyrings/amnezia-ppa.asc`; set `vpn_enabled: false`. |

## Onboarding a developer

1. Ask for their GitHub login and the email they will log in with.
2. Add the four-key entry to `dev_users`, with `ades: [orca]` or `[paseo, orca]` for Orca.
3. Apply `site.yml`: it creates and configures the VM, routes `<name>-…` to it and admits the email.
4. Verify `--tags vms,vm,ade,ingress`.
5. Send them the host name. They run `hades join <host>`; the [developer guide](developer.md) has the rest.
6. Projects using git-secret: a key holder runs `git secret tell <name>+<inventory host>@<hades_domain>` with the public key (needs `gpg_key: true`).

## Offboarding a developer

1. Set `state: absent` on the entry.
2. Apply `site.yml`. While the VM exists, the host role starts it if stopped, logs it out of Tailscale, archives `/home/<name>`, without `.cache` and `.paseo/models`, from inside it to `/var/backups/hades/<name>-offboard-<YYYYmmddTHHMMSS>.tar.gz` (root, 0600, never pruned), then deletes the VM with its databases and Docker volumes. The same run drops their jump keys, Caddy route and Access email.
3. Verify `--tags offboard`.
4. Revoke by hand: production certificate and keys, GitHub access.
5. The archive holds the developer's credentials (kubeconfig, GitHub token, agent logins). Verify expects one per absent entry: delete the entry, then the archive, when you no longer need it.

## Verify

`verify.yml` with no tags runs every set below; `--tags <tag>` runs one. Every set is read-only
except its own probes.

| Tag | Proves |
|---|---|
| `host` | the hades version and `hades-port` in the share, the box's mise is the shared one, agent CLIs, Paseo and Orca in the store |
| `vms` | each present developer's VM runs with the inventory's limits and devices and sees its CPUs and memory; the shares refuse writes from root; the shared binaries and shims resolve |
| `isolation` | table `inet hades` and its unit; the box reaches a VM; a VM reaches neither another VM, directly or routed through the box, nor the box's SSH; a VM fetches from the internet |
| `jump` | each jump key only tunnels to its VM's SSH and prints the login and address; a failed fetch keeps the keys. With `HADES_TEST_KEY`, a private key on the controller whose public key is in a present developer's `ssh_keys`: another VM, a Unix socket, a remote forward and a shell are refused; without it these probes are skipped |
| `offboard` | each absent developer's archive is root's, 0600, and holds the home; their VM, jump keys and key list line are gone |
| `ingress` | Caddy on loopback, the apex redirect, routing to a developer's VM, each developer's dev paths (403 without the token or with another email), cloudflared, the edge redirects to the login, the login page offers the One-time PIN, the Access policy equals the inventory |
| `vpn` | tunnel unit, no default route through it, handshake, DKMS module, the probe from the box and from a VM |
| `vm` | in each VM: the account, sudo, Docker, limits, the hostname, the box's Ubuntu release, shell environment, tools, `hades doctor` loading the host config, the daily cleanup's timer, Hex, Postgres with PostGIS, Chrome headless, the GPG key, Tailscale, the `dev_services` ports |
| `ade` | Paseo answers in each VM; the Orca unit is enabled where `ades` lists `orca` |
| `acceptance` | only with `-e acceptance_user=<developer>`: `hades up` for a sample project in their VM answers through the box's Caddy, `hades down` takes it away |

After a reboot, run `verify.yml`.

## Operations

| Task | Do |
|---|---|
| Add or remove a developer | [Onboarding](#onboarding-a-developer), [Offboarding](#offboarding-a-developer). |
| Resize a VM | Edit `memory`, `cpus` or `disk` (or `vm_*`) and apply; see [VMs](#vms). |
| Add or remove a tool version | [Tools](#tools). |
| Move everyone to a new ADE or agent CLI version | Change `paseo_version`, `orca_version` or a pinned `agent_clis` entry; apply `--tags host,vm`. A running Paseo or Orca keeps running: the apply that rewrites its unit prints `<name>: pending: systemctl --user restart <ade>.service`, once; pass it on to the developer. |
| Upgrade the hades CLI | Change `hades_version` to a published release, or rebuild into `hades_local_binary_dir`; apply `--tags host`. Every VM runs the new one at once. Developers reinstall the laptop CLI themselves. |
| Upgrade Ubuntu | The box first (`do-release-upgrade`), then each VM (`sudo incus exec <name> -- do-release-upgrade`), then apply. |
| Rebuild a VM | `incus delete --force <name>`, then apply. To keep the home, offboard first. The developer runs `ssh-keygen -R <alias>` and `hades join <host>`. |
| Move a developer to another box | Offboard them here, add them there, and restore the archive into the new VM by hand. |
| Add a package to every VM | `extra_packages`, apply `--tags vm`. |
| Refresh SSH keys now | [SSH entry](#ssh-entry). |
| Roll back an apply | Revert the inventory and apply again. Removed tool versions are reinstalled; packages the roles installed stay. |
| Reboot | By hand; the VMs start with the box. Then run `verify.yml`. |
| What is wrong | `incus list`, `incus info <name>`, `verify.yml --tags <tag>`, and `hades doctor` as the developer: `sudo incus exec <name> -- runuser -l <name> -c 'hades doctor'`. |

## Known limits

- A partly unpacked Chrome for Testing sticks behind its `creates` guard: remove `/opt/chrome-for-testing/<version>/` in that VM and apply again.
- Private Hex keys are passed to `mix` as arguments and show in the VM's process list while it runs.
- A changed open-files limit reaches a VM's running user manager only after it restarts.
- VM disks are not encrypted; use full-disk encryption at provisioning.
- Nothing snapshots or backs up a VM; offboarding keeps the home only.
