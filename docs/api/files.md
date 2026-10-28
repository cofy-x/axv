# Virtual-file API

[API index](README.md)

Entries, content and metadata changes, imports and deletion groups share one transactional virtual-document model. JSON writes follow the [desktop decoder contract](desktop.md#preferences-and-browser-layout). Every request also requires the original [desktop database identity](desktop.md#database-identity-and-local-recovery), including reads and multipart imports; database mismatch is distinct from a file revision conflict.

## Entries

An entry contains `id`, `parentId`, `name`, `kind` (`folder` or `file`), `mime`, `size`, `revision`, `createdAt`, `updatedAt`, `editable`, `previewable`, and `system`. Trashed entries also contain `trashedAt`. IDs are opaque strings; the protected built-in folders have the stable IDs `root`, `desktop`, and `documents`.

- `GET /api/entries`
  - Response: `{ entries }`.

- `GET /api/entries/:id`
  - Response: `{ entry, content? }`; text content when editable.

- `POST /api/entries`
  - Request: `{ parentId, name, kind, content? }`.
  - Response: `201 { entry }`.

- `PATCH /api/entries/:id`
  - Request: `{ name?, parentId?, content?, revision }`.
  - Response: `{ entry }`.

- `POST /api/entries/:id/duplicate`
  - Request: `{ parentId? }`.
  - Response: `{ entry }` for the copied root.

- `POST /api/entries/:id/trash`
  - Response: `{ entry }`.

- `POST /api/entries/:id/restore`
  - Response: `{ entry }`.

- `DELETE /api/entries/:id`
  - Response: `{ ok: true }`.

- `DELETE /api/trash`
  - Response: `{ ok: true }`.

Names must be valid UTF-8, 1–255 bytes, and contain no slashes, backslashes or control characters; blank names, `.` and `..` are rejected. Folder moves cannot create cycles. Create, rename, and move reject a conflicting sibling name with `409 name_conflict`; import, duplicate, and restore automatically number conflicting names. Name comparison is case-insensitive for ASCII through SQLite's `NOCASE` collation.

Trash operations affect a folder and its currently live descendants in a transaction. Restore recovers that deletion group; previously trashed descendants remain trashed. An unavailable original parent sends a restored item to Documents. Permanent deletion only accepts trashed items. Protected built-in folders cannot be renamed, moved, or deleted.

## Text saves and conflict handling

Include the last observed `revision` for every PATCH, including rename and move operations:

```json
{ "content": "Updated note", "revision": 3 }
```

The server atomically checks the revision before changing content or metadata. An outdated or missing revision returns `409` with code `revision_conflict`. Keep the unsaved editor text and offer loading the current server version or saving a new copy. Do not retry by silently substituting the latest revision: doing so would overwrite another editor's changes.

Editable files must be valid UTF-8, contain no binary control characters, and remain at most **1 MiB**. Successful mutations increment the revision; refresh affected metadata after file actions. Network, permission, database, and validation failures leave the editor draft unsaved.

## Import, download, and preview

`POST /api/import` accepts multipart form data with a `parentId` and one to ten file parts, returning `201 { entries }`. Each file may be at most **10 MiB**. The batch is imported transactionally, preserving file bytes and assigning a numbered name when needed. Omitting `parentId` imports into Documents.

`GET /api/entries/:id/content?download=1&databaseId=<captured-id>` downloads the original bytes when used as browser navigation. Image previews also include the captured `databaseId`; fetch clients may use the identity header instead, but must not send both. `HEAD` follows the same binding. Safe PNG, JPEG, and WebP images can be served inline by omitting `download`; unsupported formats are retained for download. MIME classification is based on stored content rather than trusting a filename or upload MIME declaration.

## Errors and metadata writes

| Status | Code | Meaning |
| --- | --- | --- |
| 409 | `revision_conflict` | PATCH revision is missing or stale |
| 409 | `name_conflict` | A live sibling already has that name |
| 409 | `unavailable` | A protected or trashed item cannot be changed or duplicated |
| 409 | `system_entry` | A protected folder cannot be trashed |
| 409 | `not_trashed` | Permanent deletion requires a trashed, unprotected item |
| 404 | `not_found` | The addressed entry does not exist |
| 413 | `text_too_large`, `file_too_large`, `too_many_files` | A content or import limit was exceeded |

Renaming or moving a file increments its revision even when its contents are unchanged.

A client that performed a metadata-only change may advance an editor's expected revision only if that editor was based on the mutation's exact previous revision. Otherwise keep its draft and handle any subsequent conflict explicitly. Unknown database errors are logged server-side and return a generic `internal_error` response without exposing internal paths.

These APIs address SQLite virtual documents by opaque entry ID. Local recovery uses the [database and browser identity contract](desktop.md#database-identity-and-local-recovery); host-project access, document links, retained snapshots and explicit copies use the [document-exchange API](project-files.md).
