# Working on AXV

AXV is a personal cloud workstation for VM management and coding-agent sessions. Keep application changes in this repository unless explicitly requested otherwise.

## Repository layout and context

- `apps/dashboard`: React browser desktop and feature views.
- `apps/server`: Go desktop service, Codex runner, compute host controller, guest service and CLI.
- `packages/nextstep`: Generic NEXTSTEP / Window Maker desktop shell (no dashboard imports or AXV app IDs).
- `images/guest`, `recipes`, `scripts`, `tests`: Guest image builders, optional VM/service recipes, build/verification launchers and Playwright E2E suites.

Before editing, follow the [context workflow](docs/README.md#choose-context) to pick the single owning document, implementation entry points and required checks. Limit reading to the affected boundaries.

## Repository rules

- Use **AXV** for the product and `axv` for executable and project identifiers ([product identity](docs/product.md#identity)). Preserve `packages/nextstep`, `@cofy-x/axv-nextstep` and existing internal identifiers; do not rename them solely for branding.
- Update facts in their [owning document](docs/README.md#content-ownership); link instead of copying. Replace obsolete text in place rather than appending caveats, and do not mirror code constants, struct fields or UI button copy already enforced by tests. Keep reading paths and maintenance rules in the map.
- Use pinned toolchains; keep `.go-version`, `.node-version`, `.nvmrc`, `package.json`, `go.work`, Go module files, README and lockfiles consistent when upgrading.
- Use English in code, comments and the default interface. Draw original generic assets; record third-party sources and licenses before reuse.
- Review current provider documentation when implementing authentication or agent protocols.

## Preserve ownership and user state

- **Composition and desktop:** Preserve feature ownership, the generic Nextstep boundary and Window Maker interactions. Keep mounted window identity stable and hidden content unable to receive input or focus. Follow [desktop](docs/desktop.md) and [design](docs/design.md) for menus, shared controls and layout persistence.
- **Execution and Codex:** Workspaces, projects, targets, conversations and runs are distinct. Codex disconnect detaches; End addresses the exact run. Never redirect old commands to a newer run or substitute a fresh conversation for missing history. Follow [lifetime boundaries](docs/architecture.md#codex-attachment-and-process-lifetime) and [Codex ownership](docs/codex.md).
- **Host ownership:** Preserve recorded tmux routing, user configuration and unrelated sessions. Never change global tmux options/environment or use `kill-server` in application code. Host projects and provider-owned history/configuration are outside AXV-owned store resets.
- **VM project edits and tasks:** Save explicit snapshots; preserve newer typing, original environment binding, hash checks and failed writes as dirty state. Draft replacement, conflict rebasing and environment rebinding require an explicit user choice. See [VM project files](docs/usage.md#project-files) and [VM architecture](docs/vm-platform.md#vm-projects).
- **Extensions and GitHub Review:** Installed extensions stay in opaque sandbox origins with private app data only ([Extensions API](docs/api/extensions.md)). GitHub Review keeps its own `reviews.sqlite`, local candidate drafts and dedicated VM test bindings ([GitHub Review API](docs/api/github-review.md)).
- **Persistence:** AXV has no stable release: consolidate the current experimental baseline instead of adding compatibility paths for obsolete development data. Stop affected writers and runs before an explicit reset of their AXV-owned store. Never silently reset invalid databases or unknown layouts; seed only on initial creation. Follow the [baseline policy](docs/storage.md#desktop-database-baseline).
- **Access and remote transports:** Host-directory metadata and target-scoped project-file access have separate contracts. Preserve [local access checks](docs/api/README.md#access-and-identity-boundaries); remote setup, VM preparation and extension administration stay machine-local, and remote transports (Tailscale, Cloudflare) enforce explicit owner authorization and isolated browser origins for published services.

## Verify and finish

- Use Prettier and shared ESLint rules, correct Hook dependencies and visible asynchronous errors.
- Run the required checks for the change in the [verification matrix](docs/development.md#verification) (and [acceptance](docs/acceptance.md) when live, device or native VM checks apply). Report results and any unavailable checks; never hide failures or suppress checks.
- Use documented launchers and this checkout's `.data/test` for generated test executables, fixtures and scratch profiles (`AXV_TEST_TMPDIR` overrides it). Use `make test-go` / `scripts/test-go.mjs`; configure local `TMPDIR` and `GOTMPDIR` for ad hoc helpers. Follow [test isolation](docs/development.md#test-isolation-and-diagnostics) for short socket paths, cleanup and unexpected process kills; do not bypass security controls.
- Do not commit dependencies, build outputs, databases, uploads, screenshots, coverage, traces or browser reports.
