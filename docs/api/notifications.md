# Work notifications

Notifications consume content-free observations from the independent [Codex runner](../codex.md#provider-activity) and [compute task owner](compute.md#vm-project-tasks). They do not own execution, approvals, task cancellation or provider conversations. The implementation is [notifications](../../apps/server/internal/notifications), composed in [server/notifications.go](../../apps/server/internal/server/notifications.go); browser controls belong to the dashboard notifications feature.

## Intake and storage

The desktop owns `AXV_HOME/notifications/state.json`, including a random persistent store identity, configuration revision, credential, checkpoints and a bounded outbox/history. Its identity is independent of desktop process incarnations and the desktop database. The directory is private (`0700`), state/lock files are owner-only (`0600`), and an exclusive owner lock prevents concurrent dispatchers. Foreign ownership, unsafe files, missing established state, oversized or malformed/ambiguous JSON and unknown versions are preserved and rejected. A notification-store failure disables this feature with a visible error while other desktop work remains usable.

Sources retain their own bounded journals. Desktop intake runs on startup and approximately every ten seconds. Checkpoints use each source's original store identity and inclusive last consumed sequence. Records and their corresponding checkpoints publish in one atomic state snapshot. Duplicate reads cannot enqueue the same observation twice. Source loss, replacement, reversed cursors, producer write failures and retention gaps remain visible; missing events never become inferred outcomes. Old runners without journals provide an explicit observation gap, not reconstructed history.

The outbox retains at most 512 records and 4,096 source checkpoints, within a 4 MiB state file. Completed records can be evicted to admit new observations; a full pending outbox or source-checkpoint limit produces a visible gap. This is bounded recoverability, not indefinite event retention. A stale return link whose record has been evicted is unavailable.

Pocket Alert observations are eligible while that adapter is enabled. Initial enable and destination/credential changes start a new observation boundary; observations from disabled intervals are not sent on re-enable. Changing credentials, application, device, return origin or enabled state cancels queued sends under the previous scope. Deselecting a kind cancels that kind's queued sends. Other queued records retain their exact targets. Already accepted alerts cannot be recalled.

## Routes and access

| Method | Route | Contract |
| --- | --- | --- |
| GET | `/api/notifications` | Snapshot with `storeId`, configuration `revision`, `canConfigure`, safe `config`, newest-first `events`, `intake`, optional `nextSendAt` and safe `error`. |
| POST | `/api/notifications/config` | Save the reviewed configuration using the original `storeId` and `revision`. |
| POST | `/api/notifications/test` | Enqueue one synthetic test for the saved enabled configuration, using `{ storeId, revision }`. |
| POST | `/api/notifications/disable` | Disable and release pending delivery, using `{ storeId, revision }`. |
| GET | `/api/notifications/:id?storeId=...` | Return the exact stored event and its current read-only resolution; never substitute another event, run or attempt. |

Reads use the existing local access checks or authenticated remote-owner authority. Pocket Alert configuration, credential changes, tests and disable are machine-local and require the exact Origin. Browser subscription routes below have their own owner-authorized remote admission. Remote snapshots omit account/configuration details and expose `canConfigure: false`. There is no public inbound provider webhook, phone-control endpoint or unauthenticated notification route.

POST requires one JSON object, `application/json`, no query, no unknown/duplicate fields, no trailing data, valid Unicode and at most 64 KiB. Configuration accepts `{ storeId, revision, enabled, tokenAction, token?, applicationId, deviceId, returnOrigin, eventTypes }`. `tokenAction` is `keep`, `replace` or `clear`; only replace accepts a nonempty token. Blank input never silently clears a credential. Responses expose only `tokenConfigured`, never the token. Configuration revision changes only for configuration writes, so delivery progress cannot overwrite an edited form.

Event kinds are `agent_waiting_approval`, `agent_waiting_input`, `agent_succeeded`, `agent_failed`, `agent_interrupted`, `task_succeeded`, `task_failed`, `task_cancelled`, `task_interrupted` and `task_unknown`. Successful agent turns and tasks are separately opt-in. Unknown task observations remain unknown until the compute owner reconciles them. A later observation coalesces obsolete agent waiting records; dispatch also rechecks the exact original target before sending.

Errors are fixed safe categories: invalid input (`400 invalid_notification_request`), unsupported browser push service (`400 push_service_unsupported`), replaced store or stale configuration (`409 notification_conflict`), missing history (`404 notification_unavailable`) and unavailable storage (`503 notification_store_unavailable`). Provider response text, native paths and credentials are not diagnostic strings.

## Delivery and uncertainty

Closing the browser leaves the desktop dispatcher running. Stopping the desktop postpones dispatch; restarting reconciles the producer journals. There is no separate always-on notification process. The worker sends sequentially, with a ten-second HTTP deadline, redirects disabled and at most three attempts per record. A durable `sending` receipt precedes the request. If the process stops or a response is lost, the result is `uncertain`; bounded retries may duplicate an accepted alert. Completion-write failures retain and retry that exact outcome before further intake, without abandoning a sending record or changing its target.

History states are `pending`, `sending`, `accepted`, `uncertain`, `failed`, `disabled` and `superseded`. `accepted` means the provider accepted the API request, not that a phone received it. Uncertain retries use bounded backoff; their `nextAttemptAt` is visible. A 429 pauses the entire adapter until the bounded provider Retry-After period (at least one minute, at most one day); a future `nextSendAt` reports that shared cooldown. Attempts remain bounded after restarts.

The optional Pocket Alert adapter uses the fixed `https://api.pocketalert.app/v1/messages` endpoint, an API-key `Token` header and normal priority. It sends a fixed status title, observation timestamp and an opaque return URL in the message text. Application/device IDs are optional; omission uses the provider's account defaults. It does not send prompts, commands, approval bodies, transcripts, filenames, file content or credentials in a payload or history message. Provider response bodies are discarded.

Pocket Alert's [message API](https://info.pocketalert.app/api/messages/create.html) documents 201 acceptance, 429 quota rejection and Retry-After. Its [plans](https://pocketalert.app/pricing) currently advertise 50 daily Free messages; limits are provider-owned and mutable. Paid action buttons and critical alerts are not required: the return URL is ordinary message text. AXV does not enroll accounts, upgrade plans or guarantee push delivery.

## Return links

The configured return origin must be an exact HTTPS origin, or HTTP on loopback for local testing. It cannot contain credentials, a path, query or fragment. The link is `/?notification=<notification-store-id>:<record-id>`; these opaque IDs grant no authority. Normal workstation authentication still applies.

Opening a link first shows its original notification details. Explicit **Open original work** opens a separate read-only inspection using the recorded Codex session/run or host/project/task/attempt. Resolved requests, old attempts, missing history and replaced producer stores are labelled accurately. Links and inspection can only read: they cannot approve, cancel, dismiss, continue or submit commands, and cannot replace existing editor or task drafts with newer work.

## Browser Web Push

Web Push consumes the same exact producer events independently of Pocket Alert configuration, token or eligibility. Its optional state lives inside the existing private notification store: an independent configuration revision, VAPID key generation/contact and at most 16 subscriptions. Each shared notification has separate bounded per-subscription delivery receipts. Pocket Alert's existing status and retries remain specific to that adapter. Dispatch progress cannot overwrite either adapter's configuration revision. Enrollment begins a new observation boundary; disabled intervals and old events are never backfilled to a new subscription.

| Method | Route | Contract |
| --- | --- | --- |
| GET | `/api/notifications/push` | Safe store/revision, public key/generation/contact and subscription metadata, including a SHA-256 capability fingerprint. Never returns endpoints, authentication keys or the private key. |
| POST | `/api/notifications/push/prepare` | Explicitly prepare sender keys with `{ storeId, revision, contact }`. Reads never generate keys. |
| POST | `/api/notifications/push/subscribe` | `{ storeId, revision, keyId, installationId, label, eventTypes, subscription: { endpoint, keys: { p256dh, auth } } }`; exact origin comes from verified request authority. |
| POST | `/api/notifications/push/test` | Synthetic test for `{ storeId, revision, keyId, installationId, subscriptionId }` only. |
| POST | `/api/notifications/push/disable` | Stop future sends for that exact subscription; does not revoke browser permission or retract accepted messages. |
| POST | `/api/notifications/push/unsubscribe` | Remove the exact server record after explicit browser unsubscribe. |
| POST | `/api/notifications/push/rotate` | Machine-local key/contact replacement using `{ storeId, revision, contact }`; cancels old pending receipts and invalidates subscriptions. |

These exact reads and subscription mutations are admitted through authenticated owner transports. Mutations additionally require one exact Origin and the same strict JSON/body protections as Pocket Alert. Rotation remains machine-local. Browser-provided installation IDs and labels grant no authority; store/revision/key/subscription/origin comparisons fence stale requests. Changing the notification store or sender key does not transfer an existing browser grant or endpoint. Explicit re-subscription is required. On return and before a test, the browser compares its actual PushManager endpoint/keys fingerprint and application-server key; changed or missing subscriptions require explicit reconciliation, without submitting replacement capabilities automatically. The fingerprint is derived from the endpoint, p256dh and auth strings separated by NUL bytes; it is computed for safe snapshots and is not persisted as a second source of truth. Store corruption/replacement remains fail-closed, without automatic reset.

The sender supports `fcm.googleapis.com`, `updates.push.services.mozilla.com` and dot-boundary subdomains of `push.apple.com`, following [Chromium's endpoints](https://chromium.googlesource.com/chromium/src/+/refs/tags/148.0.7734.4/components/push_messaging/push_messaging_constants.cc), [Mozilla's service contract](https://mozilla-services.github.io/autopush-rs/http.html) and [Apple's guidance](https://developer.apple.com/documentation/usernotifications/sending-web-push-notifications-in-web-apps-and-browsers). Endpoint paths are opaque; unknown vendors are unsupported. HTTPS/default port, valid uncompressed P-256 subscription keys and 16-byte authentication secrets are required. All resolved addresses must be public; dialing is pinned to validated addresses with normal TLS hostname verification. Environment proxies, IP literals and redirects are rejected. Contact URLs are validated but never fetched.

Payloads use RFC 8291 encryption and contain only protocol version, opaque store/key/installation/subscription/event IDs and expiry. No work content, command, approval body, filename, credential or private workstation URL is included. The explicit contact is vendor-visible in VAPID authorization; endpoint and authentication material are private store data, not diagnostics. The provider boundary bounds plaintext to 2 KiB, discards response bodies and returns fixed safe outcomes.

Receipts expire after 15 minutes and receive at most three attempts. HTTP 201 means vendor acceptance; 404/410 retire a subscription. Rate limits pause that subscription with bounded backoff; transient/lost responses remain uncertain and can duplicate delivery. A durable sending receipt precedes network I/O, and interrupted sends reconcile as uncertain after restart. Sender shutdown pauses delivery rather than creating an always-on process. Disabling cannot recall vendor-queued or displayed notifications.

The worker at `/axv-push/worker.js` is registered only by explicit enrollment and scoped to `/axv-push/`. Its script is authenticated, `no-store` and restricted by CSP from network connections. It has push/display and notification-click behavior without fetch interception, cache storage, offline application writes or background workstation requests. Public binding metadata and at most one pending opaque click reference are held separately from document/layout recovery. The reference never carries commands or protected content. Display needs no AXV fetch; click validates the binding and sends only a narrow opaque event reference to an existing matching AXV window, preserving dirty views, or opens the authenticated return link. Expired, invalid or mismatched payloads show a generic access-review alert without selecting work.

Browser/OS permission and vendor connectivity remain external. [WebKit](https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/) requires a Home Screen app and an explicit permission gesture on iPhone/iPad. Removing a server record does not revoke permission. Physical background receipt, lock-screen display and tailnet-disconnected/reconnect behavior require the [device acceptance path](../development.md#web-push-acceptance); automation does not establish those claims.
