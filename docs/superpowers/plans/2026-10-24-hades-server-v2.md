# Hades server v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the v1 collection's per-user toolchain into one shared, inventory-pinned store; add one administrator per box, a host-wide AmneziaWG intranet tunnel, per-developer production access and Tailscale node sharing, all documented as the box's rules.

**Architecture:** The `host` role gains a `toolchain.yml` that installs `mise` and every `tools` version into a root-owned store, plus the agent CLIs, Paseo and Orca under it; the `user` role shrinks to accounts, per-user state and units pointing at the store. A new optional `vpn` role runs `awg-quick@intranet` from a config delivered over pipelined stdin. `admin: true` puts a user in group `sudo`. Everything else (Tailscale sharing, production access) is docs and verify.

**Tech Stack:** ansible-core 2.21 (`pipelining = True`), mise (system installs), AmneziaWG via the vendor PPA (DKMS), systemd, Go (one CLI change).

**Spec:** `docs/superpowers/specs/2026-10-24-hades-server-v2-design.md` (amends `2026-10-23-hades-server-design.md`).

## Global Constraints

- KISS: the less code the better; delete v1 code that the store makes redundant; no toggles the inventory does not set.
- Secrets reach the host only through pipelined `stdin:` into a host-side command with `no_log: true`; never `copy: content:`, `template`, `environment:` or an argument. Nothing secret in any tracked file, report or log.
- No IP, host name, domain, token or password in any tracked file; `scripts/check-no-literals.sh` on both inventories exits 0.
- `uvx ansible-lint` production profile passes with 0 failures; `--syntax-check` passes for `site.yml`, `verify.yml`, `preflight.yml`; every task is idempotent (second real run `changed=0`) and check-mode safe on a fresh inventory.
- Store paths: `mise_store: /usr/local/share/mise`, `mise_shims: "{{ mise_store }}/shims"`, `orca_root: /opt/orca`, `orca-ide` at `/usr/local/bin/orca-ide`, VPN interface `intranet`, config `/etc/amnezia/amneziawg/intranet.conf` (root 0600), tunnel unit `awg-quick@intranet.service`.
- `tools` versions are exact: at least two numeric components, optionally a suffix (`28.5`, `1.20.4-otp-29`, `24.22.1`); preflight rejects a bare major. Keys are mise registry names (`node`, `erlang`, `elixir`, `python`, `go`, `kubectl`, `gh`; never `golang`). The first listed version of a tool is the box default. List `erlang` before `elixir`: Elixir installs need `erl` on PATH.
- Variable names in `roles/host/defaults/main.yml` (shared defaults, visible under any tag): `mise_version`, `mise_store`, `mise_shims`, `tools`, `agent_clis`, `paseo_version`, `orca_version`, `orca_releases_url`, `orca_root`, `orca_packages`, `kerl_configure_options`, `host_extra_packages`, `vpn_enabled`, `vpn_conf_env`, `vpn_probe`. Removed everywhere: `node_major`, `asdf_version`, `asdf_url`, `asdf_plugins`, `asdf_global_versions`, `user_orca_releases_url`, `user_orca_packages`.
- Live host (the reference host): every ansible command in the FOREGROUND with `timeout 590`, by tag group (`host`, `postgres`, `services`, `ingress --skip-tags cloudflared`, `user`, `protected`, `vpn`), check mode before every real apply. Never reboot, never touch `/etc/ssh`, ufw, nft beyond `table inet hades`, Docker, Postgres 18-main, protected accounts or containers, apt upgrades or removals of packages the role did not add. The roles never delete anything inside a home. The operator (owner) removes v1 leftovers from homes by hand (Task 4 step 0); an agent never touches the owner's home.
- Commit per task with the message given; `Co-Authored-By` trailer as the session requires; never commit `ansible/inventory.yml` or `.superpowers/`.

## Review Focus

1. A `tools` version mise cannot build or fetch (a typo, an Erlang build failure) must fail the host apply loudly and leave no half-installed version that a later run treats as present. Test in Task 2: install idempotence keys on `mise ls --system --json`, never on a directory's existence.
2. A project `.tool-versions` naming a version not in the store must fail `hades up` with mise's not-installed error, never trigger an install or fall through to a system binary. Test in Task 5 (acceptance): a throwaway worktree pinning `node 1.0.0` fails with that error.
3. A default-Node bump must reinstall the agent CLIs and Paseo under the new default and restart every user's `paseo.service`; the old Node's globals vanish with the prune. Test in Task 4: the unit's `ExecStart` resolves through `mise_shims`, and the restart condition keys on the host-level Paseo install result exposed as a fact, not on per-user state.
4. A VPN config with `DNS =` lines needs `resolvconf`, which the role does not install; a config with full-tunnel `AllowedIPs` would steal the default route. Test in Task 7: preflight rejects both; verify asserts the default route is not via `intranet`.
5. A kernel upgrade that leaves the DKMS module unbuilt must be visible: Test in Task 7: verify asserts `dkms status` lists `amneziawg` as `installed` for `ansible_kernel`, and `hades-status` prints the tunnel's handshake age.

---

### Task 0: mise spike (throwaway, no commit)

**Files:** none committed. Report: `.superpowers/sdd/<plan>/task-0-report.md`.

