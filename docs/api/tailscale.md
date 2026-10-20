# Tailscale remote access API

[API index](README.md)

The optional embedded Tailscale listener serves the workstation over private HTTPS. [Startup](../startup.md#remote-access) owns configuration and enrollment. The [transport](../../apps/server/internal/tailnet) and [authentication middleware](../../apps/server/internal/remoteaccess) own connection lifecycle and authorization; feature APIs retain their existing data and execution contracts.

## Identity and authorization

On first explicit enrollment, an untagged node's stable numeric user ID and stable node ID are bound in the profile's private Tailscale state. An optional configured `owner_user_id` must match. Subsequent starts reject a changed owner or node identity; missing or invalid state is never silently reset or rebound.

Every request resolves its actual connection address using that embedded node's `WhoIs`. AXV authorization requires a known, unexpired, untagged peer with a stable node ID and the bound owner's user ID. Jailed nodes and nodes restricted to the peer API are rejected. Names, email addresses, source address ranges, forwarded headers and `Tailscale-*` headers do not establish identity. Tailscale enforces [device approval](https://tailscale.com/docs/features/access-control/device-management/device-approval) and grants/ACLs before traffic reaches AXV; the optional `MachineAuthorized` field in a peer's `WhoIs` response is not an independent approval signal. Membership of the same tailnet alone does not authorize the AXV desktop. There is no multi-user sharing or public Funnel listener.

Active requests, including WebSockets, recheck the original peer every five seconds with a five-second lookup timeout. A failed lookup, loss of authorization or changed stable node ID cancels only that request. This prevents an existing stream from retaining application access after a peer is removed, retagged or replaced. Detection depends on Tailscale delivering the updated network map before AXV's next check; it does not promise immediate revocation during a control-plane outage.

HTTPS Host must exactly match the enrolled `.ts.net` origin. A supplied Origin must exactly match on every method; mutations and WebSocket upgrades require it. There is no CORS allowance, proxy-based identity or plaintext fallback. The private authentication context is tied to the actual peer, TLS connection and Host; existing feature guards accept it while keeping raw loopback checks available for local-only operations.

## Routes and lifecycle

Authenticated remote access includes the production UI, virtual documents and desktop state, Host Terminal, host directory selection, host project files and document exchange, Codex and the browser compute gateway. These capabilities operate with the deployment account's existing permissions. Remote access does not sandbox host shells or host project paths.

VM preparation and remote status/configuration are local-only and return `403 remote_local_only`. Launcher identity (`/api/runtime`), private host routes (`/v1`) and unknown API families are excluded. Guest SSH keys, metadata and raw streams remain excluded by the compute gateway. New API families must be explicitly admitted to the [remote route policy](../../apps/server/internal/server/remote.go).

Local and remote clients share the same storage and feature managers, including the four-shell Host Terminal limit and exact Codex run identities. Losing the remote entrypoint cancels only its clients: Host Terminal shells end, Codex views detach, guest terminals and accepted VM operations retain their feature-owned lifetimes. A changed self identity or HTTPS domain withdraws the listener and its clients. Local desktop readiness does not depend on remote login, certificates or connectivity.

TLS handshakes use a validated cached certificate. A separate, bounded renewal worker refreshes certificates nearing expiry while an existing valid certificate remains usable. Renewal failure appears as a diagnostic in local status. An expired certificate withdraws the remote listener and requires explicit local retry after correcting the cause; it never falls back to an expired, self-signed or unverified certificate.

Authorization failures return `403 remote_forbidden`; identity lookup timeout returns `503 remote_identity_unavailable`; unknown remote routes return `404 not_found`. During shutdown, new requests return `503 remote_stopping`. JSON failures use the shared `{ error, code }` shape.

## Local status

`GET /api/remote/status` requires the raw loopback peer/Host checks, even from an authenticated remote owner. Response:

```json
{
  "instanceId": "desktop-...",
  "enabled": true,
  "supported": true,
  "state": "ready",
  "url": "https://axv-workstation.example.ts.net",
  "ownerUserId": "123456"
}
```

States are `disabled`, `starting`, `needs-login`, `needs-https`, `ready`, `error` and `stopped`. `authUrl`, `url`, `ownerUserId` and `error` appear when relevant. An enrollment URL is a bearer capability: status is not cached and the URL is not written to AXV's persistent operational log. `axv remote status` verifies the active local runtime identity before displaying this response. `enabled` reports the saved runtime switch and `supported` reports macOS/Linux availability. Status describes the live remote controller; its profile settings are independent of the desktop launch snapshot.

## Local control

`PUT /api/remote` requires the raw loopback peer/Host and exact local Origin, even from an authenticated remote owner. Its body contains only `{ "enabled": true }` or `{ "enabled": false }`. Missing/null/non-boolean values, unknown fields, trailing JSON and bodies over 4096 bytes return `400 invalid_remote_request`. Successful responses return the same `remote` object as the connection summary below.

The controller serializes changes, uses the shared profile command lock and preserves unrelated/unknown configuration fields. It saves only `remote.enabled` before changing live transport state. A save failure returns `500 remote_config_failed` without changing the running node; an occupied profile lock returns `409 remote_config_busy`. Repeated enable is idempotent for a running node. If enabled but errored/stopped, an explicit enable retries with the current profile settings and existing identity. Disable closes only the remote transport and retains node credentials. No request controls the system Tailscale service or changes host networking.

Node start failures remain visible as `error` in local status; saving an enabled preference is not proof of readiness. A failed close returns `409 remote_close_failed` and blocks another start until desktop service restart, avoiding overlapping nodes. A stopping controller rejects changes with `503 remote_stopping`. Unsupported servers return `501 remote_unsupported` when enabling. An ambiguous interrupted request is not automatically replayed: read status before deciding to retry. All failures use `{ error, code }`.

## Desktop connection summary

`GET /api/connection` accepts the shared local or explicitly authenticated remote access checks. It returns `{ local, remote }` for the Tailscale utility:

- `local` is true only when the original loopback peer/Host/Origin checks pass. A remote identity does not make this true.
- `remote` contains `enabled`, `supported`, the embedded node's `state`, an optional verified ready HTTPS `url`, and a safe `message`. It excludes enrollment URLs, owner IDs and raw operational errors. A renewal warning retains the ready address and supplies a fixed attention message.

The application does not detect the host's system Tailscale installation. The desktop icon is a stable launcher regardless of installation or readiness.

The dashboard polls the summary every five seconds while the Tailscale panel is active; Preferences only opens the panel. Failed checks show unknown current states and mark retained details as stale. Only an active local panel reads `/api/remote/status` for enrollment or error details; its bearer URL remains panel-local and is cleared when the view becomes inactive or enrollment state changes. Status and control responses use `Cache-Control: no-store`.
