#!/bin/sh
set -eu
# Explicit configuration only. No apt update, installation or system upgrade.
mode=${1:-}
source_name=${2:-}
case "$source_name" in official|aliyun) ;; *) echo 'Select official or aliyun explicitly.' >&2; exit 1;; esac
render() {
  distro=$1 version=$2 arch=$3
  if [ "$distro" != ubuntu ] || [ "$version" != 24.04 ]; then
    echo 'Package sources support only Ubuntu 24.04; existing configuration was preserved.' >&2; return 1
  fi
  case "$arch" in
    arm64) archive=https://ports.ubuntu.com/ubuntu-ports; security=$archive;;
    amd64) archive=https://archive.ubuntu.com/ubuntu; security=https://security.ubuntu.com/ubuntu;;
    *) echo 'Package sources support only arm64 and amd64.' >&2; return 1;;
  esac
  if [ "$source_name" = aliyun ]; then
    case "$arch" in arm64) archive=https://mirrors.aliyun.com/ubuntu-ports;; amd64) archive=https://mirrors.aliyun.com/ubuntu;; esac
    security=$archive
  fi
  cat <<SOURCES
Types: deb
URIs: $archive
Suites: noble noble-updates noble-backports
Components: main restricted universe multiverse
Architectures: $arch
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: $security
Suites: noble-security
Components: main restricted universe multiverse
Architectures: $arch
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
SOURCES
}
if [ "$mode" = print ]; then render "${3:-}" "${4:-}" "${5:-}"; exit; fi
if [ "$mode" != apply ]; then echo 'Use print or apply.' >&2; exit 1; fi
# Optional offline root/architecture are used by image tooling and fixture tests.
root=${3:-/}
arch=${4:-$(dpkg --print-architecture)}
content=$(render ubuntu 24.04 "$arch")
command -v python3 >/dev/null || { echo 'Package source configuration requires preinstalled Python 3.' >&2; exit 1; }
python3 - "$source_name" "$root" "$arch" "$content" <<'PYTHON'
import fcntl
import os
from pathlib import Path
import shlex
import stat
import sys
import tempfile

source_name, root_name, arch, content = sys.argv[1:]
content = (content + "\n").encode()
cloud_content = b"# Managed by AXV package-source maintenance.\napt:\n  preserve_sources_list: true\n"
if not os.path.isabs(root_name):
    sys.exit('Target root must be absolute.')
root = Path(os.path.abspath(root_name))


def directory(relative, create=False):
    current = root
    for component in ("", *Path(relative).parts):
        if component:
            current /= component
        try:
            info = current.lstat()
        except FileNotFoundError:
            if not create:
                raise ValueError(f'Missing package configuration directory: {current}')
            current.mkdir(mode=0o755)
            info = current.lstat()
        if not stat.S_ISDIR(info.st_mode):
            raise ValueError(f'Refusing symlinked or non-directory package configuration: {current}')
    return current


def regular(path, required=False):
    try:
        info = path.lstat()
    except FileNotFoundError:
        if required:
            raise ValueError(f'Missing package configuration file: {path}')
        return False
    if not stat.S_ISREG(info.st_mode):
        raise ValueError(f'Unsupported package configuration file type: {path}')
    return True


def sync_directory(path):
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def replace(path, data, mode=0o644):
    fd, name = tempfile.mkstemp(prefix='.axv-sources-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as output:
            output.write(data)
            output.flush()
            os.fchmod(output.fileno(), mode)
            os.fsync(output.fileno())
        os.replace(name, path)
        sync_directory(path.parent)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def apply():
    directory('etc/apt/sources.list.d')
    release = root / 'etc/os-release'
    # Ubuntu normally links this file into /usr/lib; read its text, never execute it.
    fields = {}
    for line in release.read_text().splitlines():
        key, sep, value = line.partition('=')
        if sep and key in ('ID', 'VERSION_ID'):
            parsed = shlex.split(value, comments=True)
            fields[key] = parsed[0] if len(parsed) == 1 else ''
    if fields != {'ID': 'ubuntu', 'VERSION_ID': '24.04'}:
        raise ValueError('Package sources support only Ubuntu 24.04; existing configuration was preserved.')
    keyring = root / 'usr/share/keyrings/ubuntu-archive-keyring.gpg'
    if not keyring.is_file():
        raise ValueError('Ubuntu archive signing keyring is missing.')
    trust = root / 'etc/ssl/certs/ca-certificates.crt'
    if not trust.is_file() or trust.stat().st_size == 0:
        raise ValueError('HTTPS trust roots are missing; explicitly install ca-certificates before changing package sources.')
    sources = root / 'etc/apt/sources.list.d/ubuntu.sources'
    legacy = root / 'etc/apt/sources.list'
    cloud_dir = root / 'etc/cloud/cloud.cfg.d'
    cloud = cloud_dir / '99-axv-package-sources.cfg'
    for path in (legacy, sources, cloud):
        regular(path)
    for pattern in ('*.list', '*.sources'):
        for path in sources.parent.glob(pattern):
            if path != sources:
                raise ValueError('Custom package source layout: configure it manually; no files were changed.')
    # Verify existing paths before taking the lock or creating an owned backup.
    for relative in ('var', 'var/lib', 'var/lib/axv', 'etc/cloud', 'etc/cloud/cloud.cfg.d'):
        path = root / relative
        if path.exists() or path.is_symlink():
            directory(relative)
    if cloud.exists() and cloud.read_bytes() != cloud_content:
        raise ValueError('The AXV cloud-init source policy was edited; preserve or reconcile it manually.')
    state = directory('var/lib/axv', create=True)
    lock_path = state / 'package-sources.lock'
    regular(lock_path)
    lock = os.open(lock_path, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        # Kernel-held locks disappear on cancellation or process death. The
        # persistent empty file is harmless and is never treated as a stale lock.
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError('Package source configuration is already in progress; retry when it finishes.')
        # Revalidate under the lock: two explicit operations may have raced.
        for path in (legacy, sources, cloud):
            regular(path)
        if cloud.exists() and cloud.read_bytes() != cloud_content:
            raise ValueError('The AXV cloud-init source policy was edited; preserve or reconcile it manually.')
        if sources.exists() and sources.read_bytes() == content and not legacy.exists() and cloud.exists():
            print(f'{source_name} is already configured for Ubuntu 24.04 {arch}. No files or packages were changed.')
            return
        directory('etc/cloud/cloud.cfg.d', create=True)
        original = {path: (path.read_bytes(), stat.S_IMODE(path.stat().st_mode)) if path.exists() else None for path in (cloud, sources, legacy)}
        backup = Path(tempfile.mkdtemp(prefix='package-sources-', dir=state))
        for path, snapshot in original.items():
            if snapshot is not None:
                replace(backup / path.name, *snapshot)
        sync_directory(state)
        try:
            # Preserve the new sources across cloud-init boots before publishing
            # them. A crash leaves the old files in the durable backup; replay
            # finishes any remaining replacement without a stale lock.
            replace(cloud, cloud_content)
            replace(sources, content)
            if legacy.exists():
                legacy.unlink()
                sync_directory(legacy.parent)
        except BaseException:
            for path, snapshot in original.items():
                if snapshot is not None:
                    replace(path, *snapshot)
                elif path.exists():
                    path.unlink()
                    sync_directory(path.parent)
            raise
        print(f'Configured {source_name} for Ubuntu 24.04 {arch}. Backup: {backup}. No packages were updated.')
    finally:
        os.close(lock)


try:
    apply()
except (OSError, ValueError) as error:
    sys.exit(str(error))
PYTHON
