#!/bin/sh
set -eu
# Drive order is part of the AXV Firecracker image contract: root, project, seed.
for attempt in $(seq 1 30); do
  if [ -b /dev/vdb ] && [ -b /dev/vdc ]; then break; fi
  sleep 1
done
mkdir -p /projects /run/axv-seed /etc/axv
mountpoint -q /projects || mount -t ext4 -o nosuid,nodev /dev/vdb /projects
mountpoint -q /run/axv-seed || mount -t ext4 -o ro,nosuid,nodev,noexec /dev/vdc /run/axv-seed
chown axv:axv /projects
install -o axv -g axv -m 0600 /run/axv-seed/guest.json /etc/axv/guest.json
python3 - <<'PY'
import ipaddress
import json
import os
from pathlib import Path
config = json.loads(Path('/etc/axv/guest.json').read_text())
resolver = ipaddress.IPv4Address(config['dns'])
if not resolver.is_global:
    raise ValueError('Guest DNS must be a public IPv4 address')
path = Path('/etc/resolv.conf')
if path.is_symlink():
    path.unlink()
path.write_text(f'nameserver {resolver}\n')
os.chmod(path, 0o644)
PY
machine_id=$(python3 -c 'import json; print(json.load(open("/etc/axv/guest.json"))["machineId"])')
/usr/local/lib/axv/prepare-ssh "$machine_id"
