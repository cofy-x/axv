#!/usr/bin/env bash
set -euo pipefail
package_source=${AXV_GUEST_PACKAGE_SOURCE:-preserve}
case "$package_source" in preserve|official|aliyun) ;; *) echo "Select preserve, official or aliyun for AXV_GUEST_PACKAGE_SOURCE." >&2; exit 1;; esac
# Run on a dedicated native Linux image builder with libguestfs/qemu installed.
if [[ $(uname -s) != Linux ]]; then echo 'Use a native Linux image builder.' >&2; exit 1; fi
if [[ $# != 2 || $1 != /* || $2 != /* || -e $2 || ! ${CLOUD_IMAGE_SHA256:-} =~ ^[a-f0-9]{64}$ ]]; then
  echo 'Usage: CLOUD_IMAGE_SHA256=<reviewed digest> bash images/guest/build-lima.sh /absolute/base-cloud.img /absolute/new-bundle' >&2
  exit 1
fi
for tool in virt-customize qemu-img go sha256sum; do command -v "$tool" >/dev/null; done
base_image=$1
printf '%s  %s\n' "$CLOUD_IMAGE_SHA256" "$base_image" | sha256sum --check --status
case $(uname -m) in x86_64) guest_arch=amd64;; aarch64) guest_arch=arm64;; *) exit 1;; esac
image_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(cd -- "$image_dir/../.." && pwd)
mkdir -p "$repo_dir/.data/test"
chmod 0700 "$repo_dir/.data/test"
build_dir=$(mktemp -d "$repo_dir/.data/test/image-build-XXXXXX")
trap 'rm -rf -- "$build_dir"' EXIT
export TMPDIR="$build_dir" GOTMPDIR="$build_dir"
cd -- "$repo_dir"
CGO_ENABLED=0 GOOS=linux GOARCH="$guest_arch" go build -trimpath -o "$build_dir/axv-guest" ./apps/server/cmd/guest
go run ./apps/server/cmd/image-manifest --provider lima --arch "$guest_arch" --guest "$build_dir/axv-guest" --output "$build_dir/image.json"
qemu-img convert -O qcow2 "$base_image" "$build_dir/root.qcow2"
cp "$image_dir/axv-guest-lima.service" "$build_dir/axv-guest.service"
cp apps/server/internal/guest/internal/access/provision.sh "$build_dir/prepare-ssh"
virt-customize -a "$build_dir/root.qcow2" \
  --mkdir /usr/local/lib/axv \
  --upload "$repo_dir/apps/server/internal/compute/ubuntusources/configure.sh:/usr/local/lib/axv/configure-package-sources" \
  --upload "$image_dir/install-base.sh:/usr/local/lib/axv/install-base" \
  --run-command "AXV_GUEST_PACKAGE_SOURCE=$package_source sh /usr/local/lib/axv/install-base && rm /usr/local/lib/axv/install-base" \
  --copy-in "$build_dir/axv-guest:/usr/local/bin" \
  --copy-in "$build_dir/image.json:/usr/share/axv" \
  --copy-in "$build_dir/prepare-ssh:/usr/local/lib/axv" \
  --copy-in "$build_dir/axv-guest.service:/etc/systemd/system" \
  --copy-in "$repo_dir/apps/server/internal/guest/internal/access/axv-ssh.service:/etc/systemd/system" \
  --run-command 'chmod 0755 /usr/local/bin/axv-guest /usr/local/lib/axv/prepare-ssh; systemctl enable axv-guest.service axv-ssh.service; cloud-init clean --logs --machine-id'
mkdir -- "$2"
cp --sparse=always "$build_dir/root.qcow2" "$2/root.qcow2"
go run ./apps/server/cmd/image-manifest --provider lima --arch "$guest_arch" --guest "$build_dir/axv-guest" --rootfs "$2/root.qcow2" --name "AXV Lima $guest_arch" --output "$2/manifest.json"
printf '%s\n' "$CLOUD_IMAGE_SHA256" > "$2/base-image.sha256"
echo "Built immutable Lima bundle: $2/manifest.json"
