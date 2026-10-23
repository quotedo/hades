# hades

Hades is a Go CLI that sets up and tears down a git worktree for parallel development: a port, a URL, databases, an environment file, secrets and MCP wiring. ADE worktree hooks (Paseo, Orca, worktrunk) call it, and so can you from a shell.

It ships as two binaries: `hades`, and `hades-port`, a small entry point for Paseo's port hook.

## Install

Download the archive for your platform from GitHub Releases and put `hades` and `hades-port` on `PATH`, or:

```sh
go install github.com/quotedo/hades/cmd/...@latest
```

darwin/arm64 and linux/amd64 are first class; linux/arm64 and darwin/amd64 are built but untested. Details in [docs/install.md](docs/install.md).

## Quick start

```sh
cd your-repo
hades init          # writes hades.toml and ADE hook files, updates .gitignore
git add -A && git commit -m "add hades"
```

Create a worktree through your ADE; its hook runs `hades up`. Then:

```sh
hades ls            # worktrees known on this machine
```

`hades down` (run by the ADE's teardown hook) removes what `up` created. `hades doctor` reports what is misconfigured.

## Documentation

- [Install](docs/install.md)
- [Configuration](docs/configuration.md): host config, `hades.toml`, what `up` writes
- [ADE integration](docs/ades.md): Paseo, Orca, worktrunk hooks
- Design: [hades CLI](docs/superpowers/specs/2026-10-23-hades-cli-design.md) and [server](docs/superpowers/specs/2026-10-23-hades-server-design.md)

## Safety rules

- Hades never drops a database, releases a port, removes a site or deletes a vhost that the manifest it is acting on does not list.
- It never acts on another user's manifest, except `gc` as root on vanished worktrees.
- It never modifies tracked files other than the MCP configs, and never commits, pushes or changes branches.
- `up` rolls back what it claimed in the current run on failure, and nothing claimed earlier.
- `down` is best effort and exits 0 unless `--strict`; each skipped step is printed.
- Host credentials reach project commands only as the documented environment variables and are written nowhere but the two env files in the worktree.
