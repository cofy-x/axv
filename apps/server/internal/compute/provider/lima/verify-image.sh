#!/bin/sh
set -eu
# This check never downloads, installs or changes guest files.
fail() { echo "Incompatible AXV image: $*. Rebuild/import a compatible Lima image bundle." >&2; exit 1; }
for tool in python3 socat sha256sum systemctl; do
  command -v "$tool" >/dev/null || fail "missing $tool"
done
[ -x /usr/local/bin/axv-guest ] || fail 'missing axv-guest'
[ -f /etc/systemd/system/axv-guest.service ] || fail 'missing guest unit'
python3 - "$1" "$2" <<'PY'
import hashlib
import json
import sys
from pathlib import Path
try:
    manifest = json.loads(Path('/usr/share/axv/image.json').read_text())
    expected = json.loads(sys.argv[2])
    guest = manifest.get('guest', {})
    if manifest.get('version') != 1 or manifest.get('provider') != 'lima' or manifest.get('arch') != sys.argv[1]:
        raise ValueError('image identity does not match selected image')
    if guest.get('imageVersion') != expected['imageVersion'] or guest.get('protocolVersion') != expected['protocolVersion']:
        raise ValueError('guest protocol is incompatible')
    if not set(expected['capabilities']).issubset(guest.get('capabilities', [])):
        raise ValueError('declared guest capabilities are missing')
    if 'ssh' in expected['capabilities']:
        for name in ['/usr/sbin/sshd', '/usr/local/lib/axv/prepare-ssh', '/etc/systemd/system/axv-ssh.service']:
            if not Path(name).is_file():
                raise ValueError(f'missing SSH dependency {name}')
    digest = guest.get('binarySHA256')
    expected_digest = expected.get('binarySHA256')
    if expected_digest and (not digest or digest.lower() != expected_digest.lower()):
        raise ValueError('installed guest executable identity does not match selected image')
    if digest:
        with open('/usr/local/bin/axv-guest', 'rb') as executable:
            actual = hashlib.file_digest(executable, 'sha256').hexdigest()
        if actual != digest.lower():
            raise ValueError('preinstalled guest executable changed')
except (OSError, ValueError, TypeError, KeyError, AttributeError) as error:
    sys.exit(f'Incompatible AXV image: {error}. Rebuild/import a compatible Lima image bundle.')
PY
