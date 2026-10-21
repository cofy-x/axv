# Using AXV

This guide owns visible actions and their effects. Start with [project status](../README.md#project-status) for available capabilities and [startup](startup.md) for installation and service configuration. Desktop documents, host projects and VM projects are separate stores; switching desktop workspaces does not change where commands run.

## Desktop

Run the installed `axv` command to start or reopen the workstation in your browser. Closing that launching terminal does not stop its background services. Repeating the command opens the existing instance with its current settings; explicit restart applies changed settings or a newly installed executable. [Startup](startup.md#daily-commands) owns the service commands and their lifetime effects.

The **Dock** launches applications, lists their windows and supports pinning and ordering. Swipe an overflowing Dock to reach its remaining launchers and Add control. Mouse dragging changes application order; **Move Earlier** and **Move Later** in each application's Dock options also work with touch and keyboard. **Clip** switches between four named desktop workspaces and holds their shortcuts. Minimizing a window creates its own miniwindow icon; restoring it preserves the window's content. Window menus move windows between workspaces or show them on all workspaces.

Drag a title bar to move a window and its edges to resize. Double-click the title to fold it. Window menus also offer minimize, maximize, restore and close. Narrow screens fit the active window automatically. Drafts, selections and scrolling survive folding, minimizing and workspace changes.

Menus can be fixed as movable panels. A fixed application menu keeps its original window as its target and closes with that window. **Inspector** follows selection or stays pinned to an object. Both remain non-modal; confirmation dialogs own focus while open.

| Shortcut                  | Action                             |
| ------------------------- | ---------------------------------- |
| Ctrl+F6                   | Switch windows                     |
| Alt+1–4                   | Switch desktop workspace           |
| F10                       | Open the workspace menu            |
| Arrow keys, Enter, Escape | Navigate, select and dismiss menus |

Touch controls expose menus and window actions without requiring a right-click. Text inputs retain selection, clipboard and undo shortcuts. **Preferences** changes desktop appearance and clock settings and opens **Tailscale**; **Component Gallery** demonstrates the shared controls.

Welcome keeps **Show Desktop** visible and groups **Preferences** and **About AXV** under **More**.

## Applications and extensions

Open **Application Manager** from its desktop icon, Welcome or **Applications → Application Manager** in the workspace menu. Built-in applications keep their existing launchers and windows. The manager is available without a default Dock pin; use the Dock Add menu to pin it if desired.

The manager starts with installed extensions. **Open** launches an enabled application; **Enable** makes a disabled application available again. Each extension row's **More** menu contains secondary management actions. **Add package** opens package selection and review; this section opens initially for an empty directory. Collapsing it keeps the current review.

Use **Show on desktop** beside an application to show or hide its launcher in this browser's layout. New extensions start without a desktop icon. This choice is independent of Dock pinning and installation access, does not open a window and keeps the launcher's position when hidden. Uninstalling retains the choice; reinstalling can restore the icon. Workspace, Documents, Recycler, files and individual VM icons keep their existing desktop behavior.

Under **Add package**, choose **Install Checklist** to review the included example, then **Install**. **Open** launches its window. Add, edit or check items and choose **Save** to write that snapshot. Typing while saving stays unsaved. Closing a modified application or changing its installation offers Save, Cancel and Discard; a failed save retains the draft. **Export draft** in the application window downloads the current recovery copy independently of server connectivity.

Checklist rejects a change that exceeds the [application-data limit](api/extensions.md#application-data), shows an explanation and keeps the previous accepted text. Shorten or remove another item, then retry. A rejected **Add** leaves the text in **New item**.

If an application submits a draft that AXV cannot accept, the host window stays unsaved and blocks Save and changes that would lose it. Keep the window open to correct the draft, use **Export draft** when it can be represented as JSON, or explicitly discard it. The rejected content is held only in memory and is not recoverable after a browser reload.

To install your own extension, open **Add package**, select a self-contained JSON **Extension package**, review its identity, version and private-data access, then install. **Download example** provides a complete editable Checklist package. Selecting a package for an existing application offers **Replace package**, preserving its saved data and current enable setting. Installation changes require access on the AXV host. The authenticated remote desktop can run installed applications and use their private data.

**Reload** explicitly reconnects a window to the current installed version. With unsaved data, choose **Keep draft**, **Discard draft** or Cancel; keeping a draft retains its original saved revision, including after a session expires. A recovered draft offers **Restore draft** or **Discard draft** before the application opens. Restore does not overwrite a newer server document automatically. In Checklist, **Reload saved** asks before replacing dirty text; export a draft first when you need to keep both versions. Saving after another window changed the same data reports a revision conflict.

Use **More → Disable** to stop further access from the old runtime while retaining windows, drafts and saved data. **Enable**, then **Reload**, reconnects that window. **More → Uninstall** keeps saved data available under **Uninstalled applications** for export or later reinstall. **Export saved data** downloads the server snapshot; it does not include unsaved drafts in application windows. It is in **More** for installed applications and directly available for uninstalled ones. **More → Delete data** is a separate, irreversible confirmation available after uninstalling. A temporarily missing application also retains its window, Dock pin and Clip shortcuts rather than removing them during layout recovery.

Extensions in this version store only their own data. They cannot access AXV documents, registered projects, commands, terminals, agents or VMs. Package format and limits belong to the [Extensions API](api/extensions.md); authoring and the message bridge belong to [desktop architecture](desktop.md#installed-extensions).

## Remote desktop

Open **Tailscale** from its desktop icon, **Workspace → Applications** or **Preferences**. The stable desktop launcher opens one non-modal panel and can also be kept in the Dock or added to Clip. Its icon does not indicate connection readiness; the panel checks AXV's connection while active. AXV includes Tailscale and needs no system installation on its server.

On the computer running AXV:

1. Choose **Enable**. The setting is saved for this AXV service and future launches; the desktop stays open.
2. When **Sign in** appears, choose **Authorize**, use the workstation owner's Tailscale account and return to AXV.
3. If **HTTPS required** appears, follow **Tailscale DNS settings** to enable MagicDNS and HTTPS certificates. Certificate issuance publishes the node's name in public certificate transparency logs; access remains private. AXV continues automatically once settings are available.
4. When **Ready**, copy the **HTTPS URL**. Expand **Connect another device** for the official client download, connect that device with the same Tailscale account and open the URL. [Remote setup](startup.md#remote-access) owns account, policy and credential requirements.

**Disable** stops AXV's remote entrypoint immediately and saves the preference, retaining authorization for later use. When ready, its confirmation explains that remote browsers disconnect and their Host Terminal shells end; Codex views detach while Codex runs and VMs continue. Cancel leaves access enabled. Local terminals and other work stay open. Closing the panel or browser does not disable the service. An enabled but stopped/errored node offers **Retry**. Save failures and diagnostics remain visible; **Refresh** checks again without changing the setting. AXV does not manage the system Tailscale service or its networking.

Remote browsers receive a read-only connection summary. Enable, Disable, Retry and enrollment are available only on the AXV host. The remote desktop operates on the same host documents, projects, agents and VMs. Host directory paths and commands still refer to that host; changing the browser device does not move execution.

Each browser origin/client keeps its own layout and unsaved recovery copies. The local HTTP and remote HTTPS addresses are different origins: explicitly save documents before switching devices or addresses. Shared saved documents remain available, and existing revision-conflict checks still apply. Host Terminal reconnect starts a new shell; Codex and Guest Terminal retain their distinct [session lifetimes](architecture.md#codex-attachment-and-process-lifetime).

VM environment preparation and remote setup require access on the AXV host. Remote errors stay visible; retrying an interrupted operation must preserve its original identity rather than submit replacement work.

## Files, saving and recovery

**Workspace / File Manager** manages AXV's virtual disk. Selecting a directory opens the next column; selecting a file updates Inspector. Enter or double-click opens a file, and **Open** opens or activates the selected directory's window. Rename and move preserve file identity. Import and download transfer files to and from the user's computer; supported [formats and limits](api/files.md#import-download-and-preview) are separate from host-project access.

**New** groups folder and text-file creation. The selection toolbar keeps **Open** and **Inspect** visible; **More** contains secondary file commands. Inspector keeps **Open** visible and groups rename, move, download, document details and recycling under **More**. **Restore** stays visible for a deleted item. Inspector keeps location, size and access visible, with dates and revision under **Details**. Toggle **Pin** to keep the inspected item or follow the Files selection.

Initial sample documents are user files: upgrades do not rewrite them or recreate deleted samples.

**Text Editor** saves with **Save** or **Ctrl/Command+S**. Saving captures the text at that moment; typing during a save remains unsaved. Closing a modified document offers Save, Cancel and Discard. A failed save keeps the draft. If the document is no longer available, **Save Copy** is offered beside the message. A conflict offers **Load Saved** or **Save Copy**; loading the saved version discards the window's draft, so save a copy first to keep both.

Browser recovery offers unsaved drafts when reopening a document. It is separate from server storage and scoped to the original database and browser client. Saving clears only the acknowledged content; newer typing remains recoverable. Discard removes that draft's recovery copy. Clearing site data removes local copies, and abrupt shutdown may lose typing newer than the last local write. Resolve reported storage errors before relying on recovery.

Files ending in `.md` or `.markdown` offer **Edit** and **Preview** in Text Editor. Preview follows current editor text without saving it. Inspector previews saved Markdown, with its normal text-length limit. Rich Markdown preview supports up to 64 KiB; larger content is shown intact as source to keep the desktop responsive. Previews support tables, task lists and fenced code; HTML is disabled, images are shown as placeholders and relative links are inert. Markdown rendering uses the MIT-licensed [react-markdown](https://github.com/remarkjs/react-markdown) and [remark-gfm](https://github.com/remarkjs/remark-gfm) packages.

**Recycler** keeps **Restore** visible, with **Delete** and **Empty Recycler** under **More**. Restoring a deleted folder includes the items deleted with it; earlier independent deletions remain in Recycler. A missing parent falls back to Documents, with numbered names for conflicts. Permanent deletion and emptying require confirmation.

### Saved versions and project files

Text Editor's **More** menu offers **Attach**, **Export** and **Details** for its saved document, alongside **Save Copy** and **Reload**. These three document actions share a tabbed panel that preserves your selections when switching tabs. **Save** and Markdown's **Edit / Preview** stay in the toolbar. Select the current saved revision or a retained version, and expand **Preview** to review its content. Attachments and exports exclude unsaved editor text. Adding or exporting captures the selected saved revision automatically; **Keep version** in Details retains it without sending or exporting. Refresh preserves the version already reviewed. If the saved document changed, explicitly choose its updated version before trying to capture it. Retained versions keep their original name and content after later edits or source deletion. Details also organizes links to projects, VMs and conversations; links do not send content.

**Project Files** browses original files in an explicitly selected host directory or a VM project. For a host project, use **Choose Folder** or enter an absolute path, then **Open** to register it. Registration is independent of recent Codex projects and retains directory identity: an unavailable or replaced root fails visibly. Explicitly opening a replacement directory at the same path registers a new project; existing document links and import origins retain the original project identity. Narrow windows show either the directory or its selected file; **Back** restores the selection and keyboard focus. Text reads support up to 1 MiB, with the rich Markdown preview limit above; host Download supports regular files up to 10 MiB. Project paths stay inside the registered root.

For host projects, choose **Copy to Documents** after reading a project file to create an independent copy. Its original project, path and content digest remain available in document Details. **More** offers **Projects** and **Download**; a failed preview also keeps **Download** directly available. To copy in the other direction, choose **More → Export** in Text Editor, select a saved version, destination host project and new relative path, then **Export**. Parent directories must already exist. Export creates a new file, protects existing destinations and opens the result in Project Files. Transfers preserve their request identity after uncertain outcomes; retry the same action unchanged to recover its recorded result.

Project Files uses **Host / VM** tabs and preserves unfinished input when switching. In **VM**, select **Project volume**, an existing **Directory in volume** (blank selects the root) and an optional **Project name**, then choose **Open**. The volume must be retained, attached and ready, with its VM running an image that supports project files. **Refresh VMs** updates those choices; select an entry under **Projects** to reopen an existing persistent project. Registration neither clones a repository nor copies host files. Put code and Git/worktree metadata on the volume first using guest SSH. VM files have no document import/export in this phase.

The VM browser supports **New → New File / New Folder**, text editing and **Save**. Save captures the visible text with its original file hash and environment binding; later typing stays dirty. A conflict or failed write keeps the draft. **Refresh directory** refreshes names without replacing editor text/hash. **Back** returns focus to the selected file; reopening that file preserves its draft. **More → Projects** opens the launcher without closing the editor. **More → Reload** fetches the file again; **More → Reconnect** explicitly obtains a current VM binding. Failed reads offer **Retry** against the original file and binding; save and connection errors show the applicable **Reload** or **Reconnect** action directly. Both require a discard choice when unsaved text would be replaced. A stopped/replaced VM or restarted guest produces a visible error and never redirects a save to another environment or the host.

VM editor recovery is local to the browser tab and preserves the draft's original binding/hash. After reload, choose **Restore Draft** or **Discard Draft**; a stale recovered binding still fails on save until explicitly resolved. An unreadable or mismatched recovery record preserves its exact stored bytes and pauses editing, Save and creation until an explicit **Discard Recovery** confirmation; browsing remains available and Cancel keeps the record. Closing a dirty VM project window offers **Keep Draft**, **Discard** or **Cancel**. Closing the browser tab removes its recovery storage; save or copy the text first. Unresolved drafts and recovery records request a browser warning before leaving the page. **Details** shows the project, host, volume, root, VM, boot and guest-service identities. Keeping a volume and reopening the same project after VM replacement preserves files and Git state when the repository metadata and paths remain on that volume; backup restoration remains separate work.

Choose **Tasks** in a VM project to open its independent task window without replacing the file editor's draft. **New Task** opens or hides the command form while preserving its input; accepting a submission or selecting history opens the result view. Review the VM target beside the command, enter a **Shell command**, then choose **Run**. The command runs through `/bin/sh -lc` inside that captured VM environment; save file edits first. **Execution options** contains the optional **Directory in project**, timeout, CPU, memory, process and log limits, with the full binding in its **Execution environment** section. Defaults request 900 seconds, one CPU, 512 MiB memory, 64 processes and 1 MiB of combined output. Unsupported guest enforcement rejects execution; exceeding the log limit stops that attempt and retains incomplete output. Per-task disk, I/O and network controls are unavailable; VM-wide limits are separate.

Accepted tasks remain in project history when the window or browser closes. Select a task to inspect its status, original VM and output; the attempt selector appears when there is more than one attempt. If a selected attempt is missing from recorded history, choose a recorded attempt explicitly; the window does not substitute another execution. History shows submission times and short task IDs to distinguish repeated commands. On narrow screens, the toolbar's **Back to Tasks** button returns from the command form or result to history. Output is plain text, displayed in bounded 128 KiB pages with up to three extra bytes to preserve whole UTF-8 characters. **First**, **Previous** and **Next** appear when paging is needed. **Latest** appears for unfinished work or paged output and follows new cached output until another page is chosen or **Latest** is turned off. **Execution details** includes the displayed byte range and full timestamps. Incomplete output, failed refreshes and recovery actions remain visible beside retained results; ordinary action feedback appears in the status bar. A displayed VM target records its binding rather than asserting that its VM remains reachable. **Cancel** confirms cancellation of the displayed attempt in its original VM without affecting another task or later attempt. **Retry** confirms a new attempt against the explicitly connected environment after the current attempt finishes. Unknown execution cannot be retried until the original environment is reconciled or confirmed interrupted; reconnecting never silently restarts it.

If submission loses its response, **Recover Submission** queries the original receipt and, if it was not accepted, resubmits the same request and binding. This tab retains the unconfirmed payload across reload; it does not select a replacement VM. **Dismiss Recovery** only removes that recovery prompt, preserving its retry identity and leaving accepted work untouched. Review history before changing the command or VM. **More task actions** contains **Project Files**, **Reconnect VM** and **Task Help**. Reconnecting explicitly captures the current environment for subsequent submissions; historical attempts retain their original bindings. A failed connection also exposes **Reconnect VM** beside its error.

### Document attachments

To give the current Codex run a document, open **Attachments**, choose **Add** beside **Documents**, select a document and saved version, then **Attach**. Selection and preview stay in that panel, bound to its exact run. **Open Codex** in the header returns to that run. Alternatively, **More → Attach** in Text Editor lets you explicitly select a ready running conversation. Submit a message in the terminal to deliver queued attachments.

The panel keeps pending, failed and unconfirmed deliveries visible, with completed and cancelled receipts under **History**. A receipt becomes Delivered only after Codex acknowledges its turn. Unconfirmed delivery never retries automatically; closing a view does not move attachments, and an ended run fails its pending queue. Continuing a conversation requires selecting attachments again. Receipt snapshot buttons remain usable after the source document is removed. The [Codex API](api/host-codex.md#document-context) owns queue limits and older-runner behavior.

## Virtual Machines

VMs run on the machine where AXV is deployed. Open **Virtual Machines** to see its environment state and inventory. The desktop stays available while it checks dependencies and connects to the local VM service. A configured managed service starts in the background; stopped VMs still require an explicit start.

For a new macOS environment, review storage locations and allocation budgets, then review the dependency installation and image downloads before starting preparation. The window shows each step and real download progress. Closing or refreshing it does not cancel the work; reopening returns to the same task. Failed or interrupted preparation provides a retry action that retains verified downloads. Cancellation is available before configuration is committed. Successful preparation makes VM controls available immediately, without restarting AXV.

Linux uses an administrator-provisioned local Firecracker service. The environment form checks and connects to its Unix socket or readable host configuration without installing privileged components. **Environment** shows connection and capacity, with provider and path diagnostics in expandable details. [Startup](startup.md) owns platform requirements, configuration paths and CLI alternatives.

Choose **New VM** once an available provider has a ready image. The initial name is `my-vm`; choose another when it is already in use. New machines start by default; choose to create a stopped machine when needed. Names must be unique on this host, including pending renames. The manager displays VM icons with names and states. Select one for Start/Stop, Terminal and Inspect; **More** contains secondary lifecycle actions. **Inspect** opens configuration and guest access. Search and state filters only allow actions on visible selections. The manager's **Volumes**, **Images** and **Activity** views expose storage and operation status; switching views or Environment keeps unfinished forms. **View Activity** follows an accepted operation through completion or failure and hides when that operation is already selected in Activity. If the local compute service becomes unavailable, the window retains the last known inventory and disables changes until the connection recovers.

The desktop includes a **Virtual Machines** launcher by default and adds an icon for each VM. Application Manager can hide the application launcher independently of those VM icons. Opening a VM icon shows its Inspector without starting the VM or a terminal; the Inspector's back arrow opens **Virtual Machines**. Renaming preserves its desktop position. VM icons are separate from virtual files and cannot be deleted through Recycler. Environment and operation technical details include the profile log location and relevant IDs for diagnosis; [startup](startup.md) owns log files and service commands.

Volumes keeps **New Volume** and **Copy** visible, with rename, growth, detach and delete under the selected volume's **More** menu. Images keeps **Add Image** visible; the selected image's **More** menu contains **Add Version** and **Remove**. Confirmation, progress and recovery controls stay with their forms.

Development machines retain project volumes when deleted. Task machines have an expiry that can end active work; check the expiry and retention choice before creating one. Deleting a machine and permanently deleting a retained project volume are separate actions. Root disks are disposable; a retained project volume is not a full VM backup.

**Guest Terminal** detaches when its window closes and can be reopened from Inspector. **More → End Session** stops the shell; **Details** shows its VM and terminal identities. VM shutdown or guest-service restart ends live shells. Opening host Codex beside a VM still runs Codex on the host. See [startup](startup.md) for host setup and [VM architecture](vm-platform.md) for retention and provider boundaries.

The [VM CLI](startup.md#vm-command-line) manages the same inventory without opening the desktop. Use `axv vm ssh <name|id>` for standard local SSH, commands, SFTP/SCP and TCP forwarding; it requires a running VM unless `--start` is explicit. SSH has ordinary connection lifetimes and does not reconnect a browser Guest Terminal. [SSH workflow examples](startup.md#ssh-file-transfer-and-forwarding) include standalone OpenSSH configuration and current guest IP inspection.

## Host terminal

Open **Terminal** for a shell in the service user's home directory. Codex's **Host Terminal** starts a separate shell in that conversation's recorded host working directory, including the control directory for a VM project. The project name is displayed above the terminal; **Details** shows the full starting path. Inaccessible directories fail explicitly. These shells operate on the host filesystem.

Closing a live window asks for confirmation and ends its shell and foreground job. Connection loss or service shutdown also ends it. **New Session** starts a fresh shell after exit or failure. Reloading the page starts fresh shells in restored windows, using the initial project directory rather than a later `cd` location.

Copy and Paste have toolbar icons; the **More** menu includes **Clear** and **Details**. **Interrupt** stays visible. Shell keys such as Ctrl+C and Ctrl+W reach the shell; desktop navigation shortcuts remain available. Terminal requires local same-origin access; [platform requirements and limits](api/host-terminal.md) belong in its API reference.

### Terminal clipboard

Host Terminal, Guest Terminal and Codex support **Paste** through the browser's clipboard API. Complete any native paste confirmation the browser displays. If reading is unavailable or fails, AXV opens **Paste text**. You can also choose **Paste text** from the terminal's editing or More menu.

In **Text to paste**, use the system's native Paste action or type text, then review it and choose **Send**. Enter in this field adds a line; it does not send. **Close** dismisses the dialog without sending its unsent text. An accepted send disables Send to prevent a duplicate submission; the text remains available to review or copy. Rejected input stays visible with an error.

The dialog identifies its original terminal, including a short run or terminal ID for Codex and Guest Terminal. A disconnect, session replacement or closed window invalidates that operation, including when the same terminal later reconnects. Copy any text you still need and open a new paste operation explicitly. AXV does not append Enter, but pasted line breaks or control characters can affect the receiving program, including executing shell commands. Bracketed paste protection depends on that program's support. Clipboard text is not saved in desktop layouts.

## Codex

Codex runs the official CLI as the service user on macOS/Linux. Install **Codex CLI 0.161.0 or later** and **tmux 3.1 or later**, with the [compatible CLI options](api/host-codex.md#availability-and-environment), then run `codex` in a host terminal to complete sign-in and project trust. AXV uses the CLI's existing credentials, approvals, sandbox and model settings without copying credential files.

**Environment** opens diagnostics; its **Refresh** icon checks dependencies again. Start AXV from the intended user's shell so its binaries and environment are available. **CLI ready** and **Terminal connected** indicate local availability and transport, not a successful model request. Workspace, proxy and background environment settings belong in [startup](startup.md#host-agent-defaults).

Use [document attachments](#document-attachments) to select saved context for the next message in an exact run.

For a registered VM project, choose **Open with Codex** in Project Files. Its scoped **Codex Sessions** manager shows the selected VM environment; **New Session** creates a host Codex conversation associated with that project. Files stay on the VM volume. The AXV Skill accompanies your submitted messages and supplies bound file/task tools; it does not send a prompt automatically. Complete any provider trust, sign-in or approval interaction normally. The Skill guides tool use without restricting the provider's independent host permissions.

VM run windows distinguish the host agent from the VM work project. **Project Files** and **Tasks** open that VM project; **Host Terminal** remains explicitly a host shell. **Details** separates the control directory, stable project and captured VM/boot/guest identities. Ending Codex does not cancel accepted VM tasks. After a final run, **Continue** reviews the current environment for that same project before creating a new run with the existing provider conversation. A missing VM or unverifiable history fails visibly. Old windows and commands retain their original environment; reviewing a replacement creates new run context rather than rewriting them.

If starting or continuing loses its response, choose **Resolve Start** or **Resolve Continue** for the original request. Session-list rows expose pending continuation recovery even when the current CLI or history is unavailable; damaged recovery data appears beside its owning row. Its payload survives page reload in the same desktop database and browser client. Recovery actions recheck their original request before submission and after confirmation. If another window resolves or changes it, review the current state; a stale recovery action never becomes a new start or continuation. Resolve that outcome before selecting another environment or starting again; AXV does not infer failure from a lost response or silently reset damaged browser recovery state. If the original run already ended, recovery opens that exact recorded run without starting another one.

Session rows show their project and full run ID. Run windows keep **Sessions**, the attachment icon and **Interrupt** in the toolbar. **More** groups editing, project files and session commands; the application menus retain their full command lists. The context strip shows the host project and a shortened run ID; **Details** shows the full path, run identity and tmux attach command. History paging uses up/down icons; **Live** returns to current output.

| Action | Effect |
| --- | --- |
| **New Session** | Start a separate conversation in the scoped VM project, or the configured default host workspace from the host manager |
| **New in Folder** | Choose a real host project for a new conversation |
| **Recent Projects → New Session** | Start a separate conversation in a previously used project |
| **Open / Reconnect** | Attach to an existing run |
| **Session / More → Rename / Archive / Unarchive** | Organize persistent records; archiving does not stop active work or free capacity |
| **Session / More → Host Terminal** | Open an independent, ephemeral shell in the recorded host working directory |
| **Details** | View the project, runtime identity and tmux attach command |
| **End Session** | Confirm termination of the run shown in that window, affecting its attached clients |
| **Continue** | Confirm a new process using the same saved conversation after the previous run ends |
| **Open Running Session** | Open a newer active run from an old window |

Closing a Codex window, reloading or stopping AXV detaches; the run remains alive under tmux. Multiple clients share input and output. Exit or runtime loss is shown without automatically restarting work. Old windows stay attached to their original run and cannot send commands to a newer one.

Transient connection and attachment failures retry automatically against the same run. Repeated failures leave a visible error and **Reconnect** action; unsent input is discarded. Access rejection or a final run stops automatic recovery.

Continuation needs the original provider history, project and account access. It restores conversation context, not shell state, interrupted work or tmux scrollback. Missing history or an unverified identity produces an error instead of a new conversation. Use AXV's **New Session** for another conversation rather than the CLI's `/new`, fork or switch-conversation actions.

Names, archives and recent projects survive service restarts. On metadata conflict, review the latest state before retrying. Configuration changes apply to new runs after [settings reload](startup.md#agent-settings-and-background-lifecycle). See [Codex architecture](codex.md) for conversation/run ownership and the [API](api/host-codex.md) for limits and caller contracts.