**Interfaces:**
- Produces: the facts Tasks 2–5 depend on: (a) the exact mise release to pin (`mise_version`); (b) whether `mise install --system node@<v>` puts installs under `/usr/local/share/mise/installs` and shims under `/usr/local/share/mise/shims` (else which env var moves them there); (c) which file is the system-wide config that pins defaults (`/etc/mise/config.toml` or `MISE_GLOBAL_CONFIG_FILE`) and the setting that turns auto-install off (`not_found_auto_install` or its current name); (d) the `mise ls --system --json` shape; (e) the command that removes a system install (`mise uninstall --system tool@v` or deleting the directory plus `mise reshim`); (f) that `node`, `erl`, `elixir` resolve through the shims for a dev user in a login shell (`runuser -l`), a non-interactive shell (`runuser -u <u> -- sh -c`) and a transient user unit (`systemd-run --user --uid`), from the user's home with no `~/.tool-versions`; (g) a `.tool-versions` in a directory asking for an uninstalled version yields mise's not-installed error, exit non-zero, no download; (h) exact latest versions for Node Active LTS, Python 3.14, Go 1.26, kubectl 1.33 on the day.

- [ ] **Step 1:** On the reference host as root, install the latest mise release binary to `/usr/local/bin/mise.spike`, run `mise.spike install --system node@<latest LTS>` and `erlang@28.5` with `KERL_CONFIGURE_OPTIONS="--without-wx --without-javac --without-odbc"`; record where installs and shims land and how long the Erlang build takes.
- [ ] **Step 2:** Run the three-context resolution check (f) and the missing-version check (g) as `hadestest` if present, else as a throwaway `misespike` user created and removed in the same session.
- [ ] **Step 3:** Record (a)–(h) in the report; remove `/usr/local/bin/mise.spike`, the spike installs and the throwaway user. If (b) or (f) fails with every documented env var, write "FALLBACK asdf" in the report: Tasks 2–5 then use asdf v0.20.2 with `ASDF_DATA_DIR=/usr/local/share/asdf` and `ASDF_DEFAULT_TOOL_VERSIONS_FILENAME=/etc/tool-versions`, same variable names, `mise_store`/`mise_shims` renamed `tool_store`/`tool_shims`.

---

### Task 1: Pipelining and stdin secret writes

**Files:**
- Modify: `ansible/ansible.cfg`, `ansible/roles/ingress/tasks/cloudflared.yml:53-63`
- Test: `ansible/playbooks/tasks/ingress_tests.yml` (existing token checks stay), lint

**Interfaces:**
- Produces the stdin pattern every later secret write copies (Task 7 uses it verbatim for the VPN config):

  ```yaml
  - name: Write the tunnel token
    ansible.builtin.shell: |
      set -e; umask 077
      new=$(cat)
      [ -f {{ path }} ] && [ "$(cat {{ path }})" = "$new" ] && exit 0
      printf '%s' "$new" > {{ path }} && echo changed
    args:
      stdin: "{{ lookup('ansible.builtin.env', 'CLOUDFLARE_TUNNEL_TOKEN') }}"
    register: ingress_token_write
    changed_when: "'changed' in ingress_token_write.stdout"
    no_log: true
    notify: cloudflared restart
  ```

