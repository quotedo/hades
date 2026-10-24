# Server

The Ansible collection in `ansible/` turns one host into a multi-user dev server: the hades CLI and its registry, a dev Postgres, shared services, Caddy behind a Cloudflare tunnel, and per-user toolchains with Paseo or Orca. The CLI side is in [install](install.md), [configuration](configuration.md) and [ADE integration](ades.md).

## Targets

- Ubuntu 24.04+ and Debian 13+, x86_64. The reference host runs Ubuntu 26.04.
- Debian 12 is untested; some package names differ there.

## Inventory and secrets

Start from `ansible/inventory.example.yml`. Per host:

- `hades_domain`: worktree sites are `https://<label>.<domain>`. `hades_label_template` is a YAML `!unsafe` string so Ansible never expands the CLI's placeholders.
- `dev_services`: any of `rabbitmq`, `localstack`; empty means no shared-services stack.
- `dev_users`: `name`, `uid`, `state` (`present` or `absent`), `ssh_keys`, `ades` (`paseo`, `orca`), `paseo_port`, `orca_port`, `gpg_key`, `repos`. Logins must stay unique after dropping every character outside `[a-z0-9]`.
- `protected`, for workloads that were on the host before hades:
  - `floor`: guarantees `system.slice` memory and CPU weight, and gives `user.slice` a CPU weight.
  - `capped_users` and `capped_units`: CPU weight and memory caps, set by the `capped_cpu_weight` and `capped_memory_max` host vars.
  - `blocked_ports`: ports dev users may not connect to on this host, such as a production Postgres.
  - `private_homes`: these homes become mode 0750.
- Any role default can be overridden per host, for example the resource numbers in `roles/host/defaults/main.yml`.

Secrets never go into `ansible/`. Copy `.env.example` to `.env`, fill it in and export it with `set -a; . ./.env; set +a`.

- `CLOUDFLARE_TUNNEL_TOKEN` is written to `/etc/cloudflared/token` (root, 0600). Without a token, run every playbook with `--skip-tags cloudflared`.
- Private Hex repository keys are read from the environment variables that `hex_repos` names.
- Paseo passwords are generated on the host and never leave it.

## Running

From `ansible/`, with the collection's requirements installed (`ansible-galaxy collection install -r requirements.yml`):

```sh
ansible-playbook -i inventory.yml playbooks/preflight.yml
ansible-playbook -i inventory.yml playbooks/site.yml --tags host --skip-tags cloudflared
ansible-playbook -i inventory.yml playbooks/verify.yml --skip-tags cloudflared
```

- Run `preflight.yml` first on a new inventory; it changes nothing.
- On the reference host a full `site.yml` exceeds a 10-minute command timeout, so it is applied one tag group at a time, in order: `host`, `postgres`, `services`, `ingress`, `user`, `protected`. The loopback isolation rules are recomputed after every tagged run.
- From a site repository, install the collection with `ansible-galaxy collection install git+https://github.com/quotedo/hades.git#/ansible/` and wrap `quotedo.hades.site` and `quotedo.hades.verify` with `import_playbook`.

Apply times on the reference host with nothing to change:

| Tag group | Seconds | With `ANSIBLE_PIPELINING=True` |
|---|---|---|
| `host` | 279 | 82 |
| `postgres` | 78 | 23 |
| `services` | 66 | 22 |
| `ingress` (`--skip-tags cloudflared`) | 73 | 24 |
| `user` | 203 (one dev user) | 100 (two dev users) |
| `protected` | 140 | 41 |

## Interactive steps, in order

1. Run the first apply with `--skip-tags cloudflared`. It writes `/etc/hades/ingress.md`, the Cloudflare dashboard steps: create the tunnel and the Access application, then export the token and apply `--tags ingress`.
2. Before any user lists `orca`, set `tailscale_enabled: true`, apply `--tags ingress` to install Tailscale and run `tailscale up` on the host. Then add `orca` and apply `--tags user`. Preflight refuses `orca` when `tailscale_enabled` is false, and the Orca tasks fail on a host that is not logged in to Tailscale.
3. Each user runs `gh auth login`, `gh auth setup-git` and each agent CLI's own login. Private `repos` clone only after that; until then a run prints "could not clone" and goes on.
4. Paseo Desktop connects with "Remote SSH". The first apply prints each user's password once. It stays in `~/.config/hades/paseo.env` (0600; root can read it). For a phone, the user runs `paseo daemon pair --relay`.
5. Orca: the user reads the pairing URL from the unit's journal with `journalctl --user -u orca.service`.
6. For a project that uses git-secret, a current key holder runs `git secret tell` for each new user.

## ADEs

Each listed ADE runs as a user unit, `~/.config/systemd/user/<ade>.service`, under the user's lingering manager. The role starts `user@<uid>.service` right after enabling lingering, so the first user unit never races the user bus.

- **Paseo** listens on loopback only. The listen address, relay setting and `PASEO_PASSWORD` come from the unit and its 0600 environment file. Hades writes no Paseo config file.
- **Orca** binds every address and has no option to narrow it. The `input` chain of `table inet hades` rejects its ports on every interface except `lo` and `tailscale0`.
- Orca serves headless on orcad, so there is no Xvfb.
- On first start, Orca writes hooks into `~/.claude/settings.json`, `~/.codex/hooks.json` and `~/.codex/config.toml`.
- Orca terminals do not survive a unit restart, because the unit stops its whole control group.
- The Orca path was verified by a headless smoke test only, never as a unit on the reference host.

