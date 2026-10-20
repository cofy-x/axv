#!/usr/bin/python3
"""Initialize machine-owned task state once; never repair or reset it implicitly."""

import json
import os
from pathlib import Path
import re
import stat
import tempfile


def require(path, owner, mode, directory=False):
    info = path.lstat()
    expected = stat.S_ISDIR if directory else stat.S_ISREG
    if not expected(info.st_mode) or info.st_uid != owner or stat.S_IMODE(info.st_mode) != mode:
        raise ValueError(f"Unsafe task state ownership, type or permissions: {path}")
    if not directory and info.st_nlink != 1:
        raise ValueError(f"Task state must not be hard-linked: {path}")


def publish(path, value, mode, owner):
    descriptor, temporary = tempfile.mkstemp(prefix='.axv-init-', dir=path.parent)
    try:
        with os.fdopen(descriptor, 'w') as output:
            os.fchmod(output.fileno(), mode)
            os.fchown(output.fileno(), owner, owner)
            json.dump(value, output, separators=(',', ':'))
            output.write('\n')
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


if os.geteuid() != 0:
    raise SystemExit('Task state provisioning requires guest root')
parent = Path('/var/lib/axv')
root = parent / 'tasks'
marker = parent / 'tasks.owner'
require(parent, 0, 0o755, directory=True)
require(root, 1000, 0o700, directory=True)
machine = json.loads(Path('/etc/axv/guest.json').read_text())['machineId']
if not isinstance(machine, str) or not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', machine):
    raise ValueError('Invalid AXV task-store machine identity')
owner = {'version': 1, 'machineId': machine}
if marker.exists() or marker.is_symlink():
    require(marker, 0, 0o644)
    if json.loads(marker.read_text()) != owner:
        raise ValueError('Task store belongs to another machine or baseline')
    # A missing established journal is data loss, never first use.
    require(root / 'state.json', 1000, 0o600)
else:
    if list(root.iterdir()):
        raise ValueError('Refusing to adopt nonempty task state without ownership')
    publish(root / 'state.json', {**owner, 'attempts': {}}, 0o600, 1000)
    publish(marker, owner, 0o644, 0)
