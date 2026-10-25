# Hades

A shared Linux dev server for agentic development, and the CLI that runs on it.

Each developer has their own account, their own agentic development environment (ADE:
[Paseo](https://getpaseo.com) or [Orca](https://github.com/stablyai/orca)) running as a daemon, and
their own git worktrees. Every web worktree gets its own port, database names, environment file and
a URL behind a login. The CLI, `hades`, is what the ADE's worktree hooks call; it also works alone on
a laptop. The Ansible collection `quotedo.hades` builds and runs the server.

A worktree's URL is `https://<label>.<domain>`; the [CLI reference](docs/cli.md#commands) defines
the label.

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

## Quick start: developer

1. Send the administrator your GitHub login and the email you will log in with.
2. Log `gh` in on your laptop and [install the CLI](docs/cli.md#install).
3. Run `hades join <host>`. It sets up SSH, then runs the first-login steps on the box.
4. Add the box in Paseo Desktop with the address and password printed at the end.

Full journey: [Developer guide](docs/developer.md).

## Quick start: administrator

1. Copy `ansible/inventory.example.yml` to `ansible/inventory.yml` and fill it in.
2. Copy `ansible/.env.example` to `ansible/.env`, fill in the secrets and export them.
3. Apply and verify from `ansible/`:

   ```sh
   ansible-playbook playbooks/site.yml
   ansible-playbook playbooks/verify.yml
   ```

4. Give each developer read access to the hades repository and send them the host name.

Full reference: [Server reference](docs/server.md).
