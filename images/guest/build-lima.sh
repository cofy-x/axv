#!/usr/bin/env bash
set -euo pipefail
package_source=${AXV_GUEST_PACKAGE_SOURCE:-preserve}
case "$package_source" in preserve|official|aliyun) ;; *) echo "Select preserve, official or aliyun for AXV_GUEST_PACKAGE_SOURCE." >&2; exit 1;; esac
# Run explicitly on a dedicated native Linux image builder as root so exported
# filesystem ownership is preserved without relying on nested libguestfs VMs.
if [[ $(uname -s) != Linux || $EUID != 0 ]]; then
  echo 'Use a native Linux image builder as root to build the Lima guest image.' >&2
  exit 1
fi
if [[ $# != 2 || $1 != /* || $2 != /* || -e $2 || ! ${CLOUD_IMAGE_SHA256:-} =~ ^[a-f0-9]{64}$ ]]; then
  echo 'Usage: CLOUD_IMAGE_SHA256=<reviewed digest> bash images/guest/build-lima.sh /absolute/base-cloud.img /absolute/new-bundle' >&2
  exit 1
fi
for tool in qemu-img losetup blkid growpart e2fsck resize2fs mount umount chroot go node sha256sum; do
  if ! command -v "$tool" >/dev/null; then echo "Missing required image-build tool: $tool" >&2; exit 1; fi
done
base_image=$1
printf '%s  %s\n' "$CLOUD_IMAGE_SHA256" "$base_image" | sha256sum --check --status
case $(uname -m) in x86_64) guest_arch=amd64;; aarch64) guest_arch=arm64;; *) echo 'Only native amd64 and arm64 builds are supported.' >&2; exit 1;; esac
image_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(cd -- "$image_dir/../.." && pwd)
mkdir -p "$repo_dir/.data/test"
chmod 0700 "$repo_dir/.data/test"
build_dir=$(mktemp -d "$repo_dir/.data/test/image-build-XXXXXX")
loop_dev=''
mnt="$build_dir/mnt"
mounted=false
cleanup() {
  set +e
  if [[ $mounted == true ]]; then
    umount --recursive "$mnt" || umount --recursive --lazy "$mnt"
  fi
  if [[ -n $loop_dev ]]; then
    losetup -d "$loop_dev"
  fi
  if ! mountpoint -q "$mnt"; then
    rm -rf -- "$build_dir"
  fi
}
trap cleanup EXIT
export TMPDIR="$build_dir" GOTMPDIR="$build_dir"
cd -- "$repo_dir"
CGO_ENABLED=0 GOOS=linux GOARCH="$guest_arch" go build -trimpath -o "$build_dir/axv-guest" ./apps/server/cmd/guest
go run ./apps/server/cmd/image-manifest --provider lima --arch "$guest_arch" --guest "$build_dir/axv-guest" --output "$build_dir/image.json"
mkdir -- "$build_dir/distribution"
node scripts/guest-release-notices.mjs --guest "$build_dir/axv-guest" --manifest "$build_dir/image.json" --output "$build_dir/distribution/axv-guest"
qemu-img convert -O raw "$base_image" "$build_dir/root.raw"
# Expand the temporary raw disk by 2 GiB so offline security upgrades fit cleanly;
# unused blocks are trimmed before converting to compressed qcow2.
qemu-img resize -f raw "$build_dir/root.raw" +2G
cp "$image_dir/axv-guest-lima.service" "$build_dir/axv-guest.service"
cp apps/server/internal/guest/internal/access/provision.sh "$build_dir/prepare-ssh"

loop_dev=$(losetup --find --show --partscan "$build_dir/root.raw")
if command -v udevadm >/dev/null 2>&1; then
  udevadm settle || true
fi
root_part=''
boot_part=''
uefi_part=''
for part in "${loop_dev}"p*; do
  [[ -b $part ]] || continue
  label=$(blkid -s LABEL -o value "$part" 2>/dev/null || true)
  case "$label" in
    cloudimg-rootfs) root_part=$part ;;
    BOOT) boot_part=$part ;;
    UEFI) uefi_part=$part ;;
  esac
done
if [[ -z $root_part ]]; then
  for part in "${loop_dev}"p*; do
    [[ -b $part ]] || continue
    if [[ $(blkid -s TYPE -o value "$part" 2>/dev/null || true) == "ext4" ]]; then
      root_part=$part
      break
    fi
  done
fi
if [[ -z $root_part ]]; then
  echo "Could not locate ext4 root partition in $base_image ($loop_dev)." >&2
  exit 1
