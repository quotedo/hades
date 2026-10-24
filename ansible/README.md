# quotedo.hades

An Ansible collection that turns an Ubuntu or Debian x86_64 host into a multi-user dev server for hades. Inventory, secrets, interactive steps, offboarding and the verify tags are in [docs/server.md](../docs/server.md).

```sh
ansible-playbook -i inventory.yml playbooks/preflight.yml
ansible-playbook -i inventory.yml playbooks/site.yml --tags host --skip-tags cloudflared   # then postgres, services, ingress, user, protected
ansible-playbook -i inventory.yml playbooks/verify.yml --skip-tags cloudflared
```

Drop `--skip-tags cloudflared` once `CLOUDFLARE_TUNNEL_TOKEN` is exported.
