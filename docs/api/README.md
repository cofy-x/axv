# AXV API

These pages own routes, request/response shapes, limits, errors and caller retry obligations for AXV's local service. Read only the affected feature page and the shared rules below. [Project status](../../README.md#project-status) owns platform support and validation status; the service has no remote-user authentication layer.

## Functional reference

- [Desktop state](desktop.md): Runtime identity, health, bootstrap, preferences, layout and recovery identity.
- [Virtual files](files.md): Entries, revision-checked saves, trash, import, download and preview.
- [Project files and document exchange](project-files.md): Explicit host projects, root-constrained browsing, document links, immutable snapshots, origins and idempotent copies.
- [Host Terminal](host-terminal.md): An ephemeral host shell owned by one WebSocket connection.
- [Host directories](host-directories.md): Shared local directory selection and path validation for host tools.
- [Host Codex](host-codex.md): Conversations, exact process runs and attachments.
- [Compute](compute.md): Environment preparation, service readiness, machines, volumes, images and durable operations.
- [Guest execution](guest.md): Guest health, commands, detachable guest terminals, network observation and local SSH access.

For component ownership and implementation, see [architecture](../architecture.md), [Codex architecture](../codex.md) and [VM architecture](../vm-platform.md). [Startup](../startup.md) owns configuration and service operation instructions.

## Find the implementation

Follow the route handler into its service for validation and persistence rules; adjacent tests cover contract edge cases.

| Contract | Route and transport entry point | Models and validation |
| --- | --- | --- |
| Desktop and virtual files | [httpapi](../../apps/server/internal/httpapi), [runtime route](../../apps/server/internal/server/server.go) | [storage](../../apps/server/internal/storage) |
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

Desktop-state and virtual-file mutations validate a supplied Origin against the request Host or a loopback development host; they permit an omitted Origin.

Host Terminal, host-directory, Codex and compute gateway routes use the shared [local-access validator](../../apps/server/internal/localaccess/request.go): a loopback peer, a Host of `localhost` or a loopback address, and an exact scheme/Host match when Origin is supplied. Their WebSocket upgrades and mutations require Origin. Forwarded headers do not bypass these checks. The private compute socket is authorized through OS permissions; this is not remote-user authentication.

Project-file and document-exchange routes use that same local-access validator, requiring Origin for mutations. Their registered root identity and project-relative paths remain separate from virtual-document IDs and VM project-volume identities.

Virtual-file entry IDs are not host paths. Host directory selection uses absolute paths on the service's computer; guest commands use project-relative starting directories inside a selected VM. Desktop workspaces do not select an execution target. Runtime instance, database, browser client, host, machine, guest boot, conversation and process-run identities have separate lifetimes and purposes.

Compare terminal disconnect, end and recovery behavior in the [architecture lifetime table](../architecture.md#codex-attachment-and-process-lifetime); each functional API owns its exact endpoint and reconnection contract.
