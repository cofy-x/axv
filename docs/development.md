# Development and verification

Use the [pinned toolchain](../README.md#install-and-use) and follow [AGENTS.md](../AGENTS.md). The [documentation map](README.md) routes feature context; this guide owns local development, builds, automated verification, CI/CodeQL, dependency patches and test isolation. Manual device, native-VM and real-provider acceptance procedures belong to [acceptance](acceptance.md).

## Local development

Install dependencies and run development as the same account with write access to the checkout and caches (following [execution identity](startup.md#execution-identity)):

```bash
make deps
make dev
```

- `make deps` installs locked JavaScript dependencies and prefetches the server module's declared Go dependencies with `GOWORK=off` (avoiding unrelated tool archives and unexpected `go.work.sum` edits). Builds and tests use the Go workspace normally.
- `make dev` builds the development server and starts Vite on available loopback ports; open the printed Vite URL. Explicit occupied ports fail instead of reusing another server.
- Each checkout gets a persistent profile at `~/.axv/dev/<checkout-id>` (first 12 hex chars of SHA-256 over the checkout's canonical absolute path; distinct worktrees get separate profiles). `AXV_DEV_HOME` overrides that final profile root directly. Temporary build executables live under the profile's `run/` and are cleaned on exit; prepared VM storage persists. Never point `AXV_DEV_HOME` at a production profile.
- After an incompatible schema baseline change, stop affected runs/services before explicitly resetting the affected AXV-owned store per [storage baseline policy](storage.md#desktop-database-baseline).

| Override | Purpose |
| --- | --- |
| `GO` | Go executable for Make and Node launchers (default `go`) |
| `PNPM` | Make's pnpm launcher (pinned default in [Makefile](../Makefile); nested `pnpm` scripts in [package.json](../package.json) resolve `pnpm` from `PATH`) |
| `ADDR`, `PORT` | Development backend address (`ADDR` wins over `127.0.0.1:PORT`) |
| `WEB_PORT` | Development frontend port |
| `AXV_DEV_HOME` | Final development profile root (desktop data is its `data/`) |
| `E2E_PORT` | Dedicated browser-test backend port |

The dev launcher replaces inherited `AXV_HOME` and `DATA_DIR` and clears `AXV_COMPUTE_SOCKET`.

## Production build

```bash
make build
./dist/axv version
```

- [`scripts/build.mjs`](../scripts/build.mjs) embeds the production dashboard into `dist/axv` (runs from any directory without Node). `make build-go` builds `dist/server-dev` without embedded UI assets.
- Production builds and Go vulnerability scans share the `production,ts_omit_webclient` tags in [`scripts/lib.mjs`](../scripts/lib.mjs), excluding Tailscale's separate node-management Web UI ([distribution notices](../THIRD_PARTY_NOTICES.md#application-distributions)).
- Production JS/CSS support negotiated gzip; content-fingerprinted assets use immutable caching while HTML/unversioned assets require revalidation.
- `make install` runs `deps`, `build` and atomic install into Go's binary directory (`GOBIN` or `GOPATH/bin`) without initializing data or starting services. For install checks, always pass a temporary `GOBIN` and short `AXV_HOME`.
- Builds report `dev` unless `VERSION` is supplied (`[A-Za-z0-9][A-Za-z0-9._+-]*`). Pass the same `VERSION` to smoke checks:

```bash
VERSION=1.2.3 make build
VERSION=1.2.3 make smoke-production
```

### Dashboard loading and performance

Every production dashboard build checks the JS entry graph and explicitly required startup modules via [`scripts/dashboard-bundle.mjs`](../scripts/dashboard-bundle.mjs) against [`scripts/dashboard-bundle-budget.json`](../scripts/dashboard-bundle-budget.json):

- [`apps/dashboard/vite.config.ts`](../apps/dashboard/vite.config.ts) declares `src/main.tsx` as required startup (even though loaded dynamically) so splitting startup code into dynamic chunks cannot hide startup cost. Raw bytes and independent gzip estimates have separate budgets; review module attribution before raising budgets.
- The build writes `dashboard-bundle.json` under the selected test root (`AXV_BUNDLE_REPORT` overrides within that root), excluded from embedded assets; browser loading tests use it to locate emitted feature chunks.
- The startup loader omits JS dependency preloads (retaining CSS preloads) so failed module preloads cannot stay cached across explicit startup reloads in WebKit; lazy feature chunks retain dependency preloads while excluding the dynamic entry itself to allow in-page retries. Keep preload resolver dependencies reflected in the Vite chunk-hash input. Never add URL cache-busting that duplicates module identity or forces desktop reloads over dirty drafts.

Measure the embedded production executable with the isolated harness:

```bash
pnpm run build:production
node scripts/measure-dashboard.mjs --samples 3
node scripts/measure-dashboard.mjs --conditions mobile --samples 3
```

Each run writes a report directory under `.data/test` (`--binary` compares retained executables; `--output` selects a fresh child directory). It records default/empty/restored desktops, cold/cached requests, first-open feature latencies, CDP CPU durations and long tasks. Controlled local HTTP fixtures do not establish private-HTTPS, physical-phone or real-VM/model performance.

## Verification

| Change | Required verification |
| --- | --- |
| Documentation only | `make format-check check-docs` and [documentation review](#documentation-review) |
| Runtime | Format, doc links, unit tests, typecheck, lint, production build and smoke below |
| Desktop or cross-layer | Runtime checks plus `make test-e2e` and [visual/interaction acceptance](design.md#acceptance-checks) |

```bash
make format
make format-check check-docs test typecheck lint build
make smoke-production
```

For desktop or cross-layer changes, also run `make test-e2e` and complete visual/interaction acceptance. For live-provider, physical-device, or native hypervisor changes, run the applicable checks in [acceptance](acceptance.md).

### Documentation review

Follow [content ownership](README.md#content-ownership): verify changed claims, defaults, limits and commands against linked code or manifests, check relative links and heading anchors (including inbound links to moved sections), and report any code-vs-rule discrepancy rather than silently rewriting rules. Documentation-only changes do not require runtime suites; changes to scripts, configs or generated runtime inputs are not documentation-only.

### Focused checks

Run targeted Go race tests with the isolated wrapper:

```sh
# Guest protocol, image validation, SSH registration and request/terminal handling (no VM needed)
node scripts/test-go.mjs -race ./apps/server/internal/guest/...

# Startup, config precedence, launch snapshots, supervision and CLI
node scripts/test-go.mjs -race \
  ./apps/server/internal/profile/... ./apps/server/internal/diagnostics \
  ./apps/server/internal/artifactcache ./apps/server/internal/config \
  ./apps/server/internal/app/... ./apps/server/internal/daemon \
  ./apps/server/internal/cli ./apps/server/internal/setup

# Remote transports (Tailscale, Cloudflare), access guards and route composition
node scripts/test-go.mjs -race \
  ./apps/server/internal/tailnet/... \
  ./apps/server/internal/cloudflare/... \
  ./apps/server/internal/httpaccess ./apps/server/internal/server \
  ./apps/server/internal/profile ./apps/server/internal/config ./apps/server/internal/cli
```

- **Production install test:** After `make build`, run `AXV_TEST_PRODUCTION_INSTALL=1 pnpm exec vitest run scripts/install.test.mjs` (pass `VERSION` for custom versions).
- **Cloudflare browser isolation:** [`tests/cloudflare-isolation.spec.ts`](../tests/cloudflare-isolation.spec.ts) exercises the real Go publication manager with Chromium, Firefox and WebKit (PSL rejection, host-only cookies, storage isolation, service-worker rejection). Preserve the negative control; note that WebKit request interception omits Cookie headers, so full TLS bootstrap requires separate network acceptance.
- **Playwright E2E:** On fresh Linux hosts, first install system libraries via `pnpm exec playwright install-deps chromium firefox webkit`. `make test-e2e` installs browsers, builds production assets and runs against an isolated server/database. For focused iteration (use `E2E_SKIP_BUILD=1` only when `dist/axv` already has your changes):

```bash
make test-e2e E2E_ARGS="--project=chromium"
E2E_SKIP_BUILD=1 make test-e2e E2E_ARGS="--project=chromium tests/desktop.spec.ts"
```

The `chromium-dpr2` and `chromium-touch` projects set both Chromium's native device scale and Playwright's emulated scale to 2 so sandboxed frames align rendering and pointer input.

### Focused VM UI review

Run `make test-vm-ui` for VM interface checks tagged `@vm-ui` (desktop and touch Chromium with fixture VM data and screenshot attachments):

```bash
make test-vm-ui E2E_ARGS="--output=.data/test/vm-ui-results"
```

Inspect screenshot attachments via `pnpm exec playwright show-report`.

### Production smoke

[`scripts/smoke-production.mjs`](../scripts/smoke-production.mjs) runs the embedded binary from an unrelated directory to verify CLI validation, listener isolation, persistence across restarts, default-disabled remote transports and session lifecycles. Extend it when production behavior changes.

## GitHub CI and dependency maintenance

[`.github/workflows/ci.yml`](../.github/workflows/ci.yml) runs on PRs, pushes to `main`, daily at 03:17 UTC and manual dispatch with pinned toolchains (`GOTOOLCHAIN=local`; newer runs cancel earlier ones on the same ref). On PRs, [`scripts/ci-changes.mjs`](../scripts/ci-changes.mjs) classifies changed paths relative to the merge base (falling back to full PR checks if unavailable).

| Required gate | Coverage |
| --- | --- |
| `checks` | `changes` plus applicable jobs: `format` (non-runtime PRs, plus shell syntax checks for guest recipes), or `Linux verification` + `Security` (runtime changes) and `macOS native` (when Go server/toolchain paths change) |
| `e2e` | `changes` plus applicable Playwright shards: 3 `chromium` shards on browser/server PRs, or all 6 projects (`chromium`, `firefox`, `webkit`, `chromium-dpr2`, `chromium-touch`, `webkit-touch`) × 3 shards on `main` pushes, daily schedules and manual dispatch |

- **Linux verification (Ubuntu 24.04):** Frozen `deps`, `format-check`, Go/Vitest tests, `typecheck`, `lint`, `build`, `smoke-production` and temporary production install; rejects any dirty or generated dependency/toolchain files after install. Ubuntu jobs use [`scripts/ci-ubuntu-mirrors.mjs`](../scripts/ci-ubuntu-mirrors.mjs) (keeping TLS and Ubuntu APT signature verification enforced).
- **macOS native (macOS 15 ARM64):** Native server build plus focused Go race tests for profile storage, Host Terminal, Codex and Lima adapters (fixture-based, no real VMs/launchd).
- **Branch protection:** Require `checks` and `e2e` plus resolved review conversations on `main`; disable force pushes/deletion; allow admin bypass (`enforce_admins: false`) and non-strict updates (`strict: false`), with auto-merge and auto-delete head branches enabled.
- **Security audits:** [`.github/workflows/security.yml`](../.github/workflows/security.yml) runs in runtime CI, on `main` pushes, weekly on Monday and on manual dispatch. Run locally with `make security` (`make audit-js` runs pnpm audit + verifies the scoped development glob patch; `make audit-go` builds production assets and runs pinned `govulncheck -test -tags production,ts_omit_webclient ./...` with `CGO_ENABLED=0`).

### First-party CodeQL analysis

[`.github/workflows/codeql.yml`](../.github/workflows/codeql.yml) is the repository's single advanced CodeQL setup (keep GitHub's default setup disabled). It runs on every push to `main`, Monday at 05:47 UTC and manual dispatch without path filters (keeping PR iteration fast while scanning every merged commit on `main`).

- Uses [`.github/codeql/config.yml`](../.github/codeql/config.yml) (`security-extended`, full analysis with diff-informed queries/overlays disabled) and pinned CodeQL Actions `v4.39.3` (`tools: linked`, CLI/packs **2.27.4**).
- Targets: `go-linux` (Ubuntu amd64), `go-darwin` (macOS ARM64), `javascript-typescript` and `actions`.
- Go extraction uses manual builds (`GOTOOLCHAIN=local`, `CGO_ENABLED=0`, `GOFLAGS=-mod=readonly`); [`scripts/codeql-coverage.mjs`](../scripts/codeql-coverage.mjs) rebuilds all workspace packages with `-a` and `production,ts_omit_webclient`, then verifies every selected Go source file appears as a `GoFile` in the finalized database (intentionally excluding `internal/webui/development.go`, tests and unsupported-OS stubs).
- SARIF and coverage evidence are retained as `codeql-<target>` artifacts for 7 days. Route actionable findings to the affected [feature owner](README.md#read-by-task); do not add broad query exclusions to silence alerts.

### Dependency updates and patches

- **Dependabot:** [`.github/dependabot.yml`](../.github/dependabot.yml) checks Actions, root pnpm workspace and `apps/server` Go modules every Monday (grouping minor/patch updates, max 5 open PRs per ecosystem; `node`/`pnpm` and `@types/node` major updates excluded). Keep Action references pinned to full commit SHAs. Upgrade Go, Node.js and pnpm deliberately in one coordinated change across version files, manifests, Makefile, Go workspace/modules, README and lockfiles.
- **xterm.js 6.0.0 patch (`pnpm-workspace.yaml`):** Fixes empty alternate-screen buffer capacity after shrinking ([regression test](../apps/dashboard/src/shared/terminal/xterm-buffer.test.ts), [attribution](../THIRD_PARTY_NOTICES.md#application-distributions)). Remove patch when adopting an upstream fix; keep the regression test.
- **Development glob patches (`picomatch 4.0.7` & bundled `prettier 3.10.1`):** Reject unbounded repetition in extended globs (mitigating `CVE-2026-112366` in dev tooling). Verified by [`scripts/verify-glob-patches.mjs`](../scripts/verify-glob-patches.mjs) during `make audit-js` and tested by [`scripts/toolchain-globs.test.mjs`](../scripts/toolchain-globs.test.mjs). Remove each patch independently once upstream ships a fix.

## Test isolation and diagnostics

Repository verification stores generated executables, fixtures, profiles and temporary files under `.data/test` by default (`AXV_TEST_TMPDIR` overrides it):

```sh
AXV_TEST_TMPDIR=.data/test-focused make test
```

- [`scripts/test-temp.mjs`](../scripts/test-temp.mjs) creates the root with mode `0700`, rejects symlinks/foreign ownership/unsafe modes, creates per-invocation children, and sets `TMPDIR` and `GOTMPDIR`. Cleanup removes only that invocation's children.
- Always run Go tests through `make test-go` or `node scripts/test-go.mjs [...]` (never bare `go test`, which skips temp isolation). For ad hoc `go run` helpers, set `TMPDIR` and `GOTMPDIR` under the test root. Never put generated executables, fixtures or scratch profiles in `/tmp` or `/var`; short Unix socket paths may use an explicitly owned short directory with separate cleanup when platform limits require it.
- Run desktop/Go suites under the identity being verified and privileged Linux VM/network checks separately as root, each with its own private test root and compiler cache.
- If a child process is killed unexpectedly, inspect its actual executable, ProxyCommand, working directory and temp paths before retrying; never disable or bypass host security controls.
- Browser and smoke tests require `tmux` on macOS/Linux and use deterministic Codex and shell fixtures ([`scripts/codex-fixture.mjs`](../scripts/codex-fixture.mjs), [`scripts/shell-fixture.mjs`](../scripts/shell-fixture.mjs)) with a private `HOME` and `/bin/sh` — no Codex CLI, login or user profile hooks required.
- [`tests/runtime-errors.ts`](../tests/runtime-errors.ts) fails browser tests on uncaught `error` and `unhandledrejection` events across reloads while retaining Playwright `pageerror` diagnostics (handling WebKit's cancelled-request diagnostic separately).
- On macOS/Linux, launchers own their child process groups and fail if cleanup fails. [`scripts/test-e2e-diagnostics.mjs`](../scripts/test-e2e-diagnostics.mjs) dumps the last 64 KiB of `daemon.log` and Codex pane diagnostics on E2E failures before cleanup. On macOS, a transient `EPERM` while probing an exiting process group is observed until `ESRCH` confirms exit.
- Keep databases, build outputs, screenshots, traces and browser reports untracked. Prettier formats both source and Markdown.

## Image building and mirror validation

Follow [image recipes](../images/guest/README.md) for guest image prerequisites and build commands, and [mirror/package-source policy](startup.md#download-mirrors-and-guest-package-sources) for operator settings. Ordinary test launchers clear inherited `AXV_ARTIFACT_MIRRORS` and use a private cache under the test root; development and native acceptance honor user cache and mirror selections.
