# Desktop state API

[API index](README.md)

Runtime identity, virtual-document bootstrap, shared preferences and browser-specific desktop state.

## Runtime, health and bootstrap

- `GET /api/runtime`
  - Response: `{ instanceId, launchId }` for the active CLI-managed desktop process. `launchId` identifies a managed launch snapshot, or is an empty string for a foreground desktop.

- `GET /api/health`
  - Response: `{ status: "ok" }` after a successful database ping.

- `GET /api/bootstrap?clientId=...`
  - Request: Stable browser client identifier.
  - Response: `{ databaseId, entries, preferences, layout, layoutError? }`.

The launcher verifies `/api/runtime` against its private runtime record before reusing an HTTP listener, using a loopback HTTP address without proxying or redirects. `instanceId` changes on every process start; `launchId` distinguishes a managed launch from another process competing for the same profile and can survive supervisor restarts of that job. Neither identity is a database identity or authentication token. `/api/health` reports desktop/database health only; VM readiness comes from [`/api/compute/info`](compute.md#host-information).

Bootstrap returns the current [virtual entries](files.md), shared preferences and this browser client's layout. Use the same stable `clientId` for bootstrap and layout writes.

## Preferences and browser layout

`PUT /api/preferences` accepts and returns the full preferences object: `{ wallpaper, iconSnap, clock24Hour }`. Bootstrap returns these shared preferences. The wallpaper identifiers are `slate`, `dither` and `solid`; arbitrary CSS is not accepted.

`PUT /api/layout` accepts `{ clientId, layout }`, returning `{ ok: true }`. `layout` is the versioned `NextstepLayout` (`DesktopLayout` alias) exported by the Nextstep package. Use the same `clientId` for bootstrap and writes; generate it once per browser and retain it in local storage. Layout objects are limited to **256 KiB**; writes require a client identifier of **1–128 bytes**. The server validates the JSON object and size; the desktop client validates its structure and version.

Layout version 2 includes optional `desktopApplications: Record<string, boolean>` visibility overrides keyed by application ID. Omitted IDs inherit application and composition defaults. Explicit choices survive missing application definitions and uninstallation; they grant no launch access. Hiding a launcher preserves its entries in `icons` and `compactIcons`. These choices belong to the browser client layout, independently of shared preferences and Dock pinning. Resource icons follow their owning feature's rules.

If a stored layout cannot be read safely, bootstrap returns `layout: null` and an optional `layoutError` message while preserving the original record and loading file data normally. The desktop suspends layout writes until the user explicitly resets it. Unknown layout versions use the same reset flow; they never trigger a database or filesystem reset.

Desktop-state and virtual-file JSON writes require `Content-Type: application/json`, one JSON object and no unknown fields. The shared decoder caps the body at **8 MiB**; individual layout and file limits still apply.

## Database identity and local recovery

`databaseId` is a stable opaque identifier persisted in settings. It remains unchanged across server restarts and is distinct for independently created databases. Clients namespace local recovery records with this ID, their browser client ID and a unique draft ID, preventing unrelated databases on the same origin from mixing recovery copies.

Recovery is client-owned and follows the [browser recovery rules](../storage.md#browser-recovery) and [file revision contract](files.md#text-saves-and-conflict-handling). `/api/layout` carries presentation and resource references, never draft text.

See [desktop layout persistence](../desktop.md#persistent-layout-and-recovery) for the client layout queue, [database baseline](../storage.md#desktop-database-baseline) for initialization and compatibility, and [startup](../startup.md) for storage locations.
