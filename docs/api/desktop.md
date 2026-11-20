# Desktop state API

[API index](README.md)

Runtime identity, bootstrap, shared preferences and browser-specific desktop state.

## Runtime, health and bootstrap

- `GET /api/runtime`
  - Response: `{ instanceId, launchId }` for the active CLI-managed desktop process. `launchId` identifies a managed launch snapshot, or is an empty string for a foreground desktop.

- `GET /api/health`
  - Optional request header: `X-AXV-Database-ID` to verify the browser’s captured store.
  - Response: `{ status: "ok" }` after a successful database ping. A supplied identity is checked before the ping.

- `GET /api/bootstrap?clientId=...`
  - Request: Stable browser client identifier.
  - Response: `{ databaseId, preferences, layout, layoutError? }`.

The launcher verifies `/api/runtime` against its private runtime record before reusing an HTTP listener, using a loopback HTTP address without proxying or redirects. `instanceId` changes on every process start; `launchId` distinguishes a managed launch from another process competing for the same profile and can survive supervisor restarts of that job. Neither identity is a database identity or authentication token. `/api/health` reports desktop/database health only; VM readiness comes from [`/api/compute/info`](compute.md#host-information).

Bootstrap returns shared preferences and this browser client's layout. Use the same stable `clientId` for bootstrap and layout writes.

## Preferences and browser layout

`PUT /api/preferences` accepts and returns the full preferences object: `{ wallpaper, iconSnap, clock24Hour }`. Bootstrap returns these shared preferences. The wallpaper identifiers are `slate`, `dither` and `solid`; arbitrary CSS is not accepted.

`PUT /api/layout` accepts `{ clientId, layout }`, returning `{ ok: true }`. `layout` is the versioned `NextstepLayout` (`DesktopLayout` alias) exported by the Nextstep package. Use the same `clientId` for bootstrap and writes; generate it once per browser and retain it in local storage. Layout objects are limited to **256 KiB**; writes require a client identifier of **1–128 bytes**. The server validates the JSON object and size; the desktop client validates its structure and version.

Layout version 2 includes optional `desktopApplications: Record<string, boolean>` visibility overrides keyed by application ID. Omitted IDs inherit application and composition defaults. Explicit choices survive missing application definitions and uninstallation; they grant no launch access. Hiding a launcher preserves its entries in `icons` and `compactIcons`. These choices belong to the browser client layout, independently of shared preferences and Dock pinning. Resource icons follow their owning feature's rules.

Each workspace retains its stable ID and may have a custom name. An empty name uses the workspace number as its display label. Layout version 2 ignores obsolete floating menu descriptors while retaining the rest of the saved layout.

If a stored layout cannot be read safely, bootstrap returns `layout: null` and an optional `layoutError` message while preserving the original record. The desktop suspends layout writes until the user explicitly resets it. Unknown layout versions use the same reset flow; they never trigger a database or filesystem reset.

Desktop-state JSON writes require `Content-Type: application/json`, one JSON object and no unknown fields. The shared decoder caps the body at **8 MiB**; the layout limit still applies.

## Database identity and local recovery

`databaseId` is a stable opaque identifier persisted in settings. It remains unchanged across server restarts and is distinct for independently created databases. Desktop-bound browser recovery uses the captured database and browser identities. Each feature owns its recovery keys and target checks.

Every desktop-store resource request carries the `databaseId` captured by its accepted bootstrap in one `X-AXV-Database-ID` header. This includes preferences, layout, registered host projects, and Codex projects/sessions/runs. Bootstrap and Codex capability information are unbound discovery endpoints. Health supports both discovery and a bound connection probe. Identity checks follow access checks and precede request-body decoding, resource reads, filesystem operations and process attachment or creation. A same-database service restart preserves the binding.

The Codex terminal WebSocket may instead supply one `databaseId` query parameter. No other desktop-store endpoint accepts query identity; project-file downloads use the header. Sending both sources, an empty or repeated value, malformed query encoding, or an identity outside 1–128 UTF-8 bytes (including surrounding whitespace, comma, NUL, CR or LF) returns `400 invalid_database_identity`. Missing identity returns `428 database_identity_required`; a different identity returns `409 database_changed`. These failures use the standard JSON error shape and `Cache-Control: no-store`. Old clients without the precondition are rejected rather than assigned the current database.

A loaded page retains its original binding and recovery namespace. On `database_changed`, preserve mounted windows, dirty text and pending writes, stop subsequent desktop-store requests, and discard late responses. Do not re-bootstrap the existing page, rebase drafts or replay pending operations against the new database. Open AXV separately to inspect the current database. Authentication or network failures do not change database identity. This binding does not govern Host Terminal, compute/Guest Terminal/tasks or the independently identified extension store.

The [browser recovery rules](../storage.md#browser-recovery) describe the remaining per-feature draft storage. `/api/layout` carries presentation and resource references.

See [desktop layout persistence](../desktop.md#persistent-layout-and-recovery) for the client layout queue, [database baseline](../storage.md#desktop-database-baseline) for initialization and compatibility, and [startup](../startup.md) for storage locations.
