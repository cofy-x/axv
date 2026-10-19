# Development and verification

Use the [pinned toolchain](../README.md#install-and-use) and follow [AGENTS.md](../AGENTS.md). The [documentation map](README.md) selects the references needed for a change; this guide owns commands, verification and test isolation.

## Local development

Install dependencies and run development as the same account, with write access to the checkout and dependency/Vite caches. The [startup identity rules](startup.md#execution-identity) apply:

```bash
make deps
make dev
```

`make deps` installs locked JavaScript dependencies and downloads Go modules. `make dev` builds the development server, then starts Vite on available loopback ports. Open the printed Vite URL. Explicit occupied ports fail instead of reusing another server.

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

`make install` runs dependency setup, this production build and atomic installation into Go's binary directory sequentially. It does not initialize user data or launch services. [Installation and updates](startup.md#installation-and-updates) owns destination, PATH and live-service update semantics. For installation checks, always select a temporary `GOBIN` and a separate short `AXV_HOME`; never overwrite a real installation as part of automated verification.

Builds report `dev` unless `VERSION` is supplied. A version must start with an ASCII letter or digit and contain only ASCII letters, digits, `.`, `_`, `+`, or `-`. For a custom build, pass the same version to its smoke check:

```bash
VERSION=1.2.3 make build
VERSION=1.2.3 make smoke-production
```

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

The [CI workflow](../.github/workflows/ci.yml) runs for every pull request, pushes to `main`, and manual dispatches. Verification jobs use Ubuntu 24.04 hosted runners and the pinned Go, Node.js and pnpm versions from `.go-version`, `.node-version` and `package.json`. There are no path filters; documentation changes also receive both required checks. A newer run on the same ref cancels the previous run.

| Check | Coverage |
| --- | --- |
| `checks` | Locked dependency installation, formatting, Go and Vitest tests, typecheck, lint, production build, production smoke and temporary production installation |
| `e2e` | Requires every browser job to pass, covering the full `test:e2e` browser, display-scale and touch matrix |

The six Playwright projects run on separate runners, each retaining one worker and its own isolated server and profile. A failed browser does not cancel the other projects. The aggregate `e2e` job runs even when a dependency fails and succeeds only if every browser job succeeds; a failed or skipped dependency cannot produce a passing aggregate check.

Verification jobs install tmux and use deterministic Codex fixtures without provider credentials or model requests. They run the existing launchers as the runner user with the repository's private `.data/test` root. The workflow caches only the pnpm store and Go module/compiler caches. It retains each failed browser project's reports, screenshots and traces for seven days; AXV profiles and test roots are excluded from uploaded artifacts. `GOTOOLCHAIN=local` keeps checks on the selected Go version instead of downloading a newer toolchain to satisfy a dependency.

Hosted CI covers the commands above. Native daemon, VM, network and real-Codex acceptance still require their explicit environments and procedures below. Passing CI does not establish native VM support or replace visual/interaction acceptance.

Maintain `main` protection with the GitHub Actions checks `checks` and `e2e` required, the branch up to date before merging, and review conversations resolved. Disable force pushes and branch deletion. The baseline does not require approval from a second maintainer, so a sole maintainer can merge a verified pull request. Configure these repository settings after both check names have appeared in a successful run; workflow files do not configure branch protection.

The [Dependabot configuration](../.github/dependabot.yml) checks GitHub Actions, the root pnpm workspace through the `npm` ecosystem, and Go modules under `apps/server` every Monday. Each ecosystem groups minor and patch updates and permits at most five open version-update pull requests; major updates remain separate. Security alerts and security-update pull requests depend on the repository's GitHub settings and are not limited to the weekly version-update schedule. Dependabot does not merge pull requests automatically.

Review dependency changes with their lockfiles and run the same verification matrix. For pnpm updates, check the workspace manifests, overrides and frozen-lockfile installation together. The hosted Dependabot updater has been verified to install and use the pnpm version pinned by `packageManager`. Keep Action references pinned to full commit SHAs and verify replacement SHAs against the upstream release. Upgrade Go, Node.js and pnpm deliberately in one change, keeping the version files, manifests, Makefile, Go workspace/modules, README and lockfiles aligned. The npm configuration excludes `node` and `pnpm` updates; a dependency that requires a newer toolchain needs this coordinated upgrade before it can pass CI.

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

On macOS, a process-group probe can return `EPERM` while exited children await reaping; that error alone does not identify security-software intervention. The launcher keeps observing within its existing cleanup deadline and succeeds only after `ESRCH` confirms the group is absent. A persistent permission error remains a cleanup failure.

Keep generated databases, build outputs, screenshots, traces and browser reports untracked. Formatting covers documentation as well as source; use the repository's Prettier and ESLint configuration.

## Native daemon smoke

After `make build`, run `node scripts/smoke-daemon.mjs` in a macOS GUI login or Linux systemd user session when changing background supervision or environment propagation. It checks native launch/update behavior and Codex continuity using a temporary installation, private profile, deterministic CLI and tmux socket, without creating VMs. The [script](../scripts/smoke-daemon.mjs) owns its assertions; [startup](startup.md#agent-settings-and-background-lifecycle) owns background lifecycle behavior.

## Real Codex acceptance

`make verify-codex-live` is an explicit real-model check, separate from automated fixtures. First build AXV and install Chromium with `pnpm exec playwright install chromium`. Run it with the intended desktop account and its existing provider sign-in. It requires a Codex CLI executable by that account, tmux and an already trusted project; complete sign-in and trust in a host terminal beforehand.

The default project is this repository; `pnpm verify:codex-live /absolute/project` selects another. The check sends two short requests using existing model and approval settings to verify live continuity and saved-context continuation.

The check isolates AXV data, profile and tmux routing, inherits proxy configuration, and writes a credential-free result to `.cache/codex-live/last-run.json` without a transcript. It uses your model access; run it only when that acceptance is intended. The [script](../scripts/verify-codex-live.mjs) owns the detailed assertions.

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
  --kernel /absolute/vmlinux --kernel-sha256 <reviewed-sha256> \
  --firecracker /absolute/firecracker --jailer /absolute/jailer \
  --version 1.17.0 --subnet <unused-private-/22>
```

The [Linux acceptance script](../scripts/verify-vm-firecracker.mjs) builds an isolated executable and verifies real lifecycle, guest execution, Guest Terminal detach/replay/resize/End, SSH/file transfers, SSH PTY resize and local/remote TCP forwarding, Internet egress, host isolation, controller recovery, retained project data and task expiry. It creates a dedicated transient system service with delegated cgroups, independent profile and owned short `/tmp/axv-fc-*` runtime directory. It verifies image/kernel digests and removes only its own VMs, volumes, service, TAPs, firewall table and directories. Interrupted submissions retain their original request IDs; uncertain cleanup preserves the profile with recovery commands. `AXV_VM_VERIFY_KEEP_FAILURE=1` explicitly retains failed-run state. Inputs are never downloaded or provisioned automatically.

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
