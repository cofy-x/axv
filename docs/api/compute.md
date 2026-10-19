# Compute API

[API index](README.md)

Machine, storage and durable-operation APIs for the computer running AXV. The VM service and its virtual machines run on that computer, independently of the browser's platform.

The browser gateway uses `/api/compute`. Host routes use `/v1` on the private host Unix socket; `/environment` and `/preparation/*` are desktop-only. The VM CLI uses the private socket directly. Host protocol version is **3**. The [shared access boundary](README.md#access-and-identity-boundaries) applies. JSON request bodies accept one object with no unknown fields, normally limited to **64 KiB**; project writes and image uploads specify separate limits below.

## Routes

| Method | Route after prefix | Response |
| --- | --- | --- |
| GET | `/environment` | Desktop compute environment |
| GET | `/preparation/check` | Preparation checks, defaults and recent tasks |
| POST | `/preparation/plan` | Reviewed preparation plan |
| POST | `/preparation/jobs` | 202 preparation task |
| GET | `/preparation/jobs/:id` | Preparation task |
| POST | `/preparation/jobs/:id/retry` | 202 preparation task |
| POST | `/preparation/jobs/:id/cancel` | Preparation task |
| GET | `/runtime` | Controller readiness and host identity |
| GET | `/info` | Host information |
| GET | `/machines` | `{ machines }` |
| GET | `/machines/:id` | Machine |
| GET | `/machines/:id/network` | Current guest address observation; see [Guest API](guest.md#network-observation) |
| POST | `/machines` | 202 `{ machine, operation }` |
| PATCH | `/machines/:id` | 202 `{ machine, operation }` |
| POST | `/machines/:id/actions` | 202 `{ machine, operation }` |
| GET | `/volumes` | `{ volumes }` |
| POST | `/volumes` | 202 `{ volume, operation }` |
| POST | `/volumes/:id/actions` | 202 `{ volume, operation }` |
| POST | `/volumes/:id/delete` | 202 operation |
| GET | `/projects` | `{ projects }` |
| POST | `/projects` | 201 `{ project }` |
| GET | `/projects/:id` | `{ project }` |
| GET | `/projects/:id/binding` | `{ project, binding }` |
| POST | `/projects/:id/files/list` | `{ project, binding, entries, nextCursor?, truncated }` |
| POST | `/projects/:id/files/read` | `{ project, binding, path, content, sha256, size, modifiedAt }` |
| POST | `/projects/:id/files/write` | Same file response after confirmed publication |
| POST | `/projects/:id/files/mkdir` | `{ project, binding, name, path, kind, size, modifiedAt }` |
| GET | `/images` | `{ images }` |
| POST | `/images` | 202 `{ image, operation }` |
| POST | `/images/import` | 202 `{ image, operation }` |
| POST | `/images/:id/delete` | 202 operation |
| GET | `/operations` | `{ operations }` |
| GET | `/operations/:id` | One durable operation |
| GET | `/operations/by-request/:requestId` | One durable operation for the exact original request ID |

## Desktop environment

`GET /api/compute/environment` returns the running desktop's configuration context even when the host is unconfigured or offline. It requires the same local access as other compute routes, sets `Cache-Control: no-store`, and rejects other methods with `405 method_not_allowed` and `Allow: GET`. There is no `/v1/environment` host route.

| Field | Meaning |
| --- | --- |
| `platform` | Desktop operating system; independent of the browser platform |
| `configured` | Compute settings exist; this does not imply service readiness |
| `configPath`, `profileHome` | Current profile paths |
| `executablePath` | Current executable path |
| `logPath` | Optional absolute path of the initialized desktop log on the AXV host; present even while VM hosting is unconfigured or offline; host diagnostics use separate supervisor output |
| `loggingError` | Optional current desktop-log write failure; events fall back to the desktop stderr until writing recovers |
| `requiredLimaVersion`, `automaticSetupSupported` | Pinned Lima release and macOS automatic preparation support |
| `configurationSource` | Profile settings or a read-only environment override |
| `preparation` | The same preparation checks, defaults and current/latest task snapshot returned by the check endpoint, without its transport instance ID |
| `availableActions` | Currently applicable `review`, `retry` and `cancel` actions; the task and plan endpoints still validate each action |
| `runtime` | Coordinator snapshot: `state`, `mode`, `generation`, `readOnly`, optional `hostId`, `code`, `error` |

Runtime state is `not_configured`, `starting`, `ready` or `failed`. This cheap snapshot does not launch a process or trigger an expensive provider probe. A configured host is activated in the background after the desktop listener starts. Readiness requires recovered host state, matching protocol/configuration/identity, an available provider and a usable native image. Environment overrides are read-only in the wizard.

A gateway binding captures one verified socket, host identity and configuration fingerprint. New socket connections verify that identity before forwarding the application request, including a WebSocket upgrade. An in-flight request retains its original binding; old connections are never redirected to a replacement service.

## Environment preparation

These routes operate independently of the compute host and compute database. They use the local-access boundary and `Cache-Control: no-store`. Every mutation requires the exact same Origin and an `X-AXV-Instance-ID` header matching the desktop; stale instances return `409 instance_changed`. `GET /preparation/check` includes the current `instanceId` for this purpose. Preparation never accepts shell commands or arbitrary process arguments.

The check response includes `platform`, `arch`, `configured`, `configurationSource`, optional `lockedReason`, `automaticSetupSupported`, `cacheHome`, `officialImageAvailable`, `requiredLimaVersion`, `defaults`, `checks`, and optional `activeJob`/`latestJob`. Checks contain `id`, `label`, `status` (`ready`, `needed`, `blocked`) and `detail`. Background dependency diagnostics are cached; polling check does not install or download anything.

`POST /preparation/plan` accepts optional `storageRoot`, `limaHome`, `cpus`, `memoryMiB`, `diskGiB`, `machines`, `socket`, `hostConfig`, `imageSource` and `imageBundle`. Paths refer to the AXV deployment machine. Missing values use the returned defaults. `imageSource` is `ubuntu-base`, `bundle` (local manifest path) or `official` (enhanced AXV release); `imageBundle` alone explicitly selects the bundle source. [Setup policy](../startup.md#first-time-macos-setup) owns the default source and preparation choices. Unpublished official releases produce a `release_pending` plan blocker. The plan validates new storage ownership, path lengths, prerequisites and capacity values. Existing configuration retains its provider pins, storage and budgets; this API does not migrate existing storage or upgrade a provider. Linux accepts an existing administrator-managed local service configuration or Unix socket and does not install Firecracker or request elevated privileges.

The response contains `id`, `createdAt`, `expiresAt`, `platform`, `arch`, `provider`, `cacheHome`, normalized `request`, `actions`, `downloads`, `checks`, `canStart`, `configPath`, `hostConfigPath` and `activation`. Downloads expose `id`, `name`, selected `url`, optional `originUrl`, pinned `sha256`, `bytes` (zero if unknown), `cached`, `cacheState` (`missing`, `partial`, `ready`, `in_use`) and `cachePath`. A cache hit means full content verification, not mere existence. `activation` is `ready` in the desktop or `configured` for the offline CLI. Reviewing creates a plan, not a running task; clients must display the concrete changes and obtain confirmation before starting.

`POST /preparation/jobs` accepts `{ requestId, planId, confirmed: true }`. A start request binds its ID to the exact reviewed plan. Expired, blocked or stale plans cannot start. A duplicate request with the same payload returns the existing task; reusing its ID with another payload returns `409 request_conflict`. Only one task may be active per profile.

Tasks persist independently of the browser under `AXV_HOME/preparation/`. Their response contains `id`, `requestId`, `planId`, `revision`, `status`, `attempt`, `phase`, `steps`, `createdAt`, `updatedAt`, `committed`, `canCancel`, `canRetry`, the reviewed `plan`, and optional `result`, `binding` or `error`. Each step reports its label/status and optional detail/byte progress. Status is `queued`, `running`, `interrupted`, `failed`, `succeeded` or `cancelled`. Success reports `ready` only after service activation; offline CLI success reports `configured`.

Retry and cancel accept `{ requestId, expectedRevision }`. Retry resumes the same task and original plan, increments its attempt, and preserves verified artifacts. It never silently replaces the task or its settings. Cancel is permitted only before configuration commit begins. Closing a page or detaching the live CLI does not cancel work. A desktop process interruption leaves an explicit retryable task; startup does not automatically repeat installation or downloads.

Download records expose the selected `url` and optional `originUrl` when an artifact mirror is selected. The plan freezes both alongside `sha256`; mirror changes do not change cache identity. Selection is mirror-only with no automatic fallback. Source-bound partials cannot be resumed from another URL.

After configuration commit, a service-start failure retains that configuration for retry. Unsupported preparation database formats and ownership conflicts preserve files and return `503 preparation_unavailable`, while the desktop remains accessible. [VM preparation](../vm-platform.md#environment-preparation) owns artifact publication and commit recovery.

## Host information

`GET /api/compute/info` returns `{ configured: false, reason }` when no host is configured. Otherwise it reports `configured: true`, `hostId`, `protocolVersion`, `hostArch`, `capacity`, `providers`, `images`, `defaults`, `experimental` and `configFingerprint`. Provider capability flags determine available actions; images include only ready/configured catalog entries. `defaults` contains a creation `spec`, `start: true`, `taskTtlSeconds: 3600` and `taskRetainVolume: false`. The development spec defaults to name `my-vm`, 2 CPUs, 1024 MiB memory, an 8 GiB root disk, a 4 GiB retained project volume and the first compatible available provider/image. Clients raise the default root size to the selected image minimum and report capacity violations without silently reducing allocations.

`GET /v1/runtime` (also forwarded as `/api/compute/runtime`) is a cheap controller-readiness and identity endpoint available after controller recovery; it avoids provider probes. The fingerprint identifies effective host configuration for managed-host reuse, not user authentication. This is separate from desktop/database [`/api/health`](desktop.md#runtime-health-and-bootstrap). Local CLI clients verify protocol, readiness, host ID and configuration fingerprint on each newly opened connection before sending its request on that same connection; replacing the socket cannot redirect an established binding.

## Machine creation and configuration

Machine listings exclude deleted records; individual machine lookups include tombstones.

Create accepts `{ requestId, start?, spec }`; omitted `start` means **true**, and explicit false creates a stopped VM. `spec` contains `name`, `provider`, `imageId`, `kind` (`development` or `task`), `resources` (`cpus`, `memoryMiB`, `diskGiB`), `projectGiB`, `retainVolume`, optional `volumeId` and `ttlSeconds`. The host config owns image paths/digests and capacity; request bodies never provide host paths or provider arguments.

Machine names are trimmed with Go `strings.TrimSpace`, then must contain **1–80 UTF-8 bytes**, without NUL, CR or LF. Matching is case-sensitive with no Unicode normalization. The canonical machine-ID form `vm-` followed by 32 lowercase hexadecimal digits is reserved. Names are unique across nondeleted machines and pending configurations on this host. A failed pending rename keeps its name reservation until resolved; successful rename releases the old name, and completed deletion releases the name. Conflicts return `409 name_conflict`. Volume and image names retain their own rules.

Resources must fit host capacity, with at least **1 CPU**, **128 MiB memory**, **2 GiB root disk** (also respecting the image's `minRootGiB`) and **1 GiB project storage**. The Firecracker adapter supports at most **32 vCPUs per machine**, independently of the host's total CPU capacity. Development requires retention and no TTL; task TTL is **60–2592000 seconds**. A reused volume must match provider, size and retention.

Configuration accepts `{ requestId, revision, spec, renewLease? }` with the complete `spec`. CPU, memory and root-disk changes require a stopped VM; disks only grow. Provider, image, volume identity and project-storage size cannot change through this endpoint; use volume operations for project storage. `pendingSpec` preserves an unresolved configuration and blocks start. `renewLease: true` renews a task lease; changing its TTL or converting development to task also starts a new lease.

## Actions and durable operations

Machine actions are start, stop, restart, force-stop, delete and explicit package-source maintenance.

An action accepts `{ requestId, revision, action }`. Volume and image deletion accept `{ requestId, revision }`. All compute `requestId` values contain **8–128 bytes** without NUL, CR or LF, are globally unique in this host database and bind to the original payload. Retry an uncertain request with the same key and payload to retrieve its operation; changed payloads return `409 idempotency_conflict`. Acceptance is not completion: poll the operation through `queued`/`running` to `succeeded` or `failed`. A failed operation may have partial side effects; inspect affected resources before issuing a new action with a new key.

Operation listings include all active and the latest 200 completed records. Operation responses expose dependency `locks` and progress, with private payloads removed. `GET /operations/by-request/:requestId` resolves an exact request ID without submitting work; encode the complete ID as one URL path segment. It returns `404 not_found` when no record is visible. That response does not establish that an in-flight submission failed, and a changed host database cannot answer for the original host. Preserve host and request identity while recovering an uncertain response.

## Volumes

Volume listings include detached retained storage. Creation can allocate a new volume or make an independent copy. Deleting an attached volume or one with an uncertain attachment is rejected.

Volume creation accepts `{ requestId, name, provider, sizeGiB, retain, sourceVolumeId? }`. A copy locks the source and its attached VM; an attached source must be stopped. Copies are independent, cannot be smaller than the source, and may omit provider/size to use the source values.

Volume actions accept `{ requestId, revision, action, name?, retain?, sizeGiB?, machineId?, machineRevision? }`, with actions `rename`, `retain`, `resize`, `attach` and `detach`. Attachment changes require the affected VM's revision and stopped state; growth of attached storage also requires stopped state. An uncertain attachment retains `pendingMachineId` and blocks deleting either resource until resolved. Failed actions retain `pendingAction`; a retry must match that action and attachment target, using a new request ID after inspecting the final failed operation.

## VM projects

These routes are available through the desktop gateway and private host socket. Every request requires `X-AXV-Host-ID` matching the original host, including list/get/binding. Gateway mutations also require the shared local Origin check. POST bodies require `application/json`, one JSON object and no unknown fields; query parameters, invalid UTF-8 and unpaired Unicode surrogates are rejected without replacing submitted characters. The ordinary **64 KiB** body limit applies except writes, which allow an **8 MiB** JSON envelope for escaped text. These routes never access a host project directory or fall back to host execution.

`POST /projects` accepts `{ requestId, volumeId, volumeRevision, machineId, root, name? }`. The volume must be retained, ready, currently attached to that exact running VM and free of conflicting operations. Its revision must match. The selected image and current guest must both offer `project-files`. `root` is an existing canonical path relative to the project volume; the empty string selects its root. It cannot be an absolute path, `.`/`..`, a symlink root or an AXV reserved internal path. Registration does not clone Git or create missing directories.

The returned project contains `{ id, hostId, kind: "vm", name, volumeId, root, rootIdentity, revision, createdAt }`. It identifies volume data independently of the current VM. Registration receipts share the host request-ID namespace: replaying the same request returns the original project; a changed payload returns `409 idempotency_conflict`. A fresh registration of the same volume/root and unchanged filesystem identity returns the existing project without renaming it. An identity mismatch fails without adopting the replacement directory. Project-bound volumes cannot be deleted or have retention disabled; no project deletion or restored-volume rebind is available yet.

`GET /projects/:id/binding` verifies current attachment and guest/root identity and returns `{ project, binding }`. The binding contains `{ hostId, projectId, projectRevision, volumeId, volumeRevision, machineId, bootId, instanceId }`; `instanceId` identifies this guest-service incarnation, separately from the Linux boot. VM replacement on the same retained volume keeps the project ID but requires an explicit new binding. Host replacement, volume changes and guest restart invalidate old requests.

Every file POST accepts `{ binding, path }` with the complete original binding. List additionally accepts `cursor?` and `limit?`; write requires `content` and explicit `expectedSha256`. The host rejects fields belonging to other actions. It holds an access lease against lifecycle/attachment changes during forwarding, rechecks the bound machine and guest, and sends the registered root identity to the guest. A stopped or missing VM, unavailable guest or stale binding fails; callers must not automatically substitute a new binding.

Paths are canonical relative paths of at most **4096 UTF-8 bytes**, without traversal, backslashes, control characters or `.axv`/`.axv-*` components. The empty path lists the project root; other actions require a nonempty path. Listing defaults to **100** entries, accepts **1–200**, reports symlinks without opening them and caps scanning at **10,000** entries with `truncated`. `nextCursor` is valid only for the observed mutable directory; it is not a snapshot. Reads and writes accept regular UTF-8 text files up to **1 MiB** without binary control characters. Symlinks and special files cannot be opened; ancestor resolution remains confined to the project.

For writes, `expectedSha256: ""` means create-only; a lowercase SHA-256 means replace exactly the reviewed version. Missing/null content or expectations are rejected; an explicit empty content string clears a reviewed file. Parents must already exist; `mkdir` creates one directory and rejects an existing name. Conflict returns an error without refreshing the expectation. Successful writes return the new `sha256`. The guest stages/syncs content, rechecks directory/file identities and hash before atomic publication and confirms the resulting bytes. AXV writers are serialized; ordinary guest processes can still race between the last check and rename. This is not an OS-level compare-and-swap against arbitrary guest tools. See [guest enforcement](guest.md#project-file-protocol).

File writes and mkdir are synchronous and have no durable request receipt. A transport or post-publication error can mean the change succeeded; inspect the original binding/path before deciding how to retry. Never blindly repeat a write, refresh its hash or acquire another environment binding to make a failed request succeed. Registration recovery instead preserves its original request ID/payload and consults the project list. Project/file persistence survives desktop/controller restart; the current VM/guest must still be available for file access. Persistent command tasks/logs/cancellation are outside this phase.

| Status | Project/file codes |
| --- | --- |
| 400 | `invalid_request`, `invalid_path`, `invalid_directory`, `invalid_cursor`, `not_file` |
| 403 | `file_forbidden` |
| 404 | `not_found` |
| 409 | `binding_changed`, `boot_changed`, `instance_changed`, `project_changed`, `file_conflict`, `revision_conflict`, `operation_conflict`, `idempotency_conflict`, `volume_conflict`, `unavailable` |
| 413 | `file_too_large` |
| 415 | `not_text` |
| 503 | `unsupported`, `host_unavailable`, `guest_unavailable`, `guest_identity_mismatch`, `project_unavailable`, `file_unavailable` |
| 507 | `disk_full` |

[VM architecture](../vm-platform.md#vm-projects) owns durable project and file-publication mechanisms; [VM project CLI](../startup.md#vm-project-files) provides the host-agent workflow. These IDs remain separate from [registered host projects](project-files.md), virtual documents and task/run identities.

## Images

Image listings expose public catalog metadata without host paths. `POST /images` imports through HTTPS; `POST /images/import` accepts a multipart file upload. Both return 202 `{ image, operation }`. Referenced images cannot be deleted.

Image import accepts `{ requestId, name, arch, sha256, url?, guest? }`. `guest` optionally declares `{ imageVersion, protocolVersion, capabilities, binarySHA256? }`; omitted metadata gives basic VM management only and never inherits another image's guest contract. URL imports require an HTTPS URL without embedded credentials. Multipart import requires a JSON `metadata` part followed by exactly one `file` part and must omit `url`. Imports require Lima, the host's native architecture, a matching SHA-256. Files are limited to **8 GiB** and must be bootable raw or self-contained qcow2 cloud images without backing or external data files. Catalog metadata is immutable; register a new image to change it. Use provider capability fields for image, volume and resource-editing availability.

## State and failures

Machine `desired` is running/stopped/deleted; observed `status` includes queued, provisioning, starting, stopping, deleting, running, stopped, unknown, lost, failed and deleted. `ready` describes the guest independently. `guestState` is `not-installed`, `incompatible`, `unavailable` or `ready` while running; `guestReason` supplies enhancement diagnostics. `guestCapabilities` contains only the intersection of the image declaration and a successful current protocol/identity handshake. Nonrunning machines expose no usable guest capabilities. Guest failures do not turn confirmed basic VM lifecycle operations into failures. Revisions protect intent changes; status refreshes do not increment them.

| Status | Codes |
| --- | --- |
| 400 | `invalid_request` |
| 404 | `not_found` |
| 409 | `revision_conflict`, `operation_conflict`, `idempotency_conflict`, `name_conflict`, `volume_conflict`, `image_conflict`, `configuration_conflict`, `unavailable` |
| 429 | `capacity_exceeded` |
| 503 | `unsupported`, `host_unavailable` |

An `unavailable` resource conflict is distinct from an unreachable host. Refresh the affected resource before resolving a conflict; preserve request identity while an operation's outcome is uncertain.

[Guest execution](guest.md) documents health, command, terminal, network and private SSH routes. [VM architecture](../vm-platform.md) owns provider, resource and operation implementation; [startup](../startup.md) owns host configuration and service commands.

## Package source maintenance

`POST /machines/:id/actions` additionally accepts `{ requestId, revision, action: "package-source", packageSource: "official" | "aliyun" }`. The optional `packageSource` field is rejected on all other actions. It returns the ordinary 202 machine/operation receipt, with `packageSource` persisted on the operation. The [durable-operation contract](#actions-and-durable-operations) applies. This is an explicit command; creation/Start do not select or change sources.

The VM must be running with no pending configuration and use Lima; AXV guest software is not required. Unsupported providers return `unsupported`; stopped machines return `unavailable`. The operation requires preinstalled Python 3 and supports only Ubuntu 24.04 arm64/amd64. Unsupported OS, missing dependencies or keyring, unsafe file types and custom source layouts fail the operation; basic VM state remains independently observed. It performs no package refresh, installation or upgrade. [Startup](../startup.md#download-mirrors-and-guest-package-sources) owns source URLs, backups and recovery behavior.
