# AXV API

These pages own routes, request/response shapes, limits, errors and caller retry obligations for AXV's desktop service. Read only the affected feature page and the shared rules below. [Project status](../../README.md#project-status) owns platform support and validation status.

## Functional reference

- [Desktop state](desktop.md): Runtime identity, health, bootstrap, preferences, layout and recovery identity.
- [Installed applications](extensions.md): Package installation, sandboxed content, scoped runtime sessions and revision-checked private JSON data.
- [Virtual files](files.md): Entries, revision-checked saves, trash, import, download and preview.
- [Project files and document exchange](project-files.md): Explicit host projects, root-constrained browsing, document links, immutable snapshots, origins and idempotent copies.
- [Host Terminal](host-terminal.md): An ephemeral host shell owned by one WebSocket connection.
- [Host directories](host-directories.md): Shared local directory selection and path validation for host tools.
- [Host Codex](host-codex.md): Conversations, exact process runs and attachments.
- [Compute](compute.md): Environment preparation, service readiness, machines, volumes, images, durable operations and VM project bindings/files.
- [VM project previews](project-previews.md): Explicit guest HTTP/WebSocket services on independent local origins, with exact registration recovery and remote owner inspection/revocation.
- [Guest execution](guest.md): Guest health, commands, detachable guest terminals, network observation and local SSH access.
- [Cloudflare](cloudflare.md): Authenticated desktop origin, local account/configuration wizard, safe tunnel health/traffic, and independently published project services with durable resource ownership.
- [Tailscale remote access](tailscale.md): Optional private HTTPS, enrollment identity, owner authorization, route restrictions and local status.

For component ownership and implementation, see [architecture](../architecture.md), [Codex architecture](../codex.md) and [VM architecture](../vm-platform.md). [Startup](../startup.md) owns configuration and service operation instructions.

## Find the implementation

Follow the route handler into its service for validation and persistence rules; adjacent tests cover contract edge cases.

| Contract | Route and transport entry point | Models and validation |
| --- | --- | --- |
| Desktop and virtual files | [httpapi](../../apps/server/internal/httpapi), [runtime route](../../apps/server/internal/server/server.go) | [storage](../../apps/server/internal/storage) |
| Installed applications | [extensions HTTP adapter](../../apps/server/internal/extensions/http.go), [desktop composition](../../apps/server/internal/server/server.go) | [package validation](../../apps/server/internal/extensions/manifest.go), [extension store](../../apps/server/internal/extensions/store.go) and [sessions](../../apps/server/internal/extensions/manager.go) |
| Host Terminal | [terminal/manager.go](../../apps/server/internal/terminal/manager.go), [frames](../../apps/server/internal/terminal/session.go) | [terminal](../../apps/server/internal/terminal) |
| [Host directories](host-directories.md) | [hostdir/httpapi](../../apps/server/internal/hostdir/httpapi) | [directory validation](../../apps/server/internal/hostdir/directories.go) |
| Project files and document exchange | [projectfiles](../../apps/server/internal/projectfiles) | [registered references, snapshots and transfer receipts](../../apps/server/internal/storage/documents.go) |
| Host Codex | [codex/httpapi](../../apps/server/internal/codex/httpapi), [frames](../../apps/server/internal/codex/httpapi/connection.go) | [codex](../../apps/server/internal/codex), [stored records](../../apps/server/internal/storage/codex_runs.go) |
| VM preparation | [compute/prepare/httpapi](../../apps/server/internal/compute/prepare/httpapi) | [compute/prepare](../../apps/server/internal/compute/prepare) |
| Compute | [desktop gateway](../../apps/server/internal/compute/gateway), [private host API](../../apps/server/internal/compute/httpapi) | [compute](../../apps/server/internal/compute), [controller and persistence](../../apps/server/internal/compute/control) |
| Guest execution | [guest](../../apps/server/internal/guest), [host proxy](../../apps/server/internal/compute/httpapi/api.go) | [guest/protocol](../../apps/server/internal/guest/protocol/protocol.go), [shared guest transport](../../apps/server/internal/compute/guest_client.go) |
| Local guest SSH and network | [private host routes](../../apps/server/internal/compute/httpapi/guest_access.go), [guest/internal/access](../../apps/server/internal/guest/internal/access) | [access types](../../apps/server/internal/guest/protocol/access.go), [stream framing](../../apps/server/internal/sshstream/stream.go) |

