# Host Codex API

[API index](README.md)

Manages host Codex conversations, exact tmux-backed process runs, provider activity and terminal attachments on macOS/Linux. Implementation lives in [`internal/codex`](../../apps/server/internal/codex) and [`codex/httpapi`](../../apps/server/internal/codex/httpapi); architecture belongs to [Host Codex](../codex.md).

All routes use the [shared access boundary](README.md#access-and-identity-boundaries) and `Cache-Control: no-store`. JSON writes require `application/json`, one object without unknown fields, max **16 KiB**. Every route except `/info` requires the [captured desktop database identity](desktop.md#database-identity-and-local-recovery) (`X-AXV-Database-ID` header, or `databaseId` query on WS) checked before replay lookup, filesystem access or process operations.

## Routes

| Method | Route | Request / Response |
| --- | --- | --- |
| GET | `/api/host/codex/info` | Unbound capability probe (`supported`, `installed`, `ready`, CLI/tmux versions, `guidance`, `defaultProjectPath`, `network`) |
| GET | `/api/host/codex/sessions` | `{ sessions }` (each conversation with its latest run) |
| GET | `/api/host/codex/projects` | `{ projects: [{ path, lastUsedAt }] }` (up to 10 recent host dirs) |
| POST | `/api/host/codex/sessions` | `{ requestId, projectPath?, cols?, rows? }` → `{ session }` |
| GET | `/api/host/codex/sessions/:id` | `{ session }` (latest run) |
| PATCH | `/api/host/codex/sessions/:id` | `{ revision, title?, archived? }` → `{ session }` |
| POST | `/api/host/codex/sessions/:id/resume` | `{ requestId, expectedRunId, cols?, rows? }` → `{ session }` |
| GET | `/api/host/codex/sessions/:id/runs/:runId` | `{ session }` for that exact historical or active run |
| POST | `/api/host/codex/sessions/:id/runs/:runId/activity/review` | `{ eventId }` (dismiss failed/interrupted alert for that exact run/event) |
| POST | `/api/host/codex/sessions/:id/runs/:runId/end` | Terminate exact run → `{ session }` |
| GET | `/api/host/codex/sessions/:id/runs/:runId/terminal` | WS upgrade (`?cols=80&rows=24&databaseId=...`) |

## Availability and environment

- `ready` requires Codex CLI **0.161.0+** (compatible remote/app-server/inline flags; prereleases of the floor excluded) and tmux **3.1+**. Detection never starts a session, creates workspaces or verifies model auth.
- `network` reports redacted proxy source (`environment\|configuration\|system\|direct`) without credentials ([proxy precedence](../startup.md#host-agent-defaults)). Host directory browsing uses the shared [Host directory API](host-directories.md).

## Conversation, run, and activity contracts

- **Separate identities:** Conversations hold `(id, projectPath, title, metadataRevision, archivedAt?, conversationId?, historyHome)`; runs hold `(runId, tmuxSession, tmuxSocket, attachCommand, status, connections)` where `status` is `starting|running|stopping|ended|lost|failed`. Session/run IDs are opaque; callers never construct tmux targets.
- **Metadata & recent projects:** `PATCH` requires matching `metadataRevision` (`409 metadata_conflict` on stale edit; `title` max 120 Unicode chars; does not probe tmux). Recent projects (max 10) update transactionally only on a host run's first confirmed `starting → running` transition.

### Activity and review

- `activity` (`{ state, needsAttention, observation: { instanceId, revision }, eventId?, sequence?, turnId?, updatedAt?, reason? }`) tracks `unknown|idle|working|waiting_approval|waiting_input|completed|failed|interrupted` separately from process state. Never infers activity from liveness or strips completed results on idle.
- `needsAttention` is true only for pending approvals/input or undismissed `failed`/`interrupted` events. Normal `completed` turns never require acknowledgment (`409 activity_not_actionable` if reviewed).
- `POST .../activity/review` requires the exact 32-hex `eventId` (`409 activity_changed` / `activity_pending`; `503 activity_unavailable` / `activity_review_unavailable`) and persists per `(runId, eventId)`. Merge client observations by exact `(sessionId, runId)`, provider `sequence` and `(instanceId, revision)` so late list responses never revive dismissed alerts or mix runs.

## Creation and explicit continuation

- **Host projects:** Sessions accept `projectPath` (empty selects/creates default workspace). VM coding agents run natively inside Guest Terminal rather than through a host Codex bridge.
- **Idempotency & capacity:** `requestId` (1–128 UTF-8 bytes, unique per desktop DB) binds to exact payload/dimensions (`409 request_conflict` on mismatch). Retain the original `requestId` after transport or ambiguous errors — never assume no run was reserved. Limit: **1 active run per conversation**, **4 active runs per service** (including archived/transitional).
- **Explicit continuation:** Requires `expectedRunId` (`409 conversation_active` / `run_changed`), revalidates project + exact provider UUID in the stored `historyHome` (never substituting a newer `CODEX_HOME`). Reads, reload, reconnect and restart never auto-continue.
- **Uncertainty:** Temporary tmux inspection failure preserves known state with `reason` on list/detail (`session_exit_unconfirmed` stays retryable via inspection or End — never treated as `ended`/`lost`).

## Attachment protocol and ending

Dimensions: **2–500 cols × 1–200 rows** (default 80×24); max **8 open attachments per service**.

| Direction | Frame | Payload |
| --- | --- | --- |
| Client → server | Binary | Raw terminal input (max **32 KiB**) |
| Server → client | Binary | Raw terminal output |
| Client → server | Text | `{ "resize": [cols, rows] }` or `{ "scroll": lines }` (`-200..200`, `0` exits copy mode) |
| Server → client | Text | `{ "ready": true }`, `{ "error": "...", "code"?: "..." }`, or `{ "ended": true, "message": "..." }` |

- Only `attach_failed` and transient disconnects permit bounded client reconnect to the exact `(sessionId, runId)`; stop on final state or access rejection and drop unsent input. Managed TUI rejects in-TUI thread create/fork/switch.
- `POST .../end` is idempotent, addresses only `:runId`, and waits for runner cleanup lock release (`409 run_starting`, `503 codex_cleanup_pending` / `codex_ownership_unavailable` remain retryable). Closing WS or stopping AXV only detaches.
