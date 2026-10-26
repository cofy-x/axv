# Development and verification

Use the [pinned toolchain](../README.md#install-and-use) and follow [AGENTS.md](../AGENTS.md). The [documentation map](README.md) selects the references needed for a change; this guide owns commands, verification and test isolation.

## Local development

Install dependencies and run development as the same account, with write access to the checkout and dependency/Vite caches. The [startup identity rules](startup.md#execution-identity) apply:

```bash
make deps
make dev
```

`make deps` installs locked JavaScript dependencies and downloads the server module's declared Go dependencies with `GOWORK=off`. This limits prefetching to the module's requirements instead of downloading unrelated upstream development-tool archives and adding their checksums to `go.work.sum`. Builds and tests use the repository workspace normally. `make dev` builds the development server, then starts Vite on available loopback ports. Open the printed Vite URL. Explicit occupied ports fail instead of reusing another server.

Each checkout has a persistent profile at `~/.axv/dev/<checkout-id>`, where the ID is the first 12 hexadecimal characters of SHA-256 over the checkout's canonical absolute path. Symlinked paths to the same checkout share the profile; distinct worktrees do not. Desktop data lives in its `data/`. `AXV_DEV_HOME` selects a different final profile root directly, without appending another identifier. Previous checkout-local development data is not migrated.

After an incompatible baseline change, stop the affected runs and services before explicitly recreating their AXV-owned store; follow the [baseline policy](storage.md#desktop-database-baseline).

The launcher puts temporary executables under an invocation-owned directory in the profile's `run/`. Development can use [VM preparation](startup.md#first-time-macos-setup) and preserves the user's cache and mirror selections while keeping each checkout's running state separate. An overlong profile fails socket-path validation with guidance to choose a shorter `AXV_DEV_HOME`; it never relocates state silently. Stopping the launcher removes only its own temporary build directory, not prepared VM storage.

| Override | Purpose |
| --- | --- |
| `GO` | Go executable used by Make and Node launchers; defaults to `go` |
| `PNPM` | Make's pnpm launcher; the pinned default lives in [Makefile](../Makefile) |
| `ADDR`, `PORT` | Development backend address; `ADDR` wins over `127.0.0.1:PORT` |
| `WEB_PORT` | Development frontend port |
| `AXV_DEV_HOME` | Final development profile root; desktop data is its `data/` |
| `E2E_PORT` | Dedicated browser-test backend port |

Ports default to automatic allocation. The development launcher replaces inherited `AXV_HOME` and `DATA_DIR` and clears `AXV_COMPUTE_SOCKET` before starting its foreground service. Other verification launchers pass their own explicit profile and use `serve`. Do not select the production profile with `AXV_DEV_HOME`. Production configuration and isolated previews belong in [startup](startup.md#configuration-and-storage).

`PNPM` affects Make recipes only. Node launchers invoke local tools directly, and nested `pnpm` commands in [package scripts](../package.json), such as `build` and `lint:fix:format`, still resolve `pnpm` from `PATH`.

## Production build

```bash
make build
./dist/axv version
```

The [build script](../scripts/build.mjs) embeds the UI into `dist/axv`. The executable works from any directory without Node at runtime. `make build-go` produces `dist/server-dev` without embedded production assets; direct Go builds and tests do not require frontend output.

Production builds and Go vulnerability scans share the `production,ts_omit_webclient` tags declared in [the script helpers](../scripts/lib.mjs). The Tailscale option excludes its separate node-management Web UI; [distribution notices](../THIRD_PARTY_NOTICES.md#application-distributions) own that component boundary and the required license review.

Production JavaScript and CSS support negotiated gzip when compression reduces their size. Content-fingerprinted assets use private immutable caching; HTML and unversioned resources require revalidation so new builds select their current assets. The HTML displays the existing startup screen while the application bundle loads, including on a slow remote connection.

`make install` runs dependency setup, this production build and atomic installation into Go's binary directory sequentially. It does not initialize user data or launch services. [Installation and updates](startup.md#installation-and-updates) owns destination, PATH and live-service update semantics. For installation checks, always select a temporary `GOBIN` and a separate short `AXV_HOME`; never overwrite a real installation as part of automated verification.

Builds report `dev` unless `VERSION` is supplied. A version must start with an ASCII letter or digit and contain only ASCII letters, digits, `.`, `_`, `+`, or `-`. For a custom build, pass the same version to its smoke check:

```bash
VERSION=1.2.3 make build
VERSION=1.2.3 make smoke-production
```

### Dashboard loading and performance

Every production dashboard build reports and checks the complete static JavaScript entry graph through [`dashboard-bundle.mjs`](../scripts/dashboard-bundle.mjs) and the checked-in [byte budgets](../scripts/dashboard-bundle-budget.json). Shared imports count once; moving eager code into several chunks does not reduce the total. Raw bytes and the sum of independent Node gzip estimates have separate budgets. These estimates differ from the embedded Go server's actual gzip responses and exclude dynamic code until it is requested. Keep Vite's existing chunk warning enabled.

The budget leaves limited growth above the measured feature-loading baseline. Review module attribution and real startup requests before changing it; do not raise it merely to silence a failed build. Module rendered lengths are contribution hints before compression, not additive final compressed savings. The build reads final emitted files and records their digests, avoiding differences between bundler hooks and shipped artifacts.

The documented build writes a private module/chunk report to `dashboard-bundle.json` under the selected test root. `AXV_BUNDLE_REPORT` can select another file within that root. Evidence writers reject symbolic links below the selected root, including the report file, and the build excludes the actual served output directory. Reports are excluded from embedded/served assets. Browser loading regressions use that report to locate actual feature chunks rather than fixed hash filenames. Preserve the production binary, report and source identity together when comparing versions.

The Vite configuration retains initial HTML and dependency preloads while excluding each dynamic JavaScript entry itself from its preload list. This preserves same-page retries after a transient entry failure in WebKit; preloading that failed entry can poison its module cache. The bundle plugin includes the resolved preload options and self-contained resolver in chunk hashes because Vite rewrites preload lists after assigning filenames. Keep resolver dependencies reflected in that hash input. Native static-dependency failures may still require a manual reload after saving other work. Do not add URL cache-busting that duplicates module identity or automatically replace a desktop containing drafts.

Measure the current embedded production executable with the isolated launcher:

```bash
pnpm run build:production
node scripts/measure-dashboard.mjs --samples 3
node scripts/measure-dashboard.mjs --conditions mobile --samples 3
```

Each invocation prints a report directory under `.data/test` (or `AXV_TEST_TMPDIR`). `--binary` selects a retained before/after executable; the harness runs a private copy and records its hash so rebuilding the original path cannot misidentify a measurement. `--output` selects a child directory under the same test root and refuses to overwrite an existing report. The harness measures default and empty desktops, restored live fixture Codex/host-project windows, cold and cached requests, and first-open Codex, Terminal and Project Files. It records the executable hash, entry-script hash and a source manifest of its local fixture/launcher helpers, request sizes/encoding/cache, readiness, CDP CPU durations and long tasks. It has no timing pass/fail thresholds or automatic retries.

Local conditions cover desktop, 390 px and 320 px; mobile conditions use 390 px touch emulation, CPU slowdown and an explicitly recorded network profile. Readiness timings include Playwright assertion polling; investigate anomalous results with browser DOM/frame timestamps before attributing a regression. These controlled local HTTP fixtures do not establish actual private-HTTPS, physical-phone or real-VM/model performance. Use the same harness, samples and conditions before and after, retain first failures separately, and explain first-open/network-waterfall tradeoffs alongside startup savings. Real device/remote measurements remain separate acceptance when those environments are available.

## Verification

| Change | Required verification |
| --- | --- |
| Documentation only | `make format-check` and the [documentation review](#documentation-review) below |
| Runtime | Format, unit tests, typecheck, lint, production build and smoke below |
| Desktop or cross-layer | Runtime checks plus `make test-e2e` and [visual/interaction acceptance](design.md#acceptance-checks) |

```bash
make format
make format-check test typecheck lint build
make smoke-production
```

For desktop or cross-layer changes, also run `make test-e2e` and complete visual acceptance.

### Documentation review

Follow [content ownership](README.md#content-ownership) and review the diff for changed claims, examples and moved sections. Check relative file/directory links and heading fragments, including inbound links to moved content. Verify changed commands, defaults, limits and ownership against the linked implementation or manifests. Preserve an intentional rule when code disagrees and report the discrepancy.

Documentation-only changes do not require runtime suites. A script, configuration or generated runtime input change is not documentation-only merely because it supports the docs. Report the checks performed and any unresolved discrepancy in the task/PR, without adding a permanent audit page.

### Focused checks

Guest protocol, image validation, SSH registration and service request/terminal behavior can be checked without a VM:

```sh
node scripts/test-go.mjs -race ./apps/server/internal/guest/...
```

For focused startup checks covering configuration precedence, launch snapshots, service ownership and command cancellation:

```sh
node scripts/test-go.mjs -race \
  ./apps/server/internal/profile/... ./apps/server/internal/diagnostics \
  ./apps/server/internal/artifactcache ./apps/server/internal/config \
  ./apps/server/internal/app/... ./apps/server/internal/daemon \
  ./apps/server/internal/cli ./apps/server/internal/setup
```

These checks complement the [native daemon smoke](#native-daemon-smoke) when changing background supervision or captured environment behavior.

For remote transport changes, include the owner binding, certificate/lifecycle, shared request guards and production route composition:

```sh
node scripts/test-go.mjs -race \
  ./apps/server/internal/tailnet ./apps/server/internal/remoteaccess \
  ./apps/server/internal/cloudflare/... \
  ./apps/server/internal/localaccess ./apps/server/internal/server \
  ./apps/server/internal/profile ./apps/server/internal/config ./apps/server/internal/cli
```

These tests use controlled identity/transport fixtures and real local TLS/WebSockets. The production smoke also requires the default-disabled transport to create no node state. Live acceptance must exercise the [production remote desktop](startup.md#remote-access) from an independently authenticated tailnet device, including browser actions and terminal continuity. Use the [host baseline checker](#remote-host-network-verification) to compare DNS, Git access and network fingerprints; fixture tests and an unchanged host baseline do not establish production remote browser acceptance or cross-device behavior.

Cloudflare fixture checks use controlled signing keys, HTTPS key rotation and real loopback HTTP/SSE/WebSocket connections, including fixed 15-minute owner leases and expiry/reconnect behavior during signing-key outages. Management and publishing checks exercise strict provider responses, owned DNS/ingress recovery, concurrent journals, exact private Unix forwarding, persistence failures, token rotation, receipt recovery, definite-denial versus uncertain-write ownership, monitor-failure/retry retirement races and withdrawal before cleanup. Production smoke checks the disabled origin, local controls, safe management APIs, CLI instance identity and absence of publication state before use. Live Cloudflare acceptance additionally requires an administrator-provisioned Access application, owner subject and external tunnel: verify owner access, unauthenticated/other-owner rejection, document writes, Host Terminal, exact Codex reattachment and local-only route denial from an independent browser. Disable must close only desktop Cloudflare clients while local/Tailscale work continues. Publish a service on an exclusive cookie site with both reviewed owner/public visibility, inspect actual DNS/ingress ownership, restart and confirm it stays closed until explicit Resume of the original binding, then unpublish it while HTTP/SSE/WebSocket streams are active and confirm unrelated rules remain intact. Use application and matching Allow-policy sessions of at most 15 minutes. Exercise expiry, provider logout/revocation and control-plane outage separately; a reconnect must not renew the old signed deadline. Local listening status and signed fixtures do not establish live edge policy, MFA, revocation or public reachability.

The [Cloudflare browser isolation tests](../tests/cloudflare-isolation.spec.ts) use the real Go publication manager and bootstrap with locally simulated provider/guest services. Actual Chromium, Firefox and WebKit own cookie prefixes, PSL rejection, SameSite cookie storage and localStorage/IndexedDB. HTTPS edge routing is a fixture; when Playwright interception omits Fetch Metadata, the bridge supplies representative metadata explicitly and records that limitation. WebKit interception also omits request Cookie headers, so successful bootstrap/lifecycle cases explicitly skip that transport and require separate network TLS acceptance. These tests do not establish browser-to-origin TLS, actual edge header delivery or live Cloudflare authorization. Preserve the negative control demonstrating ordinary sibling-domain cookie interference, successful dedicated-site/PSL isolation, fresh-origin storage, service-worker rejection, concurrent first visits, hash/query navigation and retained-cookie withdrawal/restart behavior. Never synthesize a browser cookie jar to make the check pass or disable certificate/security controls.

For live acceptance, use an isolated profile and workspace with the production executable. Verify the enrolled `.ts.net` address in a browser with normal certificate validation, owner access, local-only route rejection and a foreground terminal connection lasting at least 90 seconds. Check that disabling the embedded node disconnects remote clients while a local terminal remains usable, and that re-enabling retains the node identity without enrollment. A separate userspace Tailscale client can verify this path without starting system Tailscale, but physical phone/tablet acceptance still requires those devices. Keep the service available until manual checks finish, then stop only the acceptance-owned processes and compare the host baseline again.

The guest service keeps Linux boot, mount and vsock setup separate from portable request handling. These unit tests complement the native image and VM acceptance below.

`make test` runs Go and Vitest tests, including install and development-launcher helpers. After a production build, `AXV_TEST_PRODUCTION_INSTALL=1 pnpm exec vitest run scripts/install.test.mjs` also verifies repeated installation and execution in a temporary Go binary directory; pass the build's `VERSION` for custom versions.

On a fresh Linux host, an administrator must first run `pnpm exec playwright install-deps chromium firefox webkit` to install the required system libraries. Then run `make test-e2e`: it downloads Chromium, Firefox and WebKit binaries, builds production assets and runs against a fresh database and dedicated server. The [Playwright configuration](../playwright.config.ts) owns browser, display-scale and touch projects; [visual tests](../tests/visual.spec.ts) own the shared viewport scenarios. For focused iteration:

```bash
make test-e2e E2E_ARGS="--project=chromium"
E2E_SKIP_BUILD=1 make test-e2e E2E_ARGS="--project=chromium tests/desktop.spec.ts"
```

Use `E2E_SKIP_BUILD=1` only when the existing production binary includes the changes under test. Focused runs complement the required full suite.

The [production smoke](../scripts/smoke-production.mjs) exercises the embedded binary from an unrelated directory: CLI validation, listener isolation, persistence across restart and session lifecycles. Its assertions are the coverage source; extend them for changed production behavior instead of maintaining a second checklist here.

## GitHub CI and dependency maintenance

The [CI workflow](../.github/workflows/ci.yml) runs for every pull request, pushes to `main`, and manual dispatches, using the pinned toolchains. There are no path filters; documentation changes also receive both required checks. A newer run on the same ref cancels the previous run.

| Check | Coverage |
| --- | --- |
| `checks` | Requires Linux verification, focused macOS native checks and dependency security to pass |
| `e2e` | Requires every browser job to pass, covering the full `test:e2e` browser, display-scale and touch matrix |

Linux verification on Ubuntu 24.04 runs locked dependency installation, formatting, Go and Vitest tests, typecheck, lint, production build, smoke and temporary production installation. Ubuntu verification and browser jobs retain the runner's official HTTPS package mirrors while removing its slow Azure mirror entry; package verification and installation failures remain enforced. After installation, CI rejects modified or newly generated dependency and toolchain files, including workspace and module checksums. This CI-only check requires their Git status to remain clean; ordinary local commands allow uncommitted work.

The macOS 15 ARM64 job builds the native server and runs focused Go race checks for profile storage, Host Terminal, Codex and Lima adapters. It uses fixture processes rather than launching VMs or native desktop supervisors. Each of the six Playwright projects runs in three shards on separate Ubuntu runners, each with one worker and an isolated server. Sharding leaves room within the job deadline for browser dependencies, tests and cleanup; each shard retains separately named failure diagnostics. A failed shard does not cancel the others. Both aggregate checks run after their dependencies and require every result to be `success`; failed, skipped or cancelled dependencies cannot produce a passing check.

Test jobs use the existing launchers and private `.data/test` root. Host-session checks use tmux and deterministic Codex fixtures without provider credentials or model requests. Caches contain only dependencies and compiler output. Failed browser reports are retained for seven days, excluding AXV profiles and test roots. `GOTOOLCHAIN=local` prevents implicit Go upgrades. Native daemon, VM, network and real-Codex acceptance remain separate explicit checks; CI does not replace them or visual/interaction acceptance.

Maintain `main` protection with the GitHub Actions checks `checks` and `e2e` required, the branch up to date before merging, and review conversations resolved. Disable force pushes and branch deletion. The baseline does not require approval from a second maintainer, so a sole maintainer can merge a verified pull request. Configure these repository settings after both check names have appeared in a successful run; workflow files do not configure branch protection.

The reusable [dependency security workflow](../.github/workflows/security.yml) runs within required CI, independently every Monday, and on manual dispatch. Run its checks locally with:

```sh
make security
```

`make audit-js` uses the pinned pnpm to scan production, development and optional dependencies, failing on low severity or higher.

`make audit-go` first runs the production build to prepare embedded assets, then installs the fixed scanner version from [its launcher](../scripts/audit-go.mjs) into private temporary storage. It runs text-mode `govulncheck -test -tags production,ts_omit_webclient ./...` from the server module with the selected Go toolchain and `CGO_ENABLED=0`, matching the production build. Reachable known vulnerabilities, including tests and the standard library, fail the check; module-only advisories remain informational. The scan covers the selected build platform. Both audits preserve registry/database and scanner errors as failures and apply no automatic fixes.

The [Dependabot configuration](../.github/dependabot.yml) checks Actions, the root pnpm workspace and `apps/server` Go modules every Monday, groups minor/patch updates, and limits each ecosystem to five open version-update pull requests. Major updates remain separate. Repository security alerts and security-update PRs complement these scans and are independent of that weekly schedule. Dependabot does not merge automatically.

Review dependency changes with their lockfiles and run the same verification matrix. For pnpm updates, check the workspace manifests, overrides and frozen-lockfile installation together. The hosted Dependabot updater has been verified to install and use the pnpm version pinned by `packageManager`. Keep Action references pinned to full commit SHAs and verify replacement SHAs against the upstream release. Review compiler-major migrations against the lint plugins' supported TypeScript range and resolve migration errors before accepting the update.

Upgrade Go, Node.js and pnpm deliberately in one change, keeping the version files, manifests, Makefile, Go workspace/modules, README and lockfiles aligned. The npm configuration excludes `node` and `pnpm` updates and `@types/node` major updates. Keep Node.js declarations in the pinned runtime's major version and upgrade them together when selecting a new Node.js major; compatible minor and patch declaration updates remain eligible. A dependency that requires a newer toolchain needs this coordinated upgrade before it can pass CI.

## Test isolation and diagnostics

Repository verification uses `.data/test` by default for generated host executables, helper fixtures, profiles and temporary files. Set `AXV_TEST_TMPDIR` to choose another private root; relative overrides resolve from the invoking working directory:

```sh
AXV_TEST_TMPDIR=.data/test-focused make test
```

The shared [temporary-directory helper](../scripts/test-temp.mjs) creates a missing root with mode `0700` and rejects a symlink, a directory owned by someone else or non-private Unix permissions. Each invocation creates fresh owned children and passes their `TMPDIR` and `GOTMPDIR` to child processes; cleanup removes only those children, preserving the configured root. Builds, smoke/browser launchers, native VM acceptance and Vitest use this helper.

Go checks use `make test-go` or `node scripts/test-go.mjs [go test arguments]`; direct `go test` does not load the environment adapter. For ad hoc `go run` or diagnostic helpers, explicitly set `TMPDIR` and `GOTMPDIR` to an owned directory under the selected test root. Do not put generated host executables, fixtures or scratch profiles in global `/tmp` or `/var` directories. Unix sockets and provider runtime state may use an explicitly owned short directory when platform limits require it, with separate cleanup; generated executables remain under the test root.

Run the full Go suite and desktop smoke/browser checks with the identity being verified. Run privileged Linux VM and network acceptance separately as root. Each identity needs its own private test root and compiler cache; keep overridden test-root paths short enough for Unix sockets.

When a child process is killed, use bounded diagnostics to record its actual executable, ProxyCommand when applicable, working directory and temporary paths before retrying. Keep executable test artifacts under the repository test root and investigate the reported paths; do not disable or bypass host security controls.

Browser and production smoke tests require tmux on macOS/Linux. They use a deterministic Codex fixture, with no installed Codex CLI, login or model request required. Their [shell fixture](../scripts/shell-fixture.mjs) gives the server a private `HOME`, selects `/bin/sh`, and clears `ENV` and `BASH_ENV`: Host Terminal still starts a login shell, without loading the user's home profile or inherited startup hooks. This fixture does not change the invoking shell or explicit native acceptance's user home and shared-cache behavior.

The [browser runtime guard](../tests/runtime-errors.ts) collects native `error` and `unhandledrejection` events from each document and frame into a host-owned record that survives reloads. It retains Playwright `pageerror` events as diagnostic attachments: WebKit also emits them for handled requests cancelled during document replacement ([upstream issue](https://github.com/microsoft/playwright/issues/43120)). Browser regressions verify uncaught exceptions and unhandled rejections remain failures, including exceptions with the same text as that engine diagnostic.

Each run owns its data, profile, project, listener and explicit tmux socket. On macOS/Linux, build, Go-test, browser and development launchers also own their child process groups. Cleanup waits for all tracked child shutdown attempts before reporting failures; a cleanup failure is visible and makes the launcher exit nonzero even when its checks succeeded. Cleanup may stop only the invocation's resources. Shared launcher and Codex fixture behavior lives in [scripts/lib.mjs](../scripts/lib.mjs) and [scripts/codex-fixture.mjs](../scripts/codex-fixture.mjs).

Before removing a failed browser run's private profile, the [diagnostic helper](../scripts/test-e2e-diagnostics.mjs) reports the final 64 KiB of its daemon log alongside existing Codex pane diagnostics.

On macOS, a process-group probe can return `EPERM` while exited children await reaping; that error alone does not identify security-software intervention. The launcher keeps observing within its existing cleanup deadline and succeeds only after `ESRCH` confirms the group is absent. A persistent permission error remains a cleanup failure.

Keep generated databases, build outputs, screenshots, traces and browser reports untracked. Formatting covers documentation as well as source; use the repository's Prettier and ESLint configuration.

## Remote host network verification

The Linux [host baseline checker](../scripts/verify-tsnet-host.mjs) independently verifies that remote-access checks preserve host networking. It performs read-only inspection of interfaces, routes, policy rules, firewall rules and resolver configuration, requires system Tailscale to remain inactive/disabled, and checks real DNS resolution and `git ls-remote origin HEAD`. Run it with the pinned Node toolchain and permission to read the host's firewall configuration; missing evidence remains an incomplete check. It never starts a Tailscale node, changes host settings or controls the workstation.

Capture a baseline before an authorized remote-access check, then compare against that same capture while the workstation is remotely available and after the check's owned processes have stopped:

```sh
node scripts/verify-tsnet-host.mjs capture
node scripts/verify-tsnet-host.mjs compare --before /absolute/capture.json
```

Reports are written under this checkout's `.data/test/tsnet` by default (`AXV_TEST_TMPDIR` selects a different test root). Reports retain outcomes and fingerprints, not raw network configuration, DNS answers, Git remote URLs or credentials. Use the printed capture path with `--before` and retain the comparison report when reporting results. This host evidence complements the production transport checks; it does not prove remote authentication, HTTPS or browser behavior.

## Native daemon smoke

After `make build`, run `node scripts/smoke-daemon.mjs` in a macOS GUI login or Linux systemd user session when changing background supervision or environment propagation. It checks native launch/update behavior and Codex continuity using a temporary installation, private profile, deterministic CLI and tmux socket, without creating VMs. The [script](../scripts/smoke-daemon.mjs) owns its assertions; [startup](startup.md#agent-settings-and-background-lifecycle) owns background lifecycle behavior.

## Real Codex acceptance

`make verify-codex-live` is an explicit real-model check, separate from automated fixtures. First build AXV and install Chromium with `pnpm exec playwright install chromium`. Run it with the intended desktop account and its existing provider sign-in. It requires a Codex CLI executable by that account, tmux and an already trusted project; complete sign-in and trust in a host terminal beforehand.

The default project is this repository; `pnpm verify:codex-live /absolute/project` selects another. The check sends two short, tool-free requests using existing model and approval settings to verify live continuity and saved-context continuation. It also verifies completed activity requires no acknowledgment, desktop restart preserves the exact completed event, and a continued run does not inherit the previous run's activity.

The check isolates AXV data, profile and tmux routing, inherits proxy configuration, and writes a credential-free result to `.cache/codex-live/last-run.json` without a transcript. It uses your model access; run it only when that acceptance is intended. The [script](../scripts/verify-codex-live.mjs) owns the detailed assertions.

Broader activity acceptance uses the official TUI and an invocation-owned desktop profile and tmux socket. When the account normally disables approvals, use an invocation-scoped `on-request` approval policy and `read-only` sandbox for bounded test commands; retain the existing model and sign-in without changing user configuration. Approve only the reviewed test command once. Verify two concurrent requests remain actionable until both resolve, and verify an actual user-input question survives desktop restart.

Stop only the acceptance-owned desktop service while a turn is working, interrupt through its original TUI, then check that the independent runner's exact event is restored. Dismiss failed/interrupted events through the UI, restart, and verify a later event cannot be dismissed using the previous event's identifier. For a controlled connection failure, scope `openai_base_url` to an invocation-owned loopback TCP endpoint that closes before TLS completes, without receiving credentials or bypassing TLS validation. If the installed CLI retries connection errors indefinitely, disable `features.unbounded_connection_retries` only for that invocation. Report this controlled fault separately from a real upstream service failure; never synthesize provider events or edit activity files to establish native acceptance. Physical device gestures remain a separate check.

## VM project workspace acceptance

Run focused fixture coverage through the same isolated Go launcher:

```sh
node scripts/test-go.mjs -race \
  ./apps/server/internal/compute/control ./apps/server/internal/compute/httpapi \
  ./apps/server/internal/compute/client ./apps/server/internal/cli \
  ./apps/server/internal/guest/...
```

Project store/service/HTTP tests cover retained-volume registration, durable receipts/replay, attachment/revision checks and stale environment rejection. Guest tests cover path confinement, text/hash checks, create-only publication and invalid/stale identities. CLI fake-host tests verify captured bindings and volume revisions, registration recovery after VM removal with the original host/payload, machine-readable output, unchanged writes, explicit creation expectations and no retry after conflicts or lost responses. Browser tests complement these service fixtures with edited snapshots, conflict/draft preservation and explicit reconnect. Required cross-layer verification remains the [matrix above](#verification); fixture success alone does not establish native VM support.

Initialization fixtures also verify typed intent admission, credential-free repository URLs, durable created-directory identities, exact task/finish replay and existing-directory preservation. Preview fixtures cover independent origins, original-environment HTTP/WebSocket forwarding, owner/Origin checks, connection revocation, manager/request replay, released active capacity and fresh hostnames across profiles and restart. Backup fixtures cover stopped-source admission, unresolved tasks, immutable copies, exact operation recovery, independent restored identities and Git/worktree path validation. Include `node scripts/test-go.mjs -race ./apps/server/internal/projectpreview ./apps/server/internal/tailnet ./apps/server/internal/server` when changing preview transport or composition; browser tests exercise setup, previews and recovery through their existing feature controls.

For VM-project Codex work, also run `node scripts/test-go.mjs -race ./apps/server/internal/codex/... ./apps/server/internal/storage ./apps/server/internal/compute/gateway ./apps/server/internal/vmwork ./apps/server/internal/cli`. These cover run-specific context publication, exact replay before live validation, explicit same-project continuation, control-workspace ownership, desktop database fences, provider Skill inputs and captured-client CLI behavior. [`vm-project-codex.spec.ts`](../tests/vm-project-codex.spec.ts) covers scoped launch, drafts, unknown-request recovery, VM replacement, continuation, source-store rejection and narrow-screen/focus behavior across the browser matrix. Validate the bundled [Skill](../apps/server/internal/vmwork/skills/axv-vm-project/SKILL.md) and its real command examples when changing the tool contract.

For native acceptance, use an invocation-owned profile/test root and a newly built compatible enhanced image; follow the platform prerequisites and cleanup rules below. Do not select an existing personal project or profile. The [Linux native script](../scripts/verify-vm-firecracker.mjs) exercises a real in-guest Git clone/worktree, CLI file access, version/path/symlink rejection, controller recovery and replacement-VM binding checks. Its cleanup leaves registered volumes protected while the host runs, then removes only its verified owned test directories after all owned VMs and the controller/cgroup are stopped. Run the workflow below as applicable, including browser actions that require separate interaction acceptance:

1. Create a development VM with a retained project volume. Through guest SSH, place a real Git repository and all worktree/common Git metadata in a directory on that volume. Record the VM, volume and host IDs; retain the original CLI request IDs for uncertain lifecycle submissions.
2. Follow [VM project CLI](startup.md#vm-project-files) to register that directory, capture its binding, list/read a tracked file, edit it with the reviewed SHA-256 and create a file/directory. Confirm actual bytes and `git status` inside the guest. Verify a changed hash, traversal, outside symlink, binary file and create-only overwrite each fail without touching another file.
3. Keep the VM running, close the browser and restart the desktop/controller using the profile's documented launcher. Confirm the same project ID and on-volume code/Git state are available. A guest-service restart must invalidate the old binding even when the boot ID remains unchanged. Explicitly reacquire a binding after reviewing the environment.
4. Stop and remove only that VM while retaining its volume. Create a replacement stopped VM, attach the retained volume using the volume UI/API and start it. Reopen the original project and compare tracked/untracked content, Git status and worktree validity. Old bindings must reject list/read/write/mkdir; a newly captured binding must target the replacement explicitly. Also verify an unavailable VM cannot cause host file access.
5. Review a browser edit, change its file independently through SSH and confirm Save preserves the draft and reports a conflict. Exercise Reload File/Reconnect choices, tab-local draft restore and close/Keep Draft. An uncertain file response requires a read/inspection, without automatic retry.
6. Delete only this invocation's VM, volume and services when unbound resources can be cleaned up safely. Project-bound volumes deliberately reject deletion and retention removal in this phase: preserve the isolated profile and its cleanup instructions rather than bypassing that protection or removing its database while a VM still runs. Stop owned services/VMs before explicitly removing the disposable test profile and its verified storage.

Persistent task fixtures additionally cover durable acceptance before dispatch, uncertain response recovery without redispatch, immutable task specifications and explicit retry attempts, exact cancellation across parallel/retried tasks, binary log offsets and offline cached reads, unavailable/corrupt state, quota and disk-admission failures, timeout/log/process/memory failures and executor interruption. Browser coverage verifies receipt recovery, historic attempt selection, exact cancellation and cached offline results across all configured browser projects.

The native script also runs persistent-task checks against real delegated guest cgroups and a pinned public repository, [ItsDangerous 2.2.0](https://github.com/pallets/itsdangerous/tree/2.2.0). It clones source on the retained volume, edits reviewed files and adds an original module/test through AXV project-file commands, then installs dependencies into a project-owned Python environment, builds a wheel and runs the complete upstream pytest suite through independently accepted tasks. Dependency downloads happen inside the VM; no project install/build/test command falls back to the host. The invocation writes bounded task logs and a credential-free workflow result under its owned test directory. It checks controller restart without duplicate execution, cancellation isolation including daemonized descendants, actual CPU/memory/process settings, nested cgroup cleanup, timeout and log limits, and interrupted attempts after VM restart. It also terminates only its own unprivileged guest executor from a task and verifies same-boot recovery, without relaunching that attempt or cancelling work in the new executor. Use a newly built guest image with `python3-venv` and working public HTTPS egress for this check.

The native launcher additionally exercises [project initialization](../scripts/lib-vm-project-initialization.mjs), [development previews](../scripts/lib-vm-project-preview.mjs) and [offline backup restoration](../scripts/lib-vm-project-backup.mjs). It records every project/copy receipt before cleanup, verifies empty creation and HTTPS clone replay, stops the source VM, copies and restores into a new volume/VM/project, and compares the original Git/worktree state. The preview helper starts a real guest development task, opens its independent origin through a production desktop link in Chromium, checks module/WebSocket behavior and control isolation, and revokes the preview while preserving that exact task before explicitly cancelling it. The preview check also verifies application sessions and bearer credentials; remote preview browsing is unavailable. Copy acceptance includes controller recovery; merely completing a provider copy does not establish restore acceptance. Per-task disk/I/O/network throttling and measured resource use remain separate [workspace acceptance targets](product.md#vm-project-workspace-constraints). Record actual native results and unavailable checks in task/PR history, updating [project status](../README.md#project-status) only when supported by evidence.

The Linux native launcher also requires tmux and runs [`lib-vm-codex-workflow.mjs`](../scripts/lib-vm-codex-workflow.mjs) against its disposable VM. It uses an isolated deterministic provider, private desktop/profile and tmux routing: the actual Codex runner forwards the Skill, and the bound CLI reads/writes real guest files and submits persistent test tasks. Checks include desktop restart, original receipt recovery, same-conversation continuation, VM replacement and old-context rejection. This establishes the real VM/runner/tool integration with a controlled provider; it does not establish real-model tool selection.

Add `--real-codex` to the native launcher's explicit arguments to also run [`lib-vm-codex-live.mjs`](../scripts/lib-vm-codex-live.mjs) with the installed Codex CLI and existing provider sign-in, model and approval settings. This opt-in uses model access and requires those settings to permit the intended bound commands. It creates an original, dependency-free Python project in the disposable VM, confirms the initial test failure, and asks the real model through AXV's terminal to repair the source and submit persistent test/build tasks using the injected Skill. Independent checks compare source and protected fixture hashes, durable task receipts, complete cached logs and the runnable zipapp artifact. A separate AXV profile and private tmux socket isolate this run; provider configuration, authentication and history retain their normal ownership. Results exclude transcripts and credentials. The host-only [real Codex acceptance](#real-codex-acceptance) remains the separate continuity/history check.

The real VM check can encounter Codex's first-use trust prompt for its newly created control workspace. When it prints `awaiting-project-trust`, open the latest `DESKTOP_READY` address, review that exact session's temporary control directory and answer the official terminal prompt within two minutes. The private `codex-live-*/progress.json` under the printed verification directory records its address, session/run IDs and control path. The harness never answers trust prompts, signs in, approves model commands or changes sandbox policies automatically. Any trust decision and saved conversation remain provider-owned data. Successful native cleanup removes the isolated AXV directories, so retain the emitted `AXV_REAL_CODEX_RESULT` summary when recording acceptance evidence.

## Native VM preparation acceptance

### macOS Lima

On a macOS host with VZ support, run the basic cache and lifecycle check:

```sh
node scripts/verify-vm-cache.mjs --yes
```

The [script](../scripts/verify-vm-cache.mjs) builds a temporary executable and uses two independent profiles to check verified warm-cache reuse, basic VM lifecycle and package-source isolation. It creates real VMs and removes only its own VMs, services and profile/runtime directories, retaining the shared cache. Failed assertions are reported without automatic reruns. This does not validate enhanced guest features.

Native acceptance and development honor the user's [cache configuration](startup.md#shared-download-cache); CI can explicitly select an absolute persistent cache to retain between runs. Ordinary Go/Vitest, browser and smoke entrypoints replace inherited cache settings with a private directory under their test root and never consult the user cache. Executables, fixtures and temporary profiles remain under the test root even when downloads use a shared cache.

The enhanced guest/SSH check requires a compatible Lima bundle from the [image recipes](../images/guest/README.md):

```sh
AXV_VM_VERIFY_IMAGE_BUNDLE=/absolute/bundle/manifest.json node scripts/verify-vm-preparation.mjs --yes
```

Both native checks use the same Go cache as Web and `axv setup`, accepting verified warm hits. Controlled TLS fixtures cover cold transfers and recovery without deliberately repeating large downloads during native acceptance.

Native acceptance uses `AXV_TEST_TMPDIR` for its profile, installed executable, helpers and file transfers. If that profile would exceed Lima's **41-byte home-path limit**, the script explicitly allocates and prints a separate owned `/tmp/axv-lima-*` directory for Lima's runtime sockets and state. That short directory follows the same cleanup or diagnostic-preservation decision as the profile; other artifacts stay under the selected test root.

For explicit failure diagnosis, `AXV_VM_VERIFY_KEEP_FAILURE=1` stops owned probes/services but preserves that run's VMs and profile after a failed check. Follow its printed commands to recover original requests, delete the exact owned VMs and stop the host before removing the retained directory. Normal runs clean up automatically, retaining a profile only when cleanup is uncertain.

The [enhanced acceptance script](../scripts/verify-vm-preparation.mjs) installs AXV temporarily, prepares Lima and the selected bundle in an isolated profile, and creates two task VMs with only one running at a time. It checks desktop/CLI service transitions, guest continuity and real OpenSSH access, transfers and forwarding. Before cleanup, it recovers uncertain creation using the original request and host identity; uncertain cleanup retains the profile with diagnostics. Detailed assertions belong to the script. Current platform and validation claims belong to [project status](../README.md#project-status).

### Linux Firecracker

Use a Linux KVM host as root, with the [host prerequisites and delegated cgroup setup](startup.md#linux-privilege-boundary), systemd 254 or newer, system OpenSSH tools, and an enhanced native Firecracker bundle from the [image recipe](../images/guest/README.md#firecracker-root-filesystem). Supply reviewed Firecracker/jailer executables of the same pinned version and a separately verified compatible kernel:

```sh
node scripts/verify-vm-firecracker.mjs --yes \
  --image-bundle /absolute/bundle/manifest.json \
  --executable /absolute/axv_0.1.0-rc.5_linux_amd64/axv \
  --kernel /absolute/vmlinux --kernel-sha256 <reviewed-sha256> \
  --firecracker /absolute/firecracker --jailer /absolute/jailer \
  --version 1.17.0 --subnet <unused-private-/22>
```

For the first Guest release acceptance, `--executable` must select the independently downloaded and verified **v0.1.0-rc.5 application executable** above, proving the exact Guest candidate works with the published consumer. Later Guest releases must explicitly select their reviewed published application version and record that exact combination; do not substitute an unreviewed newer executable. Record its digest and version together with the downloaded candidate's archive, raw disk, manifest and `candidate.json` identities. For application-source development, omit that option to build an isolated current-source executable; label that result separately. Historical local images and fixture tests do not establish acceptance of a new downloadable candidate.

Use this fixed Linux amd64 input set for candidate acceptance, independently fetched and verified before invoking the launcher:

| Input | Source and SHA-256 |
| --- | --- |
| Firecracker/jailer `1.17.0` archive | [Official release archive](https://github.com/firecracker-microvm/firecracker/releases/download/v1.17.0/firecracker-v1.17.0-x86_64.tgz), `06094a1108ae9e82aa4c23a775aa92758f53f1175d422270d9d6162cb9ade558` |
| Firecracker executable | From that archive, `99ad0f5cd0514a88aad0e9ae8cfdb3cc3b4ab9d190e1194602406c786b5de7a5` |
| jailer executable | From that archive, `65ef226e96f0ceda55ba643f445801ef2cc0ea667ef67cad8ac4f406c9c8434f` |
| Linux `6.1.155` kernel | [Firecracker CI kernel](https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.14/x86_64/vmlinux-6.1.155), `e41c7048bd2475e7e788153823fcb9166a7e0b78c4c443bd6446d015fa735f53` |
| Kernel configuration | [Matching configuration](https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.14/x86_64/vmlinux-6.1.155.config), `024b2aae62fe7131f9a9ab80f27619c882c0a37265dd105adae01d2867bef7c3` |

These are separately administered inputs, excluded from the Guest bundle. Before approving a release, record the runtime release, kernel/configuration URLs and digests, applicable notices and exact kernel source retrieval materials. Inspect `CONFIG_FHANDLE`, ext4/export-handle and virtio/vsock support plus unified cgroup CPU, memory and process controllers, `cgroup.kill` and atomic `clone3` placement. Device presence, configuration flags and a readable host cgroup tree do not prove usable service delegation or guest task enforcement; the native run must exercise both. A replacement kernel/runtime combination needs its own review and acceptance rather than inheriting these checks.

The [Linux acceptance script](../scripts/verify-vm-firecracker.mjs) connects two fresh profiles through persisted `setup --socket` configuration and verifies that both observe the exact image digest and complete guest contract. Both run as root; this checks independent profile configuration, while desktop-account socket/group permissions require a separate operator check. It then verifies real lifecycle, guest execution, Guest Terminal detach/replay/resize/End, SSH/file transfers, SSH PTY resize and local/remote TCP forwarding, Internet egress, host isolation, controller recovery, [VM project/Git retention, persistent tasks and recovery](#vm-project-workspace-acceptance), and task expiry. A current complete image also declares `project-previews`, `project-restore` and `project-initialization`; run their initialization, preview and backup/restore helpers against this same candidate. The real in-guest install/build/tests and resource/space failures remain required after changing base disk capacity. It creates a dedicated transient system service with delegated cgroups, independent profile and owned short `/tmp/axv-fc-*` runtime directory. It verifies image/kernel digests and removes only its own VMs, volumes, service, TAPs, firewall table and directories. Interrupted submissions retain their original request IDs; uncertain cleanup preserves the profile with recovery commands. `AXV_VM_VERIFY_KEEP_FAILURE=1` explicitly retains failed-run state. VM inputs are never downloaded or provisioned automatically; the project workflow downloads its source and dependencies inside the VM.

On a Docker host using the iptables backend and a restrictive forwarding policy, explicitly add `--docker-uplink <interface>` to authorize two temporary `DOCKER-USER` rules scoped to this run's TAP prefix, guest subnet and uplink. Cleanup removes their exact owner-comment handles, including when preserving failed VMs. It does not change chain policies. Other firewalls require administrator integration as described in [VM networking](vm-platform.md#host-firewall-integration).

For the native kernel firewall regression without building a VM image:

```sh
AXV_TEST_FIRECRACKER_NETWORK=1 node scripts/test-go.mjs -race \
  -run TestNativeFirewallIPv4Egress -v ./apps/server/internal/compute/provider/firecracker
```

It requires root, `unshare`, `nsenter`, `ip`, `nft`, `ping` and `sleep`. Its routes, forwarding sysctls and packet probes live in isolated network namespaces; it verifies allowed egress, address spoofing and host/private/guest isolation, plus coexistence with a later forwarding denial. It complements the real VM acceptance.

### Local OpenSSH fixture

For a focused SSH transport check without VMs, enable the optional native OpenSSH fixture:

```sh
AXV_TEST_TMPDIR=.data/test AXV_TEST_OPENSSH=1 node scripts/test-go.mjs -race -run TestNativeOpenSSH -v ./apps/server/internal/sshstream
```

It requires local `sshd`, `ssh`, `scp` and `sftp`, plus a current account that the isolated server can authenticate. The fixture creates temporary keys, configuration and loopback listeners, exercises byte-stream EOF, file transfer and forwarding, then removes its own resources. The ordinary unit suite skips this environment-dependent check; it does not replace native VM/CLI acceptance.

## Image building and mirror validation

Follow the [image recipes](../images/guest/README.md) for build prerequisites and source selection; [startup](startup.md#download-mirrors-and-guest-package-sources) owns operator configuration and fallback rules. Native Linux image builds and enhanced guest acceptance remain separate explicit checks.

Controlled fixtures cover download integrity/recovery, package-source validation and isolation. Ordinary test launchers clear inherited `AXV_ARTIFACT_MIRRORS` while selecting a private cache; native acceptance and development retain explicit user selections. CI may supply both the cache path and operator-verified artifact mirror mappings.
