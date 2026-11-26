# Desktop storage and browser recovery

This guide owns the experimental storage baseline policy, the shared SQLite migration runner, `desktop.sqlite` and `extensions.sqlite` ownership, host project root confinement and browser recovery identity. [Desktop API](api/desktop.md) owns serialized state shapes; [desktop architecture](desktop.md#persistent-layout-and-recovery) owns the layout save queue.

## Implementation map

| Store / Concern | Owner & Entry Points |
| --- | --- |
| Desktop SQLite (`desktop.sqlite`) | [`internal/storage`](../apps/server/internal/storage) (`settings.go`, `projects.go`, `migrations.go`, `migrations_test.go`, `recovery_test.go`) |
| Installed extensions (`extensions.sqlite`) | [`internal/extensions`](../apps/server/internal/extensions) (`store.go`, `migrations/0001_initial.sql`) |
| Shared migration runner | [`internal/sqlitemigrate/migrations.go`](../apps/server/internal/sqlitemigrate/migrations.go) |
| Host project files | [`internal/projectfiles`](../apps/server/internal/projectfiles) & [`projectfiles/httpapi`](../apps/server/internal/projectfiles/httpapi) ([API](api/project-files.md)) |
| Browser client identity & drafts | [`client-identity.ts`](../apps/dashboard/src/app/client-identity.ts), [`VMProjectFiles.tsx`](../apps/dashboard/src/features/project-files/VMProjectFiles.tsx), [`shared/drafts/session.ts`](../apps/dashboard/src/shared/drafts/session.ts) |

## AXV-owned store baselines

Every AXV-owned store uses an explicit format marker or version and seeds only on initial creation. Invalid, incompatible or unknown-version stores fail visibly and are never silently reset.

| Store (under `AXV_HOME`) | Format / Version | Owner & Scope |
| --- | --- | --- |
| `data/desktop.sqlite` | SQLite **AXV5** (`0x41585635`) | Database identity, shared preferences, browser layouts, host projects, Codex conversations/runs |
| `data/extensions.sqlite` | SQLite **AXE2** (`0x41584532`) | Independent store UUID, immutable extension packages, installation generations and private app JSON |
| `data/github-review/reviews.sqlite` | SQLite **AXR3** (`0x41585233`) | GitHub Review config, PR queue, evidence, comparisons and local candidate drafts ([API](api/github-review.md)) |
| `compute/compute.sqlite` | SQLite **AXC4** (`0x41584334`) | Host identity, VMs, volumes, images, operations, VM projects, persistent tasks and task events ([VM](vm-platform.md#ownership)) |
| `preparation/preparation.sqlite` | SQLite **AXP1** (`0x41585031`) | Profile-scoped VM preparation plans and jobs ([VM](vm-platform.md#ownership)) |
| `notifications/state.json` | JSON v**1** | Private notification store ID, credentials, producer checkpoints and bounded outbox ([API](api/notifications.md)) |
| `cloudflare/publications.json` | JSON v**2** | Published VM service receipts, reserved cookie sites and cloud resource ownership ([API](api/cloudflare.md#published-project-services)) |

Cloudflare account credentials live in private `AXV_HOME/config.json`. Keep `cloudflare/publications.json` reserved-site history across maintenance; missing journals do not auto-create unless no ownership lock exists.

## Notification storage

`AXV_HOME/notifications/state.json` has its own persistent store UUID, exclusive lock (`0700`/`0600`) and config revision independent of desktop/extension databases. Stop the desktop dispatcher before an explicit reset or restore; resetting creates a new store identity, invalidates old return links and discards pending sends without touching producer journals or provider history.

## Browser recovery

- Browser client ID (`nextstep-browser-id` in `localStorage`) initializes via a Web Lock + IndexedDB witness. Missing or conflicting established IDs require explicit browser storage recovery; mounted pages warn on external changes while keeping their captured identity.
- Layout and draft recovery stay separate: VM file drafts live in tab `sessionStorage` bound to `(host, project, VM, boot, guestInstance, sha256)`; extension drafts scope to the extension store UUID and per-tab editor claim ([Extensions API](api/extensions.md#browser-broker)). Installed browser windows follow the same client-ID rules as tabs.

## Project root access

Registered host projects persist a canonical path and Unix device/inode root identity independently of Codex's recent-directory list. Replacing a directory at the same path requires explicit new registration (creating a new project ID) rather than retargeting existing windows. File reads/downloads confine ancestor symlinks to the opened root, reject final symlinks and special files using nonblocking/no-follow flags, and fail explicitly on unsupported platforms ([Project Files API](api/project-files.md#browse-read-and-download)).

## Desktop database baseline

- `desktop.sqlite` initializes from one consolidated [`0001_initial.sql`](../apps/server/internal/storage/migrations/0001_initial.sql) with `PRAGMA application_id = 0x41585635` (**AXV5**), checked against `databaseApplicationID` in [`migrations.go`](../apps/server/internal/storage/migrations.go) independently of `user_version`.
- **Experimental baseline policy:** AXV has experimental prereleases and no stable release yet. Consolidate breaking schema changes into the current clean baseline rather than adding legacy migration adapters; bump the format marker when incompatible and note breaking store changes in release notes.
- **Explicit reset boundary:** Startup always rejects incompatible or damaged stores without deleting them. An explicit reset requires user choice after stopping affected services, writers and Codex runs (and preserving ownership records until live resources are stopped). Host projects, provider-owned history/credentials and unrelated tmux sessions stay outside AXV store resets.

## Installed application storage

- [`extensions`](../apps/server/internal/extensions) opens `extensions.sqlite` (**AXE2**, [`0001_initial.sql`](../apps/server/internal/extensions/migrations/0001_initial.sql)) with its own persistent store UUID scoping browser recovery, catalog mutations, content URLs and runtime sessions ([API](api/extensions.md#access)).
- On startup, the service fingerprints SQLite's full DDL catalog against a pristine in-memory database built from the embedded migrations, verifies non-null primary keys/column types/orphan rows, and validates SHA-256 digests of all retained canonical packages before serving.
- Code is stored immutably by SHA-256 (previous digests are retained; no automatic rollback or GC yet). Lifecycle changes preserve saved app JSON; uninstall retains metadata and data, and explicit data clearing is a separate revision-checked action.

## Migration runner

Each SQLite owner embeds its own `NNNN_description.sql` directory (starting at `0001`) and invokes [`sqlitemigrate`](../apps/server/internal/sqlitemigrate/migrations.go), sharing only the transactional executor — never tables or identities.

The runner owns the transaction, `PRAGMA user_version` and format-marker verification against the owning adapter; SQL files must not manage transactions or `user_version`. All pending migrations and initial seeding commit atomically or roll back completely. Unknown future versions and nonempty unversioned files are rejected without reset.
