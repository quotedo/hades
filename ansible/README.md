# quotedo.hades

The Ansible collection that turns an Ubuntu host into a hades dev server. Everything an operator needs is in [docs/server.md](../docs/server.md); what developers see is in [docs/developer.md](../docs/developer.md).

```sh
ansible-playbook -i inventory.yml playbooks/preflight.yml
ansible-playbook -i inventory.yml playbooks/site.yml --tags host   # then postgres, services, ingress, user, protected, vpn
ansible-playbook -i inventory.yml playbooks/verify.yml --tags host  # per tag
```
