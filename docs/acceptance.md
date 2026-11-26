# Native, device, and live acceptance

This guide owns manual, physical-device, native-hypervisor and real-provider acceptance procedures. Routine development, automated CI and required checks belong to [development](development.md#verification). Current platform and validation claims belong to [project status](../README.md#project-status).

Keep isolated profiles, synthetic data and evidence under this checkout's `.data/test` root (or `AXV_TEST_TMPDIR`), following [test isolation](development.md#test-isolation-and-diagnostics). Record commit SHA, environment, commands and results in the task or PR; never treat fixture tests or unavailable hardware as native acceptance.

## Online browser installation acceptance

The [browser installation workflow](startup.md#browser-installation) targets desktop Chrome/Edge and physical iPhone Safari over the enrolled [Tailscale HTTPS listener](remote-access.md#tailscale). Use current stable browser versions and record exact OS/browser versions in the task or PR. Playwright viewports and simulated standalone media queries do not establish OS installation or physical-device acceptance.

| Scenario | Automated coverage | Actual installation / device check |
| --- | --- | --- |
| Install | Served HTML/manifest fields, MIME, embedded PNG dimensions, access checks and absence of service worker registration | Browser menu, OS icon, icon cropping and standalone window; iPhone Share workflow |
| Online reopen | Ordinary bootstrap and unchanged manifest identity across service restarts | Cold launch and existing-window activation from the installed icon; Tailscale HTTPS access |
| Authorization expires | Rejected requests remain visible without discarding drafts; remote `/api/runtime` stays denied | Independent authorized device, revoke/restore access; installed authentication container behavior |
| Network loss | Failed saves remain dirty; local draft download succeeds without API access; no request replay | Disconnect/reconnect Wi-Fi or Tailscale, lock screen, background suspension and system termination |
| Upgrade | Old module failure retains other mounted editors and local Retry; fresh navigation uses current assets | Replace production build A with B (different asset hashes) at the same address/database while a dirty editor is open; verify no forced refresh and explicit new-page loading |
| Database/target changes | Old page blocks layout writes to a replacement desktop store while preserving mounted work and VM drafts | Restart an isolated profile and switch databases explicitly while an installed window remains open |
| Multiple windows | Browser client-ID initialization, copied session storage, VM draft binding and unavailable storage | Compare installed and ordinary tabs, independent profiles and repeated installation |

Use separate production executables and profiles under the test root for upgrade and database-switch checks; stop only their owned processes before switching. Preserve the old page, recovery records and original execution identifiers while observing failure.

The reproducible Chromium online-reopen harness verifies same-address upgrades (requiring two retained production builds with the same database format and different Terminal chunk hashes) and explicit database switching:

```sh
node scripts/verify-online-reopen.mjs \
  --before .data/test/build-a/axv --before-report .data/test/build-a/dashboard-bundle.json \
  --after .data/test/build-b/axv --after-report .data/test/build-b/dashboard-bundle.json
```

Reports and screenshots stay in `reopen-evidence-*` under the test root. Retain each binary's source revision and bundle report with the inputs.

To regenerate the 192/512 installation PNGs and 180 Apple touch icon from [`public/favicon.svg`](../apps/dashboard/public/favicon.svg), run `node scripts/render-install-icons.mjs` with pinned Playwright Chromium installed, and review the output at small sizes and under platform icon masks.

## Web Push acceptance

[Browser push](usage.md#browser-push-notifications) adds an installed-client delivery path. Automated checks cover subscription admission, encrypted bounded metadata, vendor destination/DNS/redirect rules, outbox receipts, expiry/restart and worker behavior; DevTools push emulation does not establish vendor or OS delivery.

Use a fresh isolated production profile with synthetic work only. For physical devices, use that profile's separately enrolled owner-authorized Tailscale HTTPS origin (see [physical browser acceptance](#physical-browser-interaction-acceptance)). Never expose a test listener publicly, copy production browser data or enroll real work just to obtain a push endpoint.

1. Open Notifications before opting in; confirm no worker registration, permission prompt, subscription or sender key was created. Exercise unsupported and denied permission paths separately.
2. On desktop Chrome/Edge and a physical iPhone/iPad Home Screen app, review the vendor-visible contact, select synthetic event types, explicitly subscribe and send a synthetic test. Record vendor acceptance separately from actual background/lock-screen display.
3. Keep AXV running while backgrounding/closing the installed client. Disconnect only the receiving device's Tailscale connection (keeping Internet/vendor push reachable), send a synthetic test from another authenticated owner client, verify the minimal alert arrives without a private-origin fetch, test clicking while disconnected, then reconnect Tailscale and reopen the original notification (verifying exact target resolution without selecting newer work).
4. With a dirty synthetic VM project editor open, click a notification and verify the mounted editor and draft remain intact. Confirm the worker has no fetch listener, protected-response cache, command action, offline writes or background AXV polling.
5. Test browser unsubscribe and server disable separately, including partial network failures. With the isolated sender stopped, verify notification-store replacement, explicit key rotation and changed origin never adopt an old endpoint or target.
6. Verify TTL expiry, bounded retries and 404/410 retirement with controlled fixtures. Clean up only test-owned subscriptions, worker registrations and isolated processes; retain sanitized evidence without endpoints, keys or provider bodies.

## Physical browser interaction acceptance

Use an owner-controlled physical iPhone Safari, iPad Safari or Android Chrome for focused checks of [narrow-screen and touch rules](design.md#narrow-screens-and-accessibility). Playwright mobile emulation does not establish native gestures, software keyboard behavior or device permissions.

1. Build `dist/axv` with `make build`. Create a fresh private directory under the test root with explicit `AXV_HOME`, desktop data dir, `AXV_WORKSPACE_DIR` and empty `CODEX_HOME` (clearing inherited `DATA_DIR` and `AXV_COMPUTE_SOCKET`). Launch `serve --addr 127.0.0.1:0`. Keep compute disabled for desktop/Host Terminal checks; for editor checks, use that profile's own disposable VM and enhanced Guest.
2. Enable that profile's own [Tailscale HTTPS node](remote-access.md#tailscale) with a distinct hostname and open the HTTPS address on the physical device with normal certificate validation. On Linux, capture and compare the [host network baseline](#remote-host-network-verification) before and after. Do not use plain LAN HTTP, rewrite Host/Origin, or weaken browser security.
3. Use a disposable workspace and synthetic files in an ordinary tab (record installed-window checks separately). In Host Terminal, explicitly `cd` to the test workspace before running harmless commands. Do not launch real agent runs, VM tasks or notification delivery during this UI check.
4. Record commit/build, date, operator, device model, OS/browser version, ordinary/installed mode, orientation, keyboard type and per-case results (`pass`, `fail`, `unavailable`, `not run`) with sanitized evidence. Disable the profile's remote node and stop only acceptance-owned processes before cleanup.

| Case | Physical action and observation |
| --- | --- |
| Software keyboard and viewport | Focus VM project editor and Host Terminal; test IME composition, show/hide keyboard, rotate and return. Verify visible text/caret, reachable controls, bounded menus/dialogs and restored geometry. |
| Native touch and selection | Swipe a long project file list and overflowing Dock without triggering activation. Long-press/select synthetic text and test native copy/paste and clipboard prompts. |
| Menus, dialogs and focus | Open submenus and dialogs via touch, cancel and confirm focus returns. Fold/minimize a terminal and switch workspaces; verify hidden content receives no input. |
| Draft preservation | Leave a synthetic edit unsaved across keyboard toggle, rotation, dialogs and workspace switches; confirm exact dirty text, then save and reopen. |
| Terminal input | Run a harmless command, inspect output, move caret, interrupt foreground output where supported and scroll via touch without duplicated input. |

### Device-cloud adoption boundary

AXV has no configured device-cloud job. Use owner-controlled devices until a provider account's eligibility, enabled products, actual devices, framework support and concurrency are verified and recorded. Open-source status alone does not establish non-commercial eligibility; verify current program terms directly ([TestingBot OSS](https://testingbot.com/open-source), [BrowserStack OSS](https://www.browserstack.com/open-source)).

Any future device-cloud job must target only an isolated synthetic fixture (never the owner's tailnet, production profile, real projects, agent sessions or credentials), must not weaken [local access checks](api/README.md#access-and-identity-boundaries) to work around localhost tunnel limits, and must keep recordings/logs strictly synthetic.

## Remote host network verification

The Linux [host baseline checker](../scripts/verify-tsnet-host.mjs) verifies that Tailscale remote-access checks leave host networking unchanged. It inspects interfaces, routes, policy rules, firewall rules, resolver config, inactive system Tailscale, DNS resolution and `git ls-remote origin HEAD` without modifying host state.

```sh
node scripts/verify-tsnet-host.mjs capture
node scripts/verify-tsnet-host.mjs compare --before /absolute/capture.json
```

Reports are written under `.data/test/tsnet` (or `AXV_TEST_TMPDIR`) with outcomes and fingerprints only (no raw config, DNS answers, remote URLs or credentials).

### Live Tailscale and Cloudflare acceptance

- **Live Tailscale:** Use an isolated profile and production binary. Verify the enrolled `.ts.net` HTTPS URL from an independent authenticated device with normal TLS validation, owner access, local-only route rejection and a foreground terminal lasting at least 90 seconds. Confirm disabling disconnects only remote clients while local work continues, and re-enabling retains node identity without reenrollment.
- **Live Cloudflare:** Requires an administrator-provisioned Access application (with application and matching Allow-policy sessions of at most 15 minutes), explicit owner subject and external tunnel. Verify owner access, unauthenticated/other-owner rejection, preference writes, Host Terminal, exact Codex reattachment and local-only route denial. Confirm Disable closes only Cloudflare clients. For service publishing, publish on an exclusive cookie site (both owner and public visibility), inspect DNS/ingress ownership, restart and verify the publication remains closed until explicit Resume of the original binding, unpublish during active HTTP/SSE/WebSocket streams and verify unrelated rules remain intact. Exercise 15-minute expiry, provider logout/revocation and control-plane outage separately (reconnecting must never extend the original signed lease).

## Native daemon smoke

After `make build`, run `node scripts/smoke-daemon.mjs` in a macOS GUI login or Linux systemd user session when changing background supervision or environment propagation. It verifies native launch/update behavior and Codex continuity using a temporary installation, private profile, deterministic CLI and tmux socket, without creating VMs.

## Real Codex acceptance

`make verify-codex-live` is an explicit real-model check separate from automated fixtures. Build AXV and install Chromium (`pnpm exec playwright install chromium`) beforehand, and ensure Codex CLI is signed in and the project is trusted in a host terminal.

- Default target is this checkout; `pnpm verify:codex-live /absolute/project` selects another.
- Sends two short, tool-free requests to verify live continuity, saved-context continuation, non-blocking completion activity and restart persistence.
- Isolates AXV data, profile and tmux routing, inherits proxy configuration and writes a credential-free result to `.cache/codex-live/last-run.json`.

For broader provider-activity acceptance using the official TUI in an isolated profile and tmux socket:

- Use invocation-scoped `on-request` approval and `read-only` sandbox when the account normally disables approvals; approve one bounded test command, verify concurrent requests remain actionable until both resolve, and verify a pending user-input question survives desktop restart.
- Stop only the acceptance-owned desktop service during an active turn, interrupt in the TUI, restart and verify the exact interrupted event restores and can be dismissed (and that a later event cannot be dismissed with an old event ID).
- For controlled transport failures, point `openai_base_url` to an invocation-owned loopback TCP listener that closes before TLS completes (and disable `features.unbounded_connection_retries` for that invocation if needed); report controlled faults separately from real upstream outages.

## VM project workspace acceptance

### Focused fixture suites

```sh
node scripts/test-go.mjs -race \
  ./apps/server/internal/compute/control ./apps/server/internal/compute/httpapi \
  ./apps/server/internal/compute/client ./apps/server/internal/cli \
  ./apps/server/internal/guest/...
```

- When changing preview transport or composition, also run `node scripts/test-go.mjs -race ./apps/server/internal/compute/preview ./apps/server/internal/tailnet ./apps/server/internal/server`.
- When changing VM-project Codex integration or the bundled [Skill](../apps/server/internal/vmwork/skills/axv-vm-project/SKILL.md), also run `node scripts/test-go.mjs -race ./apps/server/internal/codex/... ./apps/server/internal/storage ./apps/server/internal/compute/gateway ./apps/server/internal/vmwork ./apps/server/internal/cli` and [`vm-project-codex.spec.ts`](../tests/vm-project-codex.spec.ts).

### Native VM project workflow

Use an invocation-owned profile/test root and a compatible enhanced Guest image (never a personal project or profile). On Linux, [`scripts/verify-vm-firecracker.mjs`](../scripts/verify-vm-firecracker.mjs) automates the core native project workflow against real delegated guest cgroups:

1. **Retained-volume Git & file confinement:** Clones a real repository (`ItsDangerous 2.2.0`) on a retained volume, edits reviewed files via `axv vm project`, checks version/path/symlink/binary/create-only rejection, survives controller restart, replaces the VM while preserving code/Git state, and rejects stale bindings.
2. **Persistent tasks & cgroup enforcement:** Runs in-guest Python venv install, wheel build and upstream pytest suite as persistent tasks; verifies controller restart without duplicate dispatch, exact cancellation (including daemonized descendants), CPU/memory/process/timeout/log limits, nested cgroup cleanup, guest-executor termination recovery and VM-restart interruption.
3. **Initialization, previews and offline backups:** Exercises [`lib-vm-project-initialization.mjs`](../scripts/lib-vm-project-initialization.mjs) (empty dir & HTTPS clone replay), [`lib-vm-project-preview.mjs`](../scripts/lib-vm-project-preview.mjs) (independent Chromium origin, WebSocket, revocation), and [`lib-vm-project-backup.mjs`](../scripts/lib-vm-project-backup.mjs) (stopped-VM copy, restore into a new volume/VM/project, Git/worktree check and controller recovery).
4. **VM-project Codex integration:** Runs [`lib-vm-codex-workflow.mjs`](../scripts/lib-vm-codex-workflow.mjs) with a deterministic provider over real guest files and tasks, verifying restart, receipt recovery, continuation, VM replacement and old-context rejection.
5. **Opt-in real-model VM Codex (`--real-codex`):** Runs [`lib-vm-codex-live.mjs`](../scripts/lib-vm-codex-live.mjs) with installed Codex CLI and existing sign-in/approvals on a synthetic Python project inside the disposable VM. If it prints `awaiting-project-trust`, open the printed `DESKTOP_READY` URL and answer the official trust prompt within two minutes. Retain `AXV_REAL_CODEX_RESULT` before cleanup removes the isolated profile.

Browser editor conflict, Reload/Reconnect, tab-local draft recovery and close/Keep Draft checks remain separate interactive browser checks.

## Native VM preparation acceptance

### macOS Lima

On a macOS host with VZ support, verify warm-cache reuse, package-source isolation and basic VM lifecycle across two independent profiles:

```sh
node scripts/verify-vm-cache.mjs --yes
```

For enhanced guest/SSH acceptance using a locally built Lima bundle from [image recipes](../images/guest/README.md):

```sh
AXV_VM_VERIFY_IMAGE_BUNDLE=/absolute/bundle/manifest.json node scripts/verify-vm-preparation.mjs --yes
```

Notes:

- Native acceptance honors the user's [shared download cache](startup.md#shared-download-cache) (unlike ordinary Go/Vitest/E2E launchers, which isolate cache under the test root).
- If `AXV_TEST_TMPDIR` exceeds Lima's **41-byte home-path limit**, the script allocates and prints an owned `/tmp/axv-lima-*` runtime directory with matching cleanup.
- Set `AXV_VM_VERIFY_KEEP_FAILURE=1` to preserve VMs and profile state after a failed check for diagnosis; follow its printed commands to clean up afterward.

### Linux Firecracker

Run as **root** on a Linux KVM host with [host prerequisites and delegated cgroup setup](startup.md#linux-privilege-boundary), systemd 254+, OpenSSH tools and an enhanced native Firecracker bundle from the [image recipe](../images/guest/README.md#firecracker-root-filesystem):

```sh
node scripts/verify-vm-firecracker.mjs --yes \
  --image-bundle /absolute/bundle/manifest.json \
  --executable /absolute/verified-application/axv \
  --kernel /absolute/vmlinux --kernel-sha256 <reviewed-sha256> \
  --firecracker /absolute/firecracker --jailer /absolute/jailer \
  --version 1.17.0 --subnet <unused-private-/22>
```

For [Guest candidate acceptance](releasing.md#review-and-publish-the-guest), `--executable` must point to the independently verified published application binary approved for that candidate; omit `--executable` during source development to build an isolated current-source binary. On Docker hosts with a restrictive iptables forwarding policy, add `--docker-uplink <interface>` to install and clean up two scoped `DOCKER-USER` rules. Set `AXV_VM_VERIFY_KEEP_FAILURE=1` to retain state on failure.

Reviewed Linux amd64 candidate acceptance input set (independently fetched and verified before running):

| Input | Source and SHA-256 |
| --- | --- |
| Firecracker/jailer `1.17.0` archive | [Official release archive](https://github.com/firecracker-microvm/firecracker/releases/download/v1.17.0/firecracker-v1.17.0-x86_64.tgz), `06094a1108ae9e82aa4c23a775aa92758f53f1175d422270d9d6162cb9ade558` |
| Firecracker executable | From archive, `99ad0f5cd0514a88aad0e9ae8cfdb3cc3b4ab9d190e1194602406c786b5de7a5` |
| jailer executable | From archive, `65ef226e96f0ceda55ba643f445801ef2cc0ea667ef67cad8ac4f406c9c8434f` |
| Linux `6.1.155` kernel | [Firecracker CI kernel](https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.14/x86_64/vmlinux-6.1.155), `e41c7048bd2475e7e788153823fcb9166a7e0b78c4c443bd6446d015fa735f53` |
| Kernel configuration | [Matching configuration](https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.14/x86_64/vmlinux-6.1.155.config), `024b2aae62fe7131f9a9ab80f27619c882c0a37265dd105adae01d2867bef7c3` |

Before approving a replacement kernel/runtime combination, inspect `CONFIG_FHANDLE`, ext4/export-handle, virtio/vsock, unified cgroup v2 (`cpu`/`memory`/`pids`), `cgroup.kill` and atomic `clone3` support, and run full native acceptance.

For the native kernel firewall regression without building a VM image (requires root, `unshare`, `nsenter`, `ip`, `nft`, `ping`, `sleep`):

```sh
AXV_TEST_FIRECRACKER_NETWORK=1 node scripts/test-go.mjs -race \
  -run TestNativeFirewallIPv4Egress -v ./apps/server/internal/compute/provider/firecracker
```

### Local OpenSSH fixture

For a focused SSH framing/EOF/forwarding check without VMs (requires local `sshd`, `ssh`, `scp`, `sftp`):

```sh
AXV_TEST_TMPDIR=.data/test AXV_TEST_OPENSSH=1 node scripts/test-go.mjs -race -run TestNativeOpenSSH -v ./apps/server/internal/sshstream
```
