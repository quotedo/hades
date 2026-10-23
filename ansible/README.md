# quotedo.hades

An Ansible collection that provisions an Ubuntu/Debian x86_64 host into a multi-user dev
server for agentic development environments. Design: `docs/superpowers/specs/2026-10-23-hades-server-design.md`.

## Using it from a site repository

1. Install the collection and its dependencies:

   ```sh
   ansible-galaxy collection install git+https://github.com/quotedo/hades.git#/ansible/
   ```

2. Copy `inventory.example.yml` to `inventory.yml` and replace every placeholder
   (`hades_domain`, `dev_users`, `dev_services`, `protected`, ...). Never commit secrets.
3. Copy `.env.example` to `.env`, fill in `CLOUDFLARE_TUNNEL_TOKEN` and any hex repository
   keys, and export it (`set -a; . ./.env; set +a`).
4. Wrap the playbooks in your own `site.yml`:

   ```yaml
   - ansible.builtin.import_playbook: quotedo.hades.site
   ```

   Run it with `ansible-playbook site.yml`. Use tags (`--tags host`, `--tags postgres`, ...)
   for partial runs. Read the header of `playbooks/site.yml` for the interactive one-time steps.
5. Check the result with `ansible-playbook verify.yml` (wrap `quotedo.hades.verify` the same way).

## Checks

- `playbooks/preflight.yml` validates the inventory before anything touches a host.
- `scripts/check-no-literals.sh <inventory>` fails if an inventory literal appears in the roles
  or playbooks.
- `playbooks/verify.yml --tags preflight` exercises the preflight against the fixtures in
  `tests/inventories/`.
