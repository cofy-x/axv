# Document and storage architecture

This guide owns virtual-document state, browser draft recovery and the desktop SQLite lifecycle. The [Files API](api/files.md), [project-file and document-exchange API](api/project-files.md) and [desktop-state API](api/desktop.md) own request shapes, limits and errors. [Desktop architecture](desktop.md) owns the layout save queue; layouts never contain document text. Host and guest project files remain separate from the virtual filesystem.

## Implementation map

- [The filesystem service](../apps/dashboard/src/filesystem) owns entries, selections and shared file commands. Files, Text Editor, Inspector and Recycler use the same opaque entry IDs. Metadata refreshes follow mutations and possible external changes. Inspector derives paths and directory summaries from this model and loads only the preview content it needs.
- [The document controller](../apps/dashboard/src/features/notes/note-document.ts) owns editable text, acknowledged content, expected revision and save/recovery state. Its [React adapter](../apps/dashboard/src/features/notes/useNoteDocument.ts) connects file requests and local recovery storage.
- [Browser draft storage](../apps/dashboard/src/shared/drafts) owns IndexedDB records and independent editor draft identities.
- [The Go storage package](../apps/server/internal/storage) owns desktop SQLite connections and transactions. [`entries.go`](../apps/server/internal/storage/entries.go), [`trash.go`](../apps/server/internal/storage/trash.go) and [`settings.go`](../apps/server/internal/storage/settings.go) own document mutations, deletion groups and desktop settings; [`store.go`](../apps/server/internal/storage/store.go) supplies their transaction layer. Storage reports business error codes; [`httpapi`](../apps/server/internal/httpapi) chooses response statuses.
- [`documents.go`](../apps/server/internal/storage/documents.go) owns registered project references, document links, immutable snapshots, import origins and transfer receipts. [`projectfiles/files.go`](../apps/server/internal/projectfiles/files.go) owns root-constrained reads, [`open_unix.go`](../apps/server/internal/projectfiles/open_unix.go) owns platform identity and open flags, and [`transfers.go`](../apps/server/internal/projectfiles/transfers.go) coordinates publication with storage. The package's HTTP adapter is independent of Codex lifecycle and the generic desktop.

Codex conversation/run persistence is described in [Host Codex](codex.md). Compute uses a separate database and ownership model described in [VM architecture](vm-platform.md).