Updates are self-service and are never downgraded by the playbook:

```sh
hades-ade-update paseo [version|dist-tag]
hades-ade-update orca [version]
```

Removing an ADE from a user's `ades` is not automated. Verify flags the leftover unit. The user removes it:

```sh
systemctl --user disable --now <ade>.service
rm ~/.config/systemd/user/<ade>.service
```

## Isolation and resources

- `table inet hades` is loaded by its own `hades-nftables.service`. The role never enables `nftables.service` and never edits `/etc/nftables.conf`, which may flush ufw, Docker and other rules at boot.
- Its `output` chain rejects new TCP connections to the dev ports from any uid outside root, the present dev users and the `caddy`, `cloudflared` and `hades-postgres` accounts. The dev ports are the worktree range, the dev Postgres, the shared services, Caddy and the ADE ports. A second rule keeps dev users off `protected.blocked_ports`. Both rules match only connections to this host's own addresses.
- Per dev user, the role writes `user-<uid>.slice.d/20-hades.conf` (memory, CPU, tasks) and `user@<uid>.service.d/50-hades.conf` (open files). Caps on protected units are `20-hades-cap.conf`. The `20-` names sort before the `50-*.conf` files of `systemctl set-property`, so an operator's own values still win.
- Docker is never restarted by the role (`docker_restart_on_change: false`). New log options reach containers created after the operator's next Docker restart.
- Backups run nightly. Each user's ADE state and agent logins go to `backup_dir`, and the newest `host_backup_keep` archives per user are kept.

## Offboarding

Set the user's `state: absent` and apply `--tags user`. The role then:

1. Stops `user@<uid>.service`, disables lingering and kills the user's remaining processes.
2. Releases the user's worktrees with `hades gc --owner <name>`.
3. Archives the home to `backup_dir/<name>-offboard-<YYYYMMDDTHHMMSS>.tar.gz`. Every run archives again under its own time stamp. Offboard archives are never pruned, and a fresh dev home is about 2 GB because of asdf and npm.
4. Removes the per-uid drop-in directories and reloads systemd.
5. Deletes the account and its home last, so the next run finishes an interrupted one.

The reference host's throwaway dev user was offboarded this way. Its `state: absent` entry stays in the inventory, so re-runs stay idempotent and `verify --tags offboard` keeps a subject.

`state: absent` applies only to accounts hades created. The run fails for an existing account that is not in group `hades` or whose uid differs from the inventory. An absent login that does not exist is a no-op, but `verify --tags offboard` expects an archive for every absent entry, so remove entries for logins that never existed.

## Verify

`verify.yml` selects its checks by tag:

| Tag | Proves | Time on the reference host |
|---|---|---|
| `preflight` | preflight rejects the bad fixtures in `tests/inventories/` and accepts the valid one; no inventory literal appears in the roles or playbooks | 4 s |
| `host` | registry, host config, sudoers rule, kernel settings, `hades.slice`, set-property caps still effective, CPU governor | 144 s |
| `isolation` | the rule table is loaded at boot; a throwaway non-dev account is refused while root and ssh still connect | 44 s |
| `ops` | gc and backup timers, a backup run, `hades-status`, journald and Docker log caps, Chrome headless with its sandbox | 137 s |
| `postgres` | dev cluster on loopback in `hades.slice`, `pg_hba.conf`, credentials, wrong password refused | 26 s |
| `services` | containers running in `hades.slice` on loopback, no dev user in group docker, RabbitMQ and LocalStack answer | 26 s |
| `ingress` | Caddy on loopback only, API on its unix socket, catch-all 404, gated paths 403 without the Access header; cloudflared when a token is set | 48 s |
| `user` | accounts, lingering, resource policy, login toolchain, no sudo, `hades doctor` | 58 s |
| `ade` | each ADE's unit and listeners, Paseo's password, Orca's port in the input chain, no stale units | 44 s |
| `offboard` | absent users gone and archived, GPG keys exported | 17 s |
| `protected` | floor, caps and private homes | 60 s |
| `acceptance` | the sample project end to end, described below | 118 s |

The `acceptance` check runs only with `-e acceptance_user=<a throwaway dev user>` and is skipped otherwise. It works in that user's home with the sample project in `tests/sample-project/`:

1. It commits the project and adds a worktree.
2. It runs `hades up`.
3. It checks the `hades ls` entry, both site files, Caddy routing to the worktree port, the dev database and loopback isolation, and that `hades doctor` passes.
4. It runs `hades down` and checks that the sites, the port claim and the database are gone.
5. It runs `hades gc` as root.

It cleans up after itself even when a check fails.

The reference host has had no throwaway dev user since its offboarding, so its verify runs now skip `acceptance`.

`-e reboot_test=true` reboots the host after the other checks and repeats the `host`, `isolation`, `postgres`, `services`, `ingress` and `ade` checks. It has not been exercised on the reference host.

## Known limits

- Hosts whose sshd uses `AllowUsers` need a drop-in that lists the dev users. This is not automated.
- A changed open-files limit reaches an already running user manager only after it restarts.
- A partly downloaded Chrome for Testing sticks behind its `creates:` guard. Remove `browsers/` under `chrome_root` and run again.
- `hades-status` embeds the dev Postgres password. That password is readable by every local user anyway, by design of the dev cluster.
