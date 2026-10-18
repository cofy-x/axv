#!/bin/sh
set -eu
machine_id=${1:?Supply the AXV machine identity}
mode=${2:-provision}
case "$machine_id" in ''|*[!A-Za-z0-9_-]*) echo 'Invalid AXV machine identity' >&2; exit 1;; esac
case "$mode" in provision|--repair-account) ;; *) echo 'Invalid SSH provisioning mode' >&2; exit 1;; esac
[ "$(id -u)" -eq 0 ] || { echo 'SSH provisioning requires guest root' >&2; exit 1; }
[ "$(id -u axv)" -eq 1000 ] || { echo 'The AXV guest user must have UID 1000' >&2; exit 1; }

repair_account() {
  # Lima's cloud-init may lock the account again on boot. Repair only the
  # impossible password originally installed by AXV, preserving other values.
  password=$(getent shadow axv)
  password=${password#*:}
  password=${password%%:*}
  case "$password" in '!*NP*') usermod --password '*NP*' axv;; esac
}

# This root-owned directory is deliberately separate from axv-owned /etc/axv.
ssh_dir=/etc/ssh/axv
[ ! -L "$ssh_dir" ] || { echo 'The AXV SSH directory is a symlink' >&2; exit 1; }
if [ "$mode" = --repair-account ]; then
  [ -d "$ssh_dir" ] && [ -f "$ssh_dir/initialized" ] && [ ! -L "$ssh_dir/initialized" ] && [ "$(cat "$ssh_dir/initialized")" = 1 ] || { echo 'AXV SSH has not been initialized' >&2; exit 1; }
fi
if [ ! -d "$ssh_dir" ]; then install -d -o root -g root -m 0755 "$ssh_dir"; fi
[ "$(stat -c %u "$ssh_dir")" -eq 0 ] || { echo 'The AXV SSH directory must be root-owned' >&2; exit 1; }
[ "$(stat -c %a "$ssh_dir")" = 755 ] || { echo 'The AXV SSH directory must have mode 0755' >&2; exit 1; }
if [ -f "$ssh_dir/owner" ] && [ ! -L "$ssh_dir/owner" ]; then
  [ "$(cat "$ssh_dir/owner")" = "$machine_id" ] || { echo 'SSH host keys belong to a different machine' >&2; exit 1; }
else
  [ "$mode" != --repair-account ] || { echo 'AXV SSH ownership is missing' >&2; exit 1; }
  [ -z "$(ls -A "$ssh_dir")" ] || { echo 'Refusing to adopt existing SSH files' >&2; exit 1; }
  printf '%s\n' "$machine_id" > "$ssh_dir/owner"
  chmod 0600 "$ssh_dir/owner"
fi

if [ "$mode" = --repair-account ]; then
  repair_account
  exit 0
fi
command -v ssh-keygen >/dev/null
[ -x /usr/sbin/sshd ] || { echo 'The guest image needs openssh-server' >&2; exit 1; }

key=$ssh_dir/ssh_host_ed25519_key
if [ ! -e "$key" ] && [ ! -L "$key" ]; then
  [ ! -e "$ssh_dir/initialized" ] || { echo 'The existing SSH host private key is missing; refusing to replace its identity' >&2; exit 1; }
  ssh-keygen -q -t ed25519 -N '' -C 'AXV guest host' -f "$key"
fi
[ -f "$key" ] && [ ! -L "$key" ] || { echo 'Invalid SSH host private key' >&2; exit 1; }
[ "$(stat -c %u "$key")" -eq 0 ] || { echo 'The SSH host private key must be root-owned' >&2; exit 1; }
chmod 0600 "$key"
public_key=$(ssh-keygen -y -f "$key")
# OpenSSH may include the comment stored in its private-key format. Identity
# comparisons use only the key type and bytes, just like the public-key file.
public_key=$(printf '%s\n' "$public_key" | awk '{print $1 " " $2}')
case "$public_key" in 'ssh-ed25519 '*) ;; *) echo 'Expected an Ed25519 SSH host key' >&2; exit 1;; esac
if [ -e "$key.pub" ] || [ -L "$key.pub" ]; then
  [ -f "$key.pub" ] && [ ! -L "$key.pub" ] || { echo 'Invalid SSH host public key' >&2; exit 1; }
  existing_key=$(awk '{print $1 " " $2}' "$key.pub")
  [ "$existing_key" = "$public_key" ] || { echo 'The SSH host public key does not match its private key' >&2; exit 1; }
fi
printf '%s\n' "$public_key" > "$key.pub"
chown root:root "$key.pub"
chmod 0644 "$key.pub"

clients=/var/lib/axv-ssh/clients
[ ! -L /var/lib/axv-ssh ] && [ ! -L "$clients" ] || { echo 'The SSH client directory is a symlink' >&2; exit 1; }
if [ ! -d /var/lib/axv-ssh ]; then install -d -o root -g root -m 0755 /var/lib/axv-ssh; fi
[ "$(stat -c %u /var/lib/axv-ssh)" -eq 0 ] && [ "$(stat -c %a /var/lib/axv-ssh)" = 755 ] || { echo 'The SSH client parent must be root-owned with mode 0755' >&2; exit 1; }
if [ ! -d "$clients" ]; then install -d -o axv -g axv -m 0700 "$clients"; fi
[ "$(stat -c %u "$clients")" -eq 1000 ] && [ "$(stat -c %a "$clients")" = 700 ] || { echo 'The SSH client directory must be private and axv-owned' >&2; exit 1; }

if [ ! -e "$ssh_dir/initialized" ]; then
  # A leading ! locks public-key login with UsePAM=no too. *NP* is an
  # impossible password value without that Linux account-lock marker.
  usermod --password '*NP*' axv
else
  repair_account
fi
cat > "$ssh_dir/sshd_config" <<'AXV_SSH_CONFIG'
ListenAddress 127.0.0.1:22222
HostKey /etc/ssh/axv/ssh_host_ed25519_key
PidFile /run/axv-ssh/sshd.pid
AuthorizedKeysFile /var/lib/axv-ssh/clients/authorized_keys
AllowUsers axv
PermitRootLogin no
AuthenticationMethods publickey
PubkeyAuthentication yes
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitEmptyPasswords no
UsePAM no
StrictModes yes
AllowTcpForwarding yes
GatewayPorts no
AllowAgentForwarding no
X11Forwarding no
PermitTunnel no
PermitUserEnvironment no
Subsystem sftp internal-sftp
LoginGraceTime 30
MaxAuthTries 3
MaxSessions 16
MaxStartups 10:30:32
PrintMotd no
UseDNS no
AXV_SSH_CONFIG
chmod 0644 "$ssh_dir/sshd_config"
install -d -o root -g root -m 0755 /run/sshd
/usr/sbin/sshd -t -f "$ssh_dir/sshd_config"
printf '1\n' > "$ssh_dir/initialized"
chmod 0600 "$ssh_dir/initialized"
