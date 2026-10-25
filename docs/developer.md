# Developer guide

For a developer with a VM on a hades box. Command flags, exit codes and files are in the
[CLI reference](cli.md); the administrator's side is in the [server reference](server.md).

## What you get

| | |
|---|---|
| VM | Your own Ubuntu VM on the box, sized by the administrator. It is your machine: `sudo` without a password, Docker, your own Postgres. |
| Access | SSH and Paseo over your own tailnet; through the box for first contact and as the fallback. |
| ADE | Paseo, Orca or both, running in your VM, always on. |
| Tools | One shared store with exact versions chosen by the administrator; `mise ls` lists them. |
| Per worktree | A label, database names, `.env.worktree` and `.envrc`; web projects also get a port and `https://<you>-<project>-<name>.<domain>`. |

## Before you start

| Need | Why |
|---|---|
| Read access to the hades GitHub repository | Its releases are private; the CLI download goes through your GitHub login. |
| `gh` on your laptop, logged in (`gh auth login`) | Downloads the CLI; `hades join` reads your GitHub login from it and uploads a key when GitHub has none of yours. |
| A Tailscale account, Tailscale on your laptop (and phone) | Your VM joins your own tailnet during onboarding; daily use goes over it. |
| Paseo Desktop on your laptop (Orca users: the Orca app) | Connects to your ADE. |
| An entry in the box's inventory | Send the administrator your GitHub login and the email you will log in with; they apply and send you the host name. |
| `hades init` run once per project, its files committed | Without `paseo.json` or `orca.yaml` in the repository, no worktree runs `hades up`. See the [CLI reference](cli.md#commands). |

## Join

1. [Install the CLI](cli.md#install) on your laptop.
2. Run:

   ```sh
   hades join <host>
   ```

   Add `--as <name>` when your name in the inventory differs from your laptop's `$USER`,
   `--alias <alias>` for an IP address or a host name without a dot, `--github <GitHub login>` when
   `gh` is not logged in.
3. Go through the [first-login steps](#first-login-steps) it starts in your VM.
4. Connect your ADE with what they print at the end (see [Connect your ADE](#connect-your-ade)).

Rerunning `hades join` is always safe. It writes two blocks in `~/.ssh/config` and touches nothing
else; the [CLI reference](cli.md#join) has every step.

| Alias | Reaches your VM | For |
|---|---|---|
| `<alias>` | written once onboarding completes: at its tailnet name when this laptop reaches it there, else through the box | daily use: `ssh <alias>`, Paseo Desktop |
| `<alias>-via-host` | always through the box: `ProxyCommand ssh -W %h:%p -l hades … <host>` | first contact, and when your VM's tailnet login has lapsed |

The alias is the host's first DNS label (`box` for `box.example.test`) unless you pass `--alias`.
Both blocks store your VM's host key under `<alias>` (`HostKeyAlias`) and set `ForwardAgent yes`,
which lets you reach production hosts from your VM with keys that stay on your laptop. ssh takes
the first value it finds for each option, so an earlier `Host *` entry that sets `User`,
`IdentityFile` or `ForwardAgent` wins over the blocks: move that entry below them.

## First-login steps

`hades-onboard` runs in your VM from `hades join`. To run it again, rerun `hades join <host>` or
`ssh -t <alias>-via-host hades-onboard <alias>`; it refuses to run without a terminal, and the
second `<alias>` goes into the printed Paseo address. Each step prints `already done` or does its work.

| Step | Does |
|---|---|
| GitHub | `gh auth login -h github.com -p https -w`: a code to enter in your browser. |
| git | `gh auth setup-git`, so git reaches GitHub with your `gh` login. |
| Claude Code, Codex | `claude auth login` and `codex login --device-auth`, for each one not logged in. |
| Repositories | Clones each repository the administrator listed into `~/<name>`, unless that directory exists. A failed clone ends the run with `onboarding incomplete: clone <names> failed; run hades-onboard again`. |
| Tailscale | `sudo tailscale up` prints a login link and waits: log in to your own tailnet. Your VM joins it named after the box. A failure ends the run with `onboarding incomplete`. |
| Orca | When Orca runs for you: restarted, so it pairs on your VM's tailnet address. |
| Check | `hades doctor`. A FAIL ends the run with `onboarding incomplete: fix the FAIL lines above and run hades-onboard again`. |
| Done | Writes `~/.config/hades/onboarded`, prints `onboarding complete`, then the connection details. |

Until onboarding completes, every interactive shell prints
`Run hades-onboard to finish setting up your account.`

## Connect your ADE

`hades-onboard` ends with:

```
Paseo Desktop: Settings > Add host > Remote SSH
  ssh://<you>@<alias>
  Daemon password: <password>
Paseo on your phone (Tailscale on): <tailnet name>:6767, same password
Orca pairing: <url>
Tailscale: disable key expiry for <tailnet name> in your Tailscale admin console, or it leaves your tailnet after 180 days.
```

| | |
|---|---|
| Paseo Desktop | Settings, Add host, Remote SSH: the address and the daemon password. It follows `<alias>` in your `~/.ssh/config`. |
| Paseo on your phone | With Tailscale on, `<tailnet name>:6767` and the same password. |
| Orca | Printed when Orca runs for you: with your laptop on your tailnet, open the URL in the Orca app. |
| Tailscale | Disable key expiry for your VM as the last line says. |

## Worktrees and URLs

Your ADE's worktree hooks run `hades up` when a worktree opens and `hades down` when it goes:

1. `hades up` claims a port, writes `.env.worktree` and `.envrc`, names the databases, adds the worktree's site to your VM's Caddy, then runs the project's setup (`mix setup` creates the databases).
2. You start the app on `$PORT` (direnv loads `.env.worktree`), or declare it as a Paseo service, which gets the port from `hades-port`.
3. `https://<you>-<project>-<name>.<domain>` serves it: Cloudflare, the box's Caddy, your VM's Caddy, `127.0.0.1:<port>`. Log in with your email and the PIN Cloudflare mails you; the session lasts 30 days.
4. `hades down` runs the project's teardown, drops the databases, removes the site and releases the port.

| Fact | Value |
|---|---|
| Label | `<you>-<project>-<name>`, at most 63 characters. `hades up` prints `hades: <label> up at <url>` on stderr. |
| Databases | `<app>_dev<suffix>` and `<app>_test<suffix>`, suffix `_<you>_<name>`. The project's setup creates them; `hades down` drops only those. |
| Variables | `hades env` prints them; the [CLI reference](cli.md#what-up-writes) lists them. |

Day to day: `hades ls` lists your worktrees, `hades env` and `hades port` describe the current
one, and `hades doctor` says what is wrong and how to fix it.

## Your VM

| | |
|---|---|
| sudo | Without a password: install what you need with `apt`. The shared tools stay read-only, also for root. |
| Docker | Docker CE; you are in group `docker`. |
| Postgres | Ubuntu's `postgresql` with PostGIS on `127.0.0.1:5432`, `postgres`/`postgres`: hades' laptop defaults, exported as `DB_*`. Tuned for speed (`fsync` off), so the data is throwaway. `shared_buffers` is a quarter of the VM's memory, at most 8 GB. |
| RabbitMQ | When the box lists it: AMQP `127.0.0.1:5672`, MQTT `127.0.0.1:1883`, management `http://127.0.0.1:15672`, `guest`/`guest`; a vhost per worktree when `hades.toml` asks for one. |
| LocalStack | When the box lists it: S3 at `http://127.0.0.1:14566`, no credentials, state lost on restart. |
| Redis | When the box lists it: `127.0.0.1:6379`. |
| Chrome | Chrome for Testing at `/opt/chrome-for-testing/chrome` (`CHROME_PATH`), for the chrome-devtools MCP entry. |
| Paseo | Listens on `0.0.0.0:6767`; it asks for its password. |

## Tools and versions

- A project's `.tool-versions` or `mise.toml` picks among the installed versions.
- A version the store lacks fails with `Tool not installed for shim: <tool>`. Ask the administrator to add it; every VM has it at once.
- `~/.tool-versions` in your home overrides the box default for you alone.
- Hex, rebar and Mix archives live in your own `~/.mix`.
- Agent CLIs do not self-update; the administrator moves everyone together.

## Your SSH keys

- The box writes your VM's `~/.ssh/authorized_keys` and your keys for the jump account from `https://github.com/<GitHub login>.keys`, plus any key the administrator listed for you, every hour and at every apply.
- A key you add on GitHub works within the hour, and so does a removal, except of your last GitHub key: an empty fetch keeps the keys.
- A new laptop: run `hades join` there.

## Who can see what

- The administrator is root in your VM.
- Other VMs cannot reach yours; your tailnet reaches every port of it.
- Every email on the Access allow-list can open every worktree URL, yours included.
- Every agent your ADE runs is you, with `sudo`: it can use your logins and credentials at any time.

## Intranet and production

- **Intranet:** when the administrator enabled the VPN, intranet hosts are reachable from your VM.
- **Kubernetes:** put a kubeconfig with your own client certificate in `~/.kube/config` in your VM and `chmod 600` it. `kubectl` is in the tool store when the administrator lists it in `tools`. Keep a production kubeconfig on your laptop if you want a prompt.
- **SSH to production:** keys stay on your laptop; the forwarded agent is used while your session is open.

## Troubleshooting

`hades join` says `not authorised yet` when the box or your VM refuses your key. Its second line
says whether your key is on GitHub. The causes:

| Cause | Fix |
|---|---|
| Your key is not on GitHub | Follow the `github:` line printed earlier. `gh is not logged in`: `gh auth login`, then rerun. `gh lacks the admin:public_key scope`: `gh auth refresh -h github.com -s admin:public_key`, then rerun. `gh is logged in as <account>, not <GitHub login>`, or any other error: add the printed key at https://github.com/settings/keys. Without GitHub: send the printed key to the administrator. |
| Your key is on GitHub, the box has not refreshed yet | Wait for the hourly refresh, or ask the administrator to run `sudo hades-keys <name>`. Then rerun. |
| Your entry names another GitHub login | The message names the GitHub login your key is on; the administrator sets that as `github:` in your entry and applies. |
| Your name in the inventory differs from your laptop's `$USER` | Rerun with `--as <name>`. |

Other symptoms:

| Symptom | Fix |
|---|---|
| `join: ssh: Host key verification failed.` | The box's host key changed, or your VM's (kept under `<alias>`). Confirm with the administrator, then `ssh-keygen -R <host>` or `ssh-keygen -R <alias>` and rerun. |
| `join: ssh: ...` naming a host, timeout or refusal | The host name or network is wrong; check `hades join`'s arguments. |
| `hades-onboard needs a terminal` | Run `ssh -t <alias>-via-host hades-onboard <alias>`. |
| `onboarding incomplete: clone <name> failed` | You lack access to that repository on GitHub; ask for it, then rerun `hades join <host>`. |
| `<alias> goes through the box: this laptop does not reach <name> on your tailnet` | Log this laptop in to the tailnet your VM joined, then rerun `hades join <host>`. |
| `ssh <alias>` or Paseo times out | Turn Tailscale on on your laptop. When your VM's tailnet login has lapsed, `ssh -t <alias>-via-host hades-onboard <alias>` logs it in again. |
| `WARN ade-hooks: no paseo.json or orca.yaml` | Run `hades init` in the repository and commit its files. |
| Paseo Desktop cannot connect | Check the address and password from `hades-onboard`; in your VM, `systemctl --user status paseo.service`. The password is also in `~/.config/hades/paseo.env`. |
| `hades: port: no hades manifest for <path>; run hades up` | The worktree has no manifest: run `hades up` in it. |
| `hades: port: <label> has no web port` | The project is not a web project: set `web = true` in `hades.toml`, then `hades up`. |
| The URL answers 502 | Nothing listens on the worktree port: start the app (`hades port` prints the port). |
| The URL redirects to the apex domain | No worktree is live under that label; `hades ls` shows the labels. |
| `label ... taken by ...` | Two of your worktrees have the same project and branch: rename one branch. |
| `hades up` fails on a setup step | The manifest and env files are kept; fix the project and run `hades up` again. |
| `mix` cannot write to `/usr/local/share/mise/...` | Your shell lacks `MIX_HOME`: open a new shell, or `export MIX_HOME=~/.mix MIX_ARCHIVES=~/.mix/archives`. |
| Deleted a worktree without `down` | `hades gc` releases its port, site and databases. |
| Paseo or Orca is down | `systemctl --user restart paseo.service` (or `orca.service`). Orca terminals do not survive a restart. |
| Anything else | `hades doctor`: every FAIL line ends with its fix. |

## Leaving

The administrator sets your entry to `absent`: your home is archived (root-only, on the box) and
your VM is deleted with its databases and Docker volumes. Revoke your own credentials too:
production certificate and keys, GitHub access, and the VM in your tailnet.
