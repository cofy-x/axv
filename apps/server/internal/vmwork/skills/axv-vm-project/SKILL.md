---
name: axv-vm-project
description: Read and edit an AXV-bound VM project and run persistent installation, build, and test tasks from a host Codex session. Use the exact bound command supplied by AXV; this skill does not select a different VM, bind a new environment, or grant host permissions.
---

# AXV VM project

The agent runs on the host. Project files and project tasks run inside the VM through AXV. The host working directory is a control workspace, not a checkout or a mount of the VM project. Keep project changes in the bound VM project.

Use the absolute bound command supplied in the current AXV run context. In the examples below, `BOUND` means that complete command, including its original context-file argument. Preserve both paths exactly, quote them as shell arguments, and append only the operation and its arguments. Do not use another `axv` from PATH, resolve a profile, capture a new binding, change the context file, or substitute a newer run. The bound command's `--help` and `task --help` describe its flags.

The context identifies one persistent project, retained volume and reviewed environment: host, VM, boot, guest-service instance and revisions. A stale-binding or changed-host error requires an explicit environment selection in AXV. Preserve the original context and report the error; do not refresh or rebind automatically. The skill supplies workflow guidance, not a security boundary: provider approval and host permissions still apply, and task processes retain the ordinary guest user's permissions.

## Read and edit

Start by listing the project and reading its relevant `AGENTS.md` or other project instructions through the bound command. File paths are relative to the registered project root. Do not use host file tools or apply patches in the control workspace as a way to change VM code.

```sh
BOUND files ''
BOUND read AGENTS.md
BOUND read src/main.go
```

Directory results can be paginated: continue with the returned cursor and the same binding. Reads return UTF-8 content and its `sha256`. File writes accept up to 1 MiB of UTF-8 text and require an explicit expectation:

```sh
BOUND write src/main.go --expected-sha256 ORIGINAL_SHA256 --file HOST_STAGING_FILE
BOUND write notes.txt --create --file HOST_STAGING_FILE
BOUND mkdir source
```

Use a temporary host staging file only to hold the intended UTF-8 content; it is not a synchronized copy. Preserve the reviewed hash for the exact file you read. A hash conflict preserves the current VM file: retain your intended content, inspect the conflicting content, and resolve the conflict explicitly with the user before replacing it. Never automatically change the expectation or overwrite newer changes. If a write response is lost, read the exact path to determine what committed before deciding whether another write is needed; do not blindly replay a mutation.

## Install, build and test

Use persistent project tasks for guest commands, including package installation, builds, tests and repository commands. Commands are argv after `--`; shell interpretation occurs only when you explicitly choose a guest shell. Directory selection remains relative to the registered project root.

```sh
BOUND task submit --request-id UNIQUE_REQUEST_ID --timeout 15m -- sh -ec 'npm ci && npm test'
BOUND task list
BOUND task inspect TASK_ID
BOUND task logs TASK_ID ATTEMPT_ID --offset 0
```

Choose and retain a unique request ID before submission. AXV prints the original recovery identity before submitting and returns a durable Task ID and Attempt ID. Acceptance does not mean completion. Inspect the recorded task until its exact attempt reaches a final state: `succeeded`, `failed`, `cancelled`, or `interrupted`. An `unknown` result is unresolved, not a confirmed failure or permission to retry. Do not treat a disconnected CLI, browser or desktop as task cancellation.

Keep logs' byte offsets and use `nextOffset` for subsequent chunks. An empty log chunk does not prove you have the full tail. Inspect the attempt's `cachedBytes`, `logBytes`, `syncError`, and `logLimited` before reporting completeness; controller caching may lag guest output even after execution finishes. JSON logs contain base64-encoded bytes; `--raw` prints the exact bytes. Report truncation, output-limit failures, cache gaps, and unavailable logs instead of presenting incomplete output as complete.

If a submit or retry response is lost, recover the original request receipt:

```sh
BOUND task receipt ORIGINAL_REQUEST_ID
```

Do not submit a new request or retry while the original outcome is uncertain. A missing or `unknown` receipt leaves uncertainty; it does not prove the command failed to run. Reconcile the original request and exact attempt first. Retained task history and receipts can be read even when the original VM is gone. Executor or VM loss interrupts an attempt without automatic replay. Explain a confirmed interrupted outcome and obtain the user's decision before retrying it. An unknown attempt cannot be retried until its original outcome is reconciled, even if the user wants to continue.

Cancellation always names the recorded Task ID and Attempt ID, including a historical attempt. It never means “cancel whichever attempt is current”:

```sh
BOUND task cancel TASK_ID ATTEMPT_ID
```

Inspect the same attempt afterward to confirm termination. An explicit retry creates another attempt of the recorded command using this run's captured binding and a new request ID:

```sh
BOUND task retry TASK_ID --request-id UNIQUE_RETRY_REQUEST_ID
```

Report the exact project and VM context, changed files, Task/Attempt IDs, final test results and any unresolved failure. Reopening a session should recover the same conversation and task records; it must not invent history, move work to a host checkout, or silently target a replacement VM.
