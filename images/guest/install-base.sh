#!/bin/sh
set -eu
# Build-time only: both provider formats share this dependency and user contract.
# Refuse a previously initialized machine instead of distributing its tokens,
# host keys or authorized client profiles inside a new image.
for path in /etc/axv/guest.json /etc/ssh/axv /var/lib/axv-ssh; do
  if [ -e "$path" ] || [ -L "$path" ]; then
    echo "Image input contains per-machine AXV state at $path; use a clean base." >&2
    exit 1
  fi
done
if id axv >/dev/null 2>&1 && [ "$(id -u axv)" != 1000 ]; then
  echo 'The image input has an axv account with an incompatible UID.' >&2; exit 1
fi
if getent group axv >/dev/null && [ "$(getent group axv | cut -d: -f3)" != 1000 ]; then
  echo 'The image input has an axv group with an incompatible GID.' >&2; exit 1
fi
export DEBIAN_FRONTEND=noninteractive
case ${AXV_GUEST_PACKAGE_SOURCE:-preserve} in
  preserve) ;;
  official|aliyun)
    # Minimal OCI inputs can lack TLS roots. Bootstrap this build dependency
    # from the input's signed APT sources before selecting an HTTPS mirror.
    if [ ! -s /etc/ssl/certs/ca-certificates.crt ] || ! command -v python3 >/dev/null; then
      apt-get update
      apt-get install -y --no-install-recommends ca-certificates python3
    fi
    sh /usr/local/lib/axv/configure-package-sources apply "$AXV_GUEST_PACKAGE_SOURCE"
    ;;
  *) echo "AXV_GUEST_PACKAGE_SOURCE must be preserve, official or aliyun." >&2; exit 1;;
esac
apt-get update
apt-get install -y --no-install-recommends \
  systemd systemd-sysv udev sudo bash ca-certificates iproute2 \
  util-linux e2fsprogs python3 git curl socat openssh-server
rm -rf /var/lib/apt/lists/*
if getent passwd 1000 >/dev/null && [ "$(getent passwd 1000 | cut -d: -f1)" != axv ]; then
  userdel "$(getent passwd 1000 | cut -d: -f1)"
fi
if getent group 1000 >/dev/null; then
  if [ "$(getent group 1000 | cut -d: -f1)" != axv ]; then groupmod --new-name axv "$(getent group 1000 | cut -d: -f1)"; fi
else
  groupadd --gid 1000 axv
fi
id axv >/dev/null 2>&1 || useradd --uid 1000 --gid axv --create-home --shell /bin/bash axv
passwd --lock root
usermod --password '*NP*' axv
install -d -m 0755 /etc/axv /projects /usr/share/axv /usr/local/lib/axv /var/lib/dbus
truncate -s 0 /etc/machine-id
rm -f /var/lib/dbus/machine-id
ln -s /etc/machine-id /var/lib/dbus/machine-id
rm -f /etc/ssh/ssh_host_*
