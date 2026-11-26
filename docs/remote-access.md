# Remote access and service publishing

This guide owns operator setup, configuration and CLI workflows for private remote desktop access (**Tailscale**) and external tunnel access / project service publishing (**Cloudflare**). Both transports are optional and disabled by default; local access remains primary.

API contracts, authorization checks and failure codes belong to [Tailscale API](api/tailscale.md), [Cloudflare API](api/cloudflare.md) and [shared access boundaries](api/README.md#access-and-identity-boundaries). Visible UI actions belong to [Using AXV](usage.md#remote-desktop); live acceptance belongs to [acceptance](acceptance.md#live-tailscale-and-cloudflare-acceptance).

## Tailscale

AXV embeds its own [tsnet](https://tailscale.com/docs/features/tsnet) node and serves the production desktop over HTTPS on macOS and Linux. It does not require the system Tailscale service, does not run `tailscale up`, and does not change host DNS, routes or firewall rules. Keep system Tailscale stopped on hosts where the tailnet CGNAT range conflicts with system DNS servers.

### Enable via desktop or CLI

- **Desktop:** Open **Tailscale** from the desktop icon, **Workspace → Applications** or **Preferences**, then choose **Enable**. When status is `needs-login`, choose **Authorize** and sign in with the workstation owner's Tailscale account.
- **CLI (local or headless):**

```bash
axv remote status [--json]
axv remote enable [--hostname axv-workstation] [--owner-user-id 12345]
axv remote disable
```

When the desktop is running, `axv remote enable` starts the embedded node immediately and prints the authorization URL (`Authorize: ...`) or ready HTTPS URL. When stopped, it persists `remote.enabled: true` in `AXV_HOME/config.json` for the next launch. Local desktop readiness never waits for remote access.

### Profile configuration

Optional `remote` settings in `AXV_HOME/config.json`:

```json
{
  "remote": {
    "enabled": false,
    "hostname": "axv-workstation",
    "owner_user_id": 12345
  }
}
```

- `hostname`: lowercase DNS label (1–63 characters, default `axv`). Choose distinct names across profiles.
- `owner_user_id` (optional): requires a specific positive numeric Tailscale user ID before first enrollment.
- Editing `config.json` does not mutate a running node: disable and re-enable to apply hostname or owner constraints. Existing owner bindings are preserved; changing the enrolled owner or node requires explicit maintenance while the desktop is stopped.

### Prerequisites, credentials and shutdown

- Enable **MagicDNS** and **HTTPS certificates** in [Tailscale DNS settings](https://login.tailscale.com/admin/dns) and allow your devices to reach this node on TCP 443. Certificate issuance publishes the `.ts.net` name in certificate transparency logs without making the service public.
- First enrollment binds the stable untagged owner and node IDs under `AXV_HOME/tailscale/` (credentials, certificates and owner binding). Treat this directory as credentials; do not copy it across profiles or reset an invalid binding silently.
- Certificates renew in the background while a valid cached certificate stays active. An expired certificate or changed identity withdraws the remote listener until resolved and retried locally.
- **Disable** immediately disconnects remote browsers (ending their Host Terminal shells and detaching Codex views while independent Codex runs and VMs continue), preserves node credentials and leaves local clients open. VM preparation and remote configuration remain local-only operations.

## Cloudflare Access

Cloudflare is an optional alternative for browser access without a Tailscale client and can coexist with Tailscale. AXV supplies an authenticated loopback origin; you operate Cloudflare Access and an external `cloudflared` connector on the same machine.

### Desktop Access setup

1. Create a self-hosted [Cloudflare Access application](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/self-hosted-public-app/) protecting the **entire** desktop hostname with an explicit owner policy and MFA (no Bypass/public policies). Set the application and every matching Allow policy session duration to **at most 15 minutes**; AXV rejects longer signed JWTs. Record the team domain and application audience (`AUD`).
2. Obtain your stable Access user ID (`sub` claim in the application token — see [Cloudflare's application-token reference](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/application-token/)). Inspect tokens locally; never paste JWTs into online decoders, AXV config or diagnostics. AXV never enrolls the first visitor.
3. Open **Cloudflare → Setup → Desktop access** locally to validate and save settings, then enable access in **Overview**. Equivalent `AXV_HOME/config.json` block for manual provisioning:

```json
{
  "cloudflare": {
    "enabled": false,
    "listen": "127.0.0.1:3002",
    "origin": "https://axv.example.com",
    "issuer": "https://your-team.cloudflareaccess.com",
    "audience": "your-access-application-aud",
    "owner_subject": "your-stable-access-user-id"
  }
}
```

`listen` defaults to `127.0.0.1:3002` (numeric loopback IP and port; pick a distinct port per profile). `origin` is a canonical lowercase HTTPS DNS origin (no port/path/query/fragment). `issuer` is `https://<team>.cloudflareaccess.com` without a trailing slash.

4. Point your externally managed `cloudflared` tunnel to the loopback listener while preserving the public `Host` header (do not route to the ordinary local desktop port or rewrite Host/Origin to localhost):

```yaml
ingress:
  - hostname: axv.example.com
    service: http://127.0.0.1:3002
  - service: http_status:404
```

5. Once status shows **Listening locally**, verify from another browser that the owner can access the desktop, unauthenticated/other users fail, and local-only controls stay rejected. Inspect local status with `axv cloudflare status [--json]`.

Editing settings requires disabling and re-enabling the entrypoint locally. Disable closes only Cloudflare clients; local and Tailscale sessions stay open. Per the [Cloudflare connection contract](api/cloudflare.md#connection-lifetime), owner connections terminate at the original JWT's expiry (max 15 minutes) without renewal on reconnect. Host SSH/firewall exposure remains administrator-owned.

## Cloudflare publishing

Optional Cloudflare account integration publishes explicitly selected VM project HTTP services on isolated hostnames, independently of [local project previews](api/project-previews.md).

### Account setup

Prepare a Cloudflare zone, an existing remotely managed Tunnel and a running `cloudflared` connector on the AXV host. Create a scoped API token with **DNS Edit** (selected zone), **Cloudflare Tunnel Edit** (selected account), read access for zone/tunnel selection, and optional **Zone Analytics Read** for traffic (see [Cloudflare API permissions](https://developers.cloudflare.com/fundamentals/api/reference/permissions/)).

Use **Cloudflare → Setup → App publishing → Connect account** locally to validate and save the configuration. The API token is stored only in private `AXV_HOME/config.json` and never returned to browsers:

```json
{
  "cloudflare": {
    "management": {
      "api_token": "your-scoped-api-token",
      "account_id": "your-account-id",
      "zone_id": "your-zone-id",
      "tunnel_id": "your-existing-tunnel-id",
      "domain": "example.net",
      "listen": "127.0.0.1:3003",
      "audience": "your-publishing-access-application-aud"
    }
  }
}
```

### Allocation domain and certificates

- `domain` must be an **allocation suffix**: either a dedicated registrable domain (e.g. `example.net`, distinct from the desktop's `example.com`) or an existing browser-recognized Public Suffix List suffix inside your zone. Ordinary subdomains like `apps.example.com` are rejected.
- Each publication gets a generated first-level hostname `<name>-<32-hex-generation>.<domain>` and permanently reserves its cookie site, even after Unpublish. A normal registrable domain therefore serves one publication for its lifetime. See [browser isolation and hostname lifetime](api/cloudflare.md#browser-isolation-and-hostname-lifetime) for PSL, cookie-proof and client rules.
- Ensure edge SSL covers the generated hostname depth (Cloudflare Universal SSL covers apex + first-level subdomains).
- `listen` defaults to `127.0.0.1:3003` and must differ from the desktop listener. For owner-only publications, configure an Access application (max 15-minute session) for the generated hostnames and set its `AUD` as `management.audience`. Explicit **Public** visibility requires no `audience`.

### Publishing workflow and CLI

Publish from **Cloudflare → Publishing** (or **Publish** beside a project preview) or via the CLI against the running local desktop:

```bash
axv cloudflare config
axv cloudflare check --force
axv cloudflare traffic --hours 24
axv cloudflare publications
axv cloudflare publish PROJECT_ID --port 3000 --name editor --domain example.net
axv cloudflare unpublish PUBLICATION_ID
axv cloudflare retry PUBLICATION_ID
```

- Visibility defaults to `owner`; pass `--visibility public` for public visitors. `--binding-file` accepts a captured `axv vm project binding` file.
- For recovery after an uncertain publish response, inspect `publications` first; replaying with `--request-id` also requires the original binding file, `--domain` and journal `--instance-id`.
- `axv cloudflare configure --file SETTINGS.json` (or `--file -` for stdin) applies a reviewed configuration snapshot without putting tokens in shell arguments.
- **Unpublish** immediately withdraws local access before cleaning DNS/ingress rules without stopping the VM process. After a desktop restart, retained publications stay closed until explicitly **Resumed** against their original target. Use **Rotate token** to replace credentials without removing publications. Complete cleanup of all publications before changing account or routing settings.
