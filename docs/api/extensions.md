# Installed application API

[API index](README.md)

Installed applications use a separate catalog, immutable HTML packages and one private JSON document per application. The [extensions service](../../apps/server/internal/extensions) owns validation, persistence and runtime sessions; the [desktop application feature](../../apps/dashboard/src/features/extensions) owns the browser broker and recovery UI. These endpoints do not address virtual documents, host files or execution targets. [Storage ownership](../storage.md#installed-application-storage) and [desktop integration](../desktop.md) describe those boundaries.

## Access

All routes use the shared [request guard](README.md#access-and-identity-boundaries). Local requests require a loopback peer and Host; a supplied Origin must match exactly. POST, PUT and DELETE require Origin. Authenticated remote requests may read the catalog, saved data and current content, create or release sessions and call storage RPCs. Installation, enable/disable, uninstall and data clearing remain local-only. The remote listener explicitly allows these runtime route shapes and methods; it does not enable the entire API prefix for future operations.

The desktop rejects `/api` requests with `Origin: null` or `Sec-Fetch-Site: cross-site` before dispatch. This includes simple browser GETs that omit Origin. Forwarded identity headers do not authorize a request. The trusted desktop calls these APIs on an application's behalf; its backend session ID is never passed to extension code.

After reading the catalog, every operation except that catalog read and content navigation must supply exactly one `X-AXV-Extension-Store` header containing the UUID captured for the operation. A missing or duplicate header returns `400 invalid_extension_request`; a different UUID returns `409 extension_store_changed` before reading or changing application state. Content navigation instead carries this identity in its URL. The store UUID is an identity, not an authorization credential. Preserve the original scope across asynchronous confirmation, session creation, writes and retries; a changed store requires reviewing the new catalog rather than substituting its UUID. Identical application IDs, digests, generations or revisions in another store do not authorize retargeting old operations or recovery drafts.

## Package and catalog

A package is one JSON object:

```json
{
  "manifest": {
    "schemaVersion": 1,
    "id": "local.checklist",
    "title": "Checklist",
    "version": "1.0.0",
    "apiVersion": 1,
    "capabilities": ["storage"],
    "icon": "checklist"
  },
  "html": "<!doctype html><title>Checklist</title>"
}
```

The ID is at most 96 ASCII characters and must match `^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`. It is independent of the display title. The title has 1–80 Unicode characters, no surrounding whitespace and no control characters. A version has 1–64 ASCII characters and matches `^[A-Za-z0-9][A-Za-z0-9.+_-]{0,63}$`. Both schema and API versions must be `1`; the capability list must be exactly `["storage"]`. Unknown fields, differently cased field names and duplicate JSON keys are rejected, including within nested request objects.

`icon` is optional and selects host-owned artwork by the safe name `document` or `checklist`. Omission uses the desktop's `document` default and preserves an existing package's canonical bytes and digest. An explicitly declared icon is part of the package digest; changing it requires the usual generation-checked installation. Empty, null or unknown values, markup and image URLs are rejected. Packages cannot supply SVG or other icon content.

HTML must be nonempty UTF-8 without NUL and at most **512 KiB**. The service encodes the parsed package to canonical JSON and limits that representation to **1 MiB**; its lowercase SHA-256 is the package `digest`. Installation accepts at most 1 MiB plus 4096 bytes for the complete request envelope. Code is stored by digest and has no mutation endpoint; startup verifies the complete retained packages and their digests before accepting requests. Packages are self-contained, precompiled HTML; there is no bundle-directory or multi-file asset route.

`GET /api/extensions` returns:

```json
{
  "storeId": "ad5414e8-8ec9-4535-94d1-32f2a6f2521a",
  "apps": [
    {
      "id": "local.checklist",
      "title": "Checklist",
      "version": "1.0.0",
      "apiVersion": 1,
      "capabilities": ["storage"],
      "icon": "checklist",
      "digest": "<64 lowercase hexadecimal characters>",
      "generation": 1,
      "enabled": true,
      "installed": true
    }
  ]
}
```

`storeId` is the persistent UUID of this extension store, separate from the virtual-document database identity. Results include uninstalled records so saved data and restored windows keep their original application identity. Each record includes `icon` only when declared by its package, using the same safe names. The catalog does not include HTML or user data.

## Local management

| Method and route | Request | Result |
| --- | --- | --- |
| `POST /api/extensions/install` | `{ package, expectedGeneration? }` | `201 { app }` for a new ID; `200 { app }` for an existing ID |
| `PUT /api/extensions/:id` | `{ enabled, expectedGeneration }` | `{ app }` |
| `DELETE /api/extensions/:id?generation=N` | No body | `{ app }` with `installed: false` and `enabled: false` |
| `DELETE /api/extensions/:id/data?revision=N` | No body | `{ value: null, revision }` |

A new installation must omit `expectedGeneration`. Replacing any existing record, including an uninstalled one, requires its exact current generation. Installation starts at generation 1; every successful installation, enable/disable or uninstall advances it, including an installation of the same digest. Upgrading an installed application preserves its enabled state; reinstalling an uninstalled application enables it. A generation mismatch returns `409 extension_conflict` without changing the installation.

Management changes invalidate all older runtime sessions. Uninstall retains the catalog record, code and saved JSON document. Data clearing is a separate explicit action, allowed only while the application is uninstalled, and requires the exact saved revision. It writes JSON `null` and increments that revision; it does not delete the application's identity. Query-based mutations accept exactly one parameter of the documented name.

Management responses may be lost after committing. Read the catalog or saved document and review the resulting state before repeating an action; never substitute a new generation or revision automatically.

## Content and runtime sessions

`POST /api/extensions/:id/sessions` accepts:

```json
{
  "digest": "<current package digest>",
  "generation": 1,
  "clientId": "<browser client ID>",
  "windowId": "<desktop window ID>"
}
```

The application must still be installed and enabled with that exact digest and generation in the store selected by the request header. Client and window IDs must each be 1–128 bytes without surrounding whitespace or control characters. The response is `201 { sessionId, contentUrl }`, where `contentUrl` is `/api/extensions/:id/content/:storeId/:digest`, with no credential or query string. Session IDs are random 256-bit values held in server memory and by the trusted browser host. Each session captures its store UUID, application, digest, generation, client and window. Multiple frames can hold independent sessions even when their client and window IDs match.

Sessions expire **30 minutes** after creation; activity does not extend them. The service allows at most **256** live sessions. Expired entries are reclaimed when sessions are created or used. Restarting the desktop loses all sessions. Disable, uninstall and upgrade invalidate earlier generations immediately through the persisted scope check, as well as removing their in-memory session entries. `DELETE /api/extensions/:id/sessions/:sessionId` releases one session and returns `{ ok: true }`.

`GET /api/extensions/:id/content/:storeId/:digest` returns only the currently installed, enabled digest from that exact store. A different UUID returns `409 extension_store_changed`; disabled, uninstalled or replaced content returns `404 extension_not_found`. This navigation needs no custom header because the path fixes its store scope. The response uses `text/html; charset=utf-8`, `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: SAMEORIGIN` and this policy:

```text
sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; connect-src 'none'; frame-src 'none'; worker-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'
```

The host also creates the iframe with `sandbox="allow-scripts"`, without `allow-same-origin`; the response's own sandbox applies when the content URL is opened directly. The desktop retains `frame-ancestors 'none'` and adds `frame-src 'self'` to constrain its child frames. Inline scripts and styles are allowed; network connections, external assets, nested frames, workers and forms are denied by their corresponding policies. This is a browser capability boundary, not a general CPU, memory or network isolation guarantee.

## Application data

`GET /api/extensions/:id/data` reads the saved document for export or inspection, including after uninstall:

```json
{ "value": null, "revision": 0 }
```

Each application starts with JSON `null` at revision 0. Its document may contain any JSON value up to **128 KiB**. Code and data occupy different tables; installation, upgrade, disable and uninstall do not replace the data.

The trusted host calls `POST /api/extensions/:id/sessions/:sessionId/rpc` with either:

```json
{ "method": "storage.get" }
```

or:

```json
{
  "method": "storage.set",
  "params": { "value": { "items": [] }, "expectedRevision": 0 }
}
```

Both return `{ value, revision }`. `storage.get` accepts no parameter object. `storage.set` requires both fields; revision comparison and the write occur in one transaction, together with the final installed/enabled/digest/generation and storage-grant checks. The session's store UUID must also match this store. A successful save increments the revision. Two frames saving against the same revision cannot overwrite each other: one receives `409 revision_conflict`. Cross-application session URLs and revoked or expired sessions return `409 extension_session_expired`.

Keep the captured value and original expected revision after failures or uncertain responses. Read the saved document to inspect an unknown outcome; do not retry by silently replacing the expected revision. The browser's [draft and update handling](../desktop.md) preserves newer typing and requires a user choice before discarding or replacing a draft.

## Browser broker

The iframe receives a dedicated `MessagePort` from its captured parent frame with `{ axv: "connect", version: 1, nonce }`. It acknowledges over that port with `{ type: "ready", nonce }`. The host requires the fresh nonce and never authorizes a document solely because its origin is `null`. The initial host message is `{ type: "init", visible, interactive, draft? }`; lifecycle changes use `{ type: "lifecycle", visible, interactive }`.

Application requests are `{ type: "request", id, method, params? }`; replies are `{ type: "response", id, result }` or `{ type: "response", id, error: { code, message } }`. Request IDs match `^[A-Za-z0-9._-]{1,64}$`, and at most 128 requests may be pending. The available methods are `storage.get`, `storage.set`, `draft.set` with `{ value, expectedRevision }`, and `window.focus`. Draft updates remain in the trusted host; only storage methods reach backend RPC. Focus applies only while the frame is visible and interactive. Saved and discarded notifications carry `{ type: "saved" | "discarded", value, revision }`.

A rejected draft or save payload, including oversized data, keeps the host's unsaved-work protection active rather than exposing an older valid draft as the latest edit. The rejected payload remains in memory for possible JSON export; it is not written to browser recovery or the backend. Save, reload, close and management actions remain blocked until a valid draft replaces it or the user explicitly discards it. Browser unload also warns; [desktop recovery](../desktop.md) owns the interaction details.

The broker exposes no full `DesktopAPI`, host paths, execution, arbitrary HTTP relay or credentials. Navigation, replacement and window disposal close the port; old asynchronous replies cannot attach to a newer connection. There is no offline write queue, background service, package dependency loader or multi-file ESM/CORS contract.

## Failures

All extension responses are uncached. JSON writes require `Content-Type: application/json`; body limits also apply before decoding. Runtime session creation accepts 4096 bytes, storage RPC accepts 128 KiB plus 4096 bytes, and enable/disable accepts 4096 bytes.

| Status | Code | Meaning |
| --- | --- | --- |
| 400 | `invalid_extension_request`, `invalid_extension_package`, `invalid_extension_data`, `invalid_extension_session`, `invalid_extension_rpc` | Malformed, unsupported or oversized input |
| 403 | `extension_forbidden` | The feature request guard or grant rejects the operation |
| 403 | `sandbox_request_forbidden` | The shared desktop boundary rejects an opaque-origin or cross-site API request |
| 404 | `extension_not_found` | The application, data or current content is unavailable |
| 409 | `extension_conflict` | A management generation is stale, or data clearing requires uninstall |
| 409 | `revision_conflict` | The expected saved revision is stale |
| 409 | `extension_session_expired` | The session is missing, expired, revoked or bound to another application/version |
| 409 | `extension_store_changed` | The captured store UUID differs from the current store; retain the draft and review the new store before continuing |
| 429 | `extension_session_limit` | The live-session limit was reached |
| 500 | `extension_unavailable` | An internal operation failed; no internal path or database detail is returned |

The remote transport can reject a request before feature dispatch with its own authorization, origin, local-only or route errors; see [Tailscale access](tailscale.md).
