# Hades

A dev server for agentic development, and the CLI that runs on it.

Several developers share one Linux box. Each has their own account, their own agentic development
environment (ADE: [Paseo](https://getpaseo.com) or [Orca](https://github.com/stablyai/orca)) running as
a daemon, and their own git worktrees. Every web worktree an agent opens gets its own port, its own
database names, an environment file and a public URL behind a login. The CLI, `hades`, is what the
ADE's worktree hooks call to make that happen; it also works alone on a laptop.

A worktree's *label* is `<project>-<branch>` (for Orca, the workspace name); its URL is
`https://<label>.<domain>`.

## How it works

```mermaid
flowchart LR
  laptop["Developer laptop<br/>Paseo or Orca app, browser"]
  cf["Cloudflare<br/>*.DOMAIN wildcard DNS, Access login"]
  laptop -- "SSH (Paseo) or Tailscale (Orca)" --> ade
  laptop -- "https://LABEL.DOMAIN" --> cf
  cfd -- "outbound tunnel" --> cf
  subgraph server["Dev server: one Linux account per developer"]
    ade["ADE daemon<br/>Paseo or Orca, per user"]
    wt["git worktree<br/>.env.worktree, .envrc"]
    cli["hades up / hades down<br/>run by the worktree hooks"]
    reg[("Registry /srv/hades<br/>port claims, site files, manifests")]
    caddy["Caddy<br/>LABEL.DOMAIN to 127.0.0.1:PORT"]
    cfd["cloudflared"]
    app["Your app<br/>started by you, listens on PORT"]
    pg[("Dev Postgres :5433<br/>databases named per worktree")]
    ade -- "opens" --> wt
    ade -- "hook" --> cli
    cli -- "claims PORT, writes site" --> reg
    cli -- "writes env" --> wt
    cli -- "sudo hades caddy-reload" --> caddy
    cfd --> caddy
    caddy --> app
    app --> pg
  end
```

| Piece | What it does |
|---|---|
| `hades` CLI | On `up`: claims a port, computes the worktree's database names and variables, writes `.env.worktree` and `.envrc`, publishes a Caddy site, points the repo's MCP configs at this worktree, runs the project's setup (which creates the databases). On `down`: the reverse. Called by ADE hooks or by hand. |
| Ansible collection `quotedo.hades` | Turns an Ubuntu box into the server: accounts, ADE daemons, a shared tool store, dev Postgres, RabbitMQ and LocalStack, Caddy, Cloudflare tunnel and Access, loopback isolation, backups, optional intranet VPN. |

## Quick start

### Developer on an existing server

1. Give the administrator your GitHub login and the email you will log in with.
2. Install the CLI on your laptop (see [below](#cli-alone-on-a-laptop)), then run `hades join <box> --as <login>`. It puts your SSH key on GitHub, writes the box into `~/.ssh/config` and tests the login. Until the box has your key it says `not authorised yet`: run the same command again once the administrator has applied. Orca users also accept the Tailscale share.
3. Log in once with `ssh <box>`. The welcome shows your Paseo password and port; run `hades-onboard` (GitHub and agent logins, your repositories, `hades doctor`).
4. Connect your ADE: Paseo Desktop "Remote SSH", host `<box>`, user `<login>`; or Orca's pairing URL from `journalctl --user -u orca.service` on the box. Open a worktree: the project's hooks (`paseo.json` or `orca.yaml`, written by `hades init`) run `hades up`.
5. Start the app on `$PORT` (direnv loads `.env.worktree`; a declared Paseo service gets the port from `hades-port`). It is then at `https://<label>.<domain>` after you log in with your email.

Details: [Developer guide](docs/developer.md).

### Operator setting up a server

1. Install `ansible-core` 2.17+ and the collection's requirements, and log `gh` in with read access to this repository (the host's CLI is a release downloaded with it). Copy `ansible/inventory.example.yml` to a private inventory: domain, tool versions, users (each with `github` and `email`), and the `protected` block (services already on the box that dev accounts must not starve or reach).
2. Export the secrets from a private `.env`: Cloudflare account id and API token, `TS_AUTHKEY` when the box runs Tailscale, the VPN config if used.
3. Apply and verify:

   ```sh
   ansible-playbook -i inventory.yml playbooks/preflight.yml
   ansible-playbook -i inventory.yml playbooks/site.yml
   ansible-playbook -i inventory.yml playbooks/verify.yml
   ```

4. Tell each developer the box's host name. Nobody types or hands over a password: the administrator's sudo password is generated and printed once in the apply output, and each developer reads their Paseo password at their first login.

Details: [Server reference](docs/server.md).

### CLI alone on a laptop

Needs Postgres on `127.0.0.1:5432` (`postgres`/`postgres`, or `HADES_DB_*` overrides) and the project config snippet that `hades init` prints. The repository is private: install from a release with your own `gh` login (other ways in the [CLI reference](docs/cli.md#install)).

```sh
gh release download v<version> --repo quotedo/hades --pattern '*darwin_arm64*' --pattern checksums.txt   # your <os>_<arch>
sha256sum --check --ignore-missing checksums.txt
tar -xzf hades_<version>_darwin_arm64.tar.gz hades hades-port && install -m 0755 hades hades-port ~/.local/bin/
cd your-repo && hades init
git add hades.toml paseo.json orca.yaml .gitignore && git commit -m "add hades"
```

Open a worktree from your ADE: `hades up` claims a port and the app, once started, is at `http://localhost:<port>`.
Details: [CLI reference](docs/cli.md).

## Documentation

- [Developer guide](docs/developer.md): using a hades server day to day.
- [Server reference](docs/server.md): inventory, secrets, apply, verify, onboarding, offboarding, operations.
- [CLI reference](docs/cli.md): commands, `hades.toml`, host config, ADE hooks, environment variables, safety rules.
