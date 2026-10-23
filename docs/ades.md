# ADE integration

`hades init` writes the hook files below into a repository. It never overwrites an existing `hades.toml`, `orca.yaml` or `paseo.json` without `--force`; when a hook file exists without hades in it, it prints the lines to add. Pick ADEs with `hades init --ade paseo,orca,wt`.

The hooks are one-liners that call a binary on `PATH`, because both Orca and Paseo read the hook file from the repository and an older branch may lack a script.

## Paseo

`paseo.json`:

```json
{
  "worktree": {
    "setup": [
      "hades up"
    ],
    "teardown": [
      "hades down"
    ],
    "servicePorts": {
      "portScript": "hades-port"
    }
  }
}
```

Paseo does not set `PORT` in any hook environment. When a declared service (for example `mix phx.server`) is started, Paseo runs `portScript` without a shell and with four positional arguments. `hades-port` ignores them, finds the worktree through `PASEO_WORKTREE_PATH` and prints the port that `hades up` claimed, so the service runs on the port hades published. It is read-only and fails if `up` has not run yet. The service declaration itself is the project's choice and is not written by `init`.

Paseo aborts worktree removal when a teardown command exits non-zero. `hades down` is best effort and exits 0 unless `--strict`.

## Orca

`orca.yaml`:

```yaml
scripts:
  setup: hades up
  archive: hades down
setupAgentStartupPolicy: wait-for-setup
```

Orca aborts archiving when the archive hook fails unless you pass `--allow-failed-archive-hook`.

## worktrunk

Two top-level keys in `.config/wt.toml` (not inside a table):

```toml
pre-start = "hades up --name {{ branch }}"
pre-remove = "hades down --name {{ branch }}"
```

`hades init --ade wt` prints this snippet and does not edit an existing `wt.toml`.

## Shell

`hades up` inside any worktree, and `hades down` to remove it.

## Identity and names

Hades finds the worktree and name from flags, then Paseo's `PASEO_*` variables, then Orca's `ORCA_*`, then git (`--ade` restricts the choice). A worktree is identified by its path: the name, label and hostname are fixed at the first `up` and recorded in the manifest, so `down` and later `up` runs reuse them even if the branch is renamed or another caller computes a different name. To get a new label, run `down` and then `up`.

## PATH in hooks

Paseo and Orca run hooks in `bash` without profile files, so `PATH` is bare and tools managed by asdf or installed in the home directory are not found. For every command it runs for the project (setup and teardown steps), hades prepends these directories, when they exist, in this order:

1. `~/.asdf/shims`
2. `~/.local/bin`
3. the host config's `tool_paths`

The rest of `PATH` is kept. On a laptop there is no host config, so only the first two apply.