fi
part_num=${root_part#"${loop_dev}"p}
growpart "$loop_dev" "$part_num"
e2fsck -f -p "$root_part" || [[ $? -eq 1 ]]
resize2fs "$root_part"

mkdir -- "$mnt"
mount "$root_part" "$mnt"
mounted=true
if [[ -n $boot_part ]]; then
  mkdir -p "$mnt/boot"
  mount "$boot_part" "$mnt/boot"
fi
if [[ -n $uefi_part ]]; then
  mkdir -p "$mnt/boot/efi"
  mount "$uefi_part" "$mnt/boot/efi"
fi
mount --bind /dev "$mnt/dev"
mount --bind /dev/pts "$mnt/dev/pts"
mount -t proc proc "$mnt/proc"
mount -t sysfs sysfs "$mnt/sys"

resolv_mode=none
resolv_link=''
if [[ -L "$mnt/etc/resolv.conf" ]]; then
  resolv_mode=symlink
  resolv_link=$(readlink "$mnt/etc/resolv.conf")
  rm -f "$mnt/etc/resolv.conf"
elif [[ -f "$mnt/etc/resolv.conf" ]]; then
  resolv_mode=file
  cp -a "$mnt/etc/resolv.conf" "$build_dir/resolv.conf.orig"
  rm -f "$mnt/etc/resolv.conf"
fi
cp -L /etc/resolv.conf "$mnt/etc/resolv.conf"

cat > "$mnt/usr/sbin/policy-rc.d" <<'EOF'
#!/bin/sh
exit 101
EOF
chmod 0755 "$mnt/usr/sbin/policy-rc.d"

install -d -m 0755 "$mnt/usr/local/lib/axv"
install -m 0755 "$repo_dir/apps/server/internal/compute/ubuntusources/configure.sh" "$mnt/usr/local/lib/axv/configure-package-sources"
install -m 0755 "$image_dir/install-base.sh" "$mnt/usr/local/lib/axv/install-base"
install -m 0755 "$image_dir/package-inventory.sh" "$mnt/usr/local/lib/axv/package-inventory"
install -m 0755 "$image_dir/package-inventory.py" "$mnt/usr/local/lib/axv/package-inventory.py"

chroot "$mnt" /usr/bin/env -i \
  PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  AXV_GUEST_PACKAGE_SOURCE="$package_source" \
  sh /usr/local/lib/axv/install-base lima

rm -f "$mnt/usr/local/lib/axv/install-base" "$mnt/usr/sbin/policy-rc.d" "$mnt/etc/resolv.conf"
case "$resolv_mode" in
  symlink) ln -s "$resolv_link" "$mnt/etc/resolv.conf" ;;
  file) cp -a "$build_dir/resolv.conf.orig" "$mnt/etc/resolv.conf" ;;
esac

install -d -m 0755 "$mnt/usr/local/bin" "$mnt/usr/share/axv" "$mnt/usr/share/doc/axv-guest" "$mnt/etc/systemd/system"
install -m 0755 "$build_dir/axv-guest" "$mnt/usr/local/bin/axv-guest"
install -m 0644 "$build_dir/image.json" "$mnt/usr/share/axv/image.json"
cp -a "$build_dir/distribution/axv-guest/." "$mnt/usr/share/doc/axv-guest/"
install -m 0755 "$build_dir/prepare-ssh" "$mnt/usr/local/lib/axv/prepare-ssh"
install -m 0755 "$image_dir/prepare-tasks.py" "$mnt/usr/local/lib/axv/prepare-tasks"
install -m 0644 "$build_dir/axv-guest.service" "$mnt/etc/systemd/system/axv-guest.service"
install -m 0644 "$repo_dir/apps/server/internal/guest/internal/access/axv-ssh.service" "$mnt/etc/systemd/system/axv-ssh.service"

chroot "$mnt" /usr/bin/env -i \
  PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  sh -eu -c 'apt-get clean; systemctl enable axv-guest.service axv-ssh.service; cloud-init clean --logs'

# systemd requires a pre-existing zero-byte /etc/machine-id during early read-only boot;
# cloud-init clean --machine-id deletes it in offline chroots without /run/systemd/system.
if [[ ! -f "$mnt/etc/machine-id" || -L "$mnt/etc/machine-id" || -s "$mnt/etc/machine-id" ]]; then
  echo "Expected zero-byte regular /etc/machine-id in finalized guest image." >&2
  exit 1
fi

mkdir -- "$2"
mkdir -- "$2/distribution"
cp -a "$mnt/usr/share/doc/axv-guest" "$2/distribution/"

if command -v fstrim >/dev/null 2>&1; then
  fstrim "$mnt" || true
  if [[ -n $boot_part ]]; then
    fstrim "$mnt/boot" || true
  fi
fi
sync
umount --recursive "$mnt"
mounted=false
losetup -d "$loop_dev"
loop_dev=''

qemu-img convert -O qcow2 -c "$build_dir/root.raw" "$2/root.qcow2"
go run ./apps/server/cmd/image-manifest --provider lima --arch "$guest_arch" --guest "$build_dir/axv-guest" --rootfs "$2/root.qcow2" --name "AXV Lima $guest_arch" --output "$2/manifest.json"
printf '%s\n' "$CLOUD_IMAGE_SHA256" > "$2/base-image.sha256"
(cd -- "$2" && { sha256sum root.qcow2 manifest.json base-image.sha256; find distribution -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum; } > SHA256SUMS)
echo "Built immutable Lima bundle: $2/manifest.json"
