# Host project files

[API index](README.md)

These routes register host project directories and provide read-only browsing and downloads. [Compute project routes](compute.md#vm-projects) address VM project volumes. [Project root access](../storage.md#project-root-access) owns persistence and filesystem enforcement.

All routes follow the [access policy](README.md#access-and-identity-boundaries) and require the [captured desktop database identity](desktop.md#database-identity-and-local-recovery) header. The service verifies the database before resolving a registered root. JSON requests contain one object with no unknown fields, use `application/json`, and are limited to **64 KiB**. Responses disable caching and MIME sniffing.

## Registered projects

| Method and route     | Request               | Response          |
| -------------------- | --------------------- | ----------------- |
| `GET /api/projects`  | —                     | `{ projects }`    |
| `POST /api/projects` | `{ rootPath, name? }` | 201 `{ project }` |

A project is `{ id, kind: "host", name, rootPath, createdAt }`. Registration resolves an existing readable absolute directory and persists its canonical path and filesystem identity. Omitted `name` uses the directory's basename; names must be valid UTF-8 and 1–255 bytes, without slashes, backslashes or control characters, and cannot be blank, `.` or `..`. Registering the same path and identity returns its existing record. Explicitly registering a replacement directory at a previously registered path creates a new opaque project ID and preserves the old reference. The registration list is independent of Codex recent directories.

Project-file access verifies the registered root identity. A different directory occupying the same path returns `409 project_changed`, rather than retargeting existing windows. Stable root identity is implemented for Unix hosts; unsupported platforms return `501 unsupported`. Projects cannot currently be renamed, rebound or removed through this API. [Project root access](../storage.md#project-root-access) owns identity and filesystem enforcement mechanisms.

## Browse, read and download

| Method and route | Response |
| --- | --- |
| `GET /api/projects/:id/files?path=&cursor=&limit=` | `{ project, entries, nextCursor, truncated }` |
| `GET /api/projects/:id/file?path=<relative-file>` | `{ projectId, path, name, content, size }` |
| `GET /api/projects/:id/download?path=<relative-file>` | Original file bytes as an attachment |

Paths are relative to the registered root. An omitted or empty listing path means the root. Absolute paths, `..` components, backslashes, invalid UTF-8, NUL/CR/LF and paths over **4096 bytes** are rejected. Components beginning `.axv-export-` are reserved and hidden from listings. Queries reject unknown or repeated parameters.

An entry is `{ name, path, kind, size, modifiedAt }`, with kind `file`, `folder` or `symlink`. Special files are omitted. Listing defaults to **100** results; `limit` is **1–200**. Use the returned cursor for the next page. A listing scans at most **10000** entries and sets `truncated` when more exist; pages describe the current directory rather than a frozen view, so refresh after external changes.

Reads and downloads stay inside the registered root. Final symlinks and special files are rejected; ancestors may resolve links only within that root. A listing's symlink entry is informational, not a grant to follow it.

Text reads accept valid UTF-8 without binary control characters, up to **1 MiB**. Downloads accept regular files up to **10 MiB**, use `application/octet-stream`, and force an attachment with a restrictive content-security policy. Neither operation renders active HTML or writes project contents into SQLite.

## Errors

| Status | Codes |
| --- | --- |
| 400 | `invalid_request`, `invalid_project`, `invalid_path`, `invalid_cursor`, `invalid_directory`, `invalid_name`, `not_file`, `not_text` |
| 403 | `host_forbidden`, `file_forbidden` |
| 404 | `not_found` |
| 409 | `project_changed` |
| 413 | `file_too_large` |
| 415 | `invalid_content_type` |
| 501 | `unsupported` |
| 503 | `project_unavailable`, `file_unavailable` |

Unknown failures return a generic `500 internal_error` without exposing internal filesystem paths.
