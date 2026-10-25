#!/usr/bin/env bash
# Usage: check-no-literals.sh <inventory-file>
# Fails when a literal from the inventory (any string value, any integer >= 1024)
# appears in ansible/roles or ansible/playbooks.
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <inventory-file>" >&2
  exit 2
fi

root="$(cd "$(dirname "$0")/.." && pwd)"
inv_json="$(ansible-inventory -i "$1" --list)"

literals="$(printf '%s' "$inv_json" | python3 -c '
import json, sys

SKIP = {"present", "absent", "true", "false", "local"}
# Role vocabulary (ADE and service names) is part of the collection interface.
VOCAB = {"paseo", "orca", "rabbitmq", "localstack"}
out = set()

def walk(v):
    if isinstance(v, bool):
        return
    if isinstance(v, int):
        if v >= 1024:
            out.add(str(v))
    elif isinstance(v, str):
        if len(v) >= 4 and v not in SKIP and v not in VOCAB and "\n" not in v:
            out.add(v)
    elif isinstance(v, dict):
        # Tool version pins are shared by design: host defaults pin the same versions.
        for k, x in v.items():
            if k != "tools":
                walk(x)
    elif isinstance(v, list):
        for x in v:
            walk(x)

walk(json.load(sys.stdin).get("_meta", {}).get("hostvars", {}))
for item in sorted(out):
    print(item)
')"

status=0
while IFS= read -r lit; do
  [ -n "$lit" ] || continue
  hits="$(grep -rnF -- "$lit" "$root/roles" "$root/playbooks" || true)"
  if [ -n "$hits" ]; then
    while IFS= read -r hit; do
      file_line="${hit%%:*}"
      rest="${hit#*:}"
      echo "${file_line}:${rest%%:*}: ${lit}"
    done <<< "$hits"
    status=1
  fi
done <<< "$literals"

exit "$status"