## Transport and failures

The Go service exposes `/api` on the same origin as the production UI. Development Vite proxies this prefix to the local server. Compute also exposes its routes under `/v1` on the private host Unix socket, used directly by the VM CLI; the [compute page](compute.md) describes that boundary. SSH metadata, registration and byte-stream upgrades are private-host-only and excluded from the browser gateway.

Application JSON failures use `{ "error": "Readable message", "code": "stable_code" }`; routing and WebSocket-upgrade failures may return non-JSON bodies. Check HTTP status before decoding or displaying a saved state. Interpret error codes in the endpoint's domain: a `409` is not necessarily a revision conflict. WebSocket byte and control-message contracts are documented on each terminal page.

Request shapes, revision rules, idempotency scope and limits belong to their functional pages. In particular, a compute operation's acceptance is not completion, and a lost guest-exec response does not prove that execution failed.

## Access and identity boundaries

The ordinary local desktop listener requires an actual loopback peer and a loopback Host before serving UI or API reads. Forwarded headers do not change this boundary. On that listener, desktop-state and virtual-file mutations validate a supplied Origin against the request Host or a loopback development host; they permit an omitted Origin.

The shared desktop boundary rejects all `/api` requests with `Origin: null` or `Sec-Fetch-Site: cross-site` or `same-site`, including simple GETs without Origin. This prevents sandboxed application frames and different-origin project preview pages from directly using existing API families. Their trusted browser host uses the [installed-application broker](extensions.md#browser-broker) and backend session scope instead.

Host Terminal, host-directory, Codex and compute gateway routes use the shared [access validator](../../apps/server/internal/localaccess/request.go): locally, a loopback peer, a Host of `localhost` or a loopback address, and an exact scheme/Host match when Origin is supplied. Their WebSocket upgrades and mutations require Origin. Forwarded headers do not bypass these checks. The private compute socket is authorized through OS permissions.

The optional remote HTTPS listener authenticates every request, including UI assets and document reads, through the embedded node's `WhoIs`. Only the bound owner's untagged nodes are authorized. Its middleware supplies a private request context to the shared feature guards without changing `RemoteAddr` or trusting identity headers. Remote mutations and WebSocket upgrades require the exact HTTPS Origin; supplied Origin must match on reads too. Preparation, `/api/remote` and Cloudflare configuration/status controls remain local-only. Exact Cloudflare health, traffic and capability reads and the separate publication API explicitly allow the authenticated owner; `/api/connection` supplies a safe desktop summary. See the [Tailscale remote access contract](tailscale.md) for identity binding, route restrictions and failures.

The optional [Cloudflare entrypoint](cloudflare.md) independently verifies Access application JWTs and the configured owner on every request. It accepts a dedicated loopback HTTP hop with the exact public Host and HTTPS Origin, without trusting identity headers or converting remote clients into local users. Both remote transports use the same route policy and feature guards. Owner JWTs have at most a 15-minute signed lifetime and their original expiry closes HTTP/SSE/WebSocket connections without renewal on reconnect; offline JWT verification does not guarantee immediate provider-side Access revocation.

Project-file and document-exchange routes use that same local-access validator, requiring Origin for mutations. Their registered root identity and project-relative paths remain separate from virtual-document IDs and VM project-volume identities.

Installed-application routes use the shared guard with Origin required for mutations. Installation, enable/disable, uninstall and data clearing are local-only; authenticated remote clients may use only the explicit catalog, data-read, content and session/RPC methods in the [application access contract](extensions.md#access). An extension's opaque browser origin is never an authenticated principal.

VM project routes use the compute gateway/private-socket boundary and require the original host identity even for reads. File requests also carry the explicit project/volume/VM/boot/guest-service binding, verified before forwarding and inside the guest. A new binding must be requested as a separate caller decision; a missing VM never causes execution or file access on the host. [Compute projects](compute.md#vm-projects) owns that contract.

Virtual-file entry IDs are not host paths. Host directory selection uses absolute paths on the service's computer; guest commands use project-relative starting directories inside a selected VM. Desktop workspaces do not select an execution target. Runtime instance, database, browser client, host, machine, guest boot, conversation and process-run identities have separate lifetimes and purposes.

Compare terminal disconnect, end and recovery behavior in the [architecture lifetime table](../architecture.md#codex-attachment-and-process-lifetime); each functional API owns its exact endpoint and reconnection contract.
