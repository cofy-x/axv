#!/usr/bin/env bash
set -euo pipefail
package_source=${AXV_GUEST_PACKAGE_SOURCE:-preserve}
case "$package_source" in preserve|official|aliyun) ;; *) echo "Select preserve, official or aliyun for AXV_GUEST_PACKAGE_SOURCE." >&2; exit 1;; esac
# Run explicitly on a dedicated Linux image builder, never during application build.
if [[ $(uname -s) != Linux || $EUID != 0 ]]; then
  echo 'Use a Linux image builder as root to preserve exported file ownership.' >&2
  exit 1
fi
if [[ ! ${BASE_IMAGE:-} =~ ^ubuntu:24\.04@sha256:[a-f0-9]{64}$ ]]; then
  echo 'Set BASE_IMAGE to ubuntu:24.04@sha256:<verified image digest>.' >&2
  exit 1
fi
if [[ $# != 1 || -e $1 ]]; then
  echo 'Usage: BASE_IMAGE=... bash images/guest/build-rootfs.sh /absolute/new-output-directory' >&2
  exit 1
fi
if [[ $1 != /* ]]; then echo 'The output directory must be absolute.' >&2; exit 1; fi
for tool in docker go node mkfs.ext4 tar sha256sum python3; do
  if ! command -v "$tool" >/dev/null; then echo "Missing required image-build tool: $tool" >&2; exit 1; fi
done
case $(uname -m) in
  x86_64) guest_arch=amd64 ;;
  aarch64) guest_arch=arm64 ;;
  *) echo 'Only native amd64 and arm64 builds are supported.' >&2; exit 1 ;;
esac
image_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_dir=$(cd -- "$image_dir/../.." && pwd)
mkdir -p "$repo_dir/.data/test"
chmod 0700 "$repo_dir/.data/test"
build_dir=$(mktemp -d "$repo_dir/.data/test/image-build-XXXXXX")
export TMPDIR="$build_dir" GOTMPDIR="$build_dir"
image_tag="axv-guest-build:$(basename "$build_dir" | tr '[:upper:]' '[:lower:]')"
container_id=''
image_created=false
cleanup() {
  if [[ -n $container_id ]]; then docker rm "$container_id" >/dev/null; fi
  if [[ $image_created == true ]]; then docker image rm "$image_tag" >/dev/null; fi
  rm -rf -- "$build_dir"
}
trap cleanup EXIT
mkdir -p "$build_dir/context" "$build_dir/rootfs"
cp "$image_dir/Dockerfile" "$image_dir/install-base.sh" "$image_dir/package-inventory.sh" "$image_dir/prepare-guest.sh" "$image_dir/prepare-tasks.py" "$image_dir/axv-prepare.service" "$image_dir/axv-guest.service" "$build_dir/context/"
cp "$repo_dir/apps/server/internal/compute/ubuntusources/configure.sh" "$build_dir/context/configure-package-sources"
cp "$repo_dir/apps/server/internal/guest/internal/access/provision.sh" "$build_dir/context/prepare-ssh"
cp "$repo_dir/apps/server/internal/guest/internal/access/axv-ssh.service" "$build_dir/context/"
cd -- "$repo_dir"
CGO_ENABLED=0 GOOS=linux GOARCH="$guest_arch" go build -trimpath -o "$build_dir/context/axv-guest" ./apps/server/cmd/guest
go run ./apps/server/cmd/image-manifest --provider firecracker --arch "$guest_arch" --guest "$build_dir/context/axv-guest" --output "$build_dir/context/image.json"
node scripts/guest-release-notices.mjs --guest "$build_dir/context/axv-guest" --manifest "$build_dir/context/image.json" --output "$build_dir/context/guest-distribution"
docker build --platform "linux/$guest_arch" --build-arg "BASE_IMAGE=$BASE_IMAGE" --build-arg "AXV_GUEST_PACKAGE_SOURCE=$package_source" --tag "$image_tag" "$build_dir/context"
image_created=true
image_platform=$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image_tag")
if [[ $image_platform != "linux/$guest_arch" ]]; then
  echo "Built image platform $image_platform does not match linux/$guest_arch." >&2
  exit 1
fi
container_id=$(docker create --platform "linux/$guest_arch" "$image_tag")
docker export "$container_id" | tar --numeric-owner -xpf - -C "$build_dir/rootfs"
truncate -s 2G "$build_dir/root.ext4"
mkfs.ext4 -F -q -L axv-root -d "$build_dir/rootfs" "$build_dir/root.ext4"
mkdir -- "$1"
cp --sparse=always "$build_dir/root.ext4" "$1/root.ext4"
mkdir -- "$1/distribution"
cp -a "$build_dir/rootfs/usr/share/doc/axv-guest" "$1/distribution/"
go run ./apps/server/cmd/image-manifest --provider firecracker --arch "$guest_arch" --guest "$build_dir/context/axv-guest" --rootfs "$1/root.ext4" --name "AXV Firecracker $guest_arch" --output "$1/manifest.json"
printf '%s\n' "$BASE_IMAGE" > "$1/base-image.txt"
(cd -- "$1" && { sha256sum root.ext4 manifest.json base-image.txt; find distribution -type f -print0 | LC_ALL=C sort -z | xargs -0 sha256sum; } > SHA256SUMS)
printf 'Created %s guest artifacts in %s. Supply and verify a compatible Firecracker kernel separately.\n' "$guest_arch" "$1"
