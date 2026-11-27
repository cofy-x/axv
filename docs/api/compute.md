# Compute API

[API index](README.md)

Machine, storage, durable-operation, VM project and persistent-task APIs for the local deployment machine (host protocol version **3**). Browser gateway uses `/api/compute`; private host Unix socket uses `/v1` (`/environment` and `/preparation/*` are desktop-only). Shared [access boundaries](README.md#access-and-identity-boundaries) apply. JSON bodies require one object with no unknown fields (default **64 KiB** cap unless specified).

Implementation: [`compute/gateway`](../../apps/server/internal/compute/gateway), [`compute/httpapi`](../../apps/server/internal/compute/httpapi), [`compute/control`](../../apps/server/internal/compute/control), [`compute/prepare`](../../apps/server/internal/compute/prepare). Architecture belongs to [VM architecture](../vm-platform.md).

## Routes

| Method | Route after prefix | Response |
| --- | --- | --- |
| GET | `/environment` | Desktop compute environment & coordinator snapshot (desktop-only) |
| GET | `/preparation/check` | Cached checks, defaults, `instanceId`, `localService?` & recent tasks (desktop-only) |
| POST | `/preparation/plan` | Reviewed frozen preparation plan (desktop-only) |
| POST | `/preparation/jobs` | `202` preparation task (desktop-only) |
| GET | `/preparation/jobs/:id` | Preparation task status/steps (desktop-only) |
| POST | `/preparation/jobs/:id/retry` | `202` retry original preparation task (desktop-only) |
| POST | `/preparation/jobs/:id/cancel` | Cancel preparation task before commit (desktop-only) |
| GET | `/runtime` | Cheap controller readiness, `hostId` and `configFingerprint` (no provider probe) |
| GET | `/info` | Host capacity, providers/capabilities, images and creation defaults |
| GET | `/machines`, `/machines/:id` | Non-deleted machine list / single machine (includes tombstones) |
| GET | `/machines/:id/network` | Guest network observation ([Guest API](guest.md#network-observation)) |
| POST / PATCH | `/machines`, `/machines/:id` | `202 { machine, operation }` create / update spec |
| POST | `/machines/:id/actions` | `202 { machine, operation }` (`start\|stop\|restart\|force-stop\|delete\|package-source`) |
| GET / POST | `/volumes`, `/volumes/:id/actions`, `/volumes/:id/delete` | Volume list / `202` create, copy, action (`rename\|retain\|resize\|attach\|detach`) or delete |
| GET / POST | `/projects`, `/projects/:id`, `/projects/:id/binding`, `/projects/:id/delete` | VM project list, `201` register, get, capture `{ project, binding }`, or unregister project metadata |
| GET / POST | `/projects/:id/github`, `/projects/:id/github/{git,review,associate,clear,refresh}` | Read-only VM project GitHub association and observations |
| GET / POST | `/project-backups`, `/projects/:id/backups`, `/project-backups/:id/{restore,bind}` | Offline backups list, `202` copy/restore, `201` explicit restored-project bind |
| POST | `/projects/:id/initialize`, `/projects/:id/initializations/:requestId/finish` | `202` directory/clone init task; explicit post-success project registration |
| POST | `/projects/:id/files/{list,read,write,mkdir}` | Bound guest project-file operations |
| POST / GET | `/projects/:id/tasks`, `/tasks/:taskId`, `/task-requests/:requestId` | `202` submit persistent task, list/inspect task, recover request receipt |
| POST / GET | `/projects/:id/tasks/:taskId/retry`, `.../attempts/:attemptId/{cancel,logs,status}` | `202` new attempt, `202` exact-attempt cancel, byte-offset logs, exact status |
| GET | `/task-events?after=` | Sanitized durable task outcome event feed |
| GET / POST | `/images`, `/images/import`, `/images/:id/delete` | Image list, `202` HTTPS/multipart import (Lima, ≤ 8 GiB), `202` delete |
| GET | `/operations`, `/operations/:id`, `/operations/by-request/:requestId` | Active + latest 200 completed operations / single / exact request lookup |

## Desktop environment

`GET /api/compute/environment` (local-only, `no-store`) returns desktop platform, `configured`, paths, `logPath`/`loggingError`, `requiredLimaVersion`, `configurationSource`, `preparation` snapshot and coordinator `runtime` (`not_configured|starting|ready|failed`, `mode`, `generation`, `hostId?`). Cheap read — never launches processes or probes providers. Gateway bindings pin `(socket, hostId, configFingerprint)` per request/WS upgrade.

## Environment preparation

Local-only, `no-store`. Mutations require exact Origin and `X-AXV-Instance-ID` (`409 instance_changed` on stale instance). Never accepts arbitrary shell commands.

- `POST /preparation/plan` freezes `storageRoot`, `limaHome`, capacity budgets, `imageSource` (`official|ubuntu-base|bundle`; defaults to `official` where a reviewed official AXV Lima image is published such as Apple Silicon `darwin/arm64`, otherwise `ubuntu-base`, and unpublished official targets report `release_pending`), `socket`/`hostConfig`, pinned digests and mirror URLs (no auto-fallback). Reviewing never mutates state.
- `GET /preparation/lima-shell?machineId=<id>` (local-only, `no-store`) resolves the exact profile-managed `limactl` executable, `LIMA_HOME`, and instance identifier for a running local Lima VM without exposing host paths to remote transports.
- `POST /preparation/jobs` (`{ requestId, planId, confirmed: true }`) is idempotent per `requestId` (`409 request_conflict` on payload mismatch; max 1 active job per profile; persisted in `AXV_HOME/preparation/preparation.sqlite`).
- `retry` and `cancel` take `{ requestId, expectedRevision }`. Cancel is valid only before config commit; retry resumes the exact original plan without deleting existing storage.

## Host information

- `GET /v1/runtime` verifies controller readiness, `protocolVersion`, `hostId` and `configFingerprint` without probing providers; CLI clients verify this on every new connection.
- `GET /api/compute/info` exposes capacity, provider capability flags (including independent `volumeCopy`), ready images and shared creation defaults (`my-vm`, 2 CPUs, 1024 MiB RAM, 8 GiB root, 4 GiB retained project volume, `start: true`, `taskTtlSeconds: 3600`, `taskRetainVolume: false`). Clients never silently shrink allocations to fit capacity.

## Machine creation and configuration

- Create takes `{ requestId, start?, spec }` (omitted `start` = `true`); `PATCH` takes `{ requestId, revision, spec, renewLease? }` with the full `spec`.
- **Names:** Trimmed 1–80 UTF-8 bytes, case-sensitive, unique host-wide across active machines and pending specs (`vm-<32-hex>` reserved; `409 name_conflict`).
- **Limits:** ≥ 1 CPU (Firecracker max 32 vCPUs/VM), ≥ 128 MiB RAM, ≥ 2 GiB root (respecting image `minRootGiB`), ≥ 1 GiB project disk. `development` requires `retainVolume: true` and no TTL; `task` TTL is **60–2,592,000s**.
- CPU, memory and root-disk edits require a stopped VM; disks only grow. `pendingSpec` blocks start until resolved.

## Actions and durable operations

All compute `requestId` values are **8–128 bytes**, globally unique in the host database and bound to the original payload (`409 idempotency_conflict` on mismatch). Acceptance (`202`) is **not** completion: poll operations through `queued|running` to `succeeded|failed`. On uncertain responses, query `GET /operations/by-request/:requestId` (a `404` does not prove submission failed) or replay the exact `(requestId, payload)` on the original host.

## Volumes

`POST /volumes` creates a new volume or copies `sourceVolumeId` (attached source must be stopped; copy cannot shrink). `POST /volumes/:id/actions` supports `rename`, `retain`, `resize`, `attach` and `detach` (attach/detach/resize of attached storage require a stopped VM and matching VM revision). Deleting an attached or uncertain-attachment volume is rejected.

## VM projects

All project, GitHub, init, backup and task routes require `X-AXV-Host-ID` (even on reads), strict UTF-8/Unicode JSON (rejecting unpaired surrogates and queries unless documented) and exact Origin on gateway mutations. Never falls back to host directories.

- **Register (`POST /projects`):** `{ requestId, volumeId, volumeRevision, machineId, root, name? }` on a retained, attached volume of a running VM confirming `project-files`. Idempotent by `requestId` and unchanged `(volume, root, rootIdentity)`. Project-bound volumes cannot be deleted or made disposable while registered projects reference them.
- **Delete (`POST /projects/:id/delete`):** `{ requestId, revision }` unregisters project metadata (including stored task history and GitHub association rows) without modifying files inside the volume. Rejected with `409` while non-final tasks or active volume operations exist; once all projects referencing a detached volume are removed, the volume can be deleted.
- **Binding (`GET /projects/:id/binding`):** Returns `{ project, binding }` where `binding = { hostId, projectId, projectRevision, volumeId, volumeRevision, machineId, bootId, instanceId }`. Every file POST sends the full original `binding`; VM replacement or guest restart invalidates old requests (callers never auto-rebind).
- **File operations (`list|read|write|mkdir`):** Relative paths ≤ 4,096 UTF-8 bytes (no traversal, backslashes or `.axv`/`.axv-*`). List: default 100 / max 200 entries (10,000 scan cap). Read/write: regular UTF-8 text ≤ **1 MiB** (8 MiB write JSON envelope); symlinks/special files rejected. Write requires `expectedSha256` (`""` = create-only; lowercase SHA-256 = replace reviewed version). Synchronous with no durable receipt — inspect the path on uncertain responses instead of blindly retrying.

### GitHub project review

`GET /projects/:id/github` returns stored `{ revision, association? }`. `POST /projects/:id/github/{git,review,associate,clear,refresh}` (8 KiB envelope, requires `project-github` + `project-files`) holds normal project leases and allocates no task receipts. Association mutations use compare-and-swap on `githubRevision` without durable request IDs; VM replacement requires explicit re-review. Read-only observations keep local HEAD, PR heads and synthetic merge-test checks distinct.

## VM project initialization

`POST /projects/:id/initialize` (`{ requestId, binding, directory, repository?, name? }`, requires `project-files` + `project-tasks` + `project-initialization`) admits a typed persistent task creating one new single-component child directory (≤ 255 bytes; empty dir or credential-free HTTPS/SSH Git clone; raw Git output withheld). Generic task Retry rejects initializations; failed clones preserve partial directories. After confirmed success, `POST /projects/:id/initializations/:requestId/finish` (`{ binding }`) verifies and idempotently registers the captured `createdRoot`.

## VM project backups

Offline whole-volume copies on `VolumeCopier` providers (Lima, Firecracker):

- `POST /projects/:id/backups` requires a stopped VM with no desired restart, no pending operations and no nonfinal/unknown tasks on the volume; locks source resources and creates a detached immutable volume with `backup` provenance + SHA-256 completion marker. Lima lock contention after controller restart publishes `phase: "waiting-for-provider"` and retries the same operation.
- `POST /project-backups/:id/restore` copies a verified backup into a new retained working volume (`restoredFrom`).
- `POST /project-backups/:id/bind` requires a running VM with `project-restore` + `project-files`, validates original root identity and Git/worktree metadata (≤ 256 linked worktrees, ≤ 64 KiB/file, no Git execution), and creates a **separate** project ID (original projects and agent runs stay on the original volume).

## VM project tasks

Runs direct `argv` (1–128 args, ≤ 32 KiB total, 256 KiB JSON envelope, no shell interpolation) inside a registered project's VM (`project-tasks` capability).

| Limit            | Default   | Accepted range                      |
| ---------------- | --------- | ----------------------------------- |
| `timeoutSeconds` | 900       | 1–86,400                            |
| `cpus`           | 1         | 1–128 (within VM allocation)        |
| `memoryMiB`      | 512       | 64–1,048,576 (within VM allocation) |
| `maxProcesses`   | 64        | 2–256                               |
| `maxLogBytes`    | 1,048,576 | 1,024–8,388,608                     |

- **Durable acceptance & history caps:** Commits task, attempt and receipt before dispatch (`202`). Host retains up to **256 tasks**, **1,024 attempts**, **256 MiB** log capacity and **8 MiB** metadata without silent eviction (`429 capacity_exceeded` / `task_limit`).
- **Attempt states & retries:** Nonfinal `accepted|dispatching|running|cancelling|unknown`; final `succeeded|failed|cancelled|interrupted`. Recover lost responses via `GET /projects/:id/task-requests/:requestId` (returns that request's exact attempt). `POST .../retry` requires a final prior attempt (never `unknown`) and a freshly reviewed binding. `POST .../cancel` addresses one exact attempt using its original binding.
- **Logs:** `GET .../logs?offset=&limit=` (default/max **65,536** bytes) returns base64 chunk + `nextOffset`, `logBytes`, `logLimited`; host-cached logs stay readable while the VM is offline (`syncError` reports lag/gaps without erasing a verified final outcome).

### Task outcome events

- `GET /task-events?after=N` (inclusive cursor, default 0; host retains newest **2,048** events, max **256**/page) returns `{ sourceId, storeId, first, next, events, gap, unavailable }` with sanitized `{ id, sequence, observedAt, state, target }` recorded atomically on transitions into `unknown` or final states.
- `GET /projects/:id/tasks/:taskId/attempts/:attemptId/status` resolves one exact historical or current attempt (`{ available, current, state, target }`) without substituting a later retry or contacting the guest.

## Images

`POST /images` (HTTPS URL, no credentials) and `POST /images/import` (multipart `metadata` + `file`) import immutable Lima native-arch raw/self-contained qcow2 cloud images (≤ **8 GiB**, SHA-256 verified) with optional `guest` capability contract. Referenced images cannot be deleted.

## State and failures

Machine `desired` (`running|stopped|deleted`), observed `status` and `guestState` (`not-installed|incompatible|unavailable|ready`) are independent; guest failures never fail confirmed basic VM lifecycle ops.

Common status codes: `400` (`invalid_request`, `invalid_path`, `invalid_directory`, `invalid_cursor`, `not_file`), `401/403` (GitHub auth/forbidden, `file_forbidden`), `404` (`not_found`), `409` (`binding_changed`, `boot_changed`, `instance_changed`, `project_changed`, `file_conflict`, `revision_conflict`, `operation_conflict`, `idempotency_conflict`, `name_conflict`, `volume_conflict`, `image_conflict`, `task_conflict`, `unavailable`, `github_*_changed`), `413` (`file_too_large`), `415` (`not_text`), `429` (`capacity_exceeded`, `task_limit`, `github_rate_limited`), `503` (`unsupported`, `host_unavailable`, `guest_unavailable`, `guest_identity_mismatch`, `task_state_unavailable`), `507` (`disk_full`). Exact mappings live in [`compute/httpapi/api.go`](../../apps/server/internal/compute/httpapi/api.go).

## Package source maintenance

`POST /machines/:id/actions` accepts `{ requestId, revision, action: "package-source", packageSource: "official" | "aliyun" }` for running Lima Ubuntu 24.04 (arm64/amd64) VMs with Python 3 (no AXV guest required; no package refresh/upgrade performed). See [package sources](../startup.md#download-mirrors-and-guest-package-sources).
