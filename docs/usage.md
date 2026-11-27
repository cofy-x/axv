# Using AXV

This guide owns visible user workflows, confirmations and persistence consequences. See [project status](../README.md#project-status) for platform support, [startup](startup.md) for installation and CLI commands, and [remote access](remote-access.md) for Tailscale/Cloudflare setup. Switching desktop workspaces changes visible windows without moving host or VM execution.

## Desktop

Run `axv` to start or reopen the workstation; background services survive closing the launching shell ([daily commands](startup.md#daily-commands)).

- **Workspaces & windows:** Switch across four workspaces (`Alt+1–4` or top-bar buttons), rename workspaces or save workspace shortcuts under **Workspace → Workspaces**, and use **Window Overview** to jump to any open or minimized window. Folding, minimizing and switching workspaces preserve drafts, selections and scroll positions.
- **Dock & Preferences:** A fresh layout pins **Codex**, **Virtual Machines**, **Project Files** and **Terminal**. Reorder or pin apps from the Dock menu. **Preferences** groups Wallpaper, Desktop grid/clock, Remote access and Notifications on one page; **Welcome → More** provides **Application Manager**, **Browser installation guide**, **Preferences**, **Component Gallery** and **About AXV**.

| Shortcut | Action |
| --- | --- |
| `Ctrl+F6` | Cycle visible windows on the current workspace |
| `Alt+1–4` | Switch desktop workspace |
| `F10` | Open the Workspace menu |
| `Shift+F10` / Context Menu on Dock tile | Open that application's Dock menu |
| Arrow keys, `Enter`, `Escape` | Navigate, select and dismiss menus |

### Installed window and updates

Follow [browser installation](startup.md#browser-installation) to open AXV in a standalone window. Browser tabs and installed windows may have separate browser recovery storage ([browser recovery](storage.md#browser-recovery)).

- **Updates:** An open desktop never auto-reloads during an update. If a lazy app fails to load, use **Retry**, or export/save unsaved drafts (`.draft.txt` in VM Project Files, **Export draft** in extensions) before reloading manually.
- **Auth / database changes:** Restore expired authorization and retry without losing drafts. If the desktop database changes, keep the old page open to recover unsaved work and open the replacement database separately.

## Applications and extensions

Open **Application Manager** from **Workspace → Applications** or **Welcome → More**. Built-ins keep their own launchers; installed extensions run in sandboxed HTML frames with private JSON storage only (no host files, commands, agents or VMs — see [Extensions API](api/extensions.md)).

- **Install & update (local host only):** Use **Add package** to install the bundled Checklist example or a reviewed JSON extension package (replacing an existing ID keeps its saved data and enable state). Authenticated remote owners can run installed extensions and use their private data.
- **Drafts, reload & conflicts:** Saving writes an explicit snapshot while newer typing remains dirty. Upgrading, disabling or uninstalling suspends the old frame without discarding your draft; **Reload** reconnects to the updated installation after choosing whether to keep or discard unsaved text. Saving over a newer server revision reports a conflict.
- **Lifecycle:** **Show on desktop** controls per-browser launcher visibility independently of Dock pinning and enable state. **Uninstall** retains saved data for export or reinstall; **Delete data** is a separate irreversible confirmation available after uninstalling.

## GitHub Review

Open **GitHub Review** on the local AXV desktop (requires host `git`, `gh` and Codex CLI).

- **Setup & schedule:** Connect host GitHub and Codex logins in **Settings**, add watched `owner/repository` entries, configure optional per-repo review rules, quick-test profiles and comparison runtimes, and enable automatic polling (1–1,440 min). The AXV daemon continues scanning while the browser is closed.
- **Review budget & scope:** Daily Codex review limit defaults to 10 (1–1,000, counted when Codex starts). Direct review requests outside watched repos are included by default unless watch-only mode is enabled. Use **Review a PR** to queue a specific PR link immediately even while polling is paused.
- **Evidence & VM tests:** Each PR review keeps pinned commits, captured author context, Codex findings, upstream GitHub Checks (with observed checkout provenance when available) and local VM quick-test results together. Retrying VM tests or refreshing checks consumes no Codex review quota. Provision dedicated test VMs with the [GitHub Review VM recipe](../recipes/github-review/README.md).
- **Candidate verification & drafts:** Reviewers assess candidates locally, write anchored Markdown comment drafts (never auto-posted to GitHub), run two-leg **Compare revisions** assertions on the diff merge base and PR head in the same VM, and optionally approve a reproduced assertion as a reusable automatic verification scenario. New PR commits mark earlier reviews superseded while retaining their history and drafts. See [GitHub Review API](api/github-review.md).

## Remote desktop

Open **Tailscale** from **Workspace → Applications**, **Preferences → Remote access** or its desktop icon.

1. On the AXV host, choose **Enable** (persisted across launches).
2. Complete **Authorize** with the workstation owner's Tailscale account and enable MagicDNS + HTTPS certificates in Tailscale DNS if prompted.
3. Open the reported `.ts.net` HTTPS URL from another device signed into the same Tailscale owner account. [Remote setup](remote-access.md#tailscale) owns policy and CLI details.

**Disable** immediately disconnects remote browsers (ending their Host Terminal shells and detaching Codex views while independent Codex runs and VMs continue) and keeps credentials for re-enabling. Local and remote origins have separate browser layouts and local draft storage — save work before switching devices. VM preparation and remote setup stay local-only.

### Cloudflare desktop access

Open **Cloudflare** from **Workspace → Applications**, **Preferences → Remote access** or its desktop icon.

- **Setup (local only):** Configure desktop Access settings and optional Cloudflare account publishing in **Setup** ([Cloudflare setup](remote-access.md#cloudflare-access)). **Listening locally** confirms AXV's loopback origin only — verify public HTTPS reachability separately.
- **Lifecycle:** **Disable** closes only Cloudflare desktop clients while local/Tailscale clients, Codex runs and VMs continue. Owner JWTs expire after at most 15 minutes without renewal on reconnect; sign in again in another tab and retry without losing drafts ([connection lifetime](api/cloudflare.md#connection-lifetime)).
- **Service publishing:** Publish running VM project HTTP ports under **Publishing** with **Owner** or **Public** visibility on an exclusive cookie site (permanently reserved even after removal). **Unpublish** withdraws access and cleans DNS/ingress without stopping the VM service; after desktop restart, retained publications stay closed until explicitly **Resumed**.

## Work notifications

Open **Notifications** from **Workspace → Applications** (or **Preferences → Notifications** for local Pocket Alert settings). Both browser Web Push and Pocket Alert are opt-in and disabled by default; closing the browser does not stop desktop dispatch.

- **Pocket Alert (local config):** Enter optional Application/Device IDs, write-only API token, reachable AXV **Return-link origin** and event types, then save and **Test**. Changing credentials, destination or enabled state cancels queued sends under the prior scope without recalling already accepted alerts.
- **History & return links:** History distinguishes `Pending / Sending`, `Accepted` (provider accepted, not guaranteed device display), `Delivery uncertain` (bounded retries up to 3 attempts), `Delivery failed` and `Disabled / Superseded`. Clicking a return link opens read-only inspection of the exact recorded Codex run or VM task attempt after normal workstation authentication — it never approves requests, cancels tasks or replaces open drafts ([Notifications API](api/notifications.md)).

### Browser push notifications

In **Notifications → This browser** at your stable HTTPS AXV origin (iPhone/iPad require an installed Home Screen app), review the vendor-visible **Push service contact**, select event types and click **Subscribe** explicitly. Opening AXV never prompts for permission or creates a subscription.

- Browser permission, browser subscription and server delivery are separate states: **Disable server delivery** pauses sends without unsubscribing; **Unsubscribe this browser** removes both browser and server records.
- Alerts carry encrypted opaque event IDs (no prompts, commands or filenames), so vendor push can arrive while Tailscale is disconnected; opening the linked work still requires authenticated connectivity to AXV. Clicking focuses an existing AXV window without discarding dirty editors.

## Project Files

**Project Files** browses files in either a registered **Host** directory (read-only + download up to 10 MiB) or a **VM** project on a retained volume (read/edit + native VM Terminal + Tasks + Previews + Backups + VM Inspector).

- **Host projects:** Register an absolute directory (tracked by filesystem identity; a replaced directory requires explicit new registration). Text reads support up to 1 MiB (rich Markdown preview up to 64 KiB).
- **VM projects & initialization:** Open an existing directory on a ready retained volume of a running enhanced VM, or choose **Create Project / Clone Repository** to run a persistent initialization task inside the volume workspace, then explicitly **Open Project** once finished. Failed clones preserve partial directories without overwriting existing files.
- **Editing & external changes:** VM files start read-only; choose **Edit** to modify and **Save** (`Ctrl/Cmd+S`) against the reviewed SHA-256 and original VM binding. Newer typing stays dirty during saves. Visible clean files auto-update on external VM changes; dirty files keep your draft and warn on conflict. Unsaved drafts are preserved in tab-local recovery across reloads (closing the tab drops session storage — save or download `.draft.txt` first). Moving a volume or restarting the guest requires explicit **Reconnect**.
- **Previews & Backups:** **Previews** exposes an already running VM HTTP port on a fresh local-only `.localhost` origin (**Stop** revokes forwarding without killing the process; desktop restart requires re-registering). **Backups** creates immutable offline copies of a stopped VM's retained volume, restores into a new volume, and binds a separate project ID after verifying root and Git/worktree paths.
- **Tasks:** **Tasks** opens an independent window to submit persistent shell commands (`/bin/sh -lc`) with explicit CPU/memory/process/timeout/log limits. Accepted tasks outlive browser close and desktop restart; **Cancel** stops only the displayed attempt, **Retry** creates a new attempt after final status (never while `unknown`), and **Recover Submission** checks the original request receipt after a lost response.

### GitHub project review

In a VM project's **GitHub** tab (requires image and guest declaring `project-github`), read local Git metadata, review the canonical repository and signed-in guest account, and explicitly associate the repository.

Configure a dedicated guest `gh` profile in Guest Terminal (`~/.local/bin/gh`, see [GitHub HTTPS](#github-https)):

```sh
umask 077
mkdir -p "$HOME/.config/axv/github"
chmod 700 "$HOME/.config/axv/github"
GH_CONFIG_DIR="$HOME/.config/axv/github" "$HOME/.local/bin/gh" auth login --hostname github.com --git-protocol https --web --insecure-storage
chmod 600 "$HOME/.config/axv/github/hosts.yml"
```

Credentials stay on the guest root disk (`0600`) and require explicit review after VM replacement. **Refresh GitHub** is on-demand and read-only (no polling, pushing or merging); **Local HEAD**, **PR head** and **Merge test checks** are displayed with their exact SHAs.

### Credentials for project tasks

Project tasks have no secret-injection field — AXV persists submitted commands, recovery payloads and task logs:

- Never pass tokens, passwords, secret URLs or `export SECRET=...` in task commands, documents or diagnostics; avoid shell tracing (`set -x`) and environment dumps.
- Configure credentials on the **VM root disk** under the guest service user's private directory (`0700` dir, `0600` files, outside retained project volumes and Git repos) so root-disk secrets stay out of [project-volume backups](startup.md#vm-project-backups). Reauthenticate explicitly after VM replacement.
- Tasks do not inherit interactive Guest Terminal shell variables: invoke an absolute-path guest wrapper/CLI that reads its private guest config at runtime.
- All processes in a VM share the guest `axv` account trust domain — use separate VMs for distinct trust domains.
- Optional dev recipes: [Resend email](../recipes/resend/README.md), [Upstash Redis](../recipes/upstash/README.md); upstream secret CLIs such as Doppler or Infisical remain user-managed inside the guest.

### Private repository authentication

Cloning private repositories runs noninteractively as the guest `axv` user independently of Guest Terminal's shell environment or host Git credentials.

#### GitHub HTTPS

Install a checksum-verified official `gh` binary under `/home/axv/.local/bin/gh` (never on the retained project volume), then run in Guest Terminal:

```bash
umask 077
mkdir -p /home/axv/.config/gh
chmod 700 /home/axv/.config/gh
/home/axv/.local/bin/gh auth login --hostname github.com --git-protocol https --web
/home/axv/.local/bin/gh auth setup-git --hostname github.com
/home/axv/.local/bin/gh auth status --hostname github.com
```

Keep `/home/axv/.config/gh/hosts.yml` at mode `0600` on the VM root disk. Revoke tokens in GitHub account settings when removing access (`gh auth logout` only clears local state).

#### SSH

Create a dedicated guest-owned key and verified host-key entries under `/home/axv/.ssh` (`0700` dir, `0600` key/config with explicit `IdentityFile` and `IdentitiesOnly yes`, passphrase-free or available to the noninteractive guest service; host SSH agent forwarding is disabled). Verify host fingerprints against the provider's published fingerprints before trusting.

#### Clone and recover

Submit the clean HTTPS/SSH URL in **Clone Repository**, wait for task completion, then choose **Open Project**. After an uncertain response use **Recover**; after a failed clone, inspect the preserved partial directory, fix guest auth and start a new clone in an unused directory with a new request.

## Virtual Machines

**Virtual Machines** manages VMs on the deployment machine ([VM architecture](vm-platform.md)).

- **Connect the VM host:** On macOS, review storage/limits/downloads in **VM Host** before starting preparation (runs in background; cancelable before config commit). On Linux, choose **Connect** for a detected `axv-host.service`, or copy and run the displayed `sudo ... host install-service` command in Host Terminal ([Linux setup](startup.md#linux-privilege-boundary)).
- **Create & manage VMs:** Choose **New VM**, set a unique name, VM type (**Development** retains project volumes on delete; **Task** expires by TTL and defaults to disposable storage) and optional resource limits under **Advanced**. VM deletion removes only the root disk; retained volumes survive independently.
- **Volumes, Images & Activity:** Manage detached/retained volumes (**Attach** requires a stopped VM; **Delete** is permanent), inspect image compatibility/checksums, and track durable operations in **Activity** (acceptance is not completion).

### Guest Terminal

On a ready VM, **Open Terminal** resumes a sole live session, opens Inspector's session list if multiple exist, or starts a new Bash shell. Closing the window detaches without killing the shell; **More → End Session** terminates its process group. VM shutdown or guest-service restart ends live guest shells. Use [`axv vm ssh`](startup.md#ssh-file-transfer-and-forwarding) for standard OpenSSH/SFTP/port forwarding.

## Host terminal

**Terminal** opens an ephemeral shell in the service user's home (or a Codex run's recorded working directory). Closing a live window, disconnecting or stopping AXV ends the shell; **New Session** and reloaded windows start fresh shells.

### Terminal clipboard

**Paste** reads the browser clipboard directly when allowed; otherwise **Paste text** opens a manual editable dialog targeting the original terminal connection. Disconnecting invalidates pending paste dialogs. Line breaks and control characters can execute shell commands.

## Codex

AXV runs the official Codex CLI as the service user via persistent tmux ([supported versions](api/host-codex.md#availability-and-environment)); sign in and trust projects in Host Terminal first. Check **Sessions → Environment** for readiness and proxy diagnostics.

- **Start work:** Start in the default host workspace or choose another host directory via **New in Folder**. For VM projects, open a native VM **Terminal** directly from **Project Files** or **Virtual Machines**.
- **Session lifecycle:** Closing a window, reloading or stopping AXV **detaches** while tmux keeps the run alive. **End Session** terminates only that exact run; **Continue** explicitly starts a new run from verified saved provider history (never silently replacing missing history or retargeting an old window). On uncertain start/continue responses, use **Resolve Start / Resolve Continue** before starting again.
- **Activity & organization:** Rename or archive sessions without affecting active runs. **Action needed** surfaces pending approvals/input (answered in the official Codex TUI) and undismissed failed/interrupted alerts (**Dismiss alert** clears that exact event). Normal completion requires no acknowledgment. See [Codex architecture](codex.md).
