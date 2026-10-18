# Host Terminal API

[API index](README.md)

Each connection starts and owns one ephemeral shell on the computer running the desktop service.

## Access and availability

Terminal runs an interactive shell as the user who started the Go service, beginning in that user's home directory unless an explicit project directory is supplied. Host files and shell processes are independent of the virtual entry APIs and SQLite. The [local access rules](README.md#access-and-identity-boundaries) apply; WebSocket requests require Origin.

Native adapters support macOS, Linux and Windows 10 version 1809 or newer with ConPTY. Unsupported platforms or unavailable shells report a reason.

`GET /api/terminal/info` returns `{ supported, shell?, reason? }` without starting a shell. `shell` identifies the executable; `reason` explains an unsupported platform or unavailable shell.

## Connection and project directory

Connect a WebSocket to `/api/terminal?cols=80&rows=24`, optionally adding one URL-encoded `projectPath` parameter. Omitted dimensions default to 80 columns and 24 rows. Columns must be **2–500**, rows **1–200**; unknown or repeated parameters are rejected. Each connection owns one shell, with at most **four active sessions per server**. Before upgrade, invalid dimensions or query shape return `400`, disallowed access returns `403`, and unavailable session capacity returns `503`.

When supplied, `projectPath` must be a nonempty absolute host directory of at most **4096 UTF-8 bytes**, without NUL bytes. After upgrade and before process creation, the server canonicalizes symlinks and checks directory access. Invalid or unavailable directories send `{ error, code: "terminal_project_unavailable" }` and close with code `1008`; they never start a home-directory fallback shell. The canonical path is passed through the process working-directory API, without constructing a shell command. HOME and the shell user's permissions are unchanged. Restored project Terminal windows and their New Session action start fresh shells in the selected directory.

## Terminal frames and lifetime

| Direction | Message | Payload |
| --- | --- | --- |
| Client → server | Binary | Raw terminal input, at most **32 KiB** per message |
| Server → client | Binary | Raw terminal output |
| Client → server | Text | `{ "resize": [cols, rows] }` |
| Server → client | Text | `{ "ready": true }` |
| Server → client | Text | `{ "error": "Readable message", "code": "stable_code" }` |

Resize messages use the same dimension limits. The server sends ready after shell startup or an error on failure; the error's `code` may be omitted.

Send text input as UTF-8 bytes and decode output through a terminal emulator. A normal shell exit closes the connection with code `1000`. Closing the socket or stopping the server ends the shell and foreground job. Sessions are ephemeral: New Session and restored windows after a page reload start new shells; they never reattach to previous processes.

The [Host directory API](host-directories.md) can supply a project selection. [Codex attachments](host-codex.md) and [guest terminals](guest.md#terminals) have separate identities and lifecycles.
