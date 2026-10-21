# Host Codex API

[API index](README.md)

Stable conversations and explicit process runs are independent of browser windows, desktop workspaces and virtual documents.

Codex runs the official CLI as the desktop service's host user. Its persistent PTY + tmux backend supports macOS and Linux; missing dependencies do not fall back to an ephemeral process.

The [local access rules](README.md#access-and-identity-boundaries) apply before dependency probes, filesystem access or process creation. Responses use `Cache-Control: no-store`. JSON writes require `Content-Type: application/json`, one object with no unknown fields, and at most **16 KiB**.

Create and resume optionally accept `X-AXV-Database-ID`. The VM-project interface supplies the desktop identity captured with its pending request. A mismatch returns `409 database_changed` before replay lookup, environment verification, directory access or run reservation. The original pending request must be retained: it may already have committed in the previous store. Changing a browser namespace does not authorize replay into another desktop database.

## Routes

- `GET /api/host/codex/info`
  - Response fields:

    ```text
    {
      supported, installed, ready,
      codexPath?, codexVersion?, tmuxPath?, tmuxVersion?,
      reason?, guidance, defaultProjectPath, configPath, network
    }
    ```

- `GET /api/host/codex/sessions`
  - Response: `{ sessions }`.

- `GET /api/host/codex/projects`
  - Response: `{ projects: [{ path, lastUsedAt }] }`.

- `POST /api/host/codex/sessions`
  - Request: `{ requestId, projectPath?, vmProject?: { binding }, cols?, rows? }`.
  - Response: `{ session }`.

- `GET /api/host/codex/sessions/:id`
  - Response: `{ session }`.

- `PATCH /api/host/codex/sessions/:id`
  - Request: `{ revision, title?, archived? }`.
  - Response: `{ session }`.

- `POST /api/host/codex/sessions/:id/resume`
  - Request: `{ requestId, expectedRunId, vmProject?: { binding }, cols?, rows? }`.
  - Response: `{ session }`.

- `GET /api/host/codex/sessions/:id/runs/:runId`
  - Response: `{ session }`.

- `POST /api/host/codex/sessions/:id/runs/:runId/end`
  - Response: `{ session }`.

- `GET /api/host/codex/sessions/:id/runs/:runId/terminal?cols=80&rows=24`
  - Request: WebSocket upgrade.
  - Response: Terminal byte and control frames.

- `GET /api/host/codex/sessions/:id/runs/:runId/context`
  - Response: `{ batches }` for that exact run.

- `POST /api/host/codex/sessions/:id/runs/:runId/context`
  - Request: `{ requestId, snapshotIds }`.
  - Response: `{ batch }`.

- `DELETE /api/host/codex/sessions/:id/runs/:runId/context/:batchId`
  - Response: `{ batch }`; only a pending batch can be cancelled.

## Availability and environment

- `supported` reports platform support; `installed` reports a discovered Codex executable. `ready` additionally requires Codex **0.161.0+** with compatible remote/app-server/inline options and tmux **3.1+**. Prereleases of the minimum release do not satisfy the version floor.
- `guidance` contains repair instructions; `reason` explains an unavailable state. Detection does not start a session, create its workspace or read credentials. Readiness does not verify authentication or model connectivity.
- `defaultProjectPath` and `configPath` report resolved defaults. `network` contains `proxySource` (`environment`, `configuration`, `system`, or `direct`) and optional redacted `proxy`, without credentials or URL parameters. [Startup](../startup.md#host-agent-defaults) owns configuration, proxy precedence and the service-user environment.

## Host directories

The shared [Host directory API](host-directories.md) owns directory browsing and validation. Choosing a directory does not start a conversation; [creation](#creation-and-explicit-continuation) validates the selected project separately.

## Conversation and run records

A session response combines stable conversation metadata with one exact process run. List and session-detail routes select the latest run; run detail selects the requested run.

- **Conversation fields:** `id`, `target: "host"`, `projectPath`, `title`, `metadataRevision`, optional `archivedAt`, `conversationId` and `historyHome`. `conversationId` is the exact provider UUID and is absent until initialized. `historyHome` is the recorded provider state directory; its contents are not returned.
- **Run fields:** `runId`, `tmuxSession`, `tmuxSocket`, `attachCommand`, `status`, `createdAt`, optional `endedAt`, `reason` and `vmProject`, and live `connections`. `vmProject` is `{ project, binding }`, using the [VM project contract](compute.md#vm-projects). Run detail returns that run's original project/environment context, even after continuation. Private compute socket paths and configuration fingerprints are never returned.
- **Continuation:** `{ state: "available" | "active" | "unavailable", reason?, activeRunId? }`. An active run is identified by `activeRunId`. Available means identity is known and no run is active; the explicit continuation request still verifies history and dependencies.

`tmuxSession` is the actual target name; `tmuxSocket` records its socket path or explicitly configured socket name (empty before the default server confirms its path). `attachCommand` is shell-quoted for that exact target. Status is one of `starting`, `running`, `stopping`, `ended`, `lost`, or `failed`. A starting run accepts attachments while the official CLI handles project trust, sign-in and conversation initialization. Running confirms initialization and runtime liveness, not a successful model request. Connection state belongs to each attachment and is separate from process lifecycle. Session and run IDs are opaque; clients must not construct tmux targets.

## Names, archives and recent projects

Metadata PATCH requires a positive `revision` matching the session's `metadataRevision` and at least one supplied field. `title` must be a string; after trimming surrounding whitespace it may contain at most **120 Unicode characters**, with no control characters or invalid UTF-8. An empty title restores the project-name display. `archived` must be a boolean; true records `archivedAt`, false clears it. Nulls, wrong types and unknown fields are rejected. A stale revision returns `409` with code `metadata_conflict` and changes nothing. A real metadata change increments `metadataRevision`; an unchanged patch does not. Lifecycle changes do not advance that revision. Clients must preserve a rename draft and obtain an explicit retry after a conflict. PATCH does not probe, reconcile, detach or terminate tmux. Archived running sessions retain their connections and count toward the active-session limit. Listing returns both current and archived records.

Recent projects are the **ten** canonical paths most recently confirmed to reach running state, sorted by `lastUsedAt` descending with path as a tie breaker. Recency updates only with a run's first confirmed starting-to-running transition. Session reads, attachments and idempotent retries can discover that transition, but do not reorder an already-confirmed run. Metadata edits and starts that never reach confirmed running state do not update recency. Selecting a recent project uses the normal create endpoint and revalidates the path; reading projects creates nothing.

## Creation and explicit continuation

For a host-project session, omitted or empty `projectPath` selects the configured default workspace and creates it if needed. Explicit paths must already exist. Creation revalidates directory access and resolves the canonical path.

For a VM-project session, supply `vmProject: { binding }` with the complete environment explicitly reviewed through the [VM project API](compute.md#vm-projects); a nonempty `projectPath` cannot accompany it. Null, incomplete and unknown context fields are rejected. The server verifies the supplied binding and captures that compute connection before reserving a run. Codex still runs on the host, using a private AXV-owned control workspace rather than a host copy of the VM project. VM control workspaces never enter the recent host-project list.

VM continuation requires another explicitly reviewed `vmProject.binding`, for the same persistent host/project/volume/root identity. It may select the current environment after VM replacement without changing any older run. Omitting it cannot convert a VM conversation into a host conversation; supplying it cannot convert a host conversation. A changed environment returns `409 vm_project_changed`; an unavailable verifier returns `503 vm_project_unavailable`; a different persistent project returns `409 project_changed`. None selects another target or creates replacement history. Matching create/resume retries recover the originally reserved run before live VM availability checks; their original binding is part of the request identity.

Generate one `requestId` per explicit start or continuation and reuse it with the same action, project/predecessor and dimensions after an uncertain outcome. IDs are **1–128 UTF-8 bytes**, without surrounding whitespace, NUL, CR or LF, and unique across Codex starts in the desktop database. A matching retry returns the original run; a changed payload returns `409 request_conflict`. Retain the original request after transport failures, `server_stopping`, identity/reconciliation errors or unrecognized HTTP errors: an error status alone does not establish that no run was reserved. Only a refusal known to occur after replay lookup and before reservation permits a fresh explicit attempt. A continuation also supplies the exact predecessor `expectedRunId`. After matching retries are resolved, an active run returns `409 conversation_active`: open that run instead. If the latest run is final but differs from the predecessor, `409 run_changed` requires refreshing before continuation.

Creation and continuation dimensions use the attachment limits below. The server allows one active run per conversation and **four active Codex runs** (`starting`, `running`, or `stopping`) across current and archived conversations, independently of ordinary Terminal's limit. Multiple explicit new starts in one project create independent conversations.

Continuation requires explicit confirmation. It revalidates the original canonical project and verifies the exact official thread in the recorded `historyHome` before launching a new run with the same provider UUID. Changing the service's current `CODEX_HOME` does not redirect an existing conversation: verification and the resumed runner use its recorded home. Missing or unreadable history, unknown provider identity, uncertain previous runtime or unavailable dependencies return an error without a replacement conversation. Reads, page reload, reconnect and service restart never invoke continuation. Recovery needs Codex's original history and restores saved conversation context, not PTYs, scrollback or interrupted commands.

When runtime inspection is temporarily unavailable, list and detail preserve known identities and statuses with an actionable `reason`; attachment reports the availability error. `session_exit_unconfirmed` means a closed pane's exit outcome is unconfirmed: retry inspection or explicitly End that run. Uncertainty is not an ended/lost verdict, and an interrupted end remains retryable. [Codex architecture](../codex.md) owns runner initialization, tmux routing and reconciliation.

## Attachment protocol and ending

Attachment dimensions are **2–500 columns** and **1–200 rows**, defaulting to **80×24**. Unknown or repeated query parameters are rejected. At most **eight attachments** can be open per service. Multiple clients share input to the same run; sizing follows the most recently used client. Reattachment and keyboard input leave history copy mode before delivering fresh input.

| Direction | Message | Payload |
| --- | --- | --- |
| Client → server | Binary | Raw terminal input, at most **32 KiB** per message |
| Server → client | Binary | Raw terminal output |
| Client → server | Text | `{ "resize": [cols, rows] }` |
| Client → server | Text | `{ "scroll": lines }`; integer **−200–200**; positive earlier, negative later, zero exits copy mode |
| Server → client | Text | `{ "ready": true }` after attachment startup |
| Server → client | Text | `{ "error": "Readable message", "code": "stable_code" }` |
| Server → client | Text | `{ "ended": true, "message": "Readable message" }` when the run is final |

An error message may omit `code`. An explicit `attach_failed` control permits bounded attachment retries against the same session/run pair; other error controls remain visible without automatic retries. Closing the WebSocket or stopping AXV detaches clients. Reopening and transient reconnects use the same session/run pair. Clients discard unsent input on disconnect and stop automatic reconnection on final run states or access rejection. Creating, forking or switching conversations inside the managed TUI is rejected with guidance to use AXV's New Session.

End is idempotent and terminates only the specified run. It waits for the runner's managed processes to finish cleanup before reporting ended. An older final run cannot affect a newer run of the same conversation. `409 run_starting` means runtime creation is still pending: retry End instead of assuming termination. `503 codex_cleanup_pending` means cleanup is still unconfirmed; an interrupted End stays stopping and can be retried. `503 codex_ownership_unavailable` reports invalid or inaccessible AXV ownership state. Continuation waits for the same cleanup boundary before verifying history or reserving its new run. The UI confirms ending separately from window closure and continuation. Session-only terminal/end routes are absent; input, resizing and scrolling also stay bound to the original run. Metadata responses contain no transcript, scrollback, credentials or document content.

## Document context

Context routes use the same access, decoding and request-body rules as the other host Codex routes. POST accepts immutable snapshot IDs from the [snapshot API](project-files.md#immutable-snapshots-and-provenance), with no client-provided text, host path or provider thread identifier. It resolves source content from this desktop database and binds the destination to the addressed session/run and its verified provider UUID. Reads and writes do not create or continue a conversation or submit a model turn.

Supply **1–8 distinct snapshot IDs** and a `requestId` with the same **1–128 UTF-8 byte** syntax as a start request. IDs are scoped to the exact run. A matching retry returns its original batch before checking runtime readiness or rereading source content; the ordered snapshot ID list must match. A different payload with the same ID returns `409 context_request_conflict`. Preserve the original request ID after a lost HTTP response and recover its receipt before offering another delivery.

Each batch permits at most **1 MiB** of combined snapshot content. A run permits **four pending batches** and retains at most **256 batches**, including terminal receipts. Receipts are retained for idempotency rather than evicted; reaching either bound returns `409 context_limit`. Cancel pending batches to free pending capacity, or explicitly end and continue the saved conversation in a new run after the receipt limit. The bridge's complete augmented protocol message must remain below **32 MiB**; otherwise those snapshots fail and the original user message is submitted without them.

A batch contains:

```text
{
  id, requestId, sessionId, runId, providerThreadId, status,
  snapshots: [{ id, databaseId, entryId, revision, name, sha256 }],
  createdAt, updatedAt, providerTurnId?, error?
}
```

Timestamps use RFC 3339 with UTC and optional fractional seconds. Snapshot metadata identifies the captured saved revision, including after the source is edited or removed; batch responses omit document content. Poll the exact run's context route to observe changes:

| Status | Meaning | Caller action |
| --- | --- | --- |
| `pending` | Stored for the next user message actually submitted by the official TUI | May cancel; queuing alone sends no message |
| `unknown` | Dispatch was durably claimed but provider acceptance is unconfirmed | Continue observing; never automatically retry or cancel an attempted delivery |
| `accepted` | The corresponding provider response confirmed its turn ID | Delivery is confirmed, independently of whether the model turn finishes successfully |
| `failed` | Explicit provider rejection, input-size rejection, or the run ended before dispatch | Show the reason; a new delivery requires a separate explicit user action |
| `cancelled` | Cancellation won before dispatch | No delivery will be made for this batch |

All pending batches accompany the next nonempty user message actually submitted by the official TUI, including an explicit message to steer an active turn. Buffered input and tool-only turns do not consume them. Original user input and turn settings remain intact. `unknown` also covers the period while awaiting acknowledgment; a provider response may advance it to accepted or failed. An explicit rejection fails the delivery without requeuing it. Lost or invalid acknowledgment, a crash or disconnection leaves it unknown and never causes automatic replay. Accepted content belongs to Codex's saved conversation history; the receipt confirms delivery independently of generation or execution success. [Codex architecture](../codex.md#document-context) owns provider protocol matching and durable dispatch.

DELETE is idempotent for an already cancelled batch. If dispatch or another terminal state already won, it returns `409 context_state_changed`; callers must refresh the receipt. End fails pending batches without redirecting them to a later run, including when called on an already final run with an initialized queue. An End rejected before stopping begins leaves pending batches intact. A provider acknowledgment arriving after End can still resolve an attempted batch. Desktop service restart and browser detachment preserve the queue; runner exit fails remaining pending batches. End still terminates the addressed runtime if queue cleanup fails, with a visible queue error.

New delivery requires a running, ready run. `409 context_not_ready` requires waiting or refreshing; an ended run cannot accept a new batch. `409 context_unsupported` identifies a live runner started before document-context capability was added: the user must explicitly end it and continue the verified conversation to use the new runner. `409 context_source_changed` rejects a snapshot whose database identity or digest cannot be verified. `503 context_unavailable` reports missing established receipts, invalid or inaccessible queue state; it is never silently reset or recreated. Receipt reads return an empty list for runs that never initialized a queue and do not initialize one themselves. No state, retry or cancellation operation substitutes a new provider conversation.

See [Codex architecture](../codex.md) for runner, protocol-identity and storage ownership; [startup](../startup.md) for configuration; and [Host Terminal](host-terminal.md) for a separate ephemeral shell in the project.
