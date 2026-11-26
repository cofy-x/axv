# Cloudflare access and publishing API

[API index](README.md)

Optional Cloudflare Tunnel + Access desktop entrypoint and isolated VM project service publishing. [Remote access](../remote-access.md#cloudflare-access) owns operator setup; [`internal/cloudflare`](../../apps/server/internal/cloudflare) (`access`, `desktop`, `publish`, `client`, `policy`, `management`, `httpapi`) and [`server/cloudflare_controller.go`](../../apps/server/internal/server/cloudflare_controller.go) own implementation.

## Authentication and authorization

- Every desktop request (UI assets and APIs) requires a valid `Cf-Access-Jwt-Assertion` (`type: app`, RS256 verified against the team's HTTPS JWKS with bounded caching/no redirects/no env proxy, exact issuer, single audience and configured owner `sub`). Never enrolls the first visitor or trusts forwarded headers/cookies alone.
- Accepts only actual loopback peers and exact configured public HTTPS `Host` (exact HTTPS `Origin` required on mutations/WS upgrades and whenever supplied on reads). Strips Access headers/cookies before feature dispatch.
- Shares [`server/remote.go`](../../apps/server/internal/server/remote.go) route policy with Tailscale: remote setup, VM preparation, extension administration, `/api/runtime`, `/v1` and local previews remain local-only (`403 remote_local_only` / `404`).

## Connection lifetime

- Signed JWT lifetime (`exp - iat`) must be **≤ 15 minutes** (longer tokens rejected even with seconds remaining). Every HTTP, SSE and WebSocket stream terminates at its original signed expiry without renewal on reconnect.
- Offline JWT verification does not promise instantaneous provider-side logout/revocation; cached valid keys may verify unexpired short tokens during a signing-key outage, while unknown/expired/overlong tokens fail closed.
- Disabling or expiry disconnects only Cloudflare clients (ending Host Terminal shells, detaching Codex views while runs/VMs and local/Tailscale clients continue). Dashboard JSON requests send AJAX markers so Access returns `401` rather than redirecting away from dirty drafts.

## Local status and control

| Method | Route | Authority and contract |
| --- | --- | --- |
| GET | `/api/cloudflare/status` | Local only; `{ instanceId, enabled, configured, state, url?, listen?, error? }` (`state`: `disabled\|starting\|listening\|error\|stopped`) |
| PUT | `/api/cloudflare` | Local + exact Origin; `{ enabled: boolean }` (persists `cloudflare.enabled` under profile lock before toggling live listener) |
| GET | `/api/connection` | Local or authenticated owner; includes safe `cloudflare` summary (omits local listener and raw diagnostics) |

`listening` confirms only AXV's loopback origin, never public tunnel/Access reachability. Changing settings other than `enabled` requires disable then enable. Failed close blocks restart until desktop restart (`409 cloudflare_close_failed`).

## Configuration and account checks

| Method | Route | Authority and contract |
| --- | --- | --- |
| GET | `/api/cloudflare/capabilities` | Local or owner; `{ configure, publish, traffic, health }` (`configure: true` local only) |
| GET | `/api/cloudflare/config` | Local only; `{ instanceId, revision, access, account }` (`hasToken`, never returns token) |
| PUT | `/api/cloudflare/config` | Local + exact Origin; revision/instance-checked complete snapshot (`tokenAction: keep\|replace\|clear`); requires desktop Access disabled and all publications removed |
| PUT | `/api/cloudflare/token` | Local + exact Origin; validate and rotate API token without disabling access or removing publications |
| POST | `/api/cloudflare/wizard` | Local + exact Origin; validate setup step (`access\|token\|account\|zone\|tunnel\|domain`) without mutating Cloudflare |
| GET | `/api/cloudflare/health` | Local or owner; cached 1 min (`?force=true` bypasses); `{ state, checkedAt, message, tunnelName?, tunnelStatus? }` |
| GET | `/api/cloudflare/traffic?hours=24` | Local or owner; zone-wide GraphQL analytics for `1\|6\|12\|24\|168\|720` completed hours (never fabricates zeroes on provider error) |

API client restricts calls to the official Cloudflare HTTPS endpoint (no proxies/redirects, bounded size/pagination) and sanitizes errors.

## Published project services

Durable VM project publications live in `AXV_HOME/cloudflare/publications.json` (experimental journal version **2**; invalid/older journals fail closed without reset), independently of [local previews](project-previews.md).

| Method | Route | Authority and contract |
| --- | --- | --- |
| GET | `/api/cloudflare-publications` | Local or owner; `{ instanceId, supported, reason?, domain?, isolation?, publications }` |
| POST | `/api/cloudflare-publications` | Local or owner + Origin + `X-AXV-Host-ID`; `202` `{ instanceId, requestId, binding, port, name, domain, visibility }` (`owner` default or `public`) |
| POST | `/api/cloudflare-publications/:id/unpublish` | Local or owner + Origin + `X-AXV-Host-ID`; revoke access immediately, then clean DNS/ingress |
| POST | `/api/cloudflare-publications/:id/retry` | Local or owner + Origin + `X-AXV-Host-ID`; retry original publish/resume or pending cleanup |

### Browser isolation and hostname lifetime

- **Exclusive cookie sites:** `management.domain` must be an exact registrable domain or browser-recognized PSL suffix (including PRIVATE rules); ordinary subdomains like `apps.example.com` are rejected. Every publication gets `<name>-<32-hex-generation>.<domain>` (ports 1024–65535; max 64 active / 4096 retained receipts) and **permanently reserves** its cookie site and hostname even after Unpublish (also excluded from future desktop origins).
- **Cookie-suffix compatibility proof:** Reserved `/__axv/isolation` intercepts initial top-level browser visits to verify host-only cookie support and PSL parent-cookie rejection before issuing a 15-minute host-only proof (not auth; invalidated on restart). Rejects service worker registration, forces `Cache-Control: no-store`, rewrites proxied `Set-Cookie` host-only, strips Access/AXV headers and permits verified non-browser clients (`Go-http-client`, `curl`, `axv`, or UA-less GET/HEAD without Origin/Upgrade).
- **Fail-closed lifecycle & cloud ownership:** Unpublish closes local access and live HTTP/SSE/WS streams _before_ persisting removal or cleaning DNS/ingress (preserving unrelated tunnel rules via random AXV DNS ownership markers; foreign resources conflict rather than being adopted). Desktop restart leaves retained routes closed (`unavailable` / `restart-required`) until explicit **Resume** against the original binding; recorded removal is irreversible and can only retry cleanup.
