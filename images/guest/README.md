# AXV guest images

These explicit recipes install dependencies and prebuilt services into immutable Linux images. They run separately from application startup and do not establish a published or native-validated release. See [project status](../../README.md#project-status) for release/validation status and [setup policy](../../docs/startup.md#first-time-macos-setup) for default image selection.

Both provider formats share `install-base.sh`, the unprivileged `axv` account (UID/GID 1000), `/projects`, the guest service, dedicated SSH/task helpers and the [`guest/image`](../../apps/server/internal/guest/image) manifest contract. The builder installs systemd, filesystem tools, Python with virtual-environment support, Bash, Git, curl, socat and OpenSSH. The image owns `/usr/share/axv/image.json`, `/usr/local/bin/axv-guest`, `/usr/local/lib/axv/prepare-ssh`, `/usr/local/lib/axv/prepare-tasks` and service units.

The installed manifest includes provider, architecture and a `guest` object with `imageVersion`, `protocolVersion`, `capabilities` and an optional integrity digest of its own guest executable. That digest never has to match the current AXV application. Bundle `manifest.json` adds an immutable image ID/name and rootfs filename/SHA-256. Builder IDs include the platform and rootfs digest, so different builds cannot reuse a fixed image identity. Ordinary custom Linux bundles may omit `guest`, retaining basic VM management. [VM architecture](../../docs/vm-platform.md#image-and-capability-contract) owns capability and runtime-handshake behavior. Project-file access requires an explicit image rebuild/import with `project-files`; selecting the old compatible image does not add that feature, and Start never replaces its guest binary.

Persistent project tasks similarly require an explicit rebuilt/imported image declaring `project-tasks`. The service units delegate `cpu`, `memory` and `pids` controllers and place the supervisor in `DelegateSubgroup=supervisor` (systemd **254+**, provided by the Ubuntu 24.04 base). The Linux guest kernel must support unified cgroup v2, those controllers, `cgroup.kill` and atomic `clone3` placement into a cgroup. Startup probes actual support rather than promising it from image metadata. [Guest task enforcement](../../docs/api/guest.md#project-task-protocol) owns task state, limits and unsupported-system behavior.

## Lima cloud disk

Use a dedicated native Linux builder with the pinned Go and Node.js toolchains, the libguestfs tools `virt-customize` and `virt-copy-out`, `qemu-img` and Ubuntu cloud-image support. The builder reports a missing tool before changing the image. Frontend dependencies are not required. Select a reviewed native Ubuntu base with the shared CLI cache, then build a separate disk:

```sh
axv cache fetch ubuntu-arm64 --output /absolute/build-input/base.img
CLOUD_IMAGE_SHA256=<reviewed-base-sha256> bash images/guest/build-lima.sh \
  /absolute/build-input/base.img /absolute/new-lima-bundle
```

Use `ubuntu-amd64` on an amd64 builder. The recipe verifies the supplied base digest, converts a private copy to qcow2, installs the common dependencies and AXV services offline, cleans cloud-init identity and emits `root.qcow2` plus `manifest.json`. It never changes the input base. `virt-customize` package installation uses the builder's network; this is an explicit build action, not VM Start.

Select the finished bundle explicitly with `axv setup --image-source bundle --image-bundle /absolute/new-lima-bundle/manifest.json`.

## Firecracker root filesystem

Use a dedicated native Linux builder with Docker, the pinned Go and Node.js toolchains, ext4 utilities and a separately verified Ubuntu OCI image digest. The recipe pins the container platform to the native guest architecture and rejects a mismatched image before export. Run as **root** so the exported root filesystem retains its file ownership:

```sh
BASE_IMAGE=ubuntu:24.04@sha256:<reviewed-native-image-digest> \
  bash images/guest/build-rootfs.sh /absolute/new-firecracker-bundle
```

The recipe creates a fresh ext4 root with the common dependencies, guest/SSH services and seed initializer. It removes only its own temporary container, image tag and build directory. Supply and verify a compatible kernel separately in the administrator's host configuration; this recipe does not fetch one. Project-file access requires kernel `CONFIG_FHANDLE` and ext4 export-handle support; [guest file enforcement](../../docs/api/guest.md#project-file-protocol) owns the identity mechanism and unsupported-system behavior. Run the separate [Linux native acceptance](../../docs/development.md#linux-firecracker) against the completed bundle; [project status](../../README.md#project-status) owns validated platforms and release availability.

## Per-machine initialization and release work

Both recipes retain distribution materials at `/usr/share/doc/axv-guest` inside the image and export the same directory as `distribution/axv-guest/` beside the disk and manifest. `SHA256SUMS` covers the disk, manifest, base identity record and exported materials. Inspect it from the bundle directory with `sha256sum --check SHA256SUMS`.

The [guest collector](../../scripts/guest-release-notices.mjs) reads the executable without running it, checks the pinned toolchain and manifest target/digest, and retains AXV's MIT license plus original Go notices. Linked modules must match the current server module graph; their downloaded sources are verified before copying notices. `build-info.json` preserves the binary digest and VCS metadata, including dirty or absent settings for local builds. Collection does not certify a release.

The common installer records the image's dpkg database in `os-packages.tsv`, including package states, binary/source versions and architecture, plus its `os-release`. Existing OS copyright files remain under `/usr/share/doc`; the exported [guest third-party notice](THIRD_PARTY_NOTICES.md) explains these separate terms. These records identify the built inputs and do not make unpinned APT resolution reproducible or replace source/license review.

Images contain generic files, with machine IDs and global SSH host keys cleared. The common installer rejects an input containing AXV machine configuration, dedicated SSH identity or authorized client profiles; use a clean base instead of repackaging a used VM. Lima's runtime initializer writes machine configuration and initializes dedicated SSH identity using the preinstalled helper. Firecracker's seed initializer mounts the project and read-only seed disks, copies its private configuration and initializes SSH identity. Tokens, machine IDs and SSH host keys never belong in a distributed image. Start does not install packages, replace the guest binary or update an existing image.

The image contains an empty axv-owned mode-0700 `/var/lib/axv/tasks` under a root-owned parent. The narrowly scoped root `ExecStartPre` helper binds that store to the configured machine on first service start, synchronizes its initial journal and publishes root-owned `/var/lib/axv/tasks.owner`. Subsequent starts verify the established owner and files; missing data, conflicting ownership or preexisting unknown state fails without seeding a replacement. Used task stores are also rejected as image-build inputs. The execution service remains unprivileged and receives no additional sudo command.

Build scratch files and helper executables stay in `.data/test/image-build-*` and are removed by the recipe's trap. Output bundles are explicit caller-selected directories; downloaded bases remain in the shared cache. Built/private bundles are not automatically added there.

Publishing still requires a reviewed clean source revision matching `build-info.json`, the base URL/digest or OCI digest, a package/license and corresponding-source review, a reviewed versioned catalog entry, and native lifecycle plus enhanced guest/SSH acceptance for that exact provider/architecture bundle. Keep the exported materials with the disk; separately supplied kernels need their own provenance and distribution review. Neither base images nor generated disks are committed to the repository. Current availability remains in [project status](../../README.md#project-status).

## Package source selection

Both builders accept `AXV_GUEST_PACKAGE_SOURCE=preserve|official|aliyun`; unset defaults to preserve, retaining the input's software sources. An explicit selection runs the shared [Ubuntu source script](../../apps/server/internal/compute/ubuntusources/configure.sh) on the private build target before dependency installation. [Source policy](../../docs/startup.md#download-mirrors-and-guest-package-sources) owns supported releases/layouts, URLs, signing checks, backups and recovery.

If a minimal input lacks TLS trust roots or Python 3, the builder first installs `ca-certificates` and `python3` from that input's existing signature-verified APT sources, then selects the HTTPS mirror. This bootstrap is an explicit build dependency step. Source selection itself invokes no package manager; the builder subsequently updates indexes and installs its declared dependencies. Input disks and shared cached bases remain unchanged.

The source configuration follows [Ubuntu's APT/deb822 contract](https://ubuntu.com/server/docs/how-to/software/package-management/) and the published [Aliyun Ubuntu Ports](https://developer.aliyun.com/mirror/ubuntu-ports/) and [Ubuntu](https://developer.aliyun.com/mirror/ubuntu/) repositories. Aliyun is opt-in. Artifact download mirrors are a separate digest-keyed host configuration; no cloud-image or Lima availability is inferred from these APT repositories. Existing running Lima VMs use the explicit [maintenance command](../../docs/startup.md#download-mirrors-and-guest-package-sources); Start never runs the source script.
