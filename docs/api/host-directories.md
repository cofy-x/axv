# Host directory API

[API index](README.md)

This shared endpoint supplies local host-directory selection for Codex, Terminal and Project Files. It lists directory metadata without registering a project, starting a session or reading file contents. Host paths remain separate from virtual-document IDs and registered project IDs.

The [local access rules](README.md#access-and-identity-boundaries) apply before filesystem access. Responses use `Cache-Control: no-store`.

## Directory listing

`GET /api/host/directories?path=...` returns:

```text
{ path, parentPath, homePath, directories: [{ name, path }], truncated }
```

The request accepts at most one `path` query parameter; omitted or empty means the service user's home. Unknown or repeated parameters are rejected. Paths must be absolute, valid UTF-8, at most **4096 bytes**, and contain no NUL. They must resolve to readable directories.

The response resolves symlinks for `path`, `parentPath` and `homePath`; `parentPath` is null at the filesystem root. Child candidates may retain symlink names until navigation or a consuming operation resolves them.

Listings contain directories only, bounded to **1000 results** and a finite scan. `truncated` signals an incomplete listing; callers can still navigate by an exact path. Selection does not create, import or modify files. Each consuming API independently validates its selected directory; see [Codex creation](host-codex.md#creation-and-explicit-continuation), [project terminals](host-terminal.md#connection-and-project-directory) and [project registration](project-files.md#registered-projects).

## Errors

| Status and code             | Meaning                                        |
| --------------------------- | ---------------------------------------------- |
| `400 invalid_directory`     | Invalid query, path syntax or directory type   |
| `400 directory_unavailable` | The path cannot be resolved or accessed        |
| `403 host_forbidden`        | Local-access validation failed                 |
| `403 directory_forbidden`   | The service user cannot read the directory     |
| `503 home_unavailable`      | The service user's home cannot be determined   |
| `500 internal_error`        | Unexpected failure; details remain server-side |

## Implementation

[`hostdir/httpapi`](../../apps/server/internal/hostdir/httpapi) owns transport, local-access checks and error mapping. [`hostdir`](../../apps/server/internal/hostdir/directories.go) owns canonical directory validation and bounded listings. The desktop server registers the endpoint independently of coding-agent availability.
