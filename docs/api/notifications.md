# Work notifications API

[API index](README.md)

Consumes content-free observations from the [Codex runner](../codex.md#provider-activity) and [compute task owner](compute.md#vm-project-tasks) for optional Pocket Alert and browser Web Push delivery. Observation never approves requests, cancels tasks or controls execution. Implementation: [`internal/notifications`](../../apps/server/internal/notifications), composed in [`server/notifications.go`](../../apps/server/internal/server/notifications.go).

## Intake and storage

- Owns `AXV_HOME/notifications/state.json` (`0700` dir, `0600` files, exclusive owner lock, persistent `storeId`, independent config revisions, credentials, inclusive producer checkpoints and bounded outbox: max **512 records**, **4,096 checkpoints**, **4 MiB** file). Invalid/unknown state fails closed without reset.
- Desktop polls producer journals on startup and ~every 10s, publishing records and checkpoints atomically. Missing journals, retention gaps or replaced stores surface as explicit gaps — never inferred outcomes.
- Both adapters are disabled by default; enabling or changing credentials/destination starts a new observation boundary and cancels queued sends from the prior scope without recalling already accepted alerts.

## Routes and access

| Method | Route | Access & Contract |
| --- | --- | --- |
| GET | `/api/notifications` | Local/owner; snapshot (`storeId`, `revision`, `canConfigure`, safe `config`, `events`, `intake`, `nextSendAt?`, `error?`; remote omits account config) |
| POST | `/api/notifications/config` | Local + Origin; revision/store-checked Pocket Alert config (`tokenAction: keep\|replace\|clear`; never returns token) |
| POST | `/api/notifications/test` | Local + Origin; enqueue synthetic Pocket Alert test (`{ storeId, revision }`) |
| POST | `/api/notifications/disable` | Local + Origin; disable Pocket Alert and cancel pending sends |
| GET | `/api/notifications/:id?storeId=...` | Local/owner; exact stored event and read-only target resolution (never substitutes newer work) |
| GET | `/api/notifications/push` | Local/owner; public VAPID key/contact and subscription metadata with SHA-256 capability fingerprint (omits endpoints/secrets) |
| POST | `/api/notifications/push/prepare` | Local/owner + Origin; generate/prepare VAPID sender keys with explicit vendor-visible `contact` |
| POST | `/api/notifications/push/subscribe` | Local/owner + Origin; register browser PushManager subscription bound to verified request origin (max 16) |
| POST | `/api/notifications/push/test` | Local/owner + Origin; synthetic push test for one exact subscription |
| POST | `/api/notifications/push/disable` | Local/owner + Origin; pause future sends for one subscription |
| POST | `/api/notifications/push/unsubscribe` | Local/owner + Origin; remove exact server subscription record |
| POST | `/api/notifications/push/rotate` | Local + Origin; rotate VAPID key/contact, invalidating prior subscriptions and pending sends |

Strict JSON rules (max 64 KiB, no unknown/duplicate fields, valid Unicode). Supported event kinds: `agent_waiting_approval`, `agent_waiting_input`, `agent_succeeded`, `agent_failed`, `agent_interrupted`, `task_succeeded`, `task_failed`, `task_cancelled`, `task_interrupted`, `task_unknown` (success events opt-in separately).

Error codes: `400` (`invalid_notification_request`, `push_service_unsupported`), `404` (`notification_unavailable`), `409` (`notification_conflict`), `503` (`notification_store_unavailable`).

## Delivery, uncertainty, and return links

- **Best-effort delivery:** Dispatcher runs only while the desktop service runs (no always-on sidecar). Each record writes a durable `sending` receipt before HTTP I/O (10s deadline, redirects disabled, max **3 attempts**). Lost responses become `uncertain` and may duplicate on bounded retry; `accepted` proves provider receipt only, never device display.
- **Pocket Alert:** Sends fixed status text + timestamp + opaque return URL to `https://api.pocketalert.app/v1/messages` (no prompts, commands, filenames or credentials). HTTP `429` pauses the adapter for the bounded `Retry-After` cooldown (1 min – 1 day).
- **Return links:** `/?notification=<storeId>:<recordId>` on an exact configured HTTPS origin (or loopback HTTP for local testing). Grants no authority; normal workstation auth applies and **Open original work** opens a read-only view bound to the recorded run/attempt.

## Browser Web Push

- **Allowed vendors & transport:** Supports `fcm.googleapis.com`, `updates.push.services.mozilla.com` and dot-boundary subdomains of `push.apple.com` only (HTTPS default port, valid P-256 + 16-byte auth keys, public-IP pinned dial, no env proxies/IP literals/redirects).
- **Encrypted minimal payloads:** RFC 8291 ciphertext (≤ 2 KiB plaintext) carrying only protocol version, opaque IDs and expiry (15-min TTL, max 3 attempts; 404/410 retires subscription). No work content or private workstation URLs.
- **Narrow service worker:** `/axv-push/worker.js` (scope `/axv-push/`, registered only on explicit subscribe, authenticated, `no-store`, `connect-src 'none'` CSP) handles push display and notification click only — zero fetch interception, response caching, offline writes or background polling. Physical iPhone/iPad background delivery requires [Web Push acceptance](../acceptance.md#web-push-acceptance).
