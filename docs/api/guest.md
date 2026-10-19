# Guest execution API

[API index](README.md)

Commands, detachable terminals and local SSH access run inside one selected VM, independently of host terminals and virtual documents.

Command and terminal routes are nested under `/machines/:id/guest`: the browser base is `/api/compute/machines/:id/guest`, and the private host socket uses `/v1/machines/:id/guest`. They require a machine with running intent and observed running state, a compatible image contract and a matching runtime capability handshake. `exec`, `terminals`, `ssh` and `network` each require their corresponding declared and runtime-confirmed capability; missing capabilities return `unsupported` while basic VM management remains available. The [compute access boundary](README.md#access-and-identity-boundaries) applies. The host injects per-machine guest authentication; browser clients never receive that token. Guest protocol version is **3**, using vsock port **1024** for Firecracker and a private Unix socket for Lima. JSON bodies accept one object with no unknown fields, limited to **64 KiB** unless a route specifies a lower limit. Older guest protocols are rejected; [guest access provisioning](../vm-platform.md#guest-ssh-and-network-access) describes provider upgrade requirements.

## Routes

- `GET /health`
  - Response: `{ version, machineId, bootId, instanceId, ready, capabilities, sshAvailable, reason? }`; `instanceId` changes for each guest-service incarnation.

- `POST /exec`
  - Request: `{ argv, directory?, timeoutSeconds? }`.
  - Response: `{ exitCode, stdout, stderr, truncated }`.

- `GET /terminals`
  - Response: `{ terminals }` for this guest-service lifetime.

- `POST /terminals`
  - Request: `{ requestId, directory?, cols?, rows? }`.
  - Response: `201 { id, bootId, status }`, or `200` for an idempotent retry.

- `GET /terminals/:id`
  - Response: Existing terminal identity/status.

- `DELETE /terminals/:id`
  - Explicitly ends its process group and returns status.

- `GET /terminals/:id/attach` (WebSocket upgrade)
  - Attaches to the existing terminal; disconnect only detaches.

## Command execution

`argv` contains **1–128 arguments** and runs directly without shell interpolation. `directory` defaults to the mounted project root; explicit directories must exist and resolve within it, including after symlink resolution. Processes remain ordinary guest processes and can access other permitted locations after startup.

Timeout is **1–300 seconds**, with omitted/zero meaning **60 seconds**. At most **eight commands** run concurrently. Each stdout/stderr stream retains **64 KiB**; `truncated` reports discarded output. A command's nonzero exit still returns HTTP 200 with `exitCode`. Exec is not idempotent: a lost response does not prove the command did not run, so never automatically retry it.

| Status | Code | Meaning |
| --- | --- | --- |
| 400 | `invalid_request`, `invalid_directory` | Invalid command, timeout or starting directory |
| 408 | `execution_timeout` | Execution deadline elapsed |
| 502 | `execution_failed` | Process could not execute |
| 503 | `execution_limit`, `guest_unavailable` | Command capacity full or guest transport unavailable |

## Terminals

Creation starts a login Bash shell and shares the command directory rules. Dimensions default to **80×24** when omitted/zero, with **2–500 columns** and **1–200 rows**. `requestId` must contain **8–128 bytes**. Reusing it with the same normalized request returns the original terminal; changed parameters return `409 idempotency_conflict`. This guarantee lasts only for the guest-service lifetime.

There can be **16 live sessions** and **1024 total records**, with `503 terminal_limit` at capacity. Status is `running` or `ended`. Only one attachment per terminal is allowed (`409 terminal_attached` when occupied).

| Direction | Message | Payload |
| --- | --- | --- |
| Server → client | Text | `{ ready: true, bootId, replayTruncated }` before output |
| Server → client | Binary | PTY output, beginning with up to the latest **1 MiB** retained output (**16 KiB** for an ended terminal) |
| Server → client | Text | `{ ended: true, message }` after the terminal ends |
| Client → server | Binary | Raw terminal input, at most **32 KiB** per message |
| Client → server | Text | `{ resize: [cols, rows] }` using the creation size limits |

Guest Terminal windows retain their original `hostId` along with machine, terminal and boot identity. Desktop requests send `X-AXV-Host-ID`; browser WebSocket connections include `hostId` in their query. A mismatched current host binding is rejected rather than redirecting an old terminal to a replacement service.

Disconnect detaches; DELETE ends the process group. Output backlog can close an attachment with code **1013**; reconnect to the same terminal to replay the retained tail. Missing IDs return `404 terminal_lost`. `bootId` identifies the VM boot, not a guest-service incarnation: a service restart can lose all terminal records without changing it. Reconnect must look up the original terminal and never call creation to replace a missing one.

## Project file protocol

VM project files use separate private guest `POST /v1/project-files/{inspect,list,read,write,mkdir}` routes. They require the guest token, confirmed `project-files` capability and a writable mounted project volume. The browser/CLI calls [compute project routes](compute.md#vm-projects), never these guest routes directly. File access is additive to guest protocol **3** and does not enable itself on images that omit the capability.

Each request carries `{ root, rootIdentity?, bootId, instanceId }`: volume-relative root, registered filesystem identity, Linux boot and guest-service incarnation. `inspect` verifies an existing directory and returns `{ root, rootIdentity }`; only initial inspection can omit the root identity. Root identity hashes the opened directory's opaque [Linux filesystem file handle](https://man7.org/linux/man-pages/man2/open_by_handle_at.2.html) together with its filesystem type and persistent filesystem ID, preserving identity on the retained filesystem across VM replacement without accepting a deleted root's reused inode or an unrelated filesystem's matching handle. The mount/device ID is excluded because it changes across mounts; ext4 derives the filesystem ID from its volume UUID. This requires kernel `CONFIG_FHANDLE` and filesystem export-handle support (provided by the guest's ext4 project volumes); unsupported systems return `unsupported`, without falling back to an inode or timestamp. Other operations require the registered identity, revalidate the mounted volume/root and reject old boot/service/root identities. List/read/mkdir add `path`; list also accepts `cursor?`, `limit?`; write adds `content` and required `expectedSha256`. [Compute projects](compute.md#vm-projects) owns the shared path, text, pagination and response fields/limits.

Private requests require `application/json`. JSON limits are **64 KiB**, with an **8 MiB** write envelope and separate **1 MiB** decoded text limit. Malformed UTF-8 and unpaired escaped Unicode surrogates are rejected before decoding so request paths and content cannot be silently rewritten. Write content must be present and non-null; an explicit empty string clears a reviewed file. Queries, unknown fields and trailing objects are rejected. The guest opens the volume/project through `os.Root`, verifies filesystem identities, rejects final symlinks and special files and confines ancestor resolution to the root. Writes and mkdir serialize within the guest service. A write stages content in the destination directory, fsyncs it, checks the original root/parent and expected file identity/hash again, then publishes atomically. Create-only publication cannot overwrite a racing new entry; replacement creates a new inode so other hard-link aliases are not modified. The parent is synced and the published digest is checked before acknowledgement. Unrelated guest tools do not share the service lock, so they can still race with replacement or change an acknowledged file later.

An uncertain response must be inspected rather than retried automatically; failed acknowledgement can follow successful publication. Disk/quota exhaustion returns `507 disk_full`; conflict and unavailable state stay visible. This file API does not impose a sandbox on guest exec, SSH or other guest processes. [`project_files.go`](../../apps/server/internal/guest/project_files.go), its platform file-opening helpers and nearby tests own enforcement.

## Boundaries

Private guest `POST /v1/shutdown` is used only by the host lifecycle controller and is not exposed by the gateway. It requires guest authentication and returns `202 { accepted: true }` after the guest's supervisor command succeeds; command failure or its three-second deadline returns `503 shutdown_failed`. Acceptance does not confirm that the VM has exited: the host still observes provider shutdown. [VM ownership](../vm-platform.md#guest-execution) describes provider-specific shutdown. [Virtual-document routes](files.md) and their opaque entry IDs retain their separate contract. Project-file routes are separate from command and terminal lifetime/permission contracts.

See [VM architecture](../vm-platform.md) for guest ownership and restart behavior. [Host Terminal](host-terminal.md) and [host Codex](host-codex.md) run on the host and use different process-lifetime contracts.

## SSH access

These routes exist only on the private host socket, outside `/guest`. The desktop gateway rejects SSH routes; there is no browser or remote SSH listener. All three routes require `X-AXV-Host-ID` matching the original host, a machine belonging to that host, and running intent/observed state.

| Method | Private host route | Request and response |
| --- | --- | --- |
| GET | `/v1/machines/:id/ssh` | `{ version, hostId, machineId, bootId, available, user, hostKey?, reason? }` |
| POST | `/v1/machines/:id/ssh/clients` | `{ bootId, clientId, publicKey }` → the confirmed normalized registration |
| GET | `/v1/machines/:id/ssh/attach?bootId=...` | HTTP upgrade to the framed SSH byte stream |

Metadata identifies user `axv` and a plain Ed25519 host key when available. Before registration or attachment, the host verifies the requested boot against guest health; the guest verifies it again. A host mismatch returns `409 binding_changed`, and a changed boot returns `409 boot_changed`. Missing boot identity or an invalid upgrade returns `400 invalid_request`. Metadata protocol or machine mismatches return `503 guest_identity_mismatch`; unavailable guest transport or a failed attachment health check returns `503 guest_unavailable`.

Registration accepts one JSON object of at most **4 KiB**. `clientId` contains **8–128 ASCII letters, digits, underscores or hyphens**. `publicKey` is at most **2048 bytes** and must contain exactly one plain Ed25519 key, without authorized-key options or a certificate. The dedicated registry holds at most **128 clients** and **64 KiB**. Re-registering the same key is idempotent; replacing one client ID updates only that client's key. Unknown or malformed existing entries are preserved and produce `503 ssh_registration_failed`, not a rewritten registry.

Attachment requires `Connection: Upgrade` and `Upgrade: axv-ssh-v1`, returning **101** on success. Each frame has a one-byte type and four-byte unsigned big-endian payload length: type **1** carries **1–32768 bytes**, while type **2** with zero length closes only that sending direction. Invalid frames or transport loss close the connection. This preserves SSH stdin EOF and final output across both providers. The standard SSH protocol runs inside these frames; AXV does not replace SSH authentication, channels or exit-status messages. The [sshstream implementation](../../apps/server/internal/sshstream/stream.go) owns framing and half-close behavior.

There are at most **32 simultaneous SSH streams** per guest (`503 ssh_connection_limit`). Connection setup has bounded deadlines; established streams have no AXV duration limit. Guest-service shutdown closes its streams. SSH sessions are ordinary `axv` processes, with no detachable-terminal IDs or replay/recovery. SFTP and TCP forwarding use the dedicated SSH server; remote forwarding binds guest loopback. Agent forwarding, X11 and tunnels are disabled. Normal guest filesystem permissions apply; SSH is not confined to the project directory. The [CLI workflow](../startup.md#ssh-file-transfer-and-forwarding) owns local keys, host-key pinning and client commands.

## Network observation

`GET /v1/machines/:id/network`, also forwarded as `/api/compute/machines/:id/network`, returns `{ version, hostId, machineId, bootId, observedAt, provider, addresses }`. It requires running intent/observed state and authenticates the current guest. It never starts a VM. A supplied `X-AXV-Host-ID` must match the host.

Each address contains `{ interface, address, prefixLength, family, reachability }`, where family is `ipv4` or `ipv6`, and reachability contains `{ from, state, reason }`. The guest reports at most **256** current global-unicast addresses on active interfaces, including private addresses but excluding loopback and link-local addresses. `observedAt` and `bootId` bound the observation; addresses are not durable connection identities.

Reachability is classified from the deployment host's perspective. Provider-known isolated guest addresses are `unreachable`; unverified addresses are `unknown` with reason `not_verified`. The result does not claim that an address is reachable from the browser, another device or the public internet. Use SSH's private transport for access instead of inferring connectivity from a reported address. Observation failures return `503 network_unavailable` or `guest_unavailable`.
