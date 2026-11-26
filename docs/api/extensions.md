# Installed application API

[API index](README.md)

Manages self-contained sandboxed HTML extensions and revision-checked private JSON storage, owned by [`internal/extensions`](../../apps/server/internal/extensions) (`extensions.sqlite`, format **AXE2**) and [`features/extensions`](../../apps/dashboard/src/features/extensions). See [extension storage](../storage.md#installed-application-storage) and [desktop host integration](../desktop.md#installed-extensions).

## Access

- Uses the [shared request guard](README.md#access-and-identity-boundaries) (`Origin: null` and cross-site `/api` calls rejected; mutations require exact Origin). Installation, enable/disable, uninstall and data clearing are **local-only**; authenticated remote owners may read catalog/data/content and manage runtime sessions/storage RPCs.
- Every route except `GET /api/extensions` and content navigation requires `X-AXV-Extension-Store: <store-uuid>` (`409 extension_store_changed` on mismatch); content URLs pin `:storeId` in the path. Never retarget operations or drafts across stores.

## Routes

| Method | Route | Access & Contract |
| --- | --- | --- |
| GET | `/api/extensions` | Local/owner; `{ storeId, apps }` (includes uninstalled records; omits HTML and user data) |
| POST | `/api/extensions/install` | Local; `{ package, expectedGeneration? }` → `201`/`200 { app }` (omit `expectedGeneration` for new ID; exact match required to replace) |
| PUT | `/api/extensions/:id` | Local; `{ enabled, expectedGeneration }` → `{ app }` |
| DELETE | `/api/extensions/:id?generation=N` | Local; uninstalls (`installed: false, enabled: false`), retains code and saved data |
| DELETE | `/api/extensions/:id/data?revision=N` | Local; clears saved JSON to `null` (allowed only while uninstalled; increments revision) |
| GET | `/api/extensions/:id/data` | Local/owner; `{ value, revision }` (readable even after uninstall) |
| POST | `/api/extensions/:id/sessions` | Local/owner; `{ digest, generation, clientId, windowId }` → `201 { sessionId, contentUrl }` |
| DELETE | `/api/extensions/:id/sessions/:sessionId` | Local/owner; release session → `{ ok: true }` |
| GET | `/api/extensions/:id/content/:storeId/:digest` | Local/owner; serves current enabled HTML with strict sandbox CSP |
| POST | `/api/extensions/:id/sessions/:sessionId/rpc` | Local/owner; `{ method: "storage.get" }` or `{ method: "storage.set", params: { value, expectedRevision } }` |

## Package, session, and data invariants

- **Package validation:** `{ manifest: { schemaVersion: 1, id, title, version, apiVersion: 1, capabilities: ["storage"], icon? }, html }`. Strict JSON (no unknown/duplicate keys); `id` reverse-domain ASCII ≤ 96 chars; `icon` optional `document|checklist` only; `html` ≤ 512 KiB UTF-8; canonical JSON ≤ 1 MiB (SHA-256 = `digest`). Packages are immutable and digest-verified on startup.
- **Sessions & sandbox CSP:** Sessions are 256-bit in-memory tokens (max **256** live, fixed **30-minute** non-sliding expiry, lost on desktop restart, immediately invalidated by management generation changes). Content responses enforce `sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; connect-src 'none'; frame-src 'none'; worker-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'` plus `X-Frame-Options: SAMEORIGIN` and `Cache-Control: no-store`.
- **Application data:** One JSON document per app (starts `null` at revision `0`, max **128 KiB**). `storage.set` checks revision, store UUID, installed/enabled state and generation in one transaction (`409 revision_conflict` / `extension_session_expired`). Never auto-bump `expectedRevision` after uncertainty.

## Browser broker

- Trusted host transfers a dedicated `MessagePort` via `{ axv: "connect", version: 1, nonce }` → `{ type: "ready", nonce }` (never trusting `origin: null` without the fresh nonce), then sends `init` / `lifecycle` (`visible`, `interactive`, `draft?`) and `saved` / `discarded` notifications.
- Extension methods (max 128 pending requests): `storage.get`, `storage.set`, `draft.set` (host-local only) and `window.focus` (visible+interactive only). No `DesktopAPI`, host paths, execution or network relay.
- Rejected/oversized drafts stay blocked in memory for JSON export (never written to recovery or server) until fixed or explicitly discarded.

## Failures

All responses are `no-store`. Error codes map cleanly: `400` (`invalid_extension_*`), `403` (`extension_forbidden`, `sandbox_request_forbidden`), `404` (`extension_not_found`), `409` (`extension_conflict`, `revision_conflict`, `extension_session_expired`, `extension_store_changed`), `429` (`extension_session_limit`), `500` (`extension_unavailable`). Exact validation rules live in [`internal/extensions`](../../apps/server/internal/extensions).
