# Project files and document exchange

[API index](README.md)

These local routes address explicitly registered host projects and immutable document snapshots. [Compute project routes](compute.md#vm-projects) separately address VM project volumes; neither replaces the [virtual-file API](files.md). [Document storage](../storage.md) owns persistence and transaction mechanisms; [Host Codex](host-codex.md) owns attaching a snapshot to an agent conversation.

All routes follow the shared [local-access policy](README.md#access-and-identity-boundaries), including Origin on mutations. All project and document-exchange routes require the [captured desktop database identity](desktop.md#database-identity-and-local-recovery) header, including host-file reads and downloads. The database is checked before resolving a registered root or performing any copy; a query parameter is not accepted as an alternative. JSON requests contain one object with no unknown fields, use `application/json`, and are limited to **64 KiB**. Responses disable caching and MIME sniffing.

## Registered projects

| Method and route     | Request               | Response          |
| -------------------- | --------------------- | ----------------- |
| `GET /api/projects`  | —                     | `{ projects }`    |
| `POST /api/projects` | `{ rootPath, name? }` | 201 `{ project }` |

A project is `{ id, kind: "host", name, rootPath, createdAt }`. Registration resolves an existing readable absolute directory and persists its canonical path and filesystem identity. Omitted `name` uses the directory's basename; names follow [virtual-file naming rules](files.md#entries). Registering the same path and identity returns its existing record. Explicitly registering a replacement directory at a previously registered path creates a new opaque project ID, preserving the old reference and all of its document links, origins and transfer receipts. Registration neither starts an agent nor imports files, and the list is independent of Codex recent directories.

Project-file access verifies the registered root identity. A different directory occupying the same path returns `409 project_changed`, rather than retargeting existing windows. Stable root identity is implemented for Unix hosts; unsupported platforms return `501 unsupported`. Projects cannot currently be renamed, rebound or removed through this API. [Project root access](../storage.md#project-root-access) owns identity and filesystem enforcement mechanisms.

## Browse, read and download

| Method and route | Response |
| --- | --- |
| `GET /api/projects/:id/files?path=&cursor=&limit=` | `{ project, entries, nextCursor, truncated }` |
| `GET /api/projects/:id/file?path=<relative-file>` | `{ projectId, path, name, content, sha256, size }` |
| `GET /api/projects/:id/download?path=<relative-file>` | Original file bytes as an attachment |

Paths are relative to the registered root. An omitted or empty listing path means the root. Absolute paths, `..` components, backslashes, invalid UTF-8, NUL/CR/LF and paths over **4096 bytes** are rejected. Components beginning `.axv-export-` are reserved for transfer recovery. Queries reject unknown or repeated parameters.

An entry is `{ name, path, kind, size, modifiedAt }`, with kind `file`, `folder` or `symlink`. Special files are omitted. Listing defaults to **100** results; `limit` is **1–200**. Use the returned cursor for the next page. A listing scans at most **10000** entries and sets `truncated` when more exist; pages describe the current directory rather than a frozen view, so refresh after external changes.

Reads and downloads stay inside the registered root. Final symlinks and special files are rejected; ancestors may resolve links only within that root. A listing's symlink entry is informational, not a grant to follow it.

Text reads accept valid UTF-8 without binary control characters, up to **1 MiB**, returning a SHA-256 of the reviewed bytes. Downloads accept regular files up to **10 MiB**, use `application/octet-stream`, and force an attachment with a restrictive content-security policy. Neither operation renders active HTML or writes project contents into SQLite.

## Document links

| Method and route                 | Request or query       | Response       |
| -------------------------------- | ---------------------- | -------------- |
| `GET /api/documents/:id/links`   | —                      | `{ links }`    |
| `POST /api/documents/:id/links`  | One target below       | 201 `{ link }` |
| `GET /api/document-links`        | One target query below | `{ links }`    |
| `DELETE /api/document-links/:id` | —                      | `{ ok: true }` |

Link requests select exactly one target:

- `{ kind: "project", projectId }`: an existing registered project.
- `{ kind: "machine", hostId, machineId }`: a host-scoped machine identity. This stores a reference; it does not verify machine availability or grant guest access.
- `{ kind: "conversation", conversationId }`: an existing AXV Codex session record ID, independent of its current process run and external provider thread ID.

A link includes `id`, `entryId`, `kind`, its target fields and `createdAt`. The source must be an available file. Repeating the same source/target pair returns the same link. Reverse lookup accepts either `projectId`, `conversationId`, or the pair `hostId` and `machineId`; it never matches a same-named replacement host. Links refer to documents and disappear on permanent source deletion; creating or deleting a link does not copy content or control the linked target.

## Immutable snapshots and provenance

| Method and route | Request | Response |
| --- | --- | --- |
| `GET /api/documents/:id/snapshots` | — | `{ snapshots }`, latest 100 metadata records |
| `POST /api/documents/:id/snapshots` | `{ revision }` | 201 `{ snapshot }` |
| `GET /api/document-snapshots/:id` | — | `{ snapshot }` including content |
| `GET /api/documents/:id/origins` | — | `{ origins }` |

A snapshot contains `{ id, databaseId, entryId, revision, name, content, sha256, createdAt }`. List responses omit `content`. Snapshot creation requires an available editable document and atomically checks its persisted revision before capturing content and provenance. Unsaved editor text is not included. A stale revision returns `409 revision_conflict`; do not retry with a substituted revision. Repeating creation for the same database, entry and revision returns the same snapshot.

Snapshots are immutable and remain readable after renaming, editing, trashing or permanently deleting the source. This retention preserves content already exported or handed to an agent. Snapshot deletion is not currently exposed.

Imported documents have origins `{ projectId, path, sha256, createdAt }`, recorded with their initial bytes. An origin describes the imported snapshot, not the current contents of the project file; later document edits do not rewrite it.

## Explicit snapshot transfers

| Method and route | Request | Response |
| --- | --- | --- |
| `POST /api/projects/:id/import` | `{ requestId, path, sha256, parentId? }` | `{ entry }` |
| `POST /api/projects/:id/export` | `{ requestId, snapshotId, path }` | `{ projectId, path, snapshotId, sha256 }` |

Import reads one regular project file up to **10 MiB**, requires its reviewed SHA-256 to match, and copies its bytes into an independent virtual document. `parentId` defaults to Documents. Name collisions are numbered under the existing virtual-file import rules. The entry, origin, project link and request receipt commit in one transaction. A changed source returns `409 source_changed` without creating a document.

Export copies an existing immutable text snapshot into a new regular project file. Its parent directory must already exist, and the request records that directory's filesystem identity. Publication never overwrites an existing destination, including one with identical bytes; choose a new path after `409 name_conflict`. A retry does not retarget a replacement parent directory: `409 transfer_unresolved` preserves its recovery anchor until the original parent is restored. Export does not save a dirty editor or synchronize future document changes.

`requestId` is **8–128 UTF-8 bytes**, without NUL/CR/LF or leading/trailing whitespace, and is globally unique across imports and exports in this desktop database. It binds the normalized original payload and target. Retry an uncertain response with the same ID and unchanged payload. Changing that payload returns `409 idempotency_conflict`; a replay returns the original receipt rather than rereading a mutable source or making another copy.

A completed receipt remains authoritative on replay. A definitive name conflict is also recorded, so replay remains a conflict even if the destination is later removed. An unresolved publication or publication-lock timeout returns `409 transfer_unresolved`; review that result instead of treating it as saved or blindly creating a second request. External host tools can modify project files independently; acknowledged export confirms the published bytes at that time, without locking those tools. [Export publication and recovery](../storage.md#export-publication-and-recovery) owns staging, locking and cleanup mechanisms.

## Errors

| Status | Codes |
| --- | --- |
| 400 | `invalid_request`, `invalid_project`, `invalid_path`, `invalid_cursor`, `invalid_directory`, `invalid_name`, `invalid_parent`, `invalid_link`, `not_file`, `not_text` |
| 403 | `host_forbidden`, `file_forbidden` |
| 404 | `not_found` |
| 409 | `project_changed`, `revision_conflict`, `unavailable`, `idempotency_conflict`, `name_conflict`, `source_changed`, `transfer_unresolved` |
| 413 | `file_too_large`, `text_too_large` |
| 415 | `invalid_content_type` |
| 501 | `unsupported` |
| 503 | `project_unavailable`, `file_unavailable` |

Unknown failures return a generic `500 internal_error` without exposing internal filesystem paths. A failed or unknown transfer response must not be shown as a completed save.
