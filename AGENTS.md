# Working on AXV

AXV is a personal cloud workstation for VM management and coding-agent sessions. Keep application changes in this repository unless explicitly requested otherwise.

## Choose context before editing

Follow the [context workflow](docs/README.md#choose-context) for project orientation, feature work or documentation-only changes. It selects the required overview, affected owner, implementation entry points and checks. Limit reading to the relevant sections.

## Repository rules

- Use **AXV** for the product and `axv` for executable and project identifiers. [Product identity](docs/product.md#identity) owns branding and stable package descriptions. Preserve `packages/nextstep`, `@cofy-x/axv-nextstep` and existing internal identifiers; do not rename them solely for branding.
- Update facts in their [owning document](docs/README.md#content-ownership); link to details instead of copying them. Keep reading paths and documentation maintenance in the map.
- Use pinned toolchains; keep `.go-version`, `.node-version`, `.nvmrc`, `package.json`, `go.work`, Go module files, README and lockfiles consistent when upgrading.
- Use English in code, comments and the default interface. Draw original generic assets; record third-party sources and licenses before reuse.
- Review current provider documentation when implementing authentication or agent protocols.

## Preserve ownership and user state

- **Composition and desktop:** Preserve feature ownership, the generic Nextstep boundary and Window Maker interactions. Keep mounted window identity stable and hidden content unable to receive input or focus. Follow [desktop](docs/desktop.md) and [design](docs/design.md) for menus, shared controls and layout persistence.
- **Execution:** Workspaces, projects, targets, conversations and runs are distinct. Codex disconnect detaches; End addresses the exact run. Never redirect old commands to a newer run or substitute a fresh conversation for missing history. Follow the [lifetime boundaries](docs/architecture.md#codex-attachment-and-process-lifetime) and [Codex ownership](docs/codex.md).
- **Host ownership:** Preserve recorded tmux routing, user configuration and unrelated sessions. Never change global tmux options/environment or use `kill-server` in application code. Host projects and provider-owned history/configuration are outside AXV document storage and resets.
- **Documents:** Save explicit snapshots; preserve newer typing, revision checks and failed writes as dirty state. Draft replacement or conflict rebasing requires an explicit user choice. Preserve recovery identity and transaction/deletion boundaries; see [storage](docs/storage.md) and [Files API](docs/api/files.md).
- **Persistence:** AXV has no stable release: consolidate the current experimental baseline instead of adding compatibility paths for obsolete development data. Stop affected writers and runs before an explicit reset of their AXV-owned store. Never silently reset invalid databases or unknown layouts; seed only on initial creation. Follow the [baseline policy](docs/storage.md#desktop-database-baseline).
- **Access:** Virtual documents use opaque IDs, never host paths. Host-directory metadata and target-scoped project-file access have separate contracts. Preserve [local access checks](docs/api/README.md#access-and-identity-boundaries); remote access requires explicit authentication and authorization design.

## Verify and finish

- Use Prettier and shared ESLint rules, correct Hook dependencies and visible asynchronous errors.
- Run the required checks for the change in the [verification matrix](docs/development.md#verification). Report results and any unavailable checks; never hide failures or suppress checks.
- Use documented launchers and this checkout's `.data/test` for generated test executables, fixtures and scratch profiles (`AXV_TEST_TMPDIR` overrides it). Use the Go test wrapper; configure local `TMPDIR` and `GOTMPDIR` for ad hoc helpers. Follow [test isolation](docs/development.md#test-isolation-and-diagnostics) for short socket paths, cleanup and unexpected process kills; do not bypass security controls.
- Do not commit dependencies, build outputs, databases, uploads, screenshots, coverage, traces or browser reports.
