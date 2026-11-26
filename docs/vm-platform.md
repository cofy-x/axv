# VM architecture

AXV runs VMs on the deployment machine for one trusted local user (Lima/VZ on macOS, Firecracker/KVM on Linux). Machine, project-volume, guest-terminal and desktop-window identities remain separate. See [architecture overview](architecture.md#vm-operation) for dispatch, [Compute API](api/compute.md) and [Guest API](api/guest.md) for wire contracts, [usage](usage.md#virtual-machines) for UI flows, and [project status](../README.md#project-status) for platform validation.

## Implementation map

| Change | Start here |
| --- | --- |
| Desktop UI, polling & request state | [`features/machines`](../apps/dashboard/src/features/machines) (`useCompute.ts`, `lifecycle.ts`, `operation-requests.ts`, `open-terminal.ts`) |
| Shared compute contracts & policy | [`compute/model.go`](../apps/server/internal/compute/model.go), [`policy.go`](../apps/server/internal/compute/policy.go), [`driver.go`](../apps/server/internal/compute/driver.go), [`image_validation.go`](../apps/server/internal/compute/image_validation.go) |
| Desktop gateway, host API & CLI client | [`compute/gateway`](../apps/server/internal/compute/gateway), [`compute/httpapi`](../apps/server/internal/compute/httpapi), [`compute/client`](../apps/server/internal/compute/client), [`compute/internal/hostconn`](../apps/server/internal/compute/internal/hostconn), [`cli/vm.go`](../apps/server/internal/cli/vm.go) |
| Environment preparation & activation | [`compute/prepare`](../apps/server/internal/compute/prepare), [`prepare/httpapi`](../apps/server/internal/compute/prepare/httpapi), [`compute/artifacts`](../apps/server/internal/compute/artifacts), [`compute/ubuntusources`](../apps/server/internal/compute/ubuntusources), [`app`](../apps/server/internal/app) |
| Controller, durable queue & resources | [`compute/control`](../apps/server/internal/compute/control): `store.go`, `queue.go`, `controller.go`, `configuration.go`, `volumes.go`, `images.go` |
| VM projects, backups & tasks | [`compute/projects.go`](../apps/server/internal/compute/projects.go), [`control/projects.go`](../apps/server/internal/compute/control/projects.go), [`compute/tasks.go`](../apps/server/internal/compute/tasks.go), [`control/tasks.go`](../apps/server/internal/compute/control/tasks.go), [`control/task_store.go`](../apps/server/internal/compute/control/task_store.go), [`control/notifications.go`](../apps/server/internal/compute/control/notifications.go), [`cli/vm_project.go`](../apps/server/internal/cli/vm_project.go) |
| Browser VM editor & local previews | [`VMProjectFiles.tsx`](../apps/dashboard/src/features/project-files/VMProjectFiles.tsx), [`vm-file-observer.ts`](../apps/dashboard/src/features/project-files/vm-file-observer.ts), [`vm-task-requests.ts`](../apps/dashboard/src/features/project-files/vm-task-requests.ts), [`compute/preview`](../apps/server/internal/compute/preview) |
| Host composition & providers | [`compute/host`](../apps/server/internal/compute/host), [`provider/lima`](../apps/server/internal/compute/provider/lima), [`provider/firecracker`](../apps/server/internal/compute/provider/firecracker), [`internal/providerutil`](../apps/server/internal/compute/internal/providerutil), [`owneddir`](../apps/server/internal/owneddir) |
| Guest service, SSH & image build | [`internal/guest`](../apps/server/internal/guest), [`guest/protocol`](../apps/server/internal/guest/protocol), [`guest/image`](../apps/server/internal/guest/image), [`guest/internal/access`](../apps/server/internal/guest/internal/access), [`sshstream`](../apps/server/internal/sshstream), [`images/guest`](../images/guest) |

Start regression work with [`recovery_test.go`](../apps/server/internal/compute/control/recovery_test.go), adjacent control/provider tests, [`vm-file-observer.test.ts`](../apps/dashboard/src/features/project-files/vm-file-observer.test.ts), [`machines.spec.ts`](../tests/machines.spec.ts) and [`vm-project-files.spec.ts`](../tests/vm-project-files.spec.ts).

## Ownership

- **Layered compute boundary:** `compute` defines shared models, policy, guest client and provider `Driver` interfaces; `compute/control` owns the controller, SQLite store, durable queue and replay; provider drivers implement `Driver`; `compute/host` wires controller + private Unix socket API. Desktop `compute/gateway` and CLI `compute/client` share private-socket verification (`hostId`, protocol, config fingerprint) through `compute/internal/hostconn`.
- **Separate databases (experimental baselines):** `compute.sqlite` uses format marker **AXC4** (`0x41584334`); profile-scoped `preparation/preparation.sqlite` uses **AXP1** (`0x41585031`). Both use [`sqlitemigrate`](../apps/server/internal/sqlitemigrate) and follow the [baseline policy](storage.md#desktop-database-baseline) (incompatible/unknown formats fail visibly without reset). Exclusive data-dir and socket locks prevent competing controllers.
- **Independent subsystems:** `compute/preview` is desktop-owned (ephemeral across desktop restarts); `compute/prepare` runs independently of `compute.sqlite` with shared [`artifactcache`](../apps/server/internal/artifactcache); `guest/internal/access` keeps guest-only SSH/network handlers unimportable by host packages.
- **Process survival:** Desktop/controller shutdown preserves running VMs and disks. Restart reconciles observed state and resumes queued work without auto-starting stopped VMs. Host socket permissions grant full VM control to allowed group members.

## Environment preparation

- Runtime coordinator activates after desktop readiness so missing VM dependencies never block desktop startup. Status reads return cached checks without installing or probing repeatedly.
- Preparation resolves a concrete, frozen plan (paths, budgets, digests, mirror URLs) before execution. Confirmed jobs persist in `preparation.sqlite` (one active job per profile), survive browser disconnects, use task-owned staging + full SHA-256 verification, and record config-commit intent before writing `host.json` / `config.json`.
- [`artifactcache`](../apps/server/internal/artifactcache) owns immutable public downloads and per-digest cross-process locks independently of profiles; profiles receive independent copies. Failed or cancelled jobs never delete existing VM storage.
- Activation verifies host identity, protocol, effective config, provider availability and usable images before publishing a gateway binding generation (never silently retargeting live Guest Terminals).

## State and operations

- **Commit-before-I/O:** Every mutating operation commits intent, logical capacity reservations, dependency locks, request fingerprint and a queued operation in `compute.sqlite` before calling providers. Machine names share one host-wide namespace across active and pending specs.
- **Idempotent replay & uncertainty:** Controller restart requeues interrupted running operations under the exclusive host lock and replays from the recorded phase against verified owned resources. Failed inspection retains reservations and locks — uncertainty never proves a VM stopped or authorizes replacing missing disks.
- **Capacity & leases:** CPU/memory admission precedes start; disk budgets account for stopped roots, detached volumes and managed images (logical admission limits, not free-space guarantees). Task TTL expiry queues deletion even while active (pauses while controller is down); project-volume retention is independent of expiry. Root disks are disposable.

## Storage and providers

- **Volumes & images:** Each VM has a root disk and at most one project volume; provider capabilities gate resize/attach/copy independently. Images are immutable catalog entries validated by SHA-256 and self-contained raw/qcow2 checks; referenced images cannot be deleted. Cleanup touches only verified AXV-owned resources.
- **Lima / VZ (macOS):** Invokes supported `limactl` commands with private `LIMA_HOME`, disabling host mounts, containerd, SSH-agent forwarding and automatic port forwarding. Never edits internal Lima state or adopts personal instances. First-use project-disk init never reformats existing/ambiguous signatures.
- **Firecracker / KVM (Linux):** Independently supervised privileged host (`axv-host.service`) using jailer, delegated cgroup v2 (`cgroupParent`) and per-host TAP/nftables state ([Linux setup](startup.md#linux-privilege-boundary)). Durable root/project/seed/kernel files stay in the owned machine dir; each stopped-to-running transition rebuilds only the runtime jail and links durable files into it (never reconstructing missing durable files or adopting unknown layouts). Project disks must link to the verified global volume.

### Host firewall integration

Firecracker installs one owned nftables `inet` table per host identity (`axv<last-6-host-id>`, TAP prefix `axv<last-6-host-id><3-digit-slot>`) at priority `-5`. It enforces a TAP-to-guest-IPv4 source binding set, allows public IPv4 egress per [`network_policy.go`](../apps/server/internal/compute/network_policy.go), masquerades owned TAP traffic, and blocks traffic to host, private/link-local/special-use IPv4, other guests and IPv6. AXV records intent and rule fingerprints in `firecracker/network.json` before installation, verifies static rules on startup, and rejects missing/tampered tables without adopting foreign rules.

- Administrators must enable IPv4 forwarding and permit egress in any later host firewall chains (an nftables `accept` only ends the current base chain).
- On Docker hosts using iptables, allow forwarding in `DOCKER-USER` strictly for the exact AXV TAP prefix, guest subnet and uplink (`new,established,related` outbound; `established,related` return) — AXV never modifies other services' chains.

## VM projects

A VM project binds a retained volume and volume-relative root (verified by Linux file-handle `rootIdentity`) independently of the current VM. Registration requires a running VM confirming `project-files` and protects the volume from deletion/disposable conversion.

Opening a project captures a complete environment binding `(host, project/rev, volume/rev, machine, boot, guestInstance)`. Host requests hold shared lifecycle leases while forwarding; guest enforces path confinement via `os.Root`, stages/fsyncs writes, rechecks identity and expected SHA-256 before atomic rename, and serializes AXV writers (unrelated guest processes can still race; callers must inspect conflicts/uncertainty without auto-retrying). No host fallback exists. See [Compute project API](api/compute.md#vm-projects) and [Guest file protocol](api/guest.md#project-file-protocol).

### GitHub project review

Stores an optional project-scoped GitHub repository/account association (`githubRevision`, independent of file/run revisions). Typed reads (`project-github`) hold normal project lifecycle leases, inspect local Git metadata without running Git/hooks, invoke a single fixed GraphQL query via the guest's dedicated `gh` profile, and reverify identities afterward. New VMs require explicit credential review.

### Project initialization

Creates a new child directory (empty or noninteractive credential-free Git clone) inside an opened parent **Volume workspace** as a typed persistent task (`project-initialization`). The guest persists `createdRoot` identity before running Git, preserves existing/partial directories on failure, and requires explicit `finish-initialization` to register the resulting project.

### Project volume backups

Offline whole-volume copies on `VolumeCopier` providers (Lima and Firecracker) require a confirmed stopped VM with no active/unknown tasks and lock source resources through completion. Completed copies carry provenance + SHA-256 markers and remain detached/immutable. Lima copy commands inherit an advisory lock so controller restarts wait (`waiting-for-provider`) rather than reclaiming active staging. Restoration copies into a new retained volume and requires `project-restore` guest verification of root identity and Git/worktree metadata before binding a **separate** project ID (original projects and runs keep their targets).

### Development previews

Desktop-owned local registrations (`compute/preview`) forward HTTP/WebSocket traffic on a fresh random `.localhost` origin to an already running guest loopback port, rechecking original project/boot/guest identities on every request. **Stop** revokes forwarding without killing the guest process; desktop restart requires re-registration. Remote browsing is unavailable; use independent [Cloudflare publications](api/cloudflare.md#published-project-services) for remote isolated origins.

## Persistent project tasks

A **Task** owns immutable `(argv, directory, limits)`; each **Attempt** owns one captured environment binding, status, logs and cancel intent. Controller commits task/attempt/receipt/storage reservations before dispatch; recovery inspects the original guest journal rather than redispatching. Explicit retry requires a final prior attempt (never `unknown`) and a freshly reviewed binding.

- **Logs & outcome events:** Controller caches combined stdout/stderr chunks in `compute.sqlite` (readable offline); root-disk guest executor state is not moved on VM replacement. Transitions into `unknown` or final states append sanitized outcome events transactionally in `compute.sqlite` for the desktop notification consumer.
- **Guest cgroup v2 enforcement:** Linux guests advertise `project-tasks` only after verifying delegated `cpu`, `memory` and `pids` controllers, `cgroup.kill`, atomic `clone3` placement and a healthy root-disk journal. Cancellation kills the full attempt cgroup including daemonized descendants. Per-task disk/I/O/network throttling is unsupported; commands run as the trusted guest `axv` user (not an adversarial sandbox).
- **Dedicated GitHub Review VM:** GitHub Review submits configured repo tests and two-leg candidate comparisons as pinned Compute Tasks against the repository's configured host/machine/project ([review VM recipe](../recipes/github-review/README.md)).

## Image and capability contract

Bundle manifests declare `version`, immutable digest-derived `id`/`name`, `provider`, `arch`, rootfs SHA-256 and optional `guest` contract (`imageVersion: 1`, `protocolVersion: 3`, `capabilities`, optional `binarySHA256` of the image's own guest binary). Provider lifecycle and guest readiness remain separate: basic VM actions still work on ordinary Linux images, while enhanced features require the intersection of declared and runtime-confirmed capabilities. VM Start never upgrades or injects guest binaries.

## Guest execution

- Linux guest service runs as `axv` (UID 1000), authenticated by per-machine tokens over Firecracker vsock (port 1024) or Lima's SSH/private Unix relay (which decouples output reads from relay exit).
- Graceful shutdown triggers reboot on Firecracker (exiting the VMM) and poweroff on Lima, with a bounded force-stop fallback.
- Guest Terminals bind `(hostId, machineId, bootId, terminalId)`, survive browser disconnect, replay a bounded tail on reattach, end only on explicit End/VM shutdown/guest-service restart, and share [clipboard input](desktop.md#clipboard-input).
- Lima explicit start repairs only AXV's exact `!*NP*` boot-added account lock marker when present, preserving healthy services and active sessions.

### Guest SSH and network access

A dedicated OpenSSH server listens only on guest loopback (`127.0.0.1:22222`, persistent Ed25519 host key in `/etc/ssh/axv`, client registry in `/var/lib/axv-ssh/clients`, public-key `axv` login only; root/password/agent-forwarding/X11 disabled). The CLI tunnels framed SSH bytes (`sshstream`, preserving half-close EOF) over the private host Unix socket and authenticated guest transport — independent of reported guest IPs or browser gateways.

## Desktop integration and mirrors

- **Browser inventory & mutations:** One browser-local inventory store serves visible VM windows (5s stable / 1s active-op polling) and desktop icons (30s when desktop-only), pausing in background tabs. [`OperationRequest`](../apps/dashboard/src/features/machines/operation-requests.ts), [`MachineActionRequests`](../apps/dashboard/src/features/machines/lifecycle.ts) and [`GuestTerminalRequests`](../apps/dashboard/src/features/machines/open-terminal.ts) capture exact targets/revisions and isolate uncertain requests from newer confirmed actions.
- **Mirrors & package sources:** `compute/artifacts` applies digest-keyed download mirrors before plan freeze; `compute/ubuntusources` owns idempotent guest APT deb822 configuration for image builds and explicit Lima `package-source` operations (never run on Start).
