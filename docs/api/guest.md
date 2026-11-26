# Guest execution API

[API index](README.md)

Commands, detachable terminals, project files, persistent tasks, read-only GitHub inspection and local SSH inside a selected VM (protocol version **3**, Firecracker vsock port **1024** or Lima private Unix socket). Host routes sit under `/v1/machines/:id/guest` (browser gateway `/api/compute/machines/:id/guest`); SSH and network routes sit directly under `/v1/machines/:id/`. Per-machine guest tokens are host-injected and never exposed to browsers. Every feature requires the intersection of image-declared and runtime-confirmed capabilities ([VM architecture](../vm-platform.md#image-and-capability-contract)). JSON bodies require one object with no unknown fields (default **64 KiB** cap unless specified).

## Routes

| Method | Route (under `/machines/:id/guest`) | Contract |
| --- | --- | --- |
| GET | `/health` | `{ version, machineId, bootId, instanceId, ready, capabilities, sshAvailable, reason? }` |
| POST | `/exec` | Synchronous `{ argv, directory?, timeoutSeconds? }` → `{ exitCode, stdout, stderr, truncated }` |
| GET | `/terminals` | Active/ended terminal records for this guest-service lifetime |
| POST | `/terminals` | Create login Bash shell `{ requestId, directory?, cols?, rows? }` (`201`, or `200` idempotent retry) |
| GET | `/terminals/:id` | Single terminal status |
| DELETE | `/terminals/:id` | Terminate terminal process group |
| GET | `/terminals/:id/attach` | WebSocket upgrade (disconnect detaches; single active attachment per terminal) |

Implementation entry points: [`internal/guest`](../../apps/server/internal/guest), wire types in [`guest/protocol`](../../apps/server/internal/guest/protocol/protocol.go) and host transport in [`compute/guest_client.go`](../../apps/server/internal/compute/guest_client.go).

## Command execution and terminals

- **Exec (`exec` capability):** 1–128 direct argv (no shell interpolation), timeout 1–300s (default 60s), max 8 concurrent, 64 KiB cap per stdout/stderr stream. Non-idempotent: never auto-retry a lost response. Starting directory resolves inside the mounted project root, but processes retain ordinary guest `axv` permissions.
- **Terminals (`terminals` capability):** Dimensions 2–500 × 1–200 (default 80×24), max 16 live / 1024 total records per guest-service lifetime, `requestId` 8–128 bytes. Browser requests pin `X-AXV-Host-ID` (or `hostId` query on WS). Reconnect must look up the original terminal (replaying up to 1 MiB tail for running / 16 KiB for ended) and never silently create a replacement.
- **WS frames:** Server sends `{ ready: true, bootId, replayTruncated }`, binary PTY output, and `{ ended: true, message }`; client sends binary input (max 32 KiB) and `{ resize: [cols, rows] }`. Backlog overflow closes with code `1013`.

## Project file protocol

Private guest routes `POST /v1/project-files/{inspect,list,read,write,mkdir}` (capability `project-files`) and `POST /v1/project-files/restore-check` (capability `project-restore`), called only by the compute host ([Compute project API](compute.md#vm-projects)).

- **Scope & root identity:** Every call carries `{ root, rootIdentity, bootId, instanceId }` (only initial `inspect` may omit `rootIdentity`). `rootIdentity` hashes the directory's Linux file handle (`open_by_handle_at`, requiring `CONFIG_FHANDLE` + ext4 export handles) with filesystem type and persistent FS ID — never falling back to inodes or timestamps.
- **Confinement & atomic writes:** Opens project root via `os.Root`, rejects final symlinks/special files and invalid UTF-8/unpaired surrogates, allows 8 MiB write JSON envelope / 1 MiB UTF-8 text. Writes serialize inside the guest service, stage+fsync in the target directory, recheck root/parent/file identity and `expectedSha256` (empty = create-only), atomically rename without modifying hard-link aliases, fsync parent and verify published digest. Unrelated guest processes are not locked out; never auto-retry uncertain writes.
- **Restore check:** Validates original root identity and Git/worktree metadata paths (without executing Git or hooks) before allowing [backup binding](compute.md#vm-project-backups).

## GitHub project reads

Private `POST /v1/project-github/{git,review,snapshot}` (`project-github` capability):

- Reads `.git`/HEAD/refs/worktrees/remotes directly via `os.Root` without executing Git/hooks, rejecting symlink/outside-volume metadata and reftable, and reverifies root/boot/instance/Git metadata after provider I/O.
- Invokes only `~/.local/bin/gh` using the pinned file-backed token from `~/.config/axv/github/hosts.yml` (`0700`/`0600`) passed via `GH_TOKEN` in an isolated temporary config dir (no keyring/default-profile fallback, 20s timeout, 2 MiB stdout / 16 KiB stderr).
- Executes one fixed read-only GraphQL document (bounded to 10 PRs and 50 checks per connection with completeness flags); allocates no task receipt.

## Project task protocol

Private `POST /v1/project-tasks/{submit,inspect,logs,cancel}` (`project-tasks` capability; typed `spec.initialization` also requires `project-initialization`), called via [Compute tasks](compute.md#vm-project-tasks).

- **Durable acceptance & isolation:** Guest durably accepts `(taskId, attemptId, spec)` before spawning (matching retries return the original receipt; changed inputs return `409 idempotency_conflict`). State lives on the VM root disk in `/var/lib/axv/tasks` bound by root-owned `tasks.owner` (max 8 active, 256 receipts, 8 MiB metadata, 256 MiB logs; actual free space checked before admission; never silently reset).
- **Cgroup v2 enforcement:** Runtime health advertises `project-tasks` only after verifying delegated `cpu`/`memory`/`pids` controllers, `cgroup.kill` and atomic `clone3` (`CLONE_INTO_CGROUP`) placement. Limits: 1–86,400s, 1–128 CPUs, 64–1,048,576 MiB, 2–256 processes, 1,024–8,388,608 log bytes. Exceeding log cap stops the attempt and keeps its exact prefix. Cancellation requires matching `(bootId, instanceId)` and kills the full attempt cgroup including daemonized descendants. Guest-service restart reconciles unfinished attempts to `interrupted` without relaunching.
- **Trusted account boundary:** Commands run as the guest `axv` account (not an adversarial sandbox against other projects on the same VM).

## Boundaries

Private `POST /v1/shutdown` is host-controller-only (never gateway-exposed), returns `202` on accepted guest reboot/poweroff (3s deadline) and leaves final exit observation to the host provider.

## SSH access

Private-host-only routes (excluded from browser gateway and remote transports; require `X-AXV-Host-ID` and running VM):

| Method | Private host route | Contract |
| --- | --- | --- |
| GET | `/v1/machines/:id/ssh` | `{ version, hostId, machineId, bootId, available, user, hostKey?, reason? }` |
| POST | `/v1/machines/:id/ssh/clients` | Register plain Ed25519 key `{ bootId, clientId, publicKey }` (max 128 clients / 64 KiB) |
| GET | `/v1/machines/:id/ssh/attach?bootId=...` | HTTP `101` upgrade (`Upgrade: axv-ssh-v1`) to framed SSH byte stream |

Frames use 1-byte type (`1` = 1–32,768 data bytes, `2` = half-close EOF) + 4-byte big-endian length ([`sshstream`](../../apps/server/internal/sshstream/stream.go)). Max 32 simultaneous streams per guest; host/boot mismatch returns `409`. Sessions are ordinary connection-bound `axv` SSH sessions (SFTP + local/loopback-remote forwarding; no agent forwarding, X11, root/password login or AXV replay).

## Network observation

`GET /v1/machines/:id/network` (and `/api/compute/machines/:id/network`) reports up to 256 current global-unicast guest addresses scoped to `(bootId, observedAt)` with host-perspective reachability (`unreachable` for isolated Firecracker guests, `unknown` when unverified). Never starts a VM or implies browser/host connectivity.