Installed applications use an independent [extension store](#installed-application-storage). Their JSON data is outside the virtual-file, document snapshot, document export and project-exchange APIs.

Cloudflare account credentials belong to the private profile configuration. Optional service publications use the feature-owned `AXV_HOME/cloudflare/publications.json` journal, separate from document storage, layouts and local preview registrations. Its unreleased version-2 baseline retains original target captures, name/generation, permanently reserved cookie sites, request receipts and cloud-resource ownership for explicit resume and cleanup; it is not a user document or export. A missing journal does not create state until publishing is accepted. Older, unknown or malformed journals are preserved and rejected. A missing journal with an existing ownership lock is an error, not a new empty baseline; do not discard history or reuse its application sites after an explicit profile reset. The [Cloudflare contract](api/cloudflare.md#published-project-services) owns its lifecycle and recovery boundaries.

For save and recovery changes, start with [`note-document.test.ts`](../apps/dashboard/src/features/notes/note-document.test.ts) and [`session.test.ts`](../apps/dashboard/src/shared/drafts/session.test.ts). For schema or stored-state changes, start with [`migrations_test.go`](../apps/server/internal/storage/migrations_test.go) and [`recovery_test.go`](../apps/server/internal/storage/recovery_test.go). These exercise failure and identity boundaries; verification commands belong to [development](development.md#verification).

## Document saves

The controller keeps current text separate from the content acknowledged by the server and the expected revision. Explicit Save captures a text-and-revision snapshot. A successful write acknowledges that snapshot; typing during the write remains a newer dirty draft. A later metadata-refresh failure does not undo a successful content write. A failed write never marks the draft saved.

Rename and move notifications carry both previous and resulting revisions. An editor may acknowledge a metadata-only revision change only when its expected revision exactly matches that previous revision. Otherwise it preserves the draft and resolves any conflict explicitly, without substituting a newer revision automatically.

Revision conflicts, missing files and trashed files are distinct recovery states. Refreshes cannot replace dirty text, and deletion cannot silently destroy an open draft. Reloading saved content or discarding a draft requires an explicit user choice.

Content and metadata PATCH operations both check the expected revision. Imports commit as one transactional batch within the file limits. Deletion groups and protected folders remain storage rules shared by every file UI; their detailed behavior belongs to the [Files API](api/files.md).

## Browser recovery

IndexedDB records are scoped by the persistent server `databaseId`, browser client ID and unique draft ID. Each record retains its file ID, draft text and original `baseRevision`, separately from desktop layout. [`session.ts`](../apps/dashboard/src/shared/drafts/session.ts) uses browser locks to prevent duplicated tabs from sharing a live draft identity. Without locks it creates an independent identity and offers the older record as a recovery copy.

Returning to AXV offers recovery. Restoring a draft does not advance its base revision to the current server revision or automatically overwrite the server file. The next save still follows the normal conflict checks.

Local writes are serialized and coalesce pending changes. Updates and cleanup preserve typing newer than an acknowledged save: saving an older snapshot cannot remove a newer recovery draft. Recovery-storage failures remain visible while the in-memory draft stays available. The [desktop-state API](api/desktop.md#database-identity-and-local-recovery) defines the identity contract; [desktop layout persistence](desktop.md#persistent-layout-and-recovery) uses its own serialized queue.

## References, snapshots and project exchange

### Project root access

Registered host projects persist a canonical root and filesystem identity independently of Codex's recent-directory list. Host-file access opens the root and checks its device/inode identity before accessing files. Explicit registration of a replacement directory creates another project ID for the new identity; existing references retain their original root identity.

File operations stay constrained to the opened root. Ancestor symlinks may resolve only within it; final symlinks and special files are rejected. Unix reads use nonblocking and no-follow flags so a racing FIFO or final symlink cannot block a request or escape the root. Unsupported platforms fail explicitly instead of falling back to path-only identity. The [Project Files API](api/project-files.md#browse-read-and-download) owns path syntax, supported operations and limits.

### Saved references and copies

Document links associate a live document with a registered project, a host-scoped machine or an AXV conversation record; a link neither relocates the document nor selects an execution target. Permanent source deletion removes its live links.

An explicit document snapshot captures the stored database ID, entry ID, exact revision, name, text and SHA-256 in one transaction. The expected-revision check and content read share that transaction. Snapshots never incorporate unacknowledged editor typing and are not updated by later saves. They deliberately have no cascading source foreign key, preserving the content already handed to an agent or exported even after permanent source deletion. The [snapshot API](api/project-files.md#immutable-snapshots-and-provenance) owns response shapes and retention guarantees.

Importing a project file creates an independent virtual document. Its entry, immutable import origin, project link and idempotent receipt commit together; the origin retains the reviewed project-relative path and source hash after later document edits. Exporting uses an already-created document snapshot and creates a new project file without replacement. These are explicit copies rather than synchronization.

### Export publication and recovery

Filesystem publication and SQLite cannot share one transaction. Export persists a random owned staging path and target-parent identity before a no-replace hard-link publication, then commits the receipt before removing the staging link. Publication and directory sync use verified open directory handles; logical parent identity is checked again before confirmation, preventing a retry from silently targeting a replacement directory.

A private lock beside the canonical database serializes publication across service instances without adding a permanent coordination file to the host project. Receipt completion permits only an immutable pending-to-terminal transition. Replay recognizes an interrupted publication by inode identity instead of adopting a user's pre-existing same-content file. A definitive name conflict records a terminal failure and releases its staging link.

After receipt commitment, replay can finish staging cleanup when the original project, parent and verified staging content remain available; the committed result remains authoritative if cleanup cannot finish. Incomplete or unverifiable publication preserves its reservation and any existing staging state for recovery. A publication-lock timeout reports an unresolved transfer even when no receipt or staging anchor has yet been created. AXV verifies published bytes before acknowledging export but cannot lock unrelated host tools. The [exchange API](api/project-files.md#explicit-snapshot-transfers) owns caller retry, identity and size contracts.

## Desktop database baseline

`desktop.sqlite` starts from one consolidated [`0001_initial.sql`](../apps/server/internal/storage/migrations/0001_initial.sql), organized by feature. Its current format marker is **AXV4** (`0x41585634`). It defines virtual documents, desktop preferences and layouts, the stable database identity, Codex conversations and runs with their actual tmux routing and optional immutable VM project/connection context, explicit project references, document links, snapshots, import origins and transfer receipts. These features do not have independent migration sequences.

SQLite's `application_id` is a file-format marker, separate from the migration version in `user_version`. Its value must agree between `databaseApplicationID` in [`migrations.go`](../apps/server/internal/storage/migrations.go) and the initial SQL migration. It distinguishes incompatible development baselines even when their migration versions match. Startup rejects incompatible or invalid databases without deleting, resetting or destructively repairing them.

AXV is in unreleased development. Breaking schema changes and refactors are allowed; keep one clean current baseline and remove obsolete compatibility code. Development data is disposable and does not require migration, old-format adapters or a parallel compatible executable. Change the format marker for an incompatible baseline, stop the affected writers and Codex runs, then delete and recreate the affected AXV-owned development store. Host projects and provider-owned history/configuration are separate and are not part of this reset. Normal startup still rejects an incompatible or invalid database rather than silently deleting an arbitrary file. Define a released-data compatibility policy when preparing the first release.

## Installed application storage

[`extensions`](../apps/server/internal/extensions) owns `extensions.sqlite`, opened separately by the desktop service in its resolved data directory. Its embedded [`0001_initial.sql`](../apps/server/internal/extensions/migrations/0001_initial.sql) uses the **AXE2** format marker (`0x41584532`). A persistent UUID identifies this store independently of `desktop.sqlite` and scopes browser recovery, management actions, data reads/writes, content navigation and runtime sessions. The backend requires the original captured identity, so another store with matching application IDs and revision counters cannot receive an old operation or draft. The [application API](api/extensions.md#access) owns the header and content-path contract. Startup initializes a newly created file only; existing uninitialized, incompatible, unknown-layout or damaged databases are rejected without resetting them. Format and schema changes follow the same [unreleased baseline policy](#desktop-database-baseline), with this store's own marker and affected writers.

Startup compares a fingerprint of SQLite's complete schema catalog with a pristine in-memory database built through the same embedded migrations and shared runner. Column definitions, types, keys, defaults, checks, references, indexes and triggers must match the owned baseline. This uses SQLite's recorded DDL directly, without permissive SQL normalization or a second manually maintained schema. Text primary keys explicitly reject NULL; generations and revisions require SQLite integer values as well as their positive/nonnegative ranges. Startup also checks data types and orphan data rather than relying on column affinity alone. The service verifies every retained package's complete canonical bytes and digest before serving requests. Catalog and runtime grant queries project only the manifest to Go; SQLite still reads the stored package JSON, but the Go metadata path does not load or repeatedly decode its HTML.

Immutable canonical packages are stored by SHA-256 in a code table. Installation metadata holds the active digest, enabled/installed state and monotonically increasing generation. A different table holds one JSON document and its revision per application. Lifecycle changes preserve this document; uninstall retains metadata and data, while a separate revision-checked action explicitly clears data only after uninstall. The [application API](api/extensions.md) owns limits, generation checks and session invalidation.

Previous package digests are retained as well. There is currently no package-history rollback or automatic code cleanup; repeated upgrades increase database size and the work needed for startup integrity checks.

Extension data has no virtual entry ID and does not participate in virtual-document resets, snapshots, exports or project transfers. Its saved-data read endpoint and browser management export are separate operations. Resetting only the desktop database does not reset extension state; an explicit extension-store reset creates a different store UUID on the next startup. Stop its desktop writer and preserve wanted application data before explicitly resetting this AXV-owned development store. Neither reset includes host projects or provider history.

## Migration runner

Each SQLite owner embeds its own migration directory. Desktop SQL lives in [`storage/migrations`](../apps/server/internal/storage/migrations); installed applications use [`extensions/migrations`](../apps/server/internal/extensions/migrations), and compute and preparation have separate [VM database ownership](vm-platform.md#ownership). They share the [`sqlitemigrate`](../apps/server/internal/sqlitemigrate) executor, never tables, identities or initialization data.

SQL files use `NNNN_description.sql` names and are embedded by Go at build time. Unique, consecutive numeric prefixes starting at `0001` define both execution order and the latest supported schema. There is no separate Go version constant or registration list to update, and the executable needs no SQL files beside it.

The [shared runner](../apps/server/internal/sqlitemigrate/migrations.go) owns the transaction and `PRAGMA user_version`; the [desktop adapter](../apps/server/internal/storage/migrations.go) supplies its format marker and document initializer. Migration scripts must not manage either. All pending files, their recorded versions and first-run seeding commit together; any failure rolls back the entire upgrade, including earlier files and version changes.

The executor also verifies that the SQL format marker matches its owning adapter before committing. Initial schema creation establishes the database identity and default preferences in that transaction. [`seed.go`](../apps/server/internal/storage/seed.go) adds sample documents only for an empty, unversioned database. Samples are enduring user content, without release-status or roadmap prose; later launches and upgrades neither reseed nor rewrite them. Unknown newer schema versions and nonempty unversioned databases are rejected without resetting data.
