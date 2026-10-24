# Hades Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An Ansible collection in this repository that turns a plain Ubuntu/Debian x86_64 host into a multi-user, multi-project, multi-ADE dev server exactly as the server design specifies, verified end to end on a real test host.

**Architecture:** One collection `quotedo.hades` under `ansible/` with six roles (`host`, `postgres`, `services`, `ingress`, `user`, `protected`), two playbooks (`site.yml`, `verify.yml`), an example inventory with placeholders only, and a no-literals scanner. Every address, domain, port, user and version comes from inventory or facts. Each task adds its assertions to `verify.yml` first (under a tag), runs them against the test host to see them fail, implements the role, runs them again to see them pass, and commits. The `hades` binary installed by the playbook is the released CLI from this repository.

**Tech Stack:** Ansible core 2.21 on the controller (Roman's Mac) with collections `ansible.posix`, `community.general`, `community.postgresql`, `community.docker`, `community.crypto` (already installed); `ansible-lint` in CI; target Ubuntu 24.04/26.04 or Debian stable, x86_64; Caddy, cloudflared, PostgreSQL from upstream apt repos; Docker CE for shared services; Node via asdf for the ADE daemons.

**Spec:** `/Users/roman/Projects/hades/docs/superpowers/specs/2026-10-23-hades-server-design.md` (binding); companion `2026-10-23-hades-cli-design.md` for the host contract (`/etc/hades/host.toml` keys, registry directories, sudoers line, `caddy-reload`, `gc`, `doctor`).

## Global Constraints

- **No address is a constant.** Every IP, CIDR, hostname, domain, zone, port, interface, user name and uid in `ansible/` comes from inventory variables, role defaults, or facts gathered at run time. `verify.yml` runs `scripts/check-no-literals.sh`, which greps the roles for every literal in the live inventory and fails on a hit.
- Secrets (`CLOUDFLARE_TUNNEL_TOKEN`, hex repository keys, generated Paseo passwords) come only from the operator's environment or are generated on the host and stored root-owned or user-owned with mode 0600; nothing secret is written under `ansible/`.
- Required variables are asserted at the start of the role that consumes them, so `--tags host` works before the Cloudflare checkpoint and a full run fails before touching the host when a tunnel token is missing.
- Host contract (CLI design 6.3): `/etc/hades/host.toml` root-owned 0644 with `domain`, `label_template`, `port_range`, `ports_dir`, `sites_dir`, `manifests_dir`, `tool_paths`, `chrome_path`, `[caddy] config/reload/live_dir`, `[postgres] host/port/user/password`, optional `[rabbitmq] management_url/amqp_url/user/password`; registry `/srv/hades/{ports,sites,manifests}` root:hades mode 3775; `/etc/caddy/hades-sites` root 0755; sudoers: group `hades` may run exactly `/usr/local/bin/hades caddy-reload` and `/usr/local/bin/hades caddy-reload --check` as root with `env_reset` and no `env_keep` for `HADES_*`, `HOME`, `PATH`.
- Dev users are local accounts (`/etc/passwd`), in group `hades`, no sudo, no docker group, lingering enabled; logins must be unique after stripping everything outside `[a-z0-9]` (preflight assertion).
- Resource policy starting values (design 6.2): per-user `MemoryHigh=88G MemoryMax=96G MemorySwapMax=2G CPUWeight=900 TasksMax=infinity`; `hades.slice` `MemoryMax=16G CPUWeight=300`; floor `system.slice MemoryLow=10G CPUWeight=300`, `user.slice CPUWeight=700`; capped users/units `CPUWeight=100 MemoryMax=6G`; `vm.swappiness=10`; `fs.inotify.max_user_instances=4096`, `fs.inotify.max_user_watches=2097152`; `DefaultLimitNOFILE=1048576`; `ERL_FLAGS="+sbwt none +sbwtdcpu none +sbwtdio none"`. All as role defaults, overridable per host.
- ADE daemons never bind beyond loopback; Paseo gets a per-user password (`PASEO_PASSWORD` via a 0600 `EnvironmentFile`); loopback isolation rejects TCP to the dev port set from any uid outside group `hades`, root, and the service accounts (`caddy`, `cloudflared`, `hades-postgres`).
- Idempotence: the second `site.yml` run reports zero changes; a newer self-installed Paseo or Orca is never downgraded.
- Commit after every task with a one-line conventional subject; `ansible-playbook --syntax-check` and `ansible-lint` clean before each commit.

## Review Focus

1. A `dev_users` entry with `state: absent` for a login that never existed: the playbook is a no-op for it, no error. Pinned in Task 9.
2. Inventory missing `hades_domain` (or `dev_users` with a malformed entry): the run fails in preflight naming the variable, before any task touches the host. Pinned in Task 1.
3. A developer has self-updated Paseo to a newer version than `paseo_version`: a re-run leaves it alone and reports zero changes. Pinned in Task 8.
4. A user with `ades: []`: account, toolchain and `hades` work, no ADE unit is created, `hades doctor` passes. Pinned in Task 7.
5. Two logins that collide after the `[a-z0-9]` projection (`j-smith`, `jsmith`): preflight fails naming both. Pinned in Task 1.

## Test host

Tasks 2–12 run their assertions against a real host named in a private inventory file (`ansible/inventory.yml`, git-ignored). The controller provides it before Task 2: either a throwaway Hetzner Cloud VPS (Ubuntu 24.04, created with `hcloud`, destroyed at the end) or, with Roman's explicit go, `tokyo`. Nothing in this plan may be applied to `tokyo` without that go. Where a step says "Run against the host", it means `ansible-playbook -i ansible/inventory.yml ansible/playbooks/<playbook>.yml …`.

---

### Task 1: Collection scaffold, preflight, no-literals scanner, CI

**Files:**
- Create: `ansible/galaxy.yml`, `ansible/ansible.cfg`, `ansible/inventory.example.yml`, `ansible/.env.example`, `ansible/playbooks/site.yml`, `ansible/playbooks/verify.yml`, `ansible/playbooks/preflight.yml` (imported by both), `ansible/scripts/check-no-literals.sh`, `ansible/README.md`, `ansible/requirements.yml`, `.github/workflows/ansible.yml`
- Modify: `.gitignore` (add `ansible/inventory.yml`, `ansible/.env`)

**Interfaces:**
- Produces: collection `quotedo.hades`; inventory group `dev_servers`; host vars `hades_domain`, `hades_label_template` (default `{{project}}-{{name}}`), `cloudflare_team_domain` (optional), `dev_services` (list), `protected` (dict), `dev_users` (list of `{name, uid, state, ssh_keys, ades, paseo_port, orca_port, gpg_key, repos}`); `preflight.yml` asserts `hades_domain` is a non-empty string, every `dev_users` entry has `name` (matching `^[a-z][a-z0-9_-]{0,31}$`), `uid` (integer ≥ 1000), `state` in `[present, absent]`, `ades` a list drawn from `[paseo, orca]`, and that the `[a-z0-9]` projections of all `present` names are unique (failure names the colliding logins).
- Produces: `scripts/check-no-literals.sh <inventory>`: reads the inventory through `ansible-inventory -i <file> --list` (JSON, parsed with `python3 -c 'import json'`, never PyYAML), collects every string value and every integer ≥ 1024 as literals, greps `ansible/roles` and `ansible/playbooks` for each, exits 1 naming file:line and literal on any hit, exits 0 otherwise. Tiny literals (`true`, `present`, names shorter than 4 characters) are skipped.
- `site.yml`: `hosts: dev_servers`, `become: true`, imports `preflight.yml`, then roles `host`, `postgres`, `services`, `ingress`, `user`, `protected` with matching tags; a header comment lists the interactive one-time steps (Cloudflare tunnel and Access application, per user `gh auth login` and agent logins, Paseo password entry and `paseo daemon pair --relay`, `tailscale up` and Orca pairing when enabled, `git secret tell`).
- CI `ansible.yml`: on push/PR, `ubuntu-latest`, `pip install ansible-core ansible-lint`, `ansible-galaxy collection install -r ansible/requirements.yml`, `ansible-playbook --syntax-check` on both playbooks with `inventory.example.yml`, `ansible-lint ansible/`, and `scripts/check-no-literals.sh ansible/inventory.example.yml`.

- [ ] **Step 1: Write the failing checks** — `verify.yml` tag `preflight`: a `tasks/preflight_tests.yml` block that runs `ansible-playbook preflight.yml` against three fixture inventories in `ansible/tests/inventories/` (`missing-domain.yml`, `colliding-logins.yml` with `j-smith` and `jsmith`, `valid.yml`) via `command` on the controller (`delegate_to: localhost`) and asserts: the first two fail with messages containing `hades_domain` and `j-smith`/`jsmith` respectively; the third passes.
- [ ] **Step 2: Run to verify failure** — `ansible-playbook -i ansible/inventory.example.yml ansible/playbooks/verify.yml --tags preflight` → FAIL (preflight.yml missing).
- [ ] **Step 3: Implement** the files above. `galaxy.yml`: `namespace: quotedo`, `name: hades`, `version: 0.1.0`, dependencies on the five collections. `ansible.cfg`: `inventory = inventory.yml`, `roles_path = roles`, `stdout_callback = yaml`, `host_key_checking = True`. `inventory.example.yml` uses placeholders `<IP>`, `<SUDO_USER>`, `<DOMAIN>`, `<TEAM>.cloudflareaccess.com`, and one example user `{name: dev1, uid: 2001, state: present, ssh_keys: ["<SSH_PUBLIC_KEY>"], ades: [paseo], paseo_port: 6767, gpg_key: true, repos: []}`.
- [ ] **Step 4: Run tests** — the Step 1 command → PASS; `ansible-lint ansible/` → no violations; `scripts/check-no-literals.sh ansible/inventory.example.yml` → exit 0.
- [ ] **Step 5: Commit** — `git add ansible .github .gitignore && git commit -m "ansible: collection scaffold, preflight, no-literals scanner, CI"`

---

### Task 2: Role `host` part 1: packages, kernel, slices, hades binary and contract

**Files:**
- Create: `ansible/roles/host/defaults/main.yml`, `ansible/roles/host/tasks/{main,packages,sysctl,slices,hades}.yml`, `ansible/roles/host/templates/{host.toml.j2,hades-sudoers.j2,hades.slice.j2,user-slice-dropin.conf.j2,system-user-manager.conf.j2,sysctl-hades.conf.j2}`, `ansible/roles/host/handlers/main.yml`

**Interfaces:**
- Consumes: `hades_domain`, `hades_label_template`, `dev_services` (to decide whether a `[rabbitmq]` block is rendered), facts.
- Produces: defaults `hades_version` (pinned release tag), `hades_port_range: "4100-4999"`, `hades_registry_root: /srv/hades`, `hades_live_dir: /etc/caddy/hades-sites`, `caddy_port: 8090`, `dev_pg_port: 5433`, `dev_pg_user/password: postgres`, `rabbitmq_management_port: 15672`, `rabbitmq_amqp_port: 5672`, `rabbitmq_user/password: guest`, `chrome_root: /opt/chrome-for-testing`, `hades_tool_paths: []`, the resource numbers from Global Constraints, `host_packages` (build deps: `autoconf m4 libssl-dev libncurses-dev libxml2-utils xsltproc inotify-tools build-essential git curl unzip jq direnv git-secret gnupg`), `erl_flags`.
- Produces on host: packages; `/etc/sysctl.d/90-hades.conf`; `/etc/systemd/system/hades.slice`; `/etc/systemd/system/user-.slice.d/50-hades.conf` (per-user defaults) and `/etc/systemd/system/user@.service.d/50-hades.conf` (`DefaultLimitNOFILE` via `user-manager` config `/etc/systemd/user.conf.d/50-hades.conf`); group `hades`; `/srv/hades/{ports,sites,manifests}` root:hades 3775; `/etc/caddy/hades-sites` root 0755; `/usr/local/bin/hades` and `/usr/local/bin/hades-port` from the GitHub release archive for `linux_amd64` at `hades_version` (`get_url` of the tarball + `checksums.txt`, verify `sha256`, `unarchive`), installed only when `hades version` differs; `/etc/hades/host.toml` from the template; `/etc/sudoers.d/hades` (validated with `visudo -cf`); governor `performance` when `/sys/devices/system/cpu/cpu0/cpufreq` exists (`cpupower` or `cpufrequtils`).

- [ ] **Step 1: Failing assertions (tag `host`)** — `verify.yml`: `getent group hades` succeeds; `stat` of the three registry dirs shows mode `3775` and group `hades`; `/etc/caddy/hades-sites` mode `0755` owner root; `/usr/local/bin/hades version` prints `hades_version`; `/etc/hades/host.toml` exists, mode `0644`, contains `domain = "{{ hades_domain }}"`, `live_dir = "{{ hades_live_dir }}"` and `port = {{ dev_pg_port }}`; `sudo -l -U <first present dev user>` (once users exist; here: `visudo -cf /etc/sudoers.d/hades` succeeds and the file contains `caddy-reload`); `sysctl vm.swappiness` == 10; `systemctl show hades.slice -p MemoryMax` == the configured value; `systemctl show user-{{ dev_users[0].uid }}.slice -p CPUWeight` == 900 (effective once the user exists; assert the drop-in file content now).
- [ ] **Step 2: Run against the host** — `verify.yml --tags host` → FAIL.
- [ ] **Step 3: Implement** the role; handlers `daemon-reload`, `sysctl reload`.
- [ ] **Step 4: Run** — `site.yml --tags host` twice (second run: `changed=0`), then `verify.yml --tags host` → PASS; `ansible-lint` clean.
- [ ] **Step 5: Commit** — `git commit -m "ansible host: packages, sysctl, slices, registry, hades binary and host.toml"`

---

### Task 3: Role `host` part 2: loopback isolation, gc timer, backups, status

**Files:**
- Create: `ansible/roles/host/tasks/{isolation,ops,chrome}.yml`, `ansible/roles/host/templates/{hades-isolation.nft.j2,hades-gc.service.j2,hades-gc.timer.j2,hades-backup.sh.j2,hades-backup.service.j2,hades-backup.timer.j2,hades-status.sh.j2,journald-hades.conf.j2,docker-daemon-log.json.j2,profile-hades.sh.j2}`

**Interfaces:**
- Consumes: `hades_port_range`, `dev_pg_port`, service ports, `caddy_port`, every `paseo_port`/`orca_port` in `dev_users`, `protected.blocked_ports`, `backup_dir` (default `/var/backups/hades`), `chrome_channel` (default `stable`).
- Produces on host: nftables table `inet hades` (file `/etc/nftables.d/hades.nft` included from `/etc/nftables.conf`, or a dedicated `hades-nftables.service` applying it when the host uses ufw/iptables, builder's call, decided by `ansible_facts.packages`): output-chain rules rejecting TCP to the dev port set from `meta skuid` not in `{root, caddy, cloudflared, hades-postgres}` and `meta skgid != hades`, on every local address; plus `protected.blocked_ports` rejected for `skgid hades`; weekly `hades-gc.timer` running `/usr/local/bin/hades gc` as root; nightly `hades-backup.timer` tarring, per present dev user, `~/.paseo` (excluding `*.log`), `~/.config/orca`, `~/.orca`, `~/.claude`, `~/.codex`, `~/.config/gh` into `backup_dir/<user>-<date>.tar.gz`, keeping 7; `/usr/local/bin/hades-status` (slice pressure from `systemd-cgtop -b -n1`, `hades ls --json` summary, dev Postgres connection count via `psql`, last OOM kills from `journalctl -k`, `df` of the root filesystem with the five largest homes); journald `SystemMaxUse=2G`; Docker `daemon.json` log rotation (`json-file`, `max-size 50m`, `max-file 3`) merged, not overwritten; Chrome for Testing under `chrome_root` via `npx -y @puppeteer/browsers install chrome@{{ chrome_channel }}` (needs Node on the host: a system Node from NodeSource at `node_major` default 24, used only for this install and for nothing else) with its runtime libraries and fonts; `/etc/profile.d/hades.sh` exporting `CHROME_PATH` and `ERL_FLAGS`.

- [ ] **Step 1: Failing assertions (tag `isolation,ops`)** — `nft list table inet hades` succeeds and contains the first and last ports of `hades_port_range` and `dev_pg_port`; from a throwaway non-dev local user created by the test (`hades-probe`, removed after), `nc -z 127.0.0.1 {{ dev_pg_port }}` fails and `nc -z 127.0.0.1 22` succeeds; `systemctl is-enabled hades-gc.timer hades-backup.timer` both `enabled`; `hades-status` exits 0 and prints `worktrees:`; `test -x {{ chrome_root }}/chrome`; `journalctl --disk-usage` reports under 2G (informational).
- [ ] **Step 2: Run against the host** → FAIL.
- [ ] **Step 3: Implement.** `chrome.yml` installs the runtime libraries list from the design (`libnss3 libatk-bridge2.0-0 libgtk-3-0 libgbm1 libasound2t64 fonts-liberation` plus what `ldd` reports missing, discovered at run time and failed loudly).
- [ ] **Step 4: Run** — `site.yml --tags host` twice (second `changed=0`), `verify.yml --tags host,isolation,ops` → PASS; `ansible-lint` clean.
- [ ] **Step 5: Commit** — `git commit -m "ansible host: loopback isolation, gc and backup timers, status, Chrome for Testing"`

---

### Task 4: Role `postgres`: the dev cluster

**Files:**
- Create: `ansible/roles/postgres/defaults/main.yml`, `ansible/roles/postgres/tasks/main.yml`, `ansible/roles/postgres/templates/{dev-tuning.conf.j2,pg_hba.conf.j2,postgresql-slice.conf.j2}`, `ansible/roles/postgres/handlers/main.yml`

**Interfaces:**
- Consumes: `dev_pg_port`, `dev_pg_user`, `dev_pg_password`; defaults `dev_pg_major: 18` (fallback to the newest available in the PGDG repo), `dev_pg_cluster: dev`, `dev_pg_os_user: hades-postgres`, tuning values (`shared_buffers: 8GB`, `work_mem: 16MB`, `max_connections: 1000`, `max_wal_size: 4GB`, `fsync: off`, `synchronous_commit: off`, `full_page_writes: off`, `log_min_messages: warning`, `huge_pages: off`).
- Produces on host: PGDG apt repository; `postgresql-{{ dev_pg_major }}` and `postgresql-contrib`/`postgis` packages (postgis so jarl's `CREATE EXTENSION postgis` works); system user `hades-postgres` (no login shell, home `/var/lib/hades-postgres`); cluster `{{ dev_pg_major }}/dev` created with `pg_createcluster --user hades-postgres --group hades-postgres -p {{ dev_pg_port }} --start-conf auto` only when `pg_lsclusters` does not list it; `conf.d/hades-dev.conf` with the tuning; `pg_hba.conf` with `scram-sha-256` for `127.0.0.1/32` and `::1/128` only; drop-in `Slice=hades.slice` on `postgresql@{{ dev_pg_major }}-dev.service`; role `postgres` password set via `community.postgresql.postgresql_user` through a Unix socket as `hades-postgres`; the service enabled and running.

- [ ] **Step 1: Failing assertions (tag `postgres`)** — `pg_lsclusters` lists `{{ dev_pg_major }} dev {{ dev_pg_port }} online`; the cluster's data directory is owned by `hades-postgres`; `psql "postgres://{{ dev_pg_user }}:{{ dev_pg_password }}@127.0.0.1:{{ dev_pg_port }}/postgres" -tAc 'show fsync'` prints `off`; `systemctl show postgresql@{{ dev_pg_major }}-dev.service -p Slice` is `hades.slice`; `psql ... -tAc "select 1 from pg_available_extensions where name='postgis'"` prints `1`.
- [ ] **Step 2: Run against the host** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — `site.yml --tags postgres` twice (second `changed=0`), `verify.yml --tags postgres` → PASS.
- [ ] **Step 5: Commit** — `git commit -m "ansible postgres: dev cluster under its own OS user, postgis available"`

---

### Task 5: Role `services`: shared compose stack

**Files:**
- Create: `ansible/roles/services/defaults/main.yml`, `ansible/roles/services/tasks/main.yml`, `ansible/roles/services/templates/{docker-compose.yml.j2,hades-services.service.j2,rabbitmq-enabled-plugins.j2,localstack-init.sh.j2}`

**Interfaces:**
- Consumes: `dev_services` (subset of `[rabbitmq, localstack, redis]`), `rabbitmq_*` ports/credentials, `localstack_port: 4566`, `localstack_buckets: []`, `redis_port: 6379`, `rabbitmq_image: rabbitmq:4-management`, `localstack_image: localstack/localstack:4`, `redis_image: redis:7`.
- Produces on host: Docker CE (from Docker's apt repo) when absent; `/srv/hades/services/docker-compose.yml` rendered from `dev_services` with every port bound to `127.0.0.1`, `cgroup_parent: hades.slice`, RabbitMQ with `rabbitmq_management` and `rabbitmq_mqtt` plugins (MQTT port `1883` on loopback); LocalStack with `SERVICES=s3` and an init script creating `localstack_buckets`; root unit `hades-services.service` (`docker compose up -d`, `Restart=on-failure`, after `docker.service`) enabled; no dev user in the `docker` group.

- [ ] **Step 1: Failing assertions (tag `services`)** — for each listed service, `docker compose -f /srv/hades/services/docker-compose.yml ps --format json` shows it running; `curl -u {{ rabbitmq_user }}:{{ rabbitmq_password }} http://127.0.0.1:{{ rabbitmq_management_port }}/api/overview` returns 200 (when rabbitmq listed); `ss -ltn` shows the service ports bound to `127.0.0.1` only; `systemctl is-enabled hades-services.service` == `enabled`.
- [ ] **Step 2: Run against the host** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — `site.yml --tags services` twice (second `changed=0`), `verify.yml --tags services` → PASS.
- [ ] **Step 5: Commit** — `git commit -m "ansible services: loopback compose stack from dev_services under hades.slice"`

---

### Task 6: Role `ingress`: Caddy, Cloudflare Tunnel, optional Tailscale

**Files:**
- Create: `ansible/roles/ingress/defaults/main.yml`, `ansible/roles/ingress/tasks/{main,caddy,cloudflared,tailscale}.yml`, `ansible/roles/ingress/templates/{Caddyfile.j2,caddy-slice.conf.j2,cloudflared.env.j2}`, `ansible/roles/ingress/handlers/main.yml`

**Interfaces:**
- Consumes: `hades_domain`, `caddy_port`, `hades_live_dir`, `cloudflare_team_domain` (optional), `CLOUDFLARE_TUNNEL_TOKEN` from the controller environment (`lookup('env', ...)`, asserted non-empty at the start of `cloudflared.yml`), `tailscale_enabled` (default: true when any present user lists `orca`, else false).
- Produces on host: Caddy from the upstream apt repo; `/etc/caddy/Caddyfile` with global `auto_https off`, `admin off`, a `(worktree)` snippet (`reverse_proxy 127.0.0.1:{args[1]}` for host `{args[0]}`; the gated paths `/dev*`, `/tidewave*`, `/phoenix/live_reload*` matched by `@gated` requiring header `Cf-Access-Jwt-Assertion` present, else `respond 403`), `import {{ hades_live_dir }}/*.caddy`, and a catch-all `:{{ caddy_port }}` site on `127.0.0.1` responding 404 with the naming convention; drop-in `Slice=hades.slice`; `cloudflared` from Cloudflare's apt repo installed as a service with the token (`cloudflared service install <token>` once; the token never written to a world-readable file: `/etc/cloudflared/` mode 0600 root); `/etc/hades/ingress.md` rendered runbook for the dashboard side (tunnel ingress rule `*.<domain>` → `http://127.0.0.1:<caddy_port>`, Access application on `*` for paths `dev`, `dev/*`, `tidewave`, `tidewave/*`, `phoenix/live_reload`, `phoenix/live_reload/*`); Tailscale apt repo + package and `ufw allow in on tailscale0` only when `tailscale_enabled`.

- [ ] **Step 1: Failing assertions (tag `ingress`)** — `caddy validate --config /etc/caddy/Caddyfile` succeeds; `curl -s -o /dev/null -w '%{http_code}' -H 'Host: nothing.{{ hades_domain }}' http://127.0.0.1:{{ caddy_port }}/` == 404; a temporary site written as root into `{{ hades_live_dir }}/probe.caddy` (`import worktree probe.{{ hades_domain }} 65000`) plus `systemctl reload caddy` makes `curl -H 'Host: probe.{{ hades_domain }}' .../dev/mailbox` return 403 without the header (cleaned up after); `systemctl is-active cloudflared` == `active` (skipped when the token is absent and the tag `cloudflared` is not requested); `systemctl show caddy -p Slice` == `hades.slice`.
- [ ] **Step 2: Run against the host** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: [CHECKPOINT: Roman] Cloudflare dashboard** — create the tunnel and the Access application for the test zone (or skip cloudflared for a test host with no zone: `--skip-tags cloudflared`), export `CLOUDFLARE_TUNNEL_TOKEN`, run `site.yml --tags ingress` twice (second `changed=0`), `verify.yml --tags ingress` → PASS.
- [ ] **Step 5: Commit** — `git commit -m "ansible ingress: loopback Caddy with gated dev paths, Cloudflare Tunnel, optional Tailscale"`

---

### Task 7: Role `user` part 1: accounts and toolchain

**Files:**
- Create: `ansible/roles/user/defaults/main.yml`, `ansible/roles/user/tasks/{main,account,toolchain,agents}.yml`, `ansible/roles/user/templates/{user-slice.conf.j2,bashrc-hades.sh.j2,hex-repo.sh.j2}`

**Interfaces:**
- Consumes: `dev_users`, `hades_tool_paths`, `erl_flags`, `chrome_root`; defaults `asdf_version: v0.20.2`, `asdf_plugins: [erlang, elixir, nodejs, golang, python]`, `asdf_global_versions: {nodejs: "24.x latest"}` (resolved at run time with `asdf latest nodejs 24`), `kerl_configure_options: "--without-wx --without-javac --without-odbc"`, `hex_repos: []` (`{name, url, key_env, fingerprint_env}`), `agent_clis: [claude, codex]` (installed with `npm install -g @anthropic-ai/claude-code @openai/codex` under the user's asdf Node).
- Produces per `present` user: Linux user with `uid`, groups `hades`, shell `/bin/bash`, `authorized_keys` from `ssh_keys`, `loginctl enable-linger`; `/etc/systemd/system/user-{{ uid }}.slice.d/50-hades.conf`; asdf binary under `~/.asdf/bin`, plugins, global Node; `~/.bashrc` sourcing `~/.config/hades/env.sh` (asdf init, direnv hook, `ERL_FLAGS`, `CHROME_PATH`, PATH with `~/.local/bin`); `mix local.hex --force`, `mix local.rebar --force` once Elixir is available (Erlang/Elixir are installed per project `.tool-versions` by `hades up`, not globally; the role only installs the plugins and a global Node); hex repos added with keys from the controller env (`mix hex.repo add`), run as the user; `gh` from GitHub's apt repo (system-wide); agent CLIs per user; `~/.config/hades/` directory 0700. Users with `state: absent` are handled in Task 9.

- [ ] **Step 1: Failing assertions (tag `user`)** — per present user: `id -u` == uid; `id -nG` contains `hades` and not `docker`; `loginctl show-user <name> -p Linger` == `yes`; `sudo -u <name> -i bash -lc 'asdf --version && node --version && claude --version && codex --version && direnv version && hades version'` succeeds; `sudo -u <name> -i bash -lc 'echo $ERL_FLAGS'` prints the configured flags; `sudo -u <name> -n true` fails (no sudo); `sudo -u <name> -i bash -lc 'hades doctor'` exits 0 or fails only on `postgres`/`rabbitmq` when those roles have not run yet (assert exit 0 in Task 12).
- [ ] **Step 2: Run against the host** → FAIL.
- [ ] **Step 3: Implement.** Erlang build deps are already in `host_packages`.
- [ ] **Step 4: Run** — `site.yml --tags user` twice (second `changed=0`), `verify.yml --tags user` → PASS.
- [ ] **Step 5: Commit** — `git commit -m "ansible user: accounts, slices, asdf toolchain, gh, agent CLIs"`

---

### Task 8: Role `user` part 2: ADE daemons (Paseo, Orca)

**Files:**
- Create: `ansible/roles/user/tasks/{ade_paseo,ade_orca}.yml`, `ansible/roles/user/templates/{paseo.service.j2,paseo-dropin.conf.j2,paseo-config.json.j2,hades-ade-update.sh.j2,orca-dropin.conf.j2}`

**Interfaces:**
- Consumes: per user `ades`, `paseo_port`, `orca_port`; defaults `paseo_version: 0.12.0`, `orca_version: 1.4.232`, `paseo_mcp_inject: false`, `orca_terminal_memory_max: 28G`; fact `tailscale_ip` (from `tailscale ip -4`) when Orca is enabled.
- Produces per user with `paseo`: `npm install -g @getpaseo/cli@{{ paseo_version }}` under the user's asdf Node only when `paseo --version` is absent or older (never downgrade); `~/.paseo/config.json` with `daemon.listen = "127.0.0.1:{{ paseo_port }}"`, `daemon.relay.enabled = false`, `daemon.mcp.injectIntoAgents = {{ paseo_mcp_inject }}`; `~/.config/hades/paseo.env` 0600 with `PASEO_PASSWORD=<generated once, 32 random chars>` (kept if present) and `PASEO_LISTEN`, `PASEO_RELAY_ENABLED=false`; user unit `~/.config/systemd/user/paseo.service` running `paseo daemon run` with `EnvironmentFile`, `Environment=PATH=%h/.local/bin:%h/.asdf/shims:/usr/local/bin:/usr/bin:/bin`, `ERL_FLAGS`, `CHROME_PATH`, `Restart=on-failure`, enabled and started via `systemctl --user` as the user (`XDG_RUNTIME_DIR=/run/user/<uid>`); `~/.local/bin/hades-ade-update` (`paseo`: npm install `@latest` or a given version, restart unit; `orca`: AppImage download, restart); the generated password printed once to the operator in the play recap (`debug` with `no_log` on the file tasks) and never stored on the controller. Per user with `orca`: AppImage at `~/.local/opt/orca/orca.AppImage` (downloaded only when absent), `~/.local/bin/orca-ide` wrapper using `--appimage-extract-and-run`, `orca-ide serve service install --port {{ orca_port }}` as the user when the unit is absent, drop-in with `ORCA_TERMINAL_MEMORY_MAX`, `ORCA_TERMINAL_MEMORY_SWAP_MAX=0`, PATH, `ERL_FLAGS`, `CHROME_PATH`, `LimitNOFILE=1048576`; the Electron/Xvfb library set installed system-wide when any user has Orca.

- [ ] **Step 1: Failing assertions (tag `ade`)** — per user with paseo: `systemctl --user -M <name>@ is-active paseo.service` == `active`; `ss -ltnp` shows `127.0.0.1:{{ paseo_port }}` owned by a `node` process of that uid and nothing on `0.0.0.0:{{ paseo_port }}`; `curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:{{ paseo_port }}/api/health` == 200 and an unauthenticated `curl .../api/agents` != 200; `stat -c %a ~/.config/hades/paseo.env` == 600. Per user with orca: the user unit is active and the port is bound to the Tailscale IP only. For a user with `ades: []`: no `paseo.service`/orca unit exists (Review Focus 4).
- [ ] **Step 2: Run against the host** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — `site.yml --tags user` twice (second `changed=0`); then, as the test user, `hades-ade-update paseo` to the `beta` dist-tag and re-run `site.yml --tags user`: `changed=0` and `paseo --version` still the newer one (Review Focus 3); `verify.yml --tags ade` → PASS.
- [ ] **Step 5: [CHECKPOINT: Roman] client connection** — from Paseo Desktop add the test host as a Remote SSH host with the printed password and confirm it connects; optionally `paseo daemon pair --relay` for the phone.
- [ ] **Step 6: Commit** — `git commit -m "ansible user: Paseo and Orca daemons per user with passwords, PATH drop-ins, update script"`

---

### Task 9: Role `user` part 3: offboarding and GPG keys

**Files:**
- Create: `ansible/roles/user/tasks/{offboard,gpg,repos}.yml`

**Interfaces:**
- Consumes: `dev_users` entries with `state: absent`; per user `gpg_key`, `repos` (list of git URLs); `backup_dir`.
- Produces: for `absent` users that exist: stop and disable their user units, `hades gc --owner <name>` as root (binary present from Task 2), `loginctl disable-linger`, `pkill -u`, `tar` of the home into `backup_dir/<name>-offboard-<date>.tar.gz` once, then `user: state=absent remove=yes`; for `absent` users that do not exist: nothing, no error (Review Focus 1). For present users with `gpg_key: true`: a GPG key pair generated once as the user (`community.crypto.openpgp_*` or `gpg --batch --quick-gen-key` with `<name>+<inventory_hostname>@<hades_domain>` and no passphrase) and the public key exported to `/etc/hades/keys/<name>.asc` (root-readable, printed in the recap so a project key holder can `git secret tell`). For `repos`: `git clone` as the user into `~/<repo basename>` when absent (uses `gh auth` credential helper, so it runs after the user has logged in; failure is a clear message, not a stop, when the clone is private and `gh` is not yet authenticated).

- [ ] **Step 1: Failing assertions (tag `offboard`)** — a test user `offb` created in a preceding play step with a manifest written by `hades up --dry-run`? No: with a real registry entry seeded by root for the test (`manifests/demo-offb.toml` owned by `offb`, a port link); after running `site.yml --tags user` with `offb` set to `state: absent`: `id offb` fails, no `offb` manifest or port link remains, one `offb-offboard-*.tar.gz` exists in `backup_dir`; running again with the same inventory → `changed=0` and no error; for a present user with `gpg_key: true`: `gpg --list-keys` as the user lists one key and `/etc/hades/keys/<name>.asc` exists.
- [ ] **Step 2: Run against the host** → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** → PASS; idempotent.
- [ ] **Step 5: Commit** — `git commit -m "ansible user: offboarding via state absent and hades gc --owner, GPG keys, repo clones"`

---

### Task 10: Role `protected`: floor for existing workloads

**Files:**
- Create: `ansible/roles/protected/defaults/main.yml`, `ansible/roles/protected/tasks/main.yml`, `ansible/roles/protected/templates/{system-slice-floor.conf.j2,user-slice-weight.conf.j2,capped-user.conf.j2,capped-unit.conf.j2}`

**Interfaces:**
- Consumes: `protected` dict: `floor` (bool), `capped_users` (list of logins), `capped_units` (list of unit names), `blocked_ports` (list, consumed by Task 3's nft template), `private_homes` (list of paths); defaults `floor_memory_low: 10G`, `floor_cpu_weight: 300`, `user_slice_cpu_weight: 700`, `capped_cpu_weight: 100`, `capped_memory_max: 6G`.
- Produces on host: `/etc/systemd/system/system.slice.d/50-hades-floor.conf`, `/etc/systemd/system/user.slice.d/50-hades.conf`, `/etc/systemd/system/user-<uid>.slice.d/50-hades-cap.conf` for each capped login (uid resolved via `getent`), `/etc/systemd/system/<unit>.d/50-hades-cap.conf` for each capped unit, `chmod 0750` on each `private_homes` path. Role is skipped entirely when `protected` is undefined.

- [ ] **Step 1: Failing assertions (tag `protected`)** — `systemctl show system.slice -p MemoryLow` == configured; `systemctl show user.slice -p CPUWeight` == 700; for each capped login `systemctl show user-<uid>.slice -p MemoryMax` == 6G; for each private home `stat -c %a` == 750.
- [ ] **Step 2: Run against the host** (with a `protected` block naming a throwaway `capper` user and a temp dir) → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** → PASS; idempotent.
- [ ] **Step 5: Commit** — `git commit -m "ansible protected: floor, caps for users and units, private homes"`

---

### Task 11: `verify.yml` completion, acceptance checks, docs

**Files:**
- Modify: `ansible/playbooks/verify.yml` (tag `acceptance`), `ansible/README.md`
- Create: `docs/server.md` (how to run: inventory, `.env`, interactive steps, verify, update), `ansible/tests/sample-project/` (a minimal Phoenix-shaped repo: `mix.exs` with `app: :sample`, `hades.toml`, `paseo.json`, a `config/dev.exs` reading the hades variables, a trivial `mix setup` alias that creates the dev database with `psql` so no Elixir toolchain build is needed for the test)

**Interfaces:**
- Produces `acceptance` tag: as the first present user, clone `ansible/tests/sample-project` into `~/sample`, create a worktree `git worktree add ~/sample-wt -b feature/acc`, run `hades up --ade shell` inside it (asdf is present; the sample's setup uses only `psql`), then assert: `hades ls --json` lists `sample-feature-acc`; `/srv/hades/sites/sample-feature-acc.caddy` and `/etc/caddy/hades-sites/sample-feature-acc.caddy` exist with the expected line; `curl -H 'Host: sample-feature-acc.<domain>' http://127.0.0.1:<caddy_port>/` reaches the worktree port (a `python3 -m http.server` started on the claimed port for the test, or 502 when nothing listens, asserting Caddy routed); the dev database `sample_dev_<suffix>` exists; `hades down` removes the site, the port link and the database; `hades gc` as root exits 0; a non-dev probe user cannot connect to the worktree port; `hades doctor` as the user exits 0 (Postgres and services now present). Plus the no-literals scan and a reboot step (`reboot:` module, then re-run the `host,postgres,services,ingress,ade` verify tags) behind `-e reboot_test=true`.

- [ ] **Step 1: Write the acceptance tasks** (they fail until the sample project and the full stack are present).
- [ ] **Step 2: Run** — `site.yml` full run (with `--skip-tags cloudflared` when the test host has no zone), then `verify.yml` → PASS including `acceptance`; `-e reboot_test=true` once → PASS after reboot.
- [ ] **Step 3: Write `docs/server.md` and the README** from the design (inventory shape, secrets via env, the interactive steps in order, `verify.yml`, `hades-ade-update`, offboarding).
- [ ] **Step 4: Commit** — `git commit -m "ansible: acceptance checks, reboot test, sample project, server docs"`

---

### Task 12: Full idempotence run and tear-down of the test host

- [ ] **Step 1:** `site.yml` twice from scratch on a fresh test host (destroy and recreate it, or `--check --diff` against the existing one): the second run reports `changed=0`; `verify.yml` all tags → PASS; record the timings in `docs/server.md`.
- [ ] **Step 2:** `scripts/check-no-literals.sh ansible/inventory.yml` → exit 0 against the real inventory.
- [ ] **Step 3:** CI (`ansible.yml`) green on the branch.
- [ ] **Step 4:** Destroy the throwaway test host (if one was created) and remove `ansible/inventory.yml` from the controller or keep it git-ignored for the tokyo run.
- [ ] **Step 5: Commit** any doc corrections — `git commit -m "ansible: idempotence verified on a fresh host"`