- [ ] **Step 1:** Add to `ansible.cfg`: `[ssh_connection]` / `pipelining = True`. Run `ANSIBLE_CONFIG=ansible.cfg ansible-playbook -i inventory.yml playbooks/verify.yml --tags host --skip-tags cloudflared` against the reference host. Expected: `failed=0`, and the run is faster than the 232 s v1 recorded (record the number for the docs).
- [ ] **Step 2:** Confirm no `requiretty`: `ssh tokyo sudo grep -r requiretty /etc/sudoers /etc/sudoers.d` prints nothing.
- [ ] **Step 3:** Replace the `copy: content:` token task in `cloudflared.yml` with the pattern above (`path` = `/etc/cloudflared/token`). Run `ansible-lint`; expected 0 failures (`no-changed-when` satisfied by `changed_when`).
- [ ] **Step 4:** Local probe with `-i localhost, -c local`: a play that writes `stdin: hello` to a temp path twice; first run `changed=1`, second `changed=0`, file mode 0600, content `hello`. Record the recap lines.
- [ ] **Step 5:** `grep -rn "content:.*lookup('ansible.builtin.env'" ansible/roles` prints nothing; `grep -rn "environment:" ansible/roles` lists only `NEEDRESTART_SUSPEND`, `PATH`, `XDG_RUNTIME_DIR`, `GIT_TERMINAL_PROMPT`, `PGPASSWORD`-free lines (the acceptance test's `PGPASSWORD` is the shared dev password, not a secret; leave it).
- [ ] **Step 6: Commit** — `git commit -m "ansible: pipelining on, secrets written over stdin instead of controller temp files"`

---

### Task 2: Shared toolchain store (host role)

**Files:**
- Create: `ansible/roles/host/tasks/toolchain.yml`, `ansible/roles/host/templates/mise-config.toml.j2`
- Modify: `ansible/roles/host/defaults/main.yml` (add `mise_version`, `mise_store`, `mise_shims`, `tools`, `kerl_configure_options`, `host_extra_packages`; remove `node_major`), `ansible/roles/host/tasks/main.yml` (import `toolchain.yml` after `packages.yml`, before `chrome.yml`), `ansible/roles/host/tasks/packages.yml` (`name: "{{ host_packages + host_extra_packages }}"`), `ansible/roles/host/templates/profile-hades.sh.j2`, `ansible/playbooks/preflight.yml`, `ansible/inventory.example.yml`
- Test: `ansible/playbooks/tasks/host_tests.yml`, `ansible/tests/inventories/` (one new bad fixture: `tools-prefix.yml` with `node: ["24"]`)

**Interfaces:**
- Consumes: Task 0's facts (`.superpowers/sdd/2026-10-24-hades-server-v2/task-0-report.md`): `mise_version: "2026.10.18"` (bare binary asset `mise-v<version>-linux-x64`, checksum line in `SHASUMS256.txt` with a `./` prefix); installs land in `/usr/local/share/mise/installs/<tool>/<version>` with no env var; shims appear only after `mise reshim --system`; the system config is `/etc/mise/config.toml`, read by every user, and MUST carry `[settings] auto_install = false` and `not_found_system_fallback = false` (without the second, a missing project version silently runs `/usr/bin/node`); `mise ls --json` has no `--system` flag and lists requested-but-missing versions too, so the installed set is the records with `installed == true` and `install_path` under `{{ mise_store }}/installs/`, keyed `<tool>@<version>`; `mise uninstall tool@v [tool@v …]` removes system installs and their shims (no flag); one `mise install --system a@1 b@2 …` command installs several versions and resolves Erlang before Elixir when listed in that order.
- Produces: `mise_store`, `mise_shims` (used by Tasks 3, 4, 5); the store's default Node at `{{ mise_store }}/installs/node/{{ tools.node[0] }}`; `host_tools_default: "{{ tools | dict2items | map(attribute='key') | zip(tools.values() | map('first')) | items2dict }}"` (set_fact, used by Task 3 and the config template); profile snippet exporting `PATH={{ mise_shims }}:$PATH` and `DISABLE_AUTOUPDATER=1` for group `hades`.
- Default `tools` in host defaults: `{node: ["24.22.1"]}` only; the example inventory shows the reference host's full list from the spec's 6.2 with the pins of 2026-10-24: node 24.22.1 (newest LTS; Node 26 becomes Active LTS on 2026-10-28, the owner decides when to move), python 3.14.9 and 3.12.10, go 1.26.10, kubectl 1.33.13, gh 2.103.1, erlang 29.1.2 / 28.5 / 28.3.1, elixir 1.20.4-otp-29 / 1.19.5-otp-28.

- [ ] **Step 1 (RED):** Add to `host_tests.yml`: `Read the tool installs` (`mise ls --json`, `changed_when: false`) and `Assert the store holds exactly the tools versions` (`that:` the set of `key ~ '@' ~ version` over records with `installed` true and `install_path` starting with `{{ mise_store }}/installs/` equals the set from `tools`; `fail_msg` listing both sets); `Assert the shims directory exists and is root 0755` (`stat`); `Assert the system config disables fall-through` (`slurp` `/etc/mise/config.toml`, both settings present as `= false`). Run `verify.yml --tags host --skip-tags cloudflared`; expected: FAIL (`mise: command not found`).
- [ ] **Step 2 (preflight RED):** Add `Assert every tools version is exact` to `preflight.yml`: `that:` each version `is match('^\d+\.\d+(\.\d+)*([.-][A-Za-z0-9.-]+)?$')` (Erlang `28.5` passes, `24` fails) and each key `in ['node','erlang','elixir','python','go','kubectl','gh']` is not required (any registry name is fine) but `golang` is rejected with the message "use go"; `fail_msg` naming the tool and value; add the `tools-prefix.yml` fixture (`node: ["24"]`) and extend the preflight verify loop so it must be rejected. Run `verify.yml --tags preflight`; expected: FAIL (the fixture is accepted).
- [ ] **Step 3:** Write `toolchain.yml`: (a) `get_url` the bare binary `https://github.com/jdx/mise/releases/download/v{{ mise_version }}/mise-v{{ mise_version }}-linux-x64` to `/usr/local/bin/mise` mode 0755 with `checksum: sha256:<url of SHASUMS256.txt>` (get_url reads the matching line; if the `./` prefix defeats it, pass the digest string from a preceding `uri` task); (b) `file` the store directory root 0755 and `/etc/mise`; (c) `template` `mise-config.toml.j2` to `/etc/mise/config.toml` (`[tools]` = `host_tools_default`, `[settings] auto_install = false`, `not_found_system_fallback = false`); (d) `Read the tool installs`: `mise ls --json`, `changed_when: false`, filtered as the test does; (e) `Install the listed tool versions`: ONE `mise install --system` command with every missing `tool@version` in inventory order (Erlang before Elixir), `environment: {KERL_CONFIGURE_OPTIONS: "{{ kerl_configure_options }}", MISE_YES: "1"}`, `changed_when: true`, `when:` the missing list is non-empty; (f) `Remove unlisted tool versions`: one `mise uninstall` with every installed-but-unlisted `tool@version`, `changed_when: true`, `when:` non-empty; (g) `mise reshim --system` when (e) or (f) changed (`# noqa: no-handler`). Add the import to `main.yml`; add the profile lines; delete `node_major`.
- [ ] **Step 4:** `site.yml --tags host --check --diff` then the real run; expected real-run changes: mise binary, store dirs, config, one install per listed version (an Erlang build can exceed the 10-minute command timeout: run the host tag again until it completes, each run installs what it can and the next continues; never pass a `tools` subset with `-e`, that would prune; record the number of runs). Then `verify.yml --tags host,preflight` → PASS; `site.yml --tags host` again → `changed=0`.
- [ ] **Step 5:** Review Focus 1 probe: with a throwaway inventory override adding `python: ["3.99.99"]`, the host apply fails at the install task with mise's error and `mise ls --system --json` afterwards does not list it. Record.
- [ ] **Step 6: Commit** — `git commit -m "ansible host: shared mise tool store pinned by the inventory, exact versions only, pruned to the list"`

---

### Task 3: Agent CLIs, Paseo, Orca and gh in the store; Chrome for Testing from its own index

**Files:**
- Create: `ansible/roles/host/tasks/agents.yml`
- Modify: `ansible/roles/host/defaults/main.yml` (add `agent_clis`, `paseo_version`, `orca_version`, `orca_releases_url`, `orca_root`, `orca_packages`), `ansible/roles/host/tasks/main.yml` (import `agents.yml` after `toolchain.yml`), `ansible/roles/host/tasks/chrome.yml` (delete the NodeSource key/repo/pin/install/assert tasks and the `npx @puppeteer/browsers` install; Chrome for Testing comes from its own index: `uri` on `https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json`, pick `channels[chrome_channel | capitalize].downloads.chrome` where `platform == 'linux64'`, `get_url` the zip to `hades_download_dir`, `unarchive` under `chrome_root` with `creates`, link `{{ chrome_root }}/chrome` to the unpacked binary; the AppArmor profile's attachment path follows the new binary location; the `ops_tests.yml` Node assertion goes), `ansible/roles/user/defaults/main.yml` (remove `agent_clis`, `paseo_version`, `orca_version`, `user_orca_releases_url`, `user_orca_packages`)
- Delete: `ansible/roles/user/tasks/gh.yml` (gh comes from `tools`: `gh: ["<latest 2.x at build time>"]` in the reference list; the `gh.yml` import in `roles/user/tasks/main.yml` goes; `gh auth login` and `gh auth setup-git` stay the user's interactive step)
- Test: `ansible/playbooks/tasks/host_tests.yml`

**Interfaces:**
- Consumes: `mise_store`, `mise_shims`, `host_tools_default` (Task 2).
- Produces: `{{ mise_shims }}/claude`, `codex`, `paseo` resolving to `{{ mise_store }}/installs/node/{{ tools.node[0] }}/bin/…`; `{{ orca_root }}/orca-{{ orca_version }}.AppImage` with symlink `{{ orca_root }}/orca.AppImage`; `/usr/local/bin/orca-ide` wrapper (`exec {{ orca_root }}/orca.AppImage --appimage-extract-and-run "$@"`); fact `host_paseo_changed` (bool, true when Paseo was (re)installed this run) for Task 4's restart condition.

- [ ] **Step 1 (RED):** Add to `host_tests.yml`: `Read the global CLI versions` (`{{ mise_shims }}/claude --version`, `codex --version`, `paseo --version`, `/usr/local/bin/orca-ide --version` as `nobody`-free root command, `changed_when: false`, `failed_when: false`) and `Assert the agent CLIs, Paseo and Orca are installed in the store` (`paseo --version` stdout `== paseo_version`; `orca.AppImage` link target ends with `orca-{{ orca_version }}.AppImage`; `claude` and `codex` exit 0). Run `verify.yml --tags host`; expected FAIL.
- [ ] **Step 2:** Write `agents.yml`: (a) `Read the installed npm globals` (`{{ mise_shims }}/npm ls -g --json --depth=0`, `changed_when: false`, `failed_when: false`); (b) `Install the agent CLIs and Paseo` with `{{ mise_shims }}/npm install -g <pkg>@<version>` looped over `agent_clis` (version `latest` unless the map value carries `@`) plus `@getpaseo/cli@{{ paseo_version }}`, `when:` the package is missing from (a) or its version differs, `changed_when: true`, `environment: {npm_config_update_notifier: "false", npm_config_fund: "false"}`; (c) `set_fact: host_paseo_changed` from (b)'s Paseo item; (d) `Install the Orca runtime libraries` (`apt: orca_packages`, `NEEDRESTART_SUSPEND`) and `Download Orca` (`get_url` to the versioned filename, mode 0755) and `Link orca.AppImage` (`file: state=link`) and `Write orca-ide`, all `when: dev_users | default([]) | selectattr('ades','defined') | map(attribute='ades') | flatten | select('eq','orca') | list | length > 0`; (e) `Remove other Orca versions` (`find` in `orca_root` for `orca-*.AppImage` not the current, `file: absent`). `mise reshim` after (b) when changed.
- [ ] **Step 3:** In `chrome.yml` replace the NodeSource and `npx` tasks with the index lookup, download, unpack and link described in Files; keep the ldd and AppArmor tasks, updating the profile's attachment path. Delete `roles/user/tasks/gh.yml` and its import; add `gh` to the reference `tools` list in `inventory.example.yml`. RED first: `host_tests.yml` asserts `{{ chrome_root }}/chrome --version` prints the index's stable version and `{{ mise_shims }}/gh --version` prints the pinned gh.
- [ ] **Step 4:** `site.yml --tags host --check --diff`, real run, `verify.yml --tags host` → PASS, real run again → `changed=0`. Read-only: `ls -l {{ mise_store }}/installs/node/*/bin/{claude,codex,paseo}`.
- [ ] **Step 5: Commit** — `git commit -m "ansible host: agent CLIs, Paseo, Orca and gh installed once in the store; Chrome for Testing from its own index, NodeSource dropped"`

---

### Task 4: User role on the store

**Files:**
- Modify: `ansible/roles/user/tasks/present.yml` (block `environment.PATH` = `{{ mise_shims }}:/usr/local/bin:/usr/bin:/bin`; delete the `Agent CLIs` import and the `Write the ADE update script` task), `ansible/roles/user/tasks/toolchain.yml` (delete lines 1–25: asdf install, plugins, global versions; keep mix/Hex), `ansible/roles/user/tasks/ade_paseo.yml` (delete lines 3–15; restart condition `[user_paseo_password, user_paseo_unit] | select('changed') | list or host_paseo_changed | default(false)`), `ansible/roles/user/tasks/ade_orca.yml` (delete lines 10–32; restart when the unit changed or `{{ orca_root }}/orca.AppImage` link target changed: register a `stat` of the link before and compare in the unit's `vars`), `ansible/roles/user/templates/ade.service.j2` (PATH `%h/.local/bin:{{ mise_shims }}{% for … %}:/usr/local/bin:/usr/bin:/bin`; `ExecStart={{ mise_shims }}/paseo daemon run`; `ExecStart=/usr/local/bin/orca-ide serve …`; drop the asdf comment), `ansible/roles/user/templates/env.sh.j2` (PATH `$HOME/.local/bin:{{ mise_shims }}…`; drop `KERL_CONFIGURE_OPTIONS`), `ansible/roles/user/tasks/account.yml` (drop the two `.asdf` directory entries), `ansible/roles/user/defaults/main.yml` (remove `asdf_*`, `kerl_configure_options`)
- Delete: `ansible/roles/user/tasks/agents.yml`, `ansible/roles/user/templates/hades-ade-update.sh.j2`
- Test: `ansible/playbooks/tasks/user_tests.yml:57` (login check becomes `node --version && claude --version && codex --version && command -v node`), new assertion that `command -v node` output starts with `{{ mise_shims }}/` and that `$HOME/.asdf/shims` is not in the login `PATH`; `ansible/playbooks/tasks/ade_tests.yml` keeps its listener checks.

**Interfaces:**
- Consumes: `mise_shims`, `host_paseo_changed`, `orca_root` (Tasks 2–3).
- Produces: user units whose `ExecStart` resolve through the store; no per-user toolchain.

- [ ] **Step 0 (operator, owner's go required):** On the reference host, for each present dev user, the OWNER removes `~/.asdf`, `~/.tool-versions`, `~/.local/opt/orca`, `~/.local/bin/hades-ade-update`, `~/.local/bin/orca-ide` (the plan's agent sets `hadestest` back to `state: present` in the git-ignored inventory first and may clean `hadestest`'s home itself; it never touches the owner's home). Record what was removed.
- [ ] **Step 1 (RED):** Update `user_tests.yml` as above. Run `verify.yml --tags user`; expected FAIL (`node` resolves to `~/.asdf/shims` or not at all).
- [ ] **Step 2:** Apply the file changes listed. `ansible-lint` 0 failures; `--syntax-check` both playbooks.
- [ ] **Step 3:** `site.yml --tags user --check --diff` (expected: `hadestest` created afresh; roman: `env.sh`, both units rewritten, units restart; nothing else), real run, `verify.yml --tags user,ade` → PASS, real run again → `changed=0`. Read-only: `systemctl --user -M roman@ show paseo.service -p ExecStart` contains `{{ mise_shims }}/paseo`; both Paseo units active.
- [ ] **Step 4 (Review Focus 3):** Local probe with `-i localhost, -c local`: render `ade.service.j2` with `ade: paseo` and assert the `ExecStart` line equals `ExecStart=/usr/local/share/mise/shims/paseo daemon run`; probe the restart expression with `host_paseo_changed: true` and both registers unchanged → `restarted`.
- [ ] **Step 5: Commit** — `git commit -m "ansible user: toolchain, agent CLIs and ADEs come from the shared store; per-user asdf, npm installs and hades-ade-update removed"`

---

### Task 5: CLI PATH for the store, acceptance on the store

**Files:**
- Modify: `internal/execx/execx.go:57-65` (`ToolPrepend`), `internal/execx/execx_test.go`
- Test: `go test ./internal/execx/...`; `ansible/playbooks/tasks/acceptance_tests.yml` (one added RED/GREEN case)

**Interfaces:**
- Produces: `ToolPrepend(home string, extra []string) []string` returning, in order, `~/.asdf/shims`, `~/.local/share/mise/shims`, `/usr/local/share/mise/shims`, `~/.local/bin`, then `extra` (directories that do not exist are skipped by `Real.withPath` as today).

- [ ] **Step 1:** Add `TestToolPrependIncludesMiseShims` asserting the exact slice above for `home="/h"`, `extra=[]string{"/x"}`. Run `go test ./internal/execx/ -run TestToolPrepend`; expected FAIL.
- [ ] **Step 2:** Change `ToolPrepend`; run the test → PASS; `go test ./...` → PASS.
- [ ] **Step 3:** Build `hades` and `hades-port` for linux/amd64 into the directory the reference inventory's `hades_local_binary_dir` names (read it from `ansible/inventory.yml`); `site.yml --tags host` installs them (expected changed: the two binaries).
- [ ] **Step 4 (Review Focus 2):** Extend `acceptance_tests.yml` after `hades down`: write `.tool-versions` with `node 1.0.0` into a second worktree `~/sample-bad`, run `hades up --ade shell` there with `failed_when: false`, assert rc != 0 and stderr contains `1.0.0` and mise's not-installed wording from Task 0 (g); clean up in `always`. Run `verify.yml --tags acceptance -e acceptance_user=hadestest` → PASS (both the happy path through the store and the bad pin).
- [ ] **Step 5: Commit** — `git commit -m "cli: tool lookup through mise shims; acceptance covers a version missing from the store"` (CLI change goes to `main` as its own PR at finish time; note it in the report).

---

### Task 6: Administrator

**Files:**
- Modify: `ansible/roles/user/tasks/account.yml:8` (`groups: "{{ [hades_group] + (['sudo'] if user.admin | default(false) else []) }}"`), `ansible/roles/user/tasks/main.yml` (new tasks before the include of `present.yml`: `Read sshd's effective settings` (`sshd -T`, `changed_when: false`) and `Assert sshd allows agent forwarding and, with an administrator, refuses password logins` (`that:` `'allowagentforwarding yes' in …stdout_lines` always, and `'passwordauthentication no' in …stdout_lines` when `user_present | selectattr('admin','defined') | selectattr('admin') | list | length > 0`; `fail_msg` telling the operator to set the value in an sshd drop-in themselves)), `ansible/playbooks/preflight.yml` (`admin`, when defined, is boolean, in the existing well-formed-entry assertion), `ansible/inventory.example.yml` (`admin: false` on the example user with a comment)
- Test: `ansible/playbooks/tasks/user_tests.yml`

**Interfaces:**
- Produces: `user.admin` (bool, default false) honoured by `account.yml`; group `sudo` membership exactly for admins.

- [ ] **Step 1 (RED):** In `user_tests.yml` add to the per-user assertion: `(item.name in ansible_facts.getent_group['sudo'][2].split(',')) == (item.admin | default(false))` and a separate `Assert sshd allows agent forwarding and refuses password logins with an administrator` using the same `sshd -T` read and the same two conditions as the role. Set `admin: true` on `roman` in the git-ignored inventory. Run `verify.yml --tags user`; expected FAIL (roman not in sudo).
- [ ] **Step 2:** Apply the changes. `site.yml --tags user --check --diff` (expected: roman's groups gain `sudo`, nothing else), real run, `verify.yml --tags user` → PASS, real run again → `changed=0`. Read-only: `id roman` lists `sudo`; `runuser -u roman -- sudo -n true` exits 1 with `sudo: a password is required`.
- [ ] **Step 3:** Operator step recorded for the docs: `sudo passwd roman` is the owner's to run; the agent does not set a password.
- [ ] **Step 4: Commit** — `git commit -m "ansible user: admin: true puts a dev user in group sudo with a password; sshd must refuse password logins"`

---

### Task 7: Intranet VPN role

**Files:**
- Create: `ansible/roles/vpn/tasks/main.yml`, `ansible/roles/vpn/handlers/main.yml` (`awg restart`: `systemd_service: name=awg-quick@intranet state=restarted`), `ansible/playbooks/tasks/vpn_tests.yml`
- Modify: `ansible/roles/host/defaults/main.yml` (`vpn_enabled: false`, `vpn_conf_env: ""`, `vpn_probe: ""`), `ansible/playbooks/site.yml` (`- { role: vpn, tags: [vpn], when: vpn_enabled }`), `ansible/playbooks/verify.yml` (import `vpn_tests.yml`, `tags: [vpn]`, `when: vpn_enabled`), `ansible/playbooks/preflight.yml`, `ansible/.env.example` (`AMNEZIA_WG_CONF_<HOST>=` with a comment: the Amnezia client's exported AmneziaWG config, `AllowedIPs` edited to the intranet subnets, no `DNS` line), `ansible/roles/host/templates/hades-status.sh.j2` (a `vpn:` section: `awg show intranet latest-handshakes` age in seconds or `unavailable`), `ansible/inventory.example.yml`

**Interfaces:**
- Consumes: Task 1's stdin pattern; `vpn_conf_env` names the env var.
- Produces: `awg-quick@intranet.service` enabled and active; `/etc/amnezia/amneziawg/intranet.conf` root 0600.

- [ ] **Step 1 (preflight RED, Review Focus 4):** Add to `preflight.yml`, per host with `vpn_enabled`: `vpn_conf_env` non-empty and the env var set; `Assert the VPN config is a split tunnel without DNS`: `that:` the value `is not search('AllowedIPs\s*=.*(0\.0\.0\.0/0|::/0)')` and `is not search('(?m)^\s*DNS\s*=')`, `no_log: true`, `fail_msg` without the content. Probe the two regexes with a local throwaway play (`-i localhost, -c local`) that feeds three strings through the same assertion: a full-tunnel `AllowedIPs = 0.0.0.0/0, ::/0`, a config with a `DNS = 1.1.1.1` line, and a split-tunnel config with neither; expected: the first two fail, the third passes. Record the three recaps.
- [ ] **Step 2 (verify RED):** Write `vpn_tests.yml`: unit active (`systemctl is-active awg-quick@intranet`); `ip -o link show intranet` rc 0; `ip route show default` does not contain ` dev intranet`; `awg show intranet latest-handshakes` second column within 180 s of `date +%s`; `dkms status` contains a line matching `^amneziawg.*{{ ansible_kernel }}.*installed`; when `vpn_probe` is set, `nc -z -w 3 <host> <port>` rc 0. Set `vpn_enabled: true` and `vpn_conf_env` in the git-ignored inventory with the owner's exported config in the environment. Run `verify.yml --tags vpn`; expected FAIL (unit missing).
- [ ] **Step 3:** Write `roles/vpn/tasks/main.yml`: `Assert this is Ubuntu` (`ansible_facts.distribution == 'Ubuntu'`, else fail "the vendor PPA covers Ubuntu only"); `apt_repository: repo=ppa:amnezia/ppa` (`NEEDRESTART_SUSPEND`); `apt: name=[linux-headers-{{ ansible_kernel }}, amneziawg, amneziawg-tools]` (exact names per Task 0-style check on the PPA, `install_recommends: false`, `NEEDRESTART_SUSPEND`); `file: /etc/amnezia/amneziawg` root 0700; the stdin write of `lookup('ansible.builtin.env', vpn_conf_env)` to the config path (Task 1 pattern, `notify: awg restart`); `systemd_service: awg-quick@intranet enabled started daemon_reload`. Add the role to `site.yml`, the verify import, the defaults, the `hades-status` section.
- [ ] **Step 4 (live, owner has approved the kernel module on the reference host):** `site.yml --tags vpn --check --diff` (expected: PPA, three packages, directory, config, unit), then the real run; expected the DKMS build to succeed for `ansible_kernel`. `verify.yml --tags vpn` → PASS; `site.yml --tags vpn` again → `changed=0`. Read-only: `ip route show default` unchanged from before; `awg show intranet` handshake present; `docker ps` same containers; `sudo ufw status numbered` 9 rules; `ping -c1` one intranet host from `vpn_probe` if set. If the DKMS build fails: stop, do not retry, remove the packages (`apt remove amneziawg amneziawg-tools`), report BLOCKED with the build log path; the design's fallback (userspace build) becomes a new task.
- [ ] **Step 5: Commit** — `git commit -m "ansible vpn: host-wide AmneziaWG intranet tunnel from a split-tunnel config delivered over stdin"`

---

### Task 7b: Cloudflare from the inventory (tunnel, DNS, Access allow-list)

**Files:**
- Modify: `ansible/roles/ingress/tasks/cloudflared.yml` (the token comes from the API; the env assert becomes "CF_ACCOUNT_ID and CF_API_TOKEN set"), new `ansible/roles/ingress/tasks/cloudflare.yml` (the `uri` tasks), `ansible/roles/ingress/tasks/main.yml` (import `cloudflare.yml` before `cloudflared.yml`, both under `tags: [cloudflared]`), `ansible/roles/host/defaults/main.yml` (`access_allow: []`, `access_session: 720h`), `ansible/playbooks/preflight.yml` (`email` required on present users when `CF_API_TOKEN` is set), `ansible/playbooks/tasks/ingress_tests.yml`, `ansible/inventory.example.yml` (`email` per user, `access_allow`), `ansible/.env.example` (`CF_ACCOUNT_ID`, `CF_API_TOKEN`; `CLOUDFLARE_TUNNEL_TOKEN` removed)
- Delete: `ansible/roles/ingress/templates/ingress.md.j2` and the task that writes `/etc/hades/ingress.md`

**Interfaces:**
- Consumes: Task 1's stdin pattern; `hades_domain`, `caddy_port`, `ingress_loopback_address`.
- Produces: the Cloudflare objects of spec 6.6a, idempotent (GET, compare, PUT/POST only on difference; `changed_when` on the difference); the token write unchanged from Task 1 except its source (`register`ed API result, `no_log`).

- [ ] **Step 1 (RED):** `ingress_tests.yml`: when `CF_API_TOKEN` is set, `uri` from the controller (`delegate_to: localhost`) to `https://probe-<random>.{{ hades_domain }}/` with `follow_redirects: none` expects 302 and a `Location` starting with the team domain (read the organisation via the API in the test); and the Access policy's include list, read via the API, equals the set from `dev_users` (present, `email`) plus `access_allow`. Run → FAIL on the include list (today's policy is a domain rule).
- [ ] **Step 2:** `cloudflare.yml`: `uri` GET tunnels filtered by name → POST when missing; PUT configurations (ingress list); GET zone id for `hades_domain`; GET DNS `*.<domain>` CNAME → POST when missing, fail when it points elsewhere; GET token; GET apps filtered by domain → POST when missing (inline policy); PUT the organisation and the application `session_duration`; GET policies → PUT the first with the computed include list when it differs. Every task `no_log: true` where the token or the tunnel token appear; `changed_when` on the create/PUT paths only. Pass the token to Task 1's stdin task from the registered result.
- [ ] **Step 3:** Lint, syntax; local probe of the include-list expression with a four-user inventory (present with email, absent with email, present without email → preflight failure, `access_allow` with an `@domain`).
- [ ] **Step 4 (live):** `site.yml --tags ingress --check --diff` then the real run with the two variables exported; expected changes: the organisation/app session (if not already 720h), the policy include list, nothing on the tunnel or DNS (they exist). If the permission classifier blocks the agent's run, report the exact command for the owner and stop. Verify `--tags ingress` → PASS; real run again → `changed=0`.
- [ ] **Step 5: Commit** — `git commit -m "ansible ingress: Cloudflare tunnel, DNS and Access allow-list managed from the inventory"`

---

### Task 8: Docs, inventory example, checklists

**Files:**
- Modify: `docs/server.md`, `ansible/inventory.example.yml`, `ansible/README.md` (the site command list gains `vpn`), `docs/superpowers/specs/2026-10-23-hades-server-design.md` (one line at the top of 6.3 and 6.4: "superseded by the v2 design")

- [ ] **Step 1:** `docs/server.md` gains, each as one short section or bullet list, in this order: "Cloudflare" (export `CF_ACCOUNT_ID` and `CF_API_TOKEN`, give each present dev an `email`, optional `access_allow`, apply `--tags ingress`; what gets created; 30-day sessions; one-time-PIN login; replaces the dashboard steps); "The rules" (multi-tenant; root belongs to the administrator; software requests go through `tools` and `host_extra_packages`; agents run with the developer's own credentials); "Administrator" (`admin: true`, `sudo passwd <name>` once, sshd must refuse password logins); "Tools" (exact versions, first is default, pruning, keep old versions until projects move, `mise ls --system`, the not-installed error means ask the administrator); "Software requests" (`host_extra_packages`, rootless podman); "VPN" (export from the Amnezia client, edit `AllowedIPs`, no `DNS`, `AMNEZIA_WG_CONF_<HOST>`, `vpn_probe`, what verify checks, DKMS and kernel upgrades, rollback `apt remove`); "Production access" (kubeconfig in `~/.kube/config` 0600, how to ask the operator for one, `ForwardAgent yes`, any laptop SSH agent, the unattended-agent exposure and the hardening options); "Accounts each developer needs" (GitHub, laptop SSH agent, Tailscale); "Tailscale" (share the node per developer from the admin console, what the developer sees, revoke at offboarding); "Offboarding checklist" (v1 steps plus revoke certificate, keys, GitHub access, Tailscale share); "Encryption" (one paragraph: not per home, full-disk at provisioning if wanted); "Migrating from v1" (the by-hand removals, then `--tags host,user`); the ADE section loses `hades-ade-update` and the per-user install sentences; the verify table gains `vpn` and the timings Task 1 measured. Remove every sentence the store made false (`asdf`, "self-service", "never downgraded").
- [ ] **Step 2:** `inventory.example.yml`: `tools` (the reference list with exact pins), `host_extra_packages: []`, `vpn_enabled: false`, `vpn_conf_env: AMNEZIA_WG_CONF_BOX`, `vpn_probe: ""`, `admin: false` on the example user. `scripts/check-no-literals.sh` on both inventories → 0.
- [ ] **Step 3:** `grep -n "asdf\|hades-ade-update\|node_major" docs/server.md ansible/` prints nothing outside `docs/superpowers/`.
- [ ] **Step 4: Commit** — `git commit -m "docs: server v2 rules, administrator, tools, VPN, production access, Tailscale sharing, offboarding checklist"`

---

### Task 9: Idempotence, CI, hadestest offboarded again

**Files:**
- Modify: `docs/server.md` (timings only)

- [ ] **Step 1:** Check mode then real run per tag group (`host`, `postgres`, `services`, `ingress --skip-tags cloudflared`, `user`, `protected`, `vpn`), each `timeout 590`; every real run `changed=0`; record seconds per group (pipelining should have cut them; the docs table takes the new numbers).
- [ ] **Step 2:** `verify.yml` per tag (`preflight host isolation ops postgres services ingress user ade offboard protected vpn`, then `acceptance -e acceptance_user=hadestest`); all `failed=0`.
- [ ] **Step 3:** `scripts/check-no-literals.sh` both inventories → 0; `ansible-lint` → 0 failures; `go test ./...` → PASS. Push `server-v2`; `env -u GH_TOKEN gh run watch` until the `ansible` and `ci` workflows are green; fix CI-only breakage in its own commit.
- [ ] **Step 4:** Set `hadestest` to `state: absent` in the git-ignored inventory; `site.yml --tags user --check --diff` (only hadestest and the nft sets), real run, `verify.yml --tags offboard,user,ade,isolation` → PASS; read-only: account gone, archive present, roman's Paseo active, `id roman` has `sudo`.
- [ ] **Step 5: Commit** — `git commit -m "ansible: v2 idempotence verified on the reference host, timings updated"`

---

## Self-review notes

- Spec coverage: 6.1 → Task 6; 6.2 → Tasks 0, 2, 3, 4, 5; 6.3 → Task 2 (`host_extra_packages`) and Task 8 docs; 6.4 → Tasks 1, 7; 6.5 → Task 6 (`allowagentforwarding`) and Task 8; 6.6, 6.7 → Task 8; 6.8 → Tasks 2, 7, 8; acceptance list → Tasks 2–7, 9.
- Names used consistently: `mise_store`, `mise_shims`, `host_tools_default`, `host_paseo_changed`, `orca_root`, `vpn_conf_env`, `vpn_probe`, `awg-quick@intranet`.
- Review Focus 1–5 each has an owning task step (2.5, 5.4, 4.4, 7.1/7.2, 7.2). The `allowagentforwarding` check lives in Task 6 with the password-login check.
