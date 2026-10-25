# Startup and background services

This guide owns installation, updates, uninstallation, CLI workflows, service operation, configuration, paths and environment. AXV-managed configuration, data and runtime records default to `~/.axv`; `AXV_HOME` selects another profile root. See [development](development.md) for builds and tests, and [project status](../README.md#project-status) for platform validation.

VMs run on the computer where AXV is deployed. Its operating system determines the VM runtime: Lima on macOS or Firecracker/KVM on Linux. The browser's operating system does not choose the execution machine.

## Execution identity

AXV runs as the operating-system user that launches it, including root, without an additional flag. Host Terminal and coding agents inherit that user's permissions; provider tools retain their own approval and sandbox policies. Default profile paths use that user's home directory. AXV does not switch users or migrate another user's profiles, provider configuration or history. The [local access rules](api/README.md#access-and-identity-boundaries) apply to every launch identity.

## Installation and updates

### Install a release archive

Published Linux amd64 prereleases are not available yet. Once a prerelease is published, use its release notes to select the exact version and read its validation scope, prerequisites and data-compatibility limits before updating. Download `axv_<version>_linux_amd64.tar.gz` and `SHA256SUMS` from that same release into a new, empty installation directory. Keep the original downloads and the extracted directory; do not overwrite an earlier installation.

Replace the version placeholder below with the version from those release notes, then run these commands from the download directory. Extraction and execution only proceed after checksum verification succeeds:

```bash
axv_release_version='<version-from-release-notes>'
axv_release_bundle="axv_${axv_release_version}_linux_amd64"
sha256sum --check --strict SHA256SUMS &&
  tar -xzf "${axv_release_bundle}.tar.gz" &&
  "./${axv_release_bundle}/axv" version
```

Confirm that the reported version matches the selected release. Run that executable by its absolute path, or add its extracted directory to your `PATH` and check `command -v axv` before use. The production executable embeds its Web UI and requires neither Go nor Node at runtime. Coding-agent clients, tmux, VM providers, kernels and Guest images are separate prerequisites; the binary archive does not install them. See [daily commands](#daily-commands) for supervision requirements and foreground operation.

For an isolated startup check, choose a new, independent `AXV_HOME` and run the extracted executable with `serve`; stop it with Ctrl+C before reviewing or removing that test profile. Maintainers should use a temporary installation and profile under this checkout's `.data/test` (or `AXV_TEST_TMPDIR`), following [test isolation](development.md#test-isolation-and-diagnostics). An archive install does not require `make install`, which builds a different executable from the checkout.

If download, verification, extraction or startup fails, keep the earlier executable and profile, preserve the failed files for diagnosis, and leave running services on their existing version. Review the [storage baseline policy](storage.md#desktop-database-baseline) before using the new executable with an existing profile; selecting an earlier executable does not undo data changes.

### Build from source

From a checkout with the [pinned toolchain](../README.md#install-and-use):

```bash
make install
axv
```

`make install` installs locked build dependencies, builds the production executable with its Web UI, then atomically installs it into the effective Go binary directory. The configured `GO` command supplies `GOBIN`; when unset, the destination is the first `GOPATH` entry's `bin`. For an explicit destination, use `GOBIN=/absolute/bin make install`. Installation accepts native builds only and retains the previous executable if building or publishing fails. `VERSION=1.2.3 make install` sets the displayed version; development builds otherwise report `dev`.

Follow the printed PATH guidance if the directory is missing or another `axv` takes precedence. Installation does not edit shell startup files, initialize profiles, start services or prepare VMs. The installed command works outside the checkout and needs no Node at runtime. `make build` only creates `dist/axv`; `make deps` only installs build dependencies.

### Apply an installed update

Replacing the installed command or selecting a new archive directory on `PATH` does not interrupt live services. Both foreground and background desktops capture immutable executable copies under `AXV_HOME/bin` for their host and Codex children. Reopening with `axv` retains the current service version and settings. Use `axv daemon restart` to apply the installed desktop version; explicit stop/start updates both managed desktop and host services. Existing Codex runs keep their original executable and session identity.

## Uninstallation

Use the same operating-system account and `AXV_HOME` as the workstation you intend to remove. Other profiles and independently supervised services have separate lifetimes.

1. Before stopping services, choose **End Session** for each [Codex run](usage.md#codex) you want to terminate, and stop the selected VMs in **Virtual Machines** or with `axv vm stop <name|id>`. If you also want to remove VM storage, use the normal machine and volume deletion controls while the host is available; review [project-volume retention](usage.md#virtual-machines) first.
2. For managed background jobs, run `axv daemon stop` for this profile. Stop a foreground `axv serve` in its launching terminal with Ctrl+C. Daemon shutdown retains independent Codex runs, VMs and project volumes; an administrator-managed Linux host service requires its own supervisor action. Confirm the intended services have stopped before removing their executable or data.
3. Remove only the installed `axv` executable (`axv.exe` on Windows). For a source installation, use the destination printed by `make install`; for an archive installation, use the extracted version directory you selected and remove its `PATH` entry if added. In a POSIX shell, `command -v axv` helps check which entry is on `PATH`; review that path before removing it. Keep the containing binary directory and its other programs.

Removing the installed command leaves profile configuration, documents, default workspace, VM storage, logs and fixed executable copies in place. Reinstallation can reuse retained state under the [baseline policy](storage.md#desktop-database-baseline). For optional data cleanup, back up wanted content, stop all affected writers and review the [resolved profile and storage paths](#configuration-and-storage) before selectively removing resources. Existing host projects, provider-owned credentials/history and unrelated tmux sessions remain outside this cleanup; keep run ownership records until their processes have stopped.

The [shared download cache](#shared-download-cache) is independent of a profile. If you want to reclaim cached downloads, use `axv cache list` and `axv cache remove <full-sha256>` before removing the command. Do not recursively remove the entire default `~/.axv`: it also contains that cache and independent development profiles. Separately installed Codex, tmux, OpenSSH and administrator-managed VM runtimes are maintained through their own installation methods.

## Daily commands

Use the installed command from any directory:

```bash
axv                      # start or reopen the background workstation in a browser
axv init                 # create a starter config without starting services
axv serve                # foreground desktop; VM service starts in the background
axv daemon start         # explicit background start without opening a browser
axv daemon status        # desktop identity and VM controller readiness
axv daemon logs          # print recent service logs
axv daemon restart       # reload desktop settings, preserving the VM host
axv daemon stop          # stop this profile's managed services
```

Running `axv` without arguments first verifies the profile's existing desktop and opens its address, including when that desktop was started with `serve`. Reuse does not apply edited configuration or a different shell environment. With no healthy instance, it starts managed background services. Concurrent launches wait for the same owned startup; an unresponsive instance reports diagnostics instead of being automatically replaced. A browser-opening failure prints the usable URL and leaves the service running.

Use `axv help` and each command's `--help` for options; help and version commands do not initialize storage. Put command-specific options after the command. `daemon start` and `restart` accept `--open`, `--addr` and `--data-dir`. `axv init` creates a private starter `config.json` only when missing, with desktop defaults and compute disabled. It preserves an existing profile and does not create databases, a project workspace or services.

The default URL is `http://127.0.0.1:3001`. The ordinary desktop listener requires a loopback peer and loopback Host; binding another interface does not authorize public desktop access. An occupied port is an error; port zero allocates a free port and prints the actual URL. Explicit repeated `daemon start` verifies both instance identity and requested desktop settings before reuse. One desktop may use a profile/database at a time. The daily opener verifies only a local HTTP address and never follows redirects or proxies its identity request.

The desktop becomes available before VM environment checks finish. Missing VM dependencies, invalid compute settings and an unavailable VM service are reported in **Virtual Machines** without preventing document and host-session use. Desktop configuration, database and listener errors still fail startup. Already configured managed services are started or verified for reuse in the background; this does not start stopped VMs.

macOS uses launchd in the current GUI login session. Linux uses a systemd user manager with `systemd-run` (systemd 254 or newer). Starting a daemon does not enable login/boot startup or alter lingering. Where native background supervision is unavailable, including Windows, use foreground `axv serve`; AXV does not silently switch supervision methods.

| Operation | Lifetime effect |
| --- | --- |
| `daemon restart` | Replace an existing desktop; end ordinary host shells, detach Codex views, preserve managed VM host and guests |
| `daemon stop` | Stop this profile's managed desktop/host jobs; Codex runs, VMs and project volumes survive |
| Foreground shutdown | Stop the desktop and only the host child this invocation started; reused or independently managed VM services stay running |

Interrupting a launch before desktop readiness removes only the desktop job started by that invocation. Reused desktops and independently supervised VM hosts retain their ownership and lifetime.

Persistent work still consumes resources after services stop. Use **End Session** for Codex and **Virtual Machines** to stop/delete VMs. Task-expiry enforcement pauses while the controller is down. A host restart observes existing VMs and resumes queued work; it does not boot previously stopped machines. Guest-shell lifetimes are described in [usage](usage.md#virtual-machines).

## VM command line

The VM CLI talks directly to the same local host service as the desktop. A browser and desktop service are optional:

```bash
axv init
axv setup
axv vm images
axv vm create dev
axv vm list
axv vm inspect dev
axv vm stop dev
axv vm start dev
```

On macOS, the first VM command starts or reuses the configured managed host under native supervision, without opening a browser or creating the desktop database. Compute configuration is resolved independently of desktop address, workspace and agent proxy settings. An external Linux service must already be running under its administrator's supervisor. Service startup never starts an existing stopped VM.

The host may be the profile's only managed job. `daemon status`, `logs` and `stop` work in that state. Running `axv` or `daemon start` adds a desktop while preserving the host; `daemon restart` requires an existing desktop and does not create one.

`vm create <name>` creates and starts a development VM by default. Use `--no-start` to create it stopped. Web and CLI share host-provided creation defaults: 2 CPUs, 1024 MiB memory, an 8 GiB root disk and a retained 4 GiB project volume. The selected image can require a larger root disk; capacity failures are reported without reducing the request. `--cpus`, `--memory`, `--disk` and `--project-disk` override those allocations. Select `--image <id>` when more than one compatible image is available. `--kind task --ttl 1h` creates a task VM with disposable project storage by default; add `--retain-volume` to retain its project volume after deletion or expiry. [Machine creation](api/compute.md#machine-creation-and-configuration) owns resource limits and the unique-name contract.

VM commands accept the exact case-sensitive name or stable machine ID. `vm list` has the alias `vm ls`; `vm images` and `vm volumes` list their respective inventories. `vm start`, `stop`, `restart` and `rm` operate on one VM. `stop --force` requests provider-supported force-stop. `rm` asks for confirmation, or accepts `--yes` (`-y`) for noninteractive use; it deletes the root disk and honors the recorded project-volume retention policy.

Mutation commands print the profile, host, target and request ID before submission and the accepted machine/operation IDs to stderr. They wait for completion by default; `--no-wait` returns after acceptance and `--timeout 10m` changes the wait bound. Interruption or timeout stops waiting without cancelling accepted work. Do not repeat a mutation while its outcome is unknown:

```bash
axv vm operation list
axv vm operation get <operation-id>
axv vm operation wait <operation-id>
axv vm operation wait --request-id <original-request-id> --host-id <original-host-id>
```

An unknown request ID is not proof that submission failed; `operation wait --request-id` continues checking until it finds a final result or times out. Keep the original `AXV_HOME` and pass the receipt's `--host-id` to operation get/wait so recovery fails if the host was replaced. `--json` produces machine-readable stdout while diagnostics remain on stderr. Inspect and operation get/wait already return JSON by default. Exit codes are **0** for success, **1** for operation failure, **2** for usage errors and **130** for interruption; SSH also preserves the remote command's exit status.

### VM project files

`axv vm project` is the explicit host-agent interface for projects on retained VM volumes. Put an existing repository and its worktree metadata on the volume using [guest SSH](#ssh-file-transfer-and-forwarding), or use [project initialization](#vm-project-initialization) to create an empty project or clone a repository. Register an existing volume-relative directory while that VM is running with the `project-files` capability:

```bash
axv vm volumes --json
axv vm project register --machine dev --volume <volume-id> --root repo --name 'My project' --json
axv vm project list --json
axv vm project binding <project-id> > project-binding.json
axv vm project files <project-id> '' --binding-file project-binding.json
axv vm project read <project-id> src/main.go --binding-file project-binding.json
axv vm project write <project-id> src/main.go --binding-file project-binding.json \
  --expected-sha256 <sha256-from-read> --file /absolute/reviewed-main.go
axv vm project mkdir <project-id> notes --binding-file project-binding.json
printf 'Project notes\n' | axv vm project write <project-id> notes/README.md \
  --binding-file project-binding.json --create
```

Omit `--root` to register the volume root. Registration reads the selected volume's revision and submits that exact machine/volume/root; the host rejects concurrent attachment changes. It prints the host/machine/volume/revision/request receipt to stderr; `--request-id` can preserve a caller-chosen registration identity. If a response is lost, inspect `project list --json` before replaying. For an exact replay, provide `--host-id <original-host-id>`, the receipt's immutable machine ID, `--volume-revision <original-revision>` and the same request ID, volume, root and name. These recovery flags must be supplied together; recovery verifies the original host and submits the original payload without looking up the current VM or volume, so a committed registration remains recoverable after VM removal. Never resolve a replacement VM name or substitute a newer revision. Registration is synchronous and does not create an operation-wait record.

Binding and file commands return JSON by default; `--json` is also accepted. The binding file must contain the full `{ project, binding }` object returned by `binding`. All file commands require that original file and never refresh it automatically. After a VM replacement, restart or guest-service change, explicitly capture a new binding only after reviewing the new environment. Old commands fail even if another VM now mounts the same volume. A missing guest never redirects work to the host. Binding files select an execution environment and contain no guest authentication token.

File paths are relative to the project; the empty path lists its root. `files` accepts `--limit` and the returned `--cursor`. `read` returns UTF-8 content and its SHA-256; `write` requires exactly one of `--expected-sha256` or `--create`, reads stdin by default (`--file -`) and accepts up to 1 MiB. Writes reject stale versions and preserve existing files on create-only conflicts. Errors do not retry, replace the binding or fetch a new hash. If a write/mkdir response is uncertain, inspect that original path before deciding what to do. [Compute project API](api/compute.md#vm-projects) owns limits and concurrency guarantees.

Projects retain their identity through desktop/controller restart and replacement of a VM using the same retained volume. [Project tasks](#vm-project-tasks) provide persistent command execution separately from file writes. Restore-to-copy rebinding, automatic worktree repair and a host-agent sandbox remain unavailable. Existing synchronous guest commands and SSH retain their [separate lifetimes](api/guest.md).

### VM project initialization

Register the retained volume root as a parent workspace, capture its binding, then initialize a new directory with an explicit persistent request ID. The selected image and guest need both `project-files` and `project-tasks`:

```bash
axv vm project register --machine dev --volume <volume-id> --name 'Volume workspace' --json
axv vm project binding <parent-project-id> > workspace-binding.json
axv vm project initialize <parent-project-id> my-repository \
  --binding-file workspace-binding.json --request-id clone-repository-001 \
  --repository https://github.com/owner/repository.git --name 'My repository'
axv vm project task receipt <parent-project-id> clone-repository-001 \
  --binding-file workspace-binding.json
axv vm project finish-initialization <parent-project-id> clone-repository-001 \
  --binding-file workspace-binding.json
```

Omit `--repository` to create an empty project directory. Only one new directory component is accepted; the parent must exist. HTTPS and SSH URLs must omit inline passwords, HTTPS credentials, query parameters and fragments. Existing guest Git authentication is used without interactive prompts; SSH also requires existing host trust. An existing destination fails without changing its files. A failed clone preserves its partial directory; inspect it in the parent workspace before explicitly choosing another destination or performing manual cleanup. AXV does not automatically overwrite or delete it.

Initialization returns an accepted persistent task, not a completed project. Inspect its original task receipt and logs until it succeeds, then explicitly finish with the original binding to register the new project. After a lost response, query the same receipt or repeat the exact initialize command with its original request ID and arguments. Replaying finish returns its original project; neither command silently selects a replacement VM. In the desktop, use **Project Files → VM → Create Project / Clone Repository**, then **Open Project** and **Codex**. [Compute initialization](api/compute.md#vm-project-initialization) owns defaults and retry boundaries.

### VM project tasks

`axv vm project task` is the host-agent interface for dependency installation, builds and tests inside a registered VM project. Its image and current guest must support `project-tasks`. Capture the intended environment explicitly, then submit argv after `--`:

```bash
axv vm project binding <project-id> > task-binding.json
axv vm project task submit <project-id> --binding-file task-binding.json \
  --request-id install-original --timeout 30m -- npm ci
axv vm project task receipt <project-id> install-original --host-id <original-host-id>
axv vm project task inspect <project-id> <task-id> --host-id <original-host-id>
axv vm project task list <project-id> --host-id <original-host-id>
```

Submission returns a task and its first execution attempt after durable acceptance; it does not wait for the command to finish. The task retains its original command and limits, while each attempt records its own VM, volume, boot and guest-service binding. Observe `attempt.status`, `exitCode` and `reason` before starting dependent work. Browser closure, CLI interruption and desktop/controller restart do not cancel accepted tasks. A task VM's own TTL remains independent and can stop its work by deleting that VM.

`submit` accepts `--directory` relative to the registered project root and per-attempt `--timeout`, `--cpus`, `--memory` (MiB), `--max-processes` and `--max-log-bytes`. Defaults are 15 minutes, one CPU, 512 MiB, 64 processes and 1 MiB of combined stdout/stderr. Limits must be supported by the guest and fit the selected VM. Exceeding the log capacity stops the attempt with a visible `log_limit` failure. Commands run without shell interpolation; use an explicit `sh -ec '<script>'` argv when a shell is intended. The starting directory does not sandbox the command's ordinary guest filesystem access. [Project task API](api/compute.md#vm-project-tasks) owns limits, states and enforcement.

Submit and retry print the original host/project/environment/request receipt to stderr before sending it; an output failure prevents submission. Keep the original `AXV_HOME`, request ID and binding after any uncertain response; recover using `receipt`, never submit a new request merely because the response was lost. `--request-id` is optional for a first submission; an omitted ID is generated and printed. An unknown receipt does not prove that submission failed. The CLI does not automatically retry mutations, refresh bindings or execute commands on the host.

Queries require `--host-id` or a saved `--binding-file`; a binding file selects the original host for queries without requiring that old VM to remain available. `inspect` includes the current attempt and complete recorded attempt history. To inspect one attempt's combined log, retain its exact ID and byte offset:

```bash
axv vm project task logs <project-id> <task-id> <attempt-id> \
  --host-id <original-host-id> --offset 0 --limit 65536
axv vm project task logs <project-id> <task-id> <attempt-id> \
  --host-id <original-host-id> --offset 0 --raw > attempt.log
axv vm project task cancel <project-id> <task-id> <attempt-id> \
  --host-id <original-host-id>
```

All commands return JSON by default; `--json` is also accepted. Log JSON contains base64 `data`, `offset` and `nextOffset`; use the returned `nextOffset` for another chunk. `--raw` writes exactly that chunk's bytes with no added newline. Host-cached logs remain readable when the guest is unavailable; `logBytes` can exceed the cached bytes, so an empty chunk alone does not prove completion. `logs` returns one chunk and does not follow new output.

Cancellation always names one attempt. With `--host-id`, the CLI reads that exact attempt's recorded binding from durable task history; it never substitutes the current attempt's binding. Alternatively supply its original `--binding-file`. Cancellation of an old attempt cannot target a later retry or replacement VM. Acceptance of cancellation is separate from confirmed process-group termination; continue inspecting the task's state.

Retry is an explicit new attempt of the original task specification after the prior attempt reaches a final state. Capture and review the intended binding separately, then submit a distinct retry request:

```bash
axv vm project binding <project-id> > retry-binding.json
axv vm project task retry <project-id> <task-id> \
  --binding-file retry-binding.json --request-id retry-original
```

An `unknown` attempt cannot be retried until reconciliation establishes its outcome or confirms interruption; this avoids overlapping unconfirmed work. Retry does not change argv, directory or limits. To change the specification, submit another task explicitly. Per-task disk/I/O/network throttling and host-agent permission constraints remain separate work. [Offline backups](#vm-project-backups) preserve project volumes independently of task execution history.

### VM project backups

Save or explicitly preserve editor drafts, finish/cancel accepted tasks, and stop the source VM explicitly before copying. Reconcile `unknown` attempts until they have a final outcome. Inspect the project, volume and stopped VM revisions; backup creation requires those exact reviewed identities. This copies the entire retained project volume, including registered roots, Git metadata, linked worktrees, untracked files and installed dependencies. It excludes the VM root disk, guest task journal, host task history, Codex history and unsaved browser drafts.

```bash
axv vm project backup list --host-id <host-id>
axv vm project backup create <project-id> --host-id <host-id> \
  --request-id backup-original --name "Before upgrade" \
  --project-revision <project-revision> --volume-revision <volume-revision> \
  --machine <stopped-machine-id> --machine-revision <machine-revision>
axv vm operation wait --host-id <host-id> --request-id backup-original
axv vm project backup restore <backup-volume-id> --host-id <host-id> \
  --request-id restore-original --revision <ready-backup-revision> \
  --name "Recovered volume"
axv vm operation wait --host-id <host-id> --request-id restore-original
```

All commands return JSON. Preserve `--host-id`, `--request-id` and every original argument when recovering a lost response. Creation and restore return accepted durable operations; wait for success and inspect the volume before continuing. The backup remains detached and immutable. The restored volume has a new ID; create a compatible VM explicitly selecting that ID (for example through Virtual Machines or `axv vm create --volume <restored-volume-id>`), then start it. Preserve the original guest mount/root locations so Git worktree metadata continues to resolve inside the copied volume.

```bash
axv vm project backup bind <backup-volume-id> --host-id <host-id> \
  --request-id bind-original --source-project <original-project-id> \
  --volume <restored-volume-id> --volume-revision <current-volume-revision> \
  --machine <new-running-machine-id> --name "Recovered project"
axv vm project binding <new-project-id> > restored-binding.json
```

Binding requires a selected image and running guest declaring `project-restore` as well as `project-files`. It validates the original project filesystem identity and Git/worktree paths, including each linked worktree's common directory and private configuration, then returns a separate project identity. It rejects external or missing worktree paths and Git configuration includes until reviewed and repaired explicitly on the recovered copy. Old bindings and agent run contexts remain attached to their original project and volume. Capture the new binding explicitly for subsequent file/task commands; no old run is redirected. [The backup API](api/compute.md#vm-project-backups) owns provider, identity and consistency limits. The Project Files **Backup and restore** panel provides the same staged workflow and preserves uncertain request payloads for explicit recovery.

### Bound VM project tools for Codex

Choose **Codex** in a registered VM project's Project Files window, then explicitly start a session in its scoped manager. The host provider uses an AXV-owned control directory. Its per-run AXV Skill receives an absolute bound command of the form `AXV_EXECUTABLE internal-vm-project RUN_CONTEXT COMMAND ...`. The private command exposes the existing `files`, `read`, `write`, `mkdir` and `task` operations with the project ID already supplied; its help describes the remaining arguments. It rejects host/binding overrides and connects using its original socket, host ID and configuration fingerprint without loading another profile or activating another controller.

The run context and packaged Skill remain in that run's private data directory. Keep the recorded executable and context path when recovering a task receipt; do not replace them with a current `axv` from PATH. Existing file version checks, persistent task request IDs, log offsets and exact-attempt cancellation still apply. A stale environment requires an explicit review and continuation in AXV; host configuration changes cannot silently retarget the old command. Task history can remain readable without a live VM. [Codex](codex.md#vm-project-work-context) owns this integration; [usage](usage.md#codex) describes start, reconnect and confirmation.

### SSH, file transfer and forwarding

Use the installed system OpenSSH client for standard guest sessions:

```bash
axv vm ssh dev
axv vm ssh dev -- uname -a
axv vm ssh dev --start
axv vm ssh dev -N -L 8080:127.0.0.1:3000
axv vm ssh dev -N -R 9000:127.0.0.1:9000
axv vm ssh dev -N -D 1080
axv vm ip dev --json
```

SSH requires a running VM; only `--start` explicitly starts a stopped one. `--timeout` bounds preparation/start, not an established connection. `-t` requests a PTY (repeat to force it), `-T` disables one, and `-N` runs forwarding without a remote command. Local (`-L`), remote (`-R`) and SOCKS (`-D`) forwarding use OpenSSH semantics; remote listeners remain on guest loopback. SSH commands preserve the remote exit status. Guest addresses from `vm ip` describe the current guest network, not guaranteed host reachability; SSH uses the private host transport instead of depending on those addresses.

`ssh-config` prepares access and prints an independent configuration for `ssh`, `scp` and `sftp`. It also requires a running VM unless `--start` is supplied, and does not edit `~/.ssh/config`. For example:

```bash
vm_config=$(mktemp)
axv vm ssh-config dev > "$vm_config"
vm_host=$(awk '/^Host / {print $2; exit}' "$vm_config")
ssh -F "$vm_config" "$vm_host" uname -a
scp -F "$vm_config" ./local.txt "$vm_host":/projects/
sftp -F "$vm_config" "$vm_host"
rm "$vm_config"
```

Generated configuration binds the profile, host identity, host configuration and stable VM ID. It remains usable after that VM is renamed or restarted, but fails if the host binding changes or the VM is deleted; a new VM with the same name is a different target. Each attachment verifies the current boot before opening the stream. Direct `vm ssh` also fixes the boot selected during preparation. Neither path silently replaces a missing target or starts a stopped VM from a saved configuration.

AXV creates a private Ed25519 client key and known-hosts file under `AXV_HOME/ssh/`. It registers only that profile's public key with the selected guest and pins the guest's key by host and machine identity. A changed host key fails and requires explicit investigation/repair of that exact known-hosts entry. Personal SSH keys, agents and configuration are not imported. SSH uses the guest `axv` account with its normal filesystem permissions; it is separate from detachable browser Guest Terminal and has no AXV session recovery. These commands provide local guest access; [remote desktop access](#remote-access) uses a separate authenticated HTTPS transport and does not expose guest SSH credentials or raw SSH streams.

See the [Guest API](api/guest.md#ssh-access) for transport and key-registration limits.

## Remote access

Private remote access is optional and disabled by default. AXV embeds its own [tsnet](https://tailscale.com/docs/features/tsnet) node and serves the production desktop over HTTPS on macOS/Linux. The deployment machine does not need the system Tailscale service running: AXV does not use `tailscale up`, change host DNS/routes or install host firewall rules. In particular, keep the system service stopped and disabled on hosts where the tailnet's CGNAT range conflicts with the system DNS servers.

Open **Tailscale** from the local desktop icon, **Workspace → Applications** or **Preferences**, then choose **Enable**. This saves `remote.enabled: true` in `AXV_HOME/config.json` and starts AXV's node immediately. It also enables the node on future AXV service launches. **Disable** saves `false` and stops only the remote transport; neither action restarts the desktop. Closing the browser or Tailscale panel leaves the service running. [Remote desktop usage](usage.md#remote-desktop) owns the visible flow and confirmations.

Local desktop readiness does not wait for remote access. When status is `needs-login`, choose **Authorize** and enroll the node with the workstation owner's Tailscale account. First enrollment binds the stable owner and node IDs in private state. Tagged server/client nodes and other users are rejected. The same owner's authorized devices can access host files, shells, agents and VM controls with this deployment account's permissions; choose the enrolling account accordingly.

Advanced configuration can include the following `remote` section in the existing profile, preserving its other settings:

```json
{
  "remote": {
    "enabled": false,
    "hostname": "axv-workstation"
  }
}
```

`hostname` is a lowercase DNS label of 1–63 characters (letters, digits and internal hyphens); the default is `axv`. Each profile owns a separate node, so choose a distinct name for separate workstations. An optional `remote.owner_user_id` can require a known positive numeric Tailscale user ID before first enrollment. These settings are read from the current profile on AXV service startup and on explicit enable/retry. Editing the file does not change a running node; use the panel to apply its enabled setting. After changing hostname or owner requirements, explicitly disable and enable, preserving the existing owner binding; a conflicting identity requires maintenance, not automatic reenrollment.

For local inspection without enabling networking:

```bash
axv remote status
axv remote status --json
```

Enable MagicDNS and HTTPS certificates in the tailnet's [DNS settings](https://login.tailscale.com/admin/dns), and permit your devices to reach this node on TCP 443 in tailnet policy. [Tailscale HTTPS setup](https://tailscale.com/docs/how-to/set-up-https-certificates) explains that certificate issuance publishes the node's `.ts.net` name in certificate transparency logs. This does not make the service public. Missing settings produce `needs-https`; status `error` includes a diagnostic. Recheck status after enrollment/settings changes. In `ready`, open the reported HTTPS URL from another device connected to Tailscale as the bound owner. There is no HTTP fallback or public Funnel.

Node credentials, certificates and the owner binding stay under `AXV_HOME/tailscale/`, independently of desktop documents and the system Tailscale installation. The Tailscale SDK also maintains private diagnostic buffers there and uploads provider diagnostics to Tailscale; these can include enrollment URLs. AXV's own operational log records remote state without enrollment URLs. Normal desktop shutdown keeps that node enrollment for the next launch. Protect this directory as credentials; do not copy it into another profile or silently reset an invalid binding. Changing the enrolled owner or node requires explicit maintenance with the desktop stopped.

AXV renews certificates in the background while continuing to serve a valid cached certificate. Renewal errors appear in local status. If the certificate actually expires, or the enrolled identity/HTTPS domain changes, AXV withdraws the remote entrypoint; resolve the reported cause and choose **Retry** in the local Tailscale panel. The local desktop remains available.

To disable remote access, choose **Disable** in the local panel. Remote browsers disconnect and their ordinary Host Terminal shells end; Codex views detach while existing Codex runs and VMs continue. Local terminals and work stay open. Node credentials and owner authorization are preserved for the next enable. A configuration-save failure leaves the live setting unchanged. If a node cannot close cleanly, AXV rejects another start until the desktop service is restarted; the error remains visible. VM preparation and remote configuration remain operations on the AXV deployment machine. The [Tailscale remote access API](api/tailscale.md) owns authorization and route restrictions; [usage](usage.md#remote-desktop) covers browser state across devices.

## Cloudflare Access

Cloudflare is an optional alternative for accessing the desktop from a browser without a Tailscale client. It is disabled by default and can coexist with the private Tailscale entrypoint. AXV supplies an authenticated desktop loopback origin; you operate Cloudflare Access and an external `cloudflared` connector on the same machine. Optional account integration also publishes explicitly selected project services through a separate origin; it does not convert existing local previews into public URLs.

1. Create a self-hosted Access application protecting the **entire** desktop hostname, with an explicit owner policy and MFA through your identity provider. Set the application and every matching Allow policy to a session duration of at most **15 minutes**; AXV rejects a JWT whose signed lifetime exceeds that limit. Do this before routing public traffic. Record its team domain and application audience (AUD). Follow [Cloudflare's setup](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/self-hosted-public-app/); avoid Bypass/public policies for the desktop.
2. Obtain your stable Access user ID, the application token's `sub`, from your own Cloudflare identity. The [application-token reference](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/application-token/) describes claims and the authenticated `get-identity` endpoint. Inspect credentials locally; do not paste a JWT into an online decoder, AXV configuration or diagnostics. AXV requires an explicit owner subject and never enrolls the first visitor.
3. Open **Cloudflare → Setup → Desktop access** locally. Fill in the desktop URL, team domain, application audience, stable owner subject and loopback address. Validate, review and save the settings, then enable access in **Overview** when the external connector and policy are ready. The form preserves other profile settings and rejects stale edits. [Cloudflare usage](usage.md#cloudflare-desktop-access) covers launcher visibility and actions. For manual provisioning, the equivalent desktop-only `cloudflare` object in `AXV_HOME/config.json` is:

```json
{
  "cloudflare": {
    "enabled": false,
    "listen": "127.0.0.1:3002",
    "origin": "https://axv.example.com",
    "issuer": "https://your-team.cloudflareaccess.com",
    "audience": "your-access-application-aud",
    "owner_subject": "your-stable-access-user-id"
  }
}
```

`listen` defaults to `127.0.0.1:3002` and must use a numeric IPv4/IPv6 loopback address and a port from 1 to 65535. Select a different port for another profile. `origin` is a canonical lowercase public HTTPS DNS origin without a port, path, trailing slash, query or fragment. `issuer` is exactly `https://<team>.cloudflareaccess.com`, also without a trailing slash. Audience and owner subject are single nonempty identifiers of at most 256 UTF-8 bytes without whitespace or control characters. Disabled profiles may retain incomplete settings; invalid settings remain visible as an error for this entrypoint without blocking the local desktop, Tailscale or independent VM administration.

4. Configure your externally managed tunnel's hostname to connect to `http://127.0.0.1:3002`, preserving the public `Host: axv.example.com`. A locally managed tunnel's ingress can be:

```yaml
ingress:
  - hostname: axv.example.com
    service: http://127.0.0.1:3002
  - service: http_status:404
```

Use your existing tunnel's identity/credential configuration and supervisor. Cloudflare's **Protect with Access** connector setting can add token verification before AXV; AXV independently verifies tokens regardless. Do not route the tunnel to the ordinary local desktop port, rewrite Host/Origin to localhost, or disable origin checks. AXV's loopback origin uses HTTP only on this local hop; the public browser URL must use HTTPS.

5. When the panel says **Listening locally**, test the HTTPS hostname from another browser: the configured owner should reach the desktop; an unauthenticated user or different owner must fail. Check a document save, Host Terminal, Codex attachment and rejection of local-only controls. This state does not claim that the public tunnel is connected. Inspect the connector and Cloudflare dashboard separately if routing or login fails.

```bash
axv cloudflare status
axv cloudflare status --json
```

Enable/disable persists across launches without restarting the workstation. Editing issuer, audience, owner, hostname or listener settings does not mutate a running entrypoint: disable it, make the change, then enable it locally. Closing the panel does not stop access. Disable immediately closes only Cloudflare-originated connections; local and Tailscale clients remain available. The [Cloudflare contract](api/cloudflare.md#connection-lifetime) explains token expiry and revocation: an expired token disconnects its Host Terminal shell and detaches Codex, while independent runs and VMs continue. The lease is fixed by the original token's signed `iat` and `exp`, with a maximum 15-minute lifetime; reconnecting cannot renew it. Logout or policy revocation is not an immediate origin-side disconnect guarantee.

The embedded Tailscale node and this tunnel origin protect their own AXV entrypoints, not the host's SSH service or unrelated ports. Host VPN/SSH, cloud firewall and IPv4/IPv6 exposure remain administrator-owned. Verify a fresh connection from another device and a cloud-console rescue path before closing public ingress. Cloudflare [tunnel firewall guidance](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/tunnel-with-firewall/) describes required outbound connectivity. Do not blindly enable system Tailscale on a host with the DNS/range conflict described above.

Cloudflare terminates browser TLS at its edge and processes HTTP traffic; review its [HTTP privacy model](https://developers.cloudflare.com/data-localization/regional-services/http-requests/). A tunnel provides transport, Access provides identity policy, and WAF adds its configured protections; none changes AXV's host-shell or agent permissions. The [Cloudflare API contract](api/cloudflare.md) owns verification and route restrictions.

### Cloudflare account and service publishing

Prepare a Cloudflare zone, an existing remotely managed Tunnel and a running connector on the AXV computer. AXV does not create the tunnel, install its service, or replace connector credentials. Create a scoped user API token with DNS Edit on the selected zone and Cloudflare Tunnel Edit on the selected account; resource selection also needs access to read the zone and tunnel. Zone Analytics Read is optional for traffic. Follow the current [API token permissions reference](https://developers.cloudflare.com/fundamentals/api/reference/permissions/) when choosing scope.

In **Cloudflare → Setup → App publishing**, choose **Connect account**. The wizard validates the token, offers account, zone and existing tunnel selections, validates the base domain, then reviews the configuration. Save the reviewed settings locally. The account's API token is stored only in the private `config.json` management section and never returned in an API response; do not put Access JWTs or connector tokens there. The optional persisted fields are:

```json
{
  "cloudflare": {
    "management": {
      "api_token": "your-scoped-api-token",
      "account_id": "your-account-id",
      "zone_id": "your-zone-id",
      "tunnel_id": "your-existing-tunnel-id",
      "domain": "example.net",
      "listen": "127.0.0.1:3003",
      "audience": "your-publishing-access-application-aud"
    }
  }
}
```

Choose an **allocation domain**, not an arbitrary shared subdomain. A normal registrable domain such as `example.net` must be dedicated to one publication, separate from the desktop's `example.com` and all previous application sites. Its site remains reserved after Unpublish; it cannot be reassigned to a replacement VM, application or port. This costs a separate domain per publication lifetime. To allocate several isolated applications under one suffix, that suffix must already be recognized by browser Public Suffix Lists and lie inside your managed zone. Ordinary `apps.example.com` is not such a suffix and is rejected. AXV does not submit PSL applications or provide a shared hosted domain. The [isolation contract](api/cloudflare.md#browser-isolation-and-hostname-lifetime) owns these boundaries.

AXV generates a fresh first-level hostname such as `editor-<32-hex-generation>.example.net` at acceptance. Configure edge certificates for the actual depth of this hostname: Cloudflare [Universal SSL on a full zone](https://developers.cloudflare.com/ssl/edge-certificates/universal-ssl/limitations/) covers the apex and its first-level subdomains; a deeper allocation suffix requires an appropriate additional certificate arrangement. Do not infer certificate coverage or public readiness from successful DNS/ingress checks.

The publication listener defaults to `127.0.0.1:3003` and must differ from the desktop listener. Keep the public Host and Origin intact when forwarding tunnel requests. Application publishing manages individual DNS and ingress records; it preserves other applications' tunnel rules. Do not independently edit the same rules while AXV is publishing or cleaning up.

For owner-only publications, configure an Access application covering the generated app hostnames, set the application and matching Allow policies to at most 15 minutes, use its AUD for the publishing audience, and retain the desktop configuration's issuer and stable owner subject. This can be a separate application from desktop Access. AXV verifies each application's JWT at its own origin; it does not create edge policies. Explicit **Public** visibility permits Internet visitors without AXV owner authentication and does not require that application AUD.

Choose a capable VM project and an already running HTTP service in **Publishing**, or use **Publish** beside a project's preview port. Review the original project/VM, port, name, allocation domain and visibility before accepting. AXV assigns the unique hostname and preserves it in the accepted receipt. Publication continues after the browser disconnects. **Unpublish** immediately withdraws AXV's application access and then removes only recorded cloud resources. Cleanup failures remain visible and retryable while local access stays closed. A persistence failure requires storage repair and restart, followed by inspection of the original receipt before retrying Unpublish; it never stops the VM service. After desktop restart, each retained publication stays closed until an explicit **Resume** review of the same hostname and original binding. Already recorded removal cleanup can continue without reopening access. Remove publications and complete cleanup before changing account or routing settings. **Rotate token** can replace an expired or revoked credential without disabling desktop access or losing resource ownership.

The CLI uses the verified running local desktop and the same durable receipts:

```bash
axv cloudflare config
axv cloudflare check --force
axv cloudflare traffic --hours 24
axv cloudflare publications
axv cloudflare publish PROJECT_ID --port 3000 --name editor --domain example.net
axv cloudflare unpublish PUBLICATION_ID
axv cloudflare retry PUBLICATION_ID
```

Browser publications perform a cookie-suffix compatibility check before serving guest content. Allow host-only cookies and use a supported current browser; failed checks show an error without clearing application data. Service-worker/offline PWAs are unsupported, while regular scripts, dedicated workers, cookies, origin storage, HTTP/SSE and WebSockets remain available. Supported native clients are identified separately from browsers and still require the existing Host/Origin and owner-JWT checks; [the API contract](api/cloudflare.md#browser-isolation-and-hostname-lifetime) defines this boundary.

Publishing defaults to owner authentication; `--visibility public` explicitly permits public visitors. `--binding-file` accepts an explicitly captured `axv vm project binding` result. Recovery with `--request-id` also requires the original binding file, allocation `--domain` and journal `--instance-id` printed before submission; the CLI refuses to redirect that request to a replacement journal; after an uncertain response inspect publications instead of generating a new request. `axv cloudflare configure --file SETTINGS.json` accepts the complete reviewed HTTP configuration snapshot; use `--file -` for standard input and keep tokens out of shell arguments. [The API contract](api/cloudflare.md#configuration-and-account-checks) owns snapshot fields, revision checks, capacities and resource recovery.

## First-time macOS setup

```bash
axv
```

Open **Virtual Machines**. The environment workflow detects dependencies, lets you choose storage and allocation budgets, and shows the exact preparation actions before you start them. Until AXV enhanced images are published, the default is the pinned official Ubuntu 24.04 base: basic creation, start, stop, restart and deletion remain available without AXV guest enhancements. The reviewed plan names that base and its capability limit, verifies its immutable artifacts, publishes the host configuration and verifies the managed service in the same desktop process. This is the public transition policy; publication will require a separate explicit default-policy change. Selecting `--image-source official` currently reports `release_pending` with no invented URL. Advanced settings accept a local custom bundle. Preparation does not create or start a VM.

Automatic preparation uses a separately installed and verified **Lima 2.2.1** under each profile, reusing that profile's matching installation when present. It copies the pinned official archive from the shared cache and extracts privately, without selecting, changing or sharing Homebrew/system installations. Explicit existing host configurations retain their administrator-selected runtime pins. The complete [official Lima release](https://github.com/lima-vm/lima/releases/tag/v2.2.1), including its Apache 2.0 license under `share/doc/lima/LICENSE`, is preserved in that private installation. The reviewed [artifact catalog](../apps/server/internal/compute/artifacts) owns release URLs and digests; the shared [preparation engine](../apps/server/internal/compute/prepare) owns plans and execution. VZ hosting needs supported macOS and the system SSH tools; a blocked prerequisite is reported before downloads begin.

Storage defaults to the profile's `images/`, `compute/` and `lima/` directories. macOS Unix sockets impose a short path limit: preparation reports an overlong profile instead of moving Lima state elsewhere. Choose a shorter `AXV_HOME` (`AXV_DEV_HOME` for development), or explicitly select a dedicated Lima directory in advanced settings. Review the resolved paths before proceeding. CPU, memory, disk and machine-count budgets limit admission rather than reserving physical capacity. Leave headroom for macOS and image copies. Existing configurations keep their storage paths and provider ownership.

Preparation runs in the background. Closing or refreshing the window does not cancel it. Failed or interrupted work retains its task and verified artifacts; use the visible retry action. Cancellation is available before configuration commit. After commit, service-start failures retain the configuration so a retry can continue activation. An existing unrelated file, directory or socket is never permission to reset or adopt it.

`axv setup` remains available for terminal use and shares the same preparation and validation rules. With a running desktop, it follows or submits a task to that verified instance. Without a desktop, it prepares configuration only and reports that the service has not started. Review the displayed plan before confirming; noninteractive preparation requires `--yes`. `axv setup --retry <task-id>` retries the original interrupted or failed task with its original settings. `axv setup --help` lists storage, capacity and local-image options. Preserve `AXV_HOME` when selecting a nondefault profile.

Guest enhancements require preinstalled services from the [image build recipes](../images/guest/README.md). For custom preparation use `axv setup --image-source bundle --image-bundle /absolute/bundle/manifest.json`. VM Start initializes machine identity but does not install packages, replace guest executables or upgrade images. See the [image and capability contract](vm-platform.md#image-and-capability-contract) for compatibility checks.

## Linux privilege boundary

Provision Firecracker as an administrator-managed system service on the same Linux computer where AXV runs, using the [host example](../config/examples/host-firecracker.json), [systemd unit](../config/examples/axv-host.service) and [guest image recipe](../images/guest). Replace digest placeholders and choose local paths, capacity, subnet and jail UID/GID ranges; the example cannot launch unchanged. Automatic Linux provisioning is not implemented.

The host requires KVM, TUN, cgroup v2 with `cgroup.kill` and delegated `cpu`, `memory` and `pids` controllers, pinned Firecracker/jailer binaries, `ip`, `nft`, `cp`, `mkfs.ext4` and `resize2fs`. Enable IPv4 forwarding separately and choose a short host data path and non-overlapping guest subnet. Existing host firewalls must also authorize guest forwarding; [VM networking](vm-platform.md#host-firewall-integration) owns coexistence with Docker and other firewall managers.

Set `firecracker.cgroupParent` to the service's delegated cgroup path relative to `/sys/fs/cgroup`, for example `system.slice/axv-host.service`. The example unit requires systemd 254 or newer: `Delegate=yes` delegates that subtree, and `DelegateSubgroup=controller` places the host process in its own leaf so VM resource controllers can be enabled beside it. AXV configures only its owned descendants inside that delegation. It does not enable controllers in global ancestor cgroups. Keep the unit name and configured parent stable while its VMs exist; `KillMode=process` preserves guests across controller restarts. Alternative supervisors must provide the equivalent writable, empty delegated parent and place the controller outside that parent or in a separate leaf.

Start AXV with the intended desktop account on that computer, then open **Virtual Machines** to configure and check the service's local Unix socket or readable host configuration. A successful connection becomes available in the current desktop without a restart. Missing permissions or an unavailable service show diagnostics and a check-again action; AXV never starts or stops the administrator's system service. Running the desktop as root does not change Firecracker's independent service ownership or enable managed Firecracker hosting.

The equivalent CLI configuration is:

```bash
axv setup --socket /run/axv-host/control.sock
```

Alternatively, `setup --host-config /etc/axv/host.json` reads a host configuration and saves its local Unix socket. The desktop needs existing socket access.

## Configuration and storage

A profile can combine desktop, agent and compute settings:

```json
{
  "runtime_version": 1,
  "workspace_dir": "/absolute/host-projects",
  "proxy_url": "http://127.0.0.1:7890",
  "desktop": {
    "addr": "127.0.0.1:3001",
    "data_dir": "/absolute/desktop-data"
  },
  "compute": {
    "mode": "managed",
    "host_config": "/absolute/axv-profile/host.json"
  }
}
```

Omitted desktop fields use defaults. Compute modes are `disabled`, `managed` with an absolute Lima `host_config`, or `external` with an absolute local Unix `socket`. The internal name `external` means that another supervisor owns the VM service process on this same computer. Setup preserves unrelated and unknown profile keys. The [profile reader](../apps/server/internal/profile/read.go) validates known fields; malformed JSON and unsupported runtime versions fail without resetting files.

The shared [desktop resolver](../apps/server/internal/config/config.go) applies startup overrides and resolves desktop storage, workspace, proxy and tmux settings from that profile. It validates requested settings before an initial profile is published and rereads the published profile so concurrent initialization preserves the winning configuration. [VM command configuration](../apps/server/internal/app/compute_settings.go) remains independent of desktop and agent settings.

| Setting | Precedence, highest first |
| --- | --- |
| Listen address | `--addr` → `ADDR` → `127.0.0.1:PORT` → `desktop.addr` → `127.0.0.1:3001` |
| Desktop data | `--data-dir` → `DATA_DIR` → `desktop.data_dir` → `AXV_HOME/data` |
| Compute connection | `AXV_COMPUTE_SOCKET` for an independently managed local VM service → profile `compute` |

Empty environment variables are unset; explicit flag values cannot be empty. Start/restart overrides are not saved to the profile. Repeat them on the next invocation or persist the settings explicitly. The environment workflow can configure and activate a previously unconfigured VM service without restarting the desktop. Existing provider/storage replacement remains an explicit maintenance operation with the original ownership preserved. Desktop and agent changes can use restart. `AXV_COMPUTE_SOCKET` is shown as a read-only launch override; writing the profile does not replace it.

| Location under AXV_HOME | Content |
| --- | --- |
| `config.json` | Shared profile |
| `host.json` | Host configuration generated by macOS setup |
| `data/` | Desktop and installed-application SQLite databases, layouts, Codex run manifests and AXV session records |
| `workspace/` | Default real host project directory, created on first use |
| `images/` | Independent verified image copies used by this profile |
| `compute/`, `lima/` | Compute state and default private Lima resources |
| `tools/lima/` | Verified private Lima releases, when needed |
| `preparation/` | Preparation task database, plans, progress and private preparation files |
| `host/control.sock` | Managed host API socket |
| `ssh/` | Private profile SSH client key and pinned guest known-hosts file |
| `tailscale/` | Optional embedded Tailscale node credentials, certificates and owner binding |
| `run/` | Locks, readiness, ownership, immutable configuration snapshots and private Codex sockets |
| `bin/` | Fixed executable copies |
| `logs/daemon.log`, `logs/daemon.log.1`–`.5` | Private structured desktop/preparation log and bounded rotated archives |
| `logs/desktop.log`, `logs/host.log` | Native supervisor output, including structured host diagnostics |
| `dev/<checkout-id>/` | Independent development profiles; see [development](development.md#local-development) |

Desktop data defaults to `AXV_HOME/data` on every platform. Documents live in `data/desktop.sqlite`, separate from configuration and real project files. Previous development directory layouts are not detected, migrated or used as fallbacks. Startup rejects incompatible databases; the [baseline contract](storage.md#desktop-database-baseline) permits deleting obsolete AXV-owned development data after stopping its writers and affected Codex runs.

Installed applications use `extensions.sqlite` beside `desktop.sqlite` in the same resolved `--data-dir`/`DATA_DIR`/profile data directory; the default is `AXV_HOME/data/extensions.sqlite`. There is no additional startup switch or app-directory scan. The desktop opens this store at startup and rejects invalid or incompatible extension databases. Application installation, enabled state and retained data persist independently of the desktop document store; [extension storage](storage.md#installed-application-storage) owns its baseline and explicit reset boundary.

Daemon ownership records use format **4**, allowing separate host and optional desktop jobs; older formats are rejected without rewriting them. Database formats belong to the [desktop baseline](storage.md#desktop-database-baseline), [extension store](storage.md#installed-application-storage) and [compute/preparation owners](vm-platform.md#ownership). Do not remove ownership records while their processes remain live or edit format markers to bypass validation.

The unified root contains AXV-owned data, not copies of every dependency or project. Existing host projects, provider-owned authentication/history and the shared tmux server retain their own locations. Explicit desktop/VM storage overrides remain supported; independently administered Linux VM services retain their configured paths. Backups must include these external locations when used. Stop writers before taking a filesystem backup, including independent Codex runs when copying their records and SQLite sidecars.

Development preview registrations and request receipts end with the desktop service. Their random hostnames isolate browser origins while ephemeral ports are released on Stop; there is no persistent port-reservation store. [Preview access](api/project-previews.md#browser-and-authorization-boundaries) owns local origins and application-session boundaries. Offline project-volume copies cover only retained guest data, not this profile or provider-owned history.

The desktop operational log is `AXV_HOME/logs/daemon.log`. Foreground and native desktops, offline setup and cache mutations append JSON lines with timestamps, severity, process role, event names and available job/operation/request IDs. Preparation phases, throttled transfer progress and desktop connection failures share this profile sink. Service composition also routes standard Go log messages through the sanitizing logger. It does not record terminal/Codex streams, guest session output, document contents, request bodies or complete configuration/environment snapshots. Credential values and URL authentication, queries and fragments are redacted; individual diagnostics and records are bounded.

The log directory is private (`0700`) and log/lock files are private regular files (`0600`) on macOS and Linux. Writers to `daemon.log` share an interprocess lock; each append reopens the active file under that lock and rotates at 10 MiB, retaining at most five archives. Unsafe links, owners or permissions are rejected. Desktop startup, offline setup and cache mutations fail clearly when their sink cannot initialize; later write failures go to stderr and the desktop environment reports `loggingError`, without undoing successful database changes. The environment exposes the initialized desktop sink as `logPath`; the browser does not derive it.

All host processes write structured diagnostics to stderr. Native launchd/systemd supervision captures this output in `logs/host.log`; a foreground desktop forwards its managed host child's stderr to its own stderr. Independently administered hosts use their own supervisor's logging. Hosts do not infer a desktop log destination from `AXV_HOME` or append to `daemon.log`. Windows foreground desktops also use structured stderr logging and omit `logPath`; AXV does not claim Unix permission bits provide private Windows files.

`axv daemon logs` prints a bounded tail of `daemon.log` followed by available `desktop.log` and `host.log` tails, including output from existing services. These native role logs are not automatically rotated. Role logs, snapshots and executable copies are not automatically pruned. Stop services and preserve needed ownership/configuration records before maintaining profile files. Development and test launchers own independent profiles; never target a real profile in tests. For an isolated preview:

```bash
AXV_HOME="$PWD/.preview/profile" \
  ADDR=127.0.0.1:0 /absolute/path/to/axv serve
```

## Shared download cache

`AXV_CACHE_HOME` selects the download cache. When unset, AXV resolves the operating-system user's home directory and uses `~/.axv/cache`, independently of `AXV_HOME`, `AXV_DEV_HOME` and `AXV_TEST_TMPDIR`. An explicit absolute path (or `~/...`) uses only that location; there is no fallback to the default. Plans and CLI diagnostics show the effective cache root, verified hits and remaining downloads.

The cache owns `format.json`, an initialization lock and `artifacts/v1/`. Each complete payload is `artifacts/v1/sha256/<full-sha256>/artifact`, with source metadata beside it. Cross-process locks live in `artifacts/v1/locks/` and are never unlinked during maintenance. Directories must be real, current-user-owned and mode `0700`; completed payloads are regular immutable `0444` files. Unsupported formats, unsafe permissions, ownership conflicts and corrupt contents fail visibly without replacement.

The catalog includes pinned official Lima archives and Ubuntu base cloud images; future published AXV images use the same cache. User uploads, project files, VM disks and snapshots are not automatically cached. Image selection follows the [setup policy](#first-time-macos-setup).

```sh
axv cache list
axv cache verify --json
axv cache fetch ubuntu-arm64
axv cache import lima-arm64 /absolute/known-profile/preparation/downloads/lima-<sha256>.tar.gz
axv cache remove <full-sha256>
```

Use `lima-amd64` or `ubuntu-amd64` on Intel systems. Import accepts only a reviewed public artifact ID and verifies its fixed digest; it preserves the source. AXV does not scan user directories. `fetch` and `import` optionally accept `--output /absolute/profile-file` to create an independent copy. `list` and `verify` hash completed entries; busy entries report `in_use`. Explicit removal refuses active downloads, imports and profile copies, and handles only recognized cache-owned files. Corruption errors include the exact removal command; repair is explicit. There is no automatic eviction.

Each profile retains its own databases, images, VM root/project disks, Lima state, installed tools, processes, SSH keys and ownership. Extraction/copy happens under its original managed paths with no shared directory symlinks or cache/profile hard links. Removing a cached artifact does not affect an already prepared profile. Profile cleanup must preserve the cache: never recursively remove an entire `AXV_HOME`, especially the default `~/.axv`, which also contains the independently owned cache and development profiles. Stop and remove only the intended profile's owned resources.

## Download mirrors and guest package sources

These are independent selections. Neither changes the shared cache's digest key or a prepared VM's image contract.

**Artifact downloads:** `AXV_ARTIFACT_MIRRORS` is a JSON object mapping a reviewed artifact's **full lowercase SHA-256** to an **exact HTTPS download URL**. Load operator-verified locations with `export AXV_ARTIFACT_MIRRORS="$(cat /absolute/artifact-mirrors.json)"`. It applies to Web preparation, `axv setup`, `axv cache fetch`, development and explicit native acceptance. Unlisted digests use their catalog URL. AXV ships no guessed third-party artifact mirror addresses; package mirrors do not imply that the same site hosts cloud disks or Lima archives.

A matching override selects that mirror only, with **no automatic fallback**. Plans display selected and original locations; confirmation freezes the URL, architecture, version and digest. Transport, HTTP and checksum failures remain visible. Clear/change the override and review a new plan to change sources; retries keep the original plan. URLs must omit credentials, query strings and fragments. HTTPS, redirect count/scheme, size and checksum rules still apply. Warm entries are verified and reused regardless of download location. Partials bind to the exact source URL and digest; a source mismatch is rejected with an explicit removal path, never appended. Use `axv cache remove <sha256>` for the inactive partial before selecting a different source. Restart the desktop daemon to capture an environment change for new plans.

**Guest APT sources:** explicit selection supports only Ubuntu **24.04**, **arm64/amd64**. ARM64 uses Ubuntu Ports (`ports.ubuntu.com/ubuntu-ports` or `mirrors.aliyun.com/ubuntu-ports`); amd64 uses Ubuntu archive/security or `mirrors.aliyun.com/ubuntu`. All selections retain the Ubuntu archive keyring via `Signed-By`; they never disable signature verification. The [image recipes](../images/guest/README.md#package-source-selection) own builder options and dependency installation.

For an existing running Lima VM, including a basic Ubuntu VM without AXV guest software:

```sh
axv vm package-source <name-or-id> --source aliyun
axv vm package-source <name-or-id> --source official
```

This explicit durable maintenance operation changes only that VM's writable disk, requires its current revision, and locks against concurrent VM actions. It requires preinstalled Python 3. It backs up `sources.list`, `ubuntu.sources` and any existing AXV cloud-init source policy under `/var/lib/axv/package-sources-*`, writes deb822 configuration and removes the superseded active `sources.list`. The cloud-init policy preserves the selected sources across boots. Repeating an already applied selection makes no new backup; an interrupted operation can resume. Write failures attempt to restore the original files; backups remain available if recovery also fails. Other active `.list`/`.sources` layouts, symlinks, edited AXV cloud-init policies, unsupported releases/architectures, missing TLS trust roots and missing signing keyrings are rejected rather than rewritten. Source changes do **not** refresh package indexes, install packages or upgrade the system. No source operation occurs during Start. Runtime Firecracker source maintenance is unsupported; select sources in its explicit image build or administer the guest directly. Shared originals, other VMs and other profiles are untouched.

## Host agent defaults

`AXV_WORKSPACE_DIR` overrides profile `workspace_dir`, whose default is `AXV_HOME/workspace`. AXV creates this real host directory when New Session first needs it; virtual documents remain separate.

Existing proxy environment variables take precedence over `proxy_url`, then enabled macOS system proxies. The profile supports `http`, `https`, `socks5` and `socks5h`. Diagnostics show a source and redacted address, not verified model connectivity.

New runs use the default tmux server unless `AXV_TMUX_SOCKET` selects another. AXV passes the current environment to each new run and persists its actual routing. Later settings changes do not redirect old runs or change global tmux options/environment. [Codex architecture](codex.md#tmux-routing-and-environment) owns run routing details.

## Agent settings and background lifecycle

Foreground and background services resolve the same workspace, proxy and tmux settings. Background startup captures a scoped environment: PATH, home/user/shell/locale, Codex/OpenAI settings, proxies and certificate paths. The exact allowlist lives in [environment.go](../apps/server/internal/app/launch/environment.go); other shell variables are not copied, and empty captured values clear supervisor defaults.

The [`app/launch`](../apps/server/internal/app/launch) package owns captured desktop inputs, the persisted `Snapshot` contract and an explicit `Inputs` fingerprint. The runtime `app.Settings` composition adds live compute settings, logging and process dependencies without defining the snapshot protocol or desktop identity. Capture and restoration clone mutable snapshot values so later runtime changes cannot alter saved inputs. Restoring a background desktop uses its captured inputs and environment; later VM activation and the remote-access controller resolve their current profile settings without changing the desktop's launch identity. Remote settings are mutable profile state and are excluded from captured desktop inputs and their fingerprint.

Native service snapshots also carry a launch identity, distinct from the process instance identity, so concurrent launchers can recognize their own job without adopting a foreground desktop. The shared profile owns immutable executable copies under `AXV_HOME/bin`; desktop composition captures one for later host and Codex children. Replacing the installed entry therefore cannot change an existing desktop's child executable. The [launch contract](../apps/server/internal/app/launch/snapshot.go) and [settings composition](../apps/server/internal/app/settings.go) are the implementation entry points.

Snapshots and ownership records are private (0600) and may contain supplied environment credentials. AXV does not read or copy Codex credential files or put snapshot values in supervisor command arguments/status output.

Editing a profile or shell environment alone does not mutate a live process. The local Tailscale panel applies and persists remote enable/disable without a desktop restart. `daemon restart` captures updated desktop settings/environment for new sessions while preserving the compute host and existing Codex runs. On Linux, desktop units deliberately retain surviving tmux processes; a prior unit may remain visible until those processes exit. See [daemon supervision](../apps/server/internal/daemon/native.go) and [native daemon verification](development.md#native-daemon-smoke) when changing this behavior.
