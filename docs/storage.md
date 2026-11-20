# Desktop storage and browser recovery

This guide owns the desktop database lifecycle, host-project registration and browser recovery identity. The [desktop-state API](api/desktop.md) owns request shapes and [desktop architecture](desktop.md) owns the layout save queue.

## Implementation map

- The [Go storage package](../apps/server/internal/storage) owns the desktop SQLite connection. [Settings](../apps/server/internal/storage/settings.go) persist preferences, layouts and database identity; [projects](../apps/server/internal/storage/projects.go) persist registered host roots. [Host Codex](codex.md) owns conversation and run persistence.
- The [host project files service](../apps/server/internal/projectfiles) verifies each registered root and supplies confined read-only file access. Its [HTTP adapter](../apps/server/internal/projectfiles/httpapi) owns request decoding and access checks. [Project Files API](api/project-files.md) owns the request contract.
- [Browser client identity](../apps/dashboard/src/app/client-identity.ts) binds a page to its client layout and recovery namespace. The [VM Project Files editor](../apps/dashboard/src/features/project-files/VMProjectFiles.tsx) keeps local drafts for its original VM binding. [Installed extensions](#installed-application-storage) own their application data and draft lifecycle.
- Begin schema and layout recovery verification with [migration tests](../apps/server/internal/storage/migrations_test.go) and [layout recovery tests](../apps/server/internal/storage/recovery_test.go). [Development](development.md#verification) owns verification commands.

Cloudflare account credentials belong to the private profile configuration. Optional service publications use the feature-owned `AXV_HOME/cloudflare/publications.json` journal. Its experimental version-2 baseline retains original targets, name generations, reserved cookie sites, request receipts and cloud-resource ownership for explicit resume and cleanup. A missing journal does not create state until publishing is accepted unless its ownership lock already exists, in which case it reports an error. Older, unknown or malformed journals are preserved and rejected. Retain publication history and reserved sites across maintenance. [Cloudflare contract](api/cloudflare.md#published-project-services) owns lifecycle and recovery.

## Notification storage

The notification feature independently owns `AXV_HOME/notifications/state.json` and its private owner lock, separate from layouts, extensions and execution specifications. This version-1 experimental state has its own persistent identity, configuration revision, credential, producer checkpoints and bounded outbox/history. It seeds only on initial creation. Invalid, unsafe, missing established or unknown-version state is preserved and leaves notifications unavailable; it is never silently reset. Stop the desktop dispatcher before an explicit restore or reset of this feature directory. A reset creates a new notification identity, invalidates old links and discards pending delivery; it does not remove producer-owned journals or provider-owned history. Preserve credential permissions. [Notification ownership and API](api/notifications.md) owns the complete retention and recovery contract.

## Browser recovery

The browser retains its existing `nextstep-browser-id` in local storage. First initialization uses a Web Lock and an IndexedDB witness to coordinate a single ID. A failed initialization offers retry. An established ID that becomes missing or conflicting requires explicit browser storage recovery. A mounted page warns if another page changes that key and retains its captured identity.

Layout and draft ownership are separate. VM file recovery stays in its owning tab's session storage and retains the original VM binding and file hash. Installed extensions use their own store identity and [draft lifecycle](api/extensions.md#browser-broker). Browser installation uses the same client-ID rules as a tab, with storage sharing determined by the browser and profile. [Desktop architecture](desktop.md#persistent-layout-and-recovery) explains the resulting layout behavior.

## Project root access

Registered host projects persist a canonical root and filesystem identity independently of Codex's recent-directory list. Host-file access opens the root and checks its device/inode identity before accessing files. Explicit registration of a replacement directory creates another project ID for the new identity; existing references retain their original root identity.

File operations stay constrained to the opened root. Ancestor symlinks may resolve only within it; final symlinks and special files are rejected. Unix reads use nonblocking and no-follow flags so a racing FIFO or final symlink cannot block a request or escape the root. Unsupported platforms fail explicitly instead of falling back to path-only identity. The [Project Files API](api/project-files.md#browse-read-and-download) owns path syntax, supported operations and limits.

## Desktop database baseline

`desktop.sqlite` starts from one consolidated [initial schema](../apps/server/internal/storage/migrations/0001_initial.sql) with format marker **AXV5** (`0x41585635`). It holds the stable database identity, shared preferences, browser layouts, registered host projects, and Codex conversations and runs with their recorded tmux routing and optional VM work context.

SQLite's `application_id` is a file-format marker, separate from the migration version in `user_version`. Its value must agree between `databaseApplicationID` in [`migrations.go`](../apps/server/internal/storage/migrations.go) and the initial SQL migration. It distinguishes incompatible development baselines even when their migration versions match. Startup rejects incompatible or invalid databases without deleting, resetting or destructively repairing them.

AXV has experimental prereleases and no stable release. Breaking schema changes and refactors are allowed; keep one clean current baseline and remove obsolete compatibility code. Obsolete development stores do not require migration, old-format adapters or a parallel compatible executable. Change the format marker for an incompatible baseline, stop the affected writers and Codex runs, then explicitly delete and recreate only the affected AXV-owned development store after the user chooses that reset. Host projects and provider-owned history/configuration are separate and are not part of this reset. Normal startup still rejects an incompatible or invalid database rather than silently deleting an arbitrary file.

Experimental release candidates do not promise automatic migration or profile compatibility between versions. Release notes must identify incompatible AXV-owned store changes. Preserve wanted data and the original profile before trying another candidate; use a fresh `AXV_HOME` when compatibility has not been established. A store reset requires an explicit user choice after stopping its affected services, writers and runs. Preserve ownership records until their resources have been stopped or cleaned up through the owning feature; do not clear an entire profile or discard Cloudflare publication history and reserved-site ownership. Host projects, provider-owned history/configuration and unrelated tmux sessions remain outside the reset. Stable-release compatibility is a separate policy decision before a stable release.

### Recover data from AXV4 before switching

AXV5 removes the virtual document library, Text Editor, Recycler and Codex document snapshots. The previous format is incompatible. Keep the original profile and its AXV4 executable, and use a fresh profile for AXV5. Close or finish any pending document deliveries and end affected old runs through the original instance before retiring its service.

Use the AXV4 interface with the original profile to download saved files, restore and download wanted Recycler items, and open retained snapshots from document Details or Codex attachment History to copy their text. For unsaved text, reopen the old editor using the same browser profile and origin, restore the draft if prompted, and use **Download Draft**. Keep browser site data and the old database until you have verified the recovered files. Host project files and provider conversation history live in their original locations.

## Installed application storage

[`extensions`](../apps/server/internal/extensions) owns `extensions.sqlite`, opened separately by the desktop service in its resolved data directory. Its embedded [`0001_initial.sql`](../apps/server/internal/extensions/migrations/0001_initial.sql) uses the **AXE2** format marker (`0x41584532`). A persistent UUID identifies this store independently of `desktop.sqlite` and scopes browser recovery, management actions, data reads/writes, content navigation and runtime sessions. The backend requires the original captured identity, so another store with matching application IDs and revision counters cannot receive an old operation or draft. The [application API](api/extensions.md#access) owns the header and content-path contract. Startup initializes a newly created file only; existing uninitialized, incompatible, unknown-layout or damaged databases are rejected without resetting them. Format and schema changes follow the same [experimental baseline policy](#desktop-database-baseline), with this store's own marker and affected writers.

Startup compares a fingerprint of SQLite's complete schema catalog with a pristine in-memory database built through the same embedded migrations and shared runner. Column definitions, types, keys, defaults, checks, references, indexes and triggers must match the owned baseline. This uses SQLite's recorded DDL directly, without permissive SQL normalization or a second manually maintained schema. Text primary keys explicitly reject NULL; generations and revisions require SQLite integer values as well as their positive/nonnegative ranges. Startup also checks data types and orphan data rather than relying on column affinity alone. The service verifies every retained package's complete canonical bytes and digest before serving requests. Catalog and runtime grant queries project only the manifest to Go; SQLite still reads the stored package JSON, but the Go metadata path does not load or repeatedly decode its HTML.

Immutable canonical packages are stored by SHA-256 in a code table. Installation metadata holds the active digest, enabled/installed state and monotonically increasing generation. A different table holds one JSON document and its revision per application. Lifecycle changes preserve this document; uninstall retains metadata and data, while a separate revision-checked action explicitly clears data only after uninstall. The [application API](api/extensions.md) owns limits, generation checks and session invalidation.

Previous package digests are retained as well. There is currently no package-history rollback or automatic code cleanup; repeated upgrades increase database size and the work needed for startup integrity checks.

Extension saved-data reads and browser management exports are scoped to the installed application. Resetting only the desktop database does not reset extension state; an explicit extension-store reset creates a different store UUID on the next startup. Stop its desktop writer and preserve wanted application data before explicitly resetting this AXV-owned development store. Neither reset includes host projects or provider history.

## Migration runner

Each SQLite owner embeds its own migration directory. Desktop SQL lives in [`storage/migrations`](../apps/server/internal/storage/migrations); installed applications use [`extensions/migrations`](../apps/server/internal/extensions/migrations), and compute and preparation have separate [VM database ownership](vm-platform.md#ownership). They share the [`sqlitemigrate`](../apps/server/internal/sqlitemigrate) executor, never tables, identities or initialization data.

SQL files use `NNNN_description.sql` names and are embedded by Go at build time. Unique, consecutive numeric prefixes starting at `0001` define both execution order and the latest supported schema. There is no separate Go version constant or registration list to update, and the executable needs no SQL files beside it.

The [shared runner](../apps/server/internal/sqlitemigrate/migrations.go) owns the transaction and `PRAGMA user_version`; the [desktop adapter](../apps/server/internal/storage/migrations.go) supplies its format marker and identity initializer. Migration scripts must not manage either. All pending files, their recorded versions and first-run seeding commit together; any failure rolls back the entire upgrade, including earlier files and version changes.

The executor also verifies that the SQL format marker matches its owning adapter before committing. Initial schema creation establishes the database identity and default preferences in that transaction. Unknown newer schema versions and nonempty unversioned databases are rejected without resetting data.
