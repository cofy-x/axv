# Using AXV

This guide owns visible actions and their effects. Start with [project status](../README.md#project-status) for available capabilities and [startup](startup.md) for installation and service configuration. Switching desktop workspaces changes the visible windows. Host and VM work continues in its original environment.

## Desktop

Run `axv` to start or reopen the workstation. Background services continue after the launching shell closes. Use an explicit restart to apply settings or a new executable. [Daily commands](startup.md#daily-commands)

A fresh layout opens **Welcome** and pins **Codex**, **Virtual Machines**, **Project Files** and **Terminal** in the Dock. Select a tile to open its application or choose among its windows. Use **+** on desktop or **Dock menu → Add application** on compact layouts to pin another app. Right-click a tile or open its compact submenu for window and pinning actions. Drag tiles to reorder with a mouse, or choose **Move Earlier** or **Move Later**. Swipe to reach overflowing tiles.

Use **1–4** to switch workspaces. **Workspace → Workspaces** renames the current workspace, manages shortcuts and lists additional workspaces from saved layouts. A blank name uses the workspace number. Choose **Add Workspace Shortcut** from a Dock or window menu to save that application or window.

Drag a title bar to move and an edge to resize. Double-click the title to fold. Use the title-bar menu to minimize, maximize, restore, move between workspaces or show a window on all workspaces. Restore a minimized window from its miniwindow icon. Folding, minimizing and switching workspaces preserve drafts, selection and scroll position.

Open **Window Overview** from the overlapping-window icon to switch to any open window, including minimized windows and other workspaces. Compact views show names and short Codex run references. Wider views show previews. Choose **Done** or press Escape to return. The AXV icon opens the **Workspace** menu. The active app's tools appear in the row below the top bar.

| Shortcut | Action |
| --- | --- |
| Ctrl+F6 | Cycle through visible windows on the current workspace |
| Alt+1–4 | Switch desktop workspace |
| F10 | Open the Workspace menu |
| Shift+F10 or Context Menu key, on a Dock tile | Open that application's Dock menu |
| Arrow keys, Enter, Escape | Navigate, select and dismiss menus |

Use **Workspace → Clock** to see the time in compact layouts. **Preferences** has **Wallpaper**, **Desktop**, **Remote access** and **Notifications** on one page. Use **Arrange Icons** beside **Snap icons to grid**. Wallpaper, snapping and the **24-hour clock** setting save automatically.

In Welcome, **Remote access** opens the matching section in Preferences, and **Show Desktop** closes Welcome. **More** contains **Application Manager**, **Browser installation guide**, **Preferences**, **Developer Tools → Component Gallery** and **About AXV**.

### Installed window and updates

Follow [browser installation](startup.md#browser-installation) to open the ordinary authenticated desktop in a standalone window. An installed icon can activate an existing window. Browser tabs and installed windows can have different local recovery data. [Browser recovery](storage.md#browser-recovery)

An open desktop stays in place during an update. If an app fails to load, use its **Retry** action. If it still fails, save or download/copy any unsaved text before reloading to load the current UI. A failed startup also offers **Reload**. In VM Project Files, **More → Download Draft** saves the current text locally as `.draft.txt`, leaving the draft unsaved in AXV. Restore a pending draft before downloading it. Extensions offer **Export draft**.

If authorization expires, retry after access is restored. If the desktop database changes, keep the original page to recover its unsaved work. **Open current AXV** opens the replacement in another page. Disconnecting a browser follows the same [shell, run and task lifetimes](architecture.md#codex-attachment-and-process-lifetime) in both browser tabs and installed windows.

## Applications and extensions

Open **Application Manager** from **Welcome → More → Application Manager** or **Workspace → Applications → Application Manager**. Its desktop icon is optional. Built-in applications keep their existing launchers and windows. Use the [Dock controls](#desktop) to pin the manager.

The manager starts with installed extensions. **Open** launches an enabled application; **Enable** makes a disabled application available again. Each extension row's **More** menu contains secondary management actions. **Add package** opens package selection and review, including the Checklist example. Collapsing it keeps the current review.

Use **Show on desktop** beside an application to show or hide its launcher in this browser's layout. New extensions start without a desktop icon. This choice is independent of Dock pinning and installation access, does not open a window and keeps the launcher's position when hidden. Uninstalling retains the choice; reinstalling can restore the icon. Individual VM icons follow the VM inventory.

Under **Add package**, choose **Install Checklist** to review the included example, then **Install**. **Open** launches its window. Add, edit or check items and choose **Save** to write that snapshot. Typing while saving stays unsaved. Closing a modified application or changing its installation offers Save, Cancel and Discard; a failed save retains the draft. **Export draft** in the application window downloads the current recovery copy independently of server connectivity.

Checklist rejects a change that exceeds the [application-data limit](api/extensions.md#application-data), shows an explanation and keeps the previous accepted text. Shorten or remove another item, then retry. A rejected **Add** leaves the text in **New item**.

If an application submits a draft that AXV cannot accept, the host window stays unsaved and blocks Save and changes that would lose it. Keep the window open to correct the draft, use **Export draft** when it can be represented as JSON, or explicitly discard it. The rejected content is held only in memory and is not recoverable after a browser reload.

To install your own extension, open **Add package**, select a self-contained JSON **Extension package**, review its identity, version and private-data access, then install. **Download example** provides a complete editable Checklist package. Selecting a package for an existing application offers **Replace package**, preserving its saved data and current enable setting. Installation changes require access on the AXV host. The authenticated remote desktop can run installed applications and use their private data.

**Reload** explicitly reconnects a window to the current installed version. With unsaved data, choose **Keep draft**, **Discard draft** or Cancel; keeping a draft retains its original saved revision, including after a session expires. A recovered draft offers **Restore draft** or **Discard draft** before the application opens. Restore does not overwrite a newer server document automatically. In Checklist, **Reload saved** asks before replacing dirty text; export a draft first when you need to keep both versions. Saving after another window changed the same data reports a revision conflict.

Use **More → Disable** to stop further access from the old runtime while retaining windows, drafts and saved data. **Enable**, then **Reload**, reconnects that window. **More → Uninstall** keeps saved data available under **Uninstalled applications** for export or later reinstall. **Export saved data** downloads the server snapshot; it does not include unsaved drafts in application windows. It is in **More** for installed applications and directly available for uninstalled ones. **More → Delete data** is a separate, irreversible confirmation available after uninstalling. A temporarily missing application retains its window, Dock pin and workspace shortcuts during layout recovery.

Extensions in this version store only their own data. They cannot access AXV documents, registered projects, commands, terminals, agents or VMs. Package format and limits belong to the [Extensions API](api/extensions.md); authoring and the message bridge belong to [desktop architecture](desktop.md#installed-extensions).

## GitHub Review

Open **GitHub Review** on the local AXV desktop. Install `git`, the official GitHub CLI (`gh`) and Codex CLI on the computer running AXV. Connect GitHub and Codex in **Settings**. AXV can attach the existing host CLI logins or display the provider's device code for a new login. Codex uses a ChatGPT sign-in and its associated usage allowance. If the GitHub account changes, use **Manage connections** and reconnect the intended account. Disconnecting in AXV detaches the integration. Revoke its provider authorization in the provider's account settings when needed.

In **Repositories**, choose **Add repository** and enter its GitHub `owner/repository`. Use **Paste multiple repositories** for a list with one name per line. Under **Review schedule**, turn on **Scan GitHub automatically**, choose a polling interval from 1 to 1,440 minutes and save. **Save settings** applies repository and schedule edits together. AXV also reviews ready PRs requested directly from the connected account, including PRs outside the watch list. **Scan now** requests an immediate scan. Expand **Automation status** above the inbox to see the next scan, any scan error and which repositories can verify candidates. The AXV daemon continues processing while the browser is closed. [Daily commands](startup.md#daily-commands) covers daemon restarts and host startup.

Choose **Configure** on a repository to edit its own **Review rules**, **VM tests** and **Automatic candidate verification**. Use **Back to repositories** to change another repository while keeping staged edits. If a setting is invalid, **Save settings** opens the affected section and focuses the field. **VM tests** shows the repository's registered environment. Keep a single VM command such as `go test ./pkg/config`, or configure named tests in priority order. Give each named test a file that must exist at the PR head, an executable and its arguments, and optionally the changed files or directories that should trigger it. For example, a deployment test can watch `deploy/scripts` and require `deploy/scripts/test_configure.py`. Put a broader test after a more specific test. AXV runs the first applicable test. Keep your own test helpers with your project or in a directory you maintain separately. Provision those helpers as read-only dependencies on the selected VM and use their guest paths in the command. [VM registration and toolchains](../recipes/github-review/README.md)

Opening **VM tests** checks whether the registered VM and project are reachable, and whether the guest reports support for Project Tasks. Choose **Recheck VM** after addressing a reported problem. The check uses no Codex allowance and submits no VM task. Each actual test still checks out its selected commit and uses the tools and dependencies available inside the VM.

Set **Daily Codex review limit** from 1 to 1,000. The default is 10. AXV counts each Codex review attempt when it starts and resumes queued work after midnight in the host's time zone. A failed or interrupted analysis still counts.

**Include direct review requests** is on by default. Turn it off to spend scheduled review capacity on watched repositories only. Existing outside requests wait in **In progress** with their hold reason. **Review latest commit** can explicitly queue a request still assigned to you.

**Pull requests** opens to **Needs action**. A PR can show a draft to review, a candidate to judge, and evidence that needs attention at the same time. Each action opens the corresponding part of the PR. Use **In progress** to follow running work and **History** for completed and superseded reviews. AXV queries each view across the connected account's reviews. Use **Next page** to reach older work and **Previous page** to return. A target-branch change without a replacement stays in **Needs action**.

Use **Review a PR** to paste a GitHub pull request link and queue that PR immediately, including while scheduled scanning is paused. The repository must be on your watch list or the PR must currently request your review. AXV opens the exact comparison and reuses a completed review of that comparison. The daily Codex limit still applies.

Use the shortcuts in **Automation status** to open the relevant schedule, account or repository settings. The inbox shows Codex starts used and the daily limit separately from discovery and VM verification status.

Open a PR to see its candidates, suggested decisions and local drafts. **View evidence** flags stage problems and expands to Codex, upstream CI, the local quick test and comparison status. Passing stages expand individually into their recorded scope, checkout and receipts. A repository without a matching quick test shows **Not applicable**. Select a candidate by title and code location, then choose **Assess candidate** to set a decision and record the reason and supporting evidence.

For a candidate that needs human verification, choose **Compare revisions** next to its reason. The action is also available under **Candidate evidence** for other eligible candidates. Select the runtime, enter the assertion and choose **Run comparison**. Use an exit code of zero when the expected behavior holds. AXV runs the same assertion on the reviewed common ancestor and PR head in the same VM. **View comparison** opens the recorded comparison directly from its result or the PR evidence overview. **Comparison evidence** shows both actual checkouts and assertion results. Expand **Logs and execution details** for full checkout IDs, logs and receipts. Compare the observed behavior before updating the candidate’s assessment. You can close the window while the two tasks run.

Open **Review draft** from the inbox or expand **Local comment draft** on a candidate. The draft retains the file, line and commit it was written for. Use **Markdown tools** to copy or view its source. **Mark reviewed** marks the draft as read. Use **Review suggestion** to accept or change a proposed candidate assessment. **Mark as unread** brings the draft back, and saving new text makes the new version ready for review. Earlier revisions retain their original draft location.

On a confirmed candidate whose recorded comparison reproduced the failure on the PR head, expand **Automatic scenario** and choose **Use as automatic scenario**. AXV enables the repository's candidate verification with that recorded assertion and runtime. Without a scenario ID, AXV matches candidates by the saved title, file and diff side. A verified result for the reviewed commits and current VM binding can be reused. Otherwise, AXV runs the same approved assertion on both revisions. **Automatic verification** shows progress or explains what needs review.

A reproduced difference adds a suggested decision and prepares a local draft when the candidate has no draft. Choose **Review suggestion** to inspect the proposed assessment. Earlier revisions retain their evidence and drafts for reference. Pause this workflow in the repository policy under **Settings**.

Codex can reference a saved scenario ID when a new finding describes the same behavior differently. AXV can run the approved assertion after verifying the scenario's scope and the current revisions. Open **View comparison** to inspect the receipt, then assess whether the observed result supports the new finding. This path keeps the candidate in **Needs action** until a person judges its relevance.

To use a particular interpreter, choose **Configure** on the repository and expand **Comparison runtimes** under **VM tests**. Enter its guest executable and flags, one argument per line. For Python, enter `python3` followed by `-c` on the next line. A repository without a saved launcher uses the guest’s POSIX shell. New comparisons work while polling is paused using the attached GitHub account.

**Issue history** keeps decisions on earlier comparisons. If AXV has not matched a newly reported candidate, **Track the same issue across revisions** lets you select its earlier occurrence. If Codex did not report an earlier issue again, expand **Earlier findings on this PR** and choose **Follow up on this revision**. The resulting reviewer follow-up preserves the original file location. Assess it with current evidence or mark it **Resolved on this revision**.

**Commits and review rules** identifies the PR head, target base, and diff merge base when recorded. New reviews also retain the PR title and description Codex received at review time. The evidence stages show which parts were tested.

- **Upstream CI** shows the checks associated with that head. Expand a job to see the steps GitHub reports as executed and its observed checkout when available. A workflow can test a merge commit. **Checkout evidence** opens the original log. **Refresh checks** updates the evidence while polling is paused. A refresh error appears alongside the previous observation, its timestamp and its check runs.
- **Local quick test** shows how AXV selected a test, which commit it checked out, and the VM task’s output. **Not applicable** means no configured test matched. A selection error means AXV could not establish applicability. A failed command records its exit status. Retry in automatic mode or choose another saved test for this PR only. Each attempt keeps its own result, and test retries use no Codex review start.

When evidence needs attention, AXV can recommend a saved repository test with its reason and exact PR head and base. **Run recommended test** confirms that one attempt. AXV reuses a verified execution receipt when the commits, saved test and configured VM scope match, including a verified failure. The original attempt and outcome stay visible, and **Other test options** holds the manual commands.

**Review latest commit** checks the selected PR even while automatic polling is paused. A new comparison consumes one daily review when Codex starts. Reopening an already reviewed comparison, refreshing checks, running a VM test and recording a candidate decision preserve the Codex review budget.

New commits move earlier reviews to **Superseded** on discovery. A change to the comparison on the target branch also expires the old decision. **History** shows each review's base so comparisons with the same head are distinguishable. Open a superseded review to see its original record and the newly observed base. Use **Review latest commit** to request a current review. Candidate comments stay local. Direct review requests outside the watched list receive a Codex review and GitHub Checks. Add the repository and configure its command to use the dedicated VM for future reviews. [API and ownership](api/github-review.md)

## Remote desktop

Open **Tailscale** from **Workspace → Applications**, **Preferences → Remote access → Open Tailscale** or its optional desktop icon. The stable desktop launcher opens one non-modal panel and can also be kept in the Dock or saved as a workspace shortcut. Its icon does not indicate connection readiness; the panel checks AXV's connection while active. AXV includes Tailscale and needs no system installation on its server.

On the computer running AXV:

1. Choose **Enable**. The setting is saved for this AXV service and future launches; the desktop stays open.
2. When **Sign in** appears, choose **Authorize**, use the workstation owner's Tailscale account and return to AXV.
3. If **HTTPS required** appears, follow **Tailscale DNS settings** to enable MagicDNS and HTTPS certificates. Certificate issuance publishes the node's name in public certificate transparency logs; access remains private. AXV continues automatically once settings are available.
4. When **Ready**, copy the **HTTPS URL**. Expand **Connect another device** for the official client download, connect that device with the same Tailscale account and open the URL. [Remote setup](startup.md#remote-access) owns account, policy and credential requirements.

**Disable** stops AXV's remote entrypoint immediately and saves the preference, retaining authorization for later use. When ready, its confirmation explains that remote browsers disconnect and their Host Terminal shells end; Codex views detach while Codex runs and VMs continue. Cancel leaves access enabled. Local terminals and other work stay open. Closing the panel or browser does not disable the service. An enabled but stopped/errored node offers **Retry**. Save failures and diagnostics remain visible; **Refresh** checks again without changing the setting. AXV does not manage the system Tailscale service or its networking.

Remote browsers receive a read-only connection summary. Enable, Disable, Retry and enrollment are available only on the AXV host. The remote desktop operates on the same host documents, projects, agents and VMs. Host directory paths and commands still refer to that host; changing the browser device does not move execution.

Each browser origin/client keeps its own layout and unsaved recovery copies. The local HTTP and remote HTTPS addresses are different origins: explicitly save documents before switching devices or addresses. Shared saved documents remain available, and existing revision-conflict checks still apply. Host Terminal reconnect starts a new shell; Codex and Guest Terminal retain their distinct [session lifetimes](architecture.md#codex-attachment-and-process-lifetime).

VM environment preparation and remote setup require access on the AXV host. Remote errors stay visible; retrying an interrupted operation must preserve its original identity rather than submit replacement work.

### Cloudflare desktop access

Open **Cloudflare** from **Workspace → Applications**, **Preferences → Remote access → Open Cloudflare** or its optional desktop icon. The cloud and tunnel drawing identifies the launcher; it does not indicate connection readiness. Application Manager can show or hide the launcher while preserving its position. Dock and workspace shortcuts open the same panel.

The window has **Overview**, **Setup**, **Publishing** and **Traffic** tabs. Each view provides its own refresh or reload action. On the AXV host, **Open setup** leads to editable desktop Access settings and an account wizard for publishing. The account wizard focuses on one step at a time; its final **Save settings** action opens a confirmation before writing. Connected accounts offer **Change connection**, with token rotation available separately. Validate values, review the destination and save explicitly. Unsaved changes stay in the window; replacing a draft or closing with edits requires a choice. The advanced manual guide is a fallback. [Cloudflare setup](startup.md#cloudflare-access) owns external Access and connector preparation.

Once configured, **Off** means the AXV entrypoint is disabled. **Enable** saves the preference and starts that authenticated local entrypoint; **Enabling…** and **Disabling…** identify a pending change. The amber **Listening locally** state means only that this origin is bound; check the public HTTPS URL separately. **Refresh** reads state without replaying a failed change. The separate local **Diagnostics** section shows the configured **Origin address**, instance and startup errors; an address alone does not mean the origin is listening. **View diagnostics** opens this section from an entrypoint error. Connection summaries and Diagnostics omit identity settings and credentials. Local Setup can edit identity settings; saved API tokens are never displayed.

**Disable** confirms that desktop Cloudflare clients will disconnect, their Host Terminal shells will end and Codex views will detach. Codex runs and VMs continue; local and Tailscale clients remain available. Closing or hiding the panel leaves saved access and publications running and stops panel polling. Authenticated remote owners can inspect tunnel health and traffic and manage published services; configuration, credential rotation and raw diagnostics stay local. If authorization is required, keep the desktop open to preserve drafts, sign in again in another tab, then retry the failed action. Owner tokens have a maximum signed lifetime of 15 minutes, and their original expiry disconnects the affected connection without renewal on reconnect; [connection lifetime](api/cloudflare.md#connection-lifetime) describes authorization and recovery limits.

**Publishing** shows the generated hostname, original VM/project, port, visibility and current phase for each service. Choose **New publication** or **Publish** beside a project preview to review a service. Enter a short **Name** and review its allocation domain; publishing assigns a fresh URL. A dedicated domain is permanently reserved for that publication, including after removal. **Owner** requires the configured owner's Access identity; **Public** explicitly allows Internet visitors. Incoming project shortcuts do not overwrite an existing publication draft without a choice. **Unpublish** withdraws the exact application's HTTP/WebSocket access and removes its recorded DNS and tunnel rule while its VM service continues. **Retry** addresses that same receipt and target. Every retry that can reopen access asks you to review the original hostname, environment and visibility, including when the displayed status is stale. Pending cleanup retries only removal. After desktop restart, this reopening action is labeled **Resume**; it does not allocate another address or select a replacement target. **Traffic** displays actual zone-wide counts for the selected range, or explains why the provider cannot supply them; it is not a public connectivity test.

## Work notifications

Open **Notifications** from Applications to see **History** on first open. **Preferences → Notifications → Open** takes you directly to Pocket Alert settings when local configuration is available. Browser push and Pocket Alert are independent, optional delivery adapters, both disabled by default. Agent requests and outcomes, plus VM task outcomes, have durable event identities; closing the panel or browser does not stop accepted work or remove notification history.

Configure delivery on the computer running AXV. In **Settings**, enter the Pocket Alert **Application ID**, **Device ID**, **API token** and **Return-link origin**, select event types, enable delivery and choose **Save**. The origin must be the AXV address reachable from the receiving device; it is separate from configuring Tailscale or Cloudflare access. Remote authenticated owners can read History and original-work details, while configuration, Test and Disable stay local. The [notification API](api/notifications.md) owns access, storage and provider limits.

The token is a password-only, write-only field. A blank **Replacement API token** keeps the server's saved token; **Remove saved token on Save** explicitly clears it. Tokens do not enter browser recovery or desktop layout. Failed saves retain the other draft fields and clear the password; refresh and review the saved state before retrying an uncertain save. **Reload saved settings** asks before replacing an unsaved draft. **Test** uses the saved configuration, so save edits first, then inspect its History entry instead of treating acceptance as delivery.

Changing credentials, application/device, return-link origin or enabled state disables queued deliveries from the previous settings. Changing event types only removes deselected types from the queue; saving unchanged settings preserves pending work. **Disable** stops further delivery and disables queued records without recalling notifications already accepted by Pocket Alert. These settings actions do not cancel runs or task attempts.

**History** distinguishes delivery from the underlying work:

| Delivery state | Meaning |
| --- | --- |
| **Pending / Sending** | Queued or being submitted; the original request is checked before delivery |
| **Accepted by Pocket Alert** | Provider acceptance, without confirmation that a device displayed or read it |
| **Delivery uncertain** | The provider may have accepted it; a displayed retry time means another bounded attempt may deliver a duplicate |
| **Delivery failed** | Delivery stopped after rejection or exhausted attempts |
| **Disabled / Superseded before delivery** | Delivery was withdrawn by settings or by the original work moving beyond the request |

Eligible temporary or uncertain failures retry automatically, with at most three delivery attempts per event. Select an event to see its attempt count, any scheduled retry and browser delivery details. **Back to History** returns to the same place in the list. A Pocket Alert quota response pauses the entire adapter until the displayed **quota cooldown** time, including queued events without their own retry time. Work continues during this pause. Source gaps, unavailable intake and failed refreshes remain visible; missing outcomes are not inferred and retained history can be stale.

A notification return link opens its recorded event details after the configured AXV origin's normal authentication. The link contains opaque notification identity, without credentials or authority to approve work. If authorization expired, restore access at that same AXV address and reopen the original link. **Open original work** explicitly opens a separate read-only inspection of the exact Codex run or VM task attempt. It does not attach terminal input, approve, dismiss, cancel, submit or start work. Stale events retain their original identities; unavailable or replaced stores never open a newer run or retry instead. Continue ordinary work from the owning Codex or Tasks window when ready.

### Browser push notifications

In Notifications, open **This browser** at the AXV origin you intend to keep. Use a secure browser context; iPhone/iPad require an installed Home Screen web app, not an ordinary Safari tab. Browser and OS permission, a browser subscription and server delivery are separate states. An installation icon does not establish a separate browser storage or permission context.

Review a **Push service contact** email (`mailto:`) or public HTTPS contact URL. This contact is included in the vendor-visible VAPID authorization, outside the encrypted alert; AXV does not substitute the private workstation address. Select the work event types and choose **Subscribe** explicitly, then respond to the browser permission prompt. Opening AXV or this panel never requests permission or creates a subscription. A denied permission must be changed through browser/OS settings; AXV cannot grant it.

After subscribing, the browser panel shows its delivery status and management actions. **Test this browser** queues one synthetic notification for this subscription only, independently of Pocket Alert. **Disable server delivery** stops future sends without changing browser permission or removing its subscription. **Unsubscribe this browser** removes the browser subscription and its exact server record; a partial failure remains visible so you can reconcile it explicitly. Neither action reliably retracts an alert already queued at a vendor or displayed by the OS. Revoking permission, removing the installed icon, clearing site data and removing a server record are separate operations.

Subscriptions belong to this notification store, exact AXV origin, browser storage context and push-key generation. A changed store, origin, sender key or browser-managed subscription requires explicit reconciliation and subscription; AXV never silently adopts an old endpoint. Tabs and an installed window may share the same context. Browser labels are descriptions, not authentication. AXV's worker storage retains only public binding and notification-routing metadata, separately from browser-managed PushManager subscription data. The server's endpoint/authentication records and private sender key stay in the host's private notification store.

Alerts use fixed conservative wording and opaque event identity. The encrypted alert can be displayed without fetching the private workstation. Vendor push may therefore arrive while Tailscale is disconnected, but opening the original activity still requires the same authenticated AXV origin and connectivity. Clicking focuses an existing AXV window through a narrow event reference without navigating away from dirty editors, or opens the original notification link in a new window. Missing or resolved work stays missing or resolved; there are no approval or cancellation buttons.

The desktop process must remain running to send. Delivery is best-effort: provider acceptance is not device display, retries can duplicate alerts, and notifications expire. The worker does not intercept desktop requests, cache protected content, perform offline application writes or poll AXV in the background. [The API contract](api/notifications.md#browser-web-push) owns vendor support, limits and retry behavior; [project status](../README.md#project-status) records live device validation.

## Project Files

**Project Files** browses original files in an explicitly selected host directory or a VM project. For a host project, use **Choose Folder** or enter an absolute path, then **Open** to register it. Registration is independent of recent Codex projects and retains directory identity: an unavailable or replaced root fails visibly. Explicitly opening a replacement directory at the same path registers a new project and preserves the existing project reference. Narrow windows show either the directory or its selected file; **Back** restores the selection and keyboard focus. Text reads support up to 1 MiB, with rich Markdown previews up to 64 KiB; host Download supports regular files up to 10 MiB. Project paths stay inside the registered root.

For host projects, **More** offers **Projects** and **Download**. A failed preview also keeps **Download** available. Markdown previews support tables, task lists and fenced code, disable active HTML, and show image placeholders.

Project Files uses **Host / VM** tabs and preserves unfinished input when switching. Returning to the VM tab checks the current project list and volume choices. Select an entry under **Projects** to reopen a registered project. Below the list in **VM**, **Project action → Open Folder** opens an existing **Directory** (blank selects the volume root) on the selected **Project volume**. The volume must be retained, attached and ready, with its VM running an image that supports project files. The Inspector's **Project Files** action opens a separate folder chooser for that VM and its attached volume. The chooser and any project window opened from it require the original attachment. After moving the volume, close the old project window and reopen the project from **Projects** to use its current VM. **Open Virtual Machines** takes you to VM management when the host or an eligible volume is unavailable. **Refresh VM projects** checks again on demand.

Choose **Create Project** or **Clone Repository** to create a new directory on that volume. Enter **New directory**, an optional **New name** and, for cloning, a credential-free HTTPS or SSH **Repository URL**. AXV opens a visible **Volume workspace** and submits initialization as a persistent task. Preconfigured guest credentials already support private clones; follow [private repository authentication](#private-repository-authentication) to set up access through **Guest Terminal**. Inline passwords, HTTPS user information, query strings and fragments are rejected. Clone failures show a bounded error and preserve partial files; existing directories are never overwritten. **Recover** keeps the original request and environment after an uncertain response or reload and never reruns an accepted clone. Recovery remains available while the project inventory is unavailable or **Open Folder** is selected. After successful initialization, **Open Project** explicitly registers its directory; **Codex** starts a session for that project.

VM project windows use **Files** as the default view. **Codex** is the main toolbar action, with **Tasks** directly beside it. **Previews** and **Backups** have their own tabs; switching preserves the file draft, editing mode, undo history, current selections and original recovery requests. Hidden sections pause input and monitoring while accepted work continues. **File Recovery/Error**, **Preview Recovery/Error/Status** and **Backup Recovery/Error/Status** open pending recovery, errors or recorded activity from another section. The status bar keeps unsaved file changes visible. **Codex**, **Tasks** and project **More** remain available across sections.

In **Files**, select a file to read with line numbers, filename-based syntax highlighting and search. VM files start read-only; the compact file toolbar shows the path first, followed by reload and **Edit**. Narrow views add a **Back** icon at the start of that action group. Choose **Edit** to make manual changes. Host source previews use the same highlighting and remain read-only. During editing, **Save** or **Ctrl/Command-S** captures the visible text with its original file hash and environment binding; later typing stays dirty, including typing during a save. A conflict or failed write keeps the draft. **Done** returns a clean file to reading; **Cancel** asks before discarding unsaved text and its recovery copy. **More → New File / New Folder** creates an entry; a new file opens for editing while that request is still the latest choice and the current view can safely switch. Opening another file returns to read-only browsing. **Back** returns focus to the selected file; reopening that same file preserves its draft and editing mode.

While a VM file is visible, AXV checks its original environment for external changes, including changes made by an agent, terminal or Git. A file without unsaved changes updates automatically, including during clean manual editing. The reading position, selection and editing mode are retained; accepting an external version starts new undo history. A dirty file instead shows **File changed in VM. Draft kept.** and keeps its text, recovery copy and original save expectation. Saving, unresolved save failures, draft recovery and unfinished input prevent automatic replacement. Hidden file views and background browser tabs pause checks; returning checks again. Observation does not require the file window to be active, so a visible preview still updates while Codex has focus. Successful automatic updates appear in the status bar without moving the code; draft conflicts and errors remain beside their recovery actions. A Save conflict replaces the duplicate external-change notice; distinct check failures remain visible. Navigation, reload, Save, editing exit and close wait for unfinished composition input to settle. Moving focus elsewhere retains the final text of an input already started in this editor.

The **Reload file** icon beside the file path explicitly fetches a new snapshot. **Refresh directory** refreshes names without replacing open text/hash. **More → Projects** opens the launcher without closing the editor. **More → Reconnect** explicitly obtains a current VM binding. Reload and reconnect return the file to read-only browsing and require a discard choice when unsaved text would be replaced. Failed reads offer **Retry** against the original file and binding; save and connection errors show the applicable **Reload** or **Reconnect** action directly. Failed automatic checks retain the displayed snapshot with a visible warning and retry; a missing or unreadable file keeps the current text. An unavailable VM keeps the snapshot and a visible warning while checks retry the original environment. A changed VM binding or restarted guest stops automatic checks until explicit reconnection; requests never redirect to another environment or the host.

VM editor recovery is local to the browser tab and preserves the draft's original binding/hash. After reload, choose **Restore Draft** to resume manual editing or **Discard Draft**; a stale recovered binding still fails on save until explicitly resolved. An unreadable or mismatched recovery record preserves its exact stored bytes and pauses editing, Save and creation until an explicit **Discard Recovery** confirmation; browsing remains available and Cancel keeps the record. Closing a dirty VM project window offers **Keep Draft**, **Discard** or **Cancel**. Closing the browser tab removes its recovery storage; save or copy the text first. Unresolved drafts and recovery records request a browser warning before leaving the page. **Details** shows the project, host, volume, root, VM, boot and guest-service identities. Keeping a volume and reopening the same project after VM replacement preserves files and Git state when the repository metadata and paths remain on that volume.

**Previews** registers an already running service's **Port**. Start the service inside the VM, then choose **Add** and **Open**. The separate browser tab supports HTTP and WebSocket traffic; configure a development server that hard-codes a host or WebSocket port to use its preview origin. Preview browsing requires a browser on the computer running AXV. Remote owners can inspect or stop local previews; remote registration and browsing are unavailable. **Stop** revokes only this preview and its live connections, leaving the development process running. Desktop restart requires explicit registration again; stale environments never reconnect to a replacement VM. After an uncertain response, **Recover** retains the original request and environment; manager restart requires explicitly releasing that recovery record before a fresh registration. [Preview behavior](api/project-previews.md) owns request receipts and access details.

The **Backups** tab copies the whole retained volume. Save drafts, finish or reconcile tasks, then choose **Stop VM** and confirm that guest sessions will end. Enter **Name**, choose **Back Up**. The panel tracks the original operation until it finishes, including after reload; **Refresh backups** reloads the inventory. A completed backup is immutable and cannot be attached as working storage. Select **Backup** and choose **Restore**, then select the independent **Restored volume**. **Create VM** uses the original VM's compatible configuration; if that original VM is unavailable, create a compatible VM with the restored volume in Virtual Machines. Once it is ready, **Open Project** checks the original root and Git/worktree paths before opening a new project identity. Failed verification preserves the copy for explicit repair. The original project and its conversations/tasks remain attached to their original volume. **Recover** replays a saved action unchanged after an uncertain response. Saved actions retain their original host, project and payload; newer recovery records survive late replies. Invalid recovery data stays visible until explicitly released. These copies remain on the same host, so keep external backups for host or disk loss.

Choose **Tasks** in a VM project to open its independent task window without replacing the file editor's draft. **New Task** opens or hides the command form while preserving its input; accepting a submission or selecting history opens the result view. Review the VM target beside the command, enter a **Shell command**, then choose **Run**. The command runs through `/bin/sh -lc` inside that captured VM environment; save file edits first. **Execution options** contains the optional **Directory in project**, timeout, CPU, memory, process and log limits, with the full binding in its **Execution environment** section. Defaults request 900 seconds, one CPU, 512 MiB memory, 64 processes and 1 MiB of combined output. Unsupported guest enforcement rejects execution; exceeding the log limit stops that attempt and retains incomplete output. Per-task disk, I/O and network controls are unavailable; VM-wide limits are separate.

Accepted tasks remain in project history when the window or browser closes. Select a task to inspect its status, original VM and output; the attempt selector appears when there is more than one attempt. If a selected attempt is missing from recorded history, choose a recorded attempt explicitly; the window does not substitute another execution. History shows submission times and short task IDs to distinguish repeated commands. On narrow screens, the toolbar's **Back to Tasks** button returns from the command form or result to history. Output is plain text, displayed in bounded 128 KiB pages with up to three extra bytes to preserve whole UTF-8 characters. **First**, **Previous** and **Next** appear when paging is needed. **Latest** appears for unfinished work or paged output and follows new cached output until another page is chosen or **Latest** is turned off. **Execution details** includes the displayed byte range and full timestamps. Incomplete output, failed refreshes and recovery actions remain visible beside retained results; ordinary action feedback appears in the status bar. A displayed VM target records its binding rather than asserting that its VM remains reachable. **Cancel** confirms cancellation of the displayed attempt in its original VM without affecting another task or later attempt. **Retry** confirms a new attempt against the explicitly connected environment after the current attempt finishes. Unknown execution cannot be retried until the original environment is reconciled or confirmed interrupted; reconnecting never silently restarts it.

If submission loses its response, **Recover Submission** queries the original receipt and, if it was not accepted, resubmits the same request and binding. This tab retains the unconfirmed payload across reload; it does not select a replacement VM. **Dismiss Recovery** only removes that recovery prompt, preserving its retry identity and leaving accepted work untouched. Review history before changing the command or VM. **More task actions** contains **Project Files**, **Reconnect VM** and **Task Help**. Reconnecting explicitly captures the current environment for subsequent submissions; historical attempts retain their original bindings. A failed connection also exposes **Reconnect VM** beside its error.

### GitHub project review

Open a VM project's **GitHub** tab in Project Files. **Read Git metadata** reads the selected repository/worktree root without running Git, hooks or project code. Candidate remotes are suggestions only. Enter the intended GitHub `owner/repository`, choose **Review repository and account**, inspect the canonical repository and signed-in account, then explicitly **Associate reviewed repository**. The association belongs to the project, independently of Codex conversations/runs, and changing it preserves file-editor drafts and execution bindings.

The selected image and running Guest must both declare `project-github`; check the image's manifest and release notes for support, and use a compatible rebuilt image when needed. See the [image and capability contract](vm-platform.md#image-and-capability-contract). Install a reviewed official GitHub CLI at the guest-service user's `~/.local/bin/gh`, following the [guest CLI installation guidance](#github-https). Sign in explicitly from Guest Terminal using the dedicated AXV profile:

```sh
umask 077
mkdir -p "$HOME/.config/axv/github"
chmod 700 "$HOME/.config/axv/github"
GH_CONFIG_DIR="$HOME/.config/axv/github" "$HOME/.local/bin/gh" auth login --hostname github.com --git-protocol https --web --insecure-storage
chmod 600 "$HOME/.config/axv/github/hosts.yml"
```

Review GitHub's account permissions during sign-in. AXV reads the active account's explicit file-backed credential from this dedicated profile; keyring-only or missing credentials are rejected. The command intentionally stores a plaintext token in guest-owned `hosts.yml`: keep it `0600` inside the private `0700` directory. Its authority can exceed this panel's fixed reads. For each read AXV supplies only the captured token to `gh` through its process environment and uses an empty temporary configuration, preventing default-profile, keyring or transport-configuration fallback. Credentials stay on the guest root disk. After VM replacement, reconnect the project and explicitly review/associate the new guest connection before refreshing; the retained repository link does not transfer authentication.

Choose **Refresh GitHub** to read the captured local HEAD, associated/branch-candidate PRs and check observations. There is no polling or webhook. Refresh creates no durable task or task receipt. Inspect each full repository/commit identity: **Local HEAD**, **PR head** and **Merge test checks** can refer to different commits. Branch candidates, detached/unborn HEAD, missing remote commits and unmatched/incomplete checks remain explicit. A successful check is an observation of its own SHA, not proof that local work was tested or that GitHub permits merging. Bounded results can omit additional PRs/checks and are marked incomplete.

Failed refreshes keep the last successful observation visibly stale and show authentication, permission, rate-limit or connectivity errors. A changed repository/account/association or VM binding requires explicit review or project reconnect; it never silently selects a replacement. Reopen/reload retains the association, not a freshly validated check result. **Clear association** removes only this project's reviewed link; it does not revoke credentials or affect GitHub. Remove the dedicated profile and revoke its authorization explicitly through the guest/provider when no longer wanted.

Repository, PR and check links open the precise external objects on GitHub for review. The panel does not push, create or merge PRs, rerun checks or submit agent messages. Those mutations and automatic polling require a separately reviewed phase with exact targets and permissions. Host project review and GitHub Enterprise hosts are outside this initial VM-project workflow.

### Credentials for project tasks

Review the project and its exact VM environment before configuring development credentials in **Guest Terminal**. The current [task contract](api/compute.md#vm-project-tasks) has no secret-injection field: AXV retains submitted commands and task output, and the desktop retains unconfirmed submissions for recovery. Never put tokens, passwords, secret-bearing URLs or `export TOKEN=value` into task commands, documents or diagnostics. Ordinary task output has no secret-redaction guarantee; avoid environment dumps, shell tracing and verbose provider/application logs.

Configure credentials for the guest-service account, not the host agent. The standard image uses `axv` with home `/home/axv`; custom images must use their actual service account. Changes made only to a terminal's environment or agent processes are not inherited by independently launched tasks. Use an absolute-path guest-owned wrapper or provider executable that reads private guest configuration at execution time. Task arguments should contain executable paths and other non-secret arguments, including project/configuration selectors; supply credentials inside the guest through the provider's supported file, standard-input or process-environment mechanism.

Keep credential files owned by that guest account with mode `0600` in a private `0700` directory on the VM root disk, outside repositories and retained project volumes. Headless tools may store tokens as plaintext when no OS keyring is available; verify their actual storage and permissions. Non-secret scripts and project selectors can stay in the repository. Credentials, login state and caches on the root disk are excluded from [project-volume backups](startup.md#vm-project-backups) and may be lost on replacement. Review the new environment and explicitly reauthenticate after replacement or restoration; do not copy credentials onto a retained volume to preserve them.

Choose development-only, least-privilege access with an expiry where supported. A CLI's project, environment or directory selector is not an authorization boundary; use a separate development project/account if the provider cannot enforce the intended scope. [Doppler service tokens](https://docs.doppler.com/docs/service-tokens) and [Infisical machine authentication](https://infisical.com/docs/documentation/platform/identities/universal-auth) are optional upstream workflows, not AXV-managed integrations. Follow their current installation, scope, storage and removal instructions; AXV does not install these CLIs or manage their accounts.

For one explicitly reviewed development email, use the optional [Resend recipe](../recipes/resend/README.md). It keeps credentials and send state inside the guest, starts with simulator recipients, and preserves uncertainty across provider response loss and finite idempotency retention.

For a development cache or rate limiter, use the optional [Upstash recipe](../recipes/upstash/README.md). It uses a claimed database, bounded synthetic checks and explicit cleanup; timeout and storage failures deny application work without changing database identity.

Before using a credential with a real application, submit a separate bounded task that consumes a disposable development value and prints only a fixed success indicator. Verify the task works without terminal environment changes, inspect its original receipt/output for leaks, and test missing credentials, denied access and network loss. Select and verify explicit cache behavior: a speed-cache option may not disable offline fallback. For a fail-closed command, require provider retrieval and reject cached fallback rather than silently choosing another identity, environment or provider. Revoke/expire the test credential and verify future access is denied; revocation cannot erase values already delivered to a process or cache. Remove only the test's local credentials/caches and revoke only its provider identity, preserving unrelated configuration. Keep provider versions and observed results in the task/PR rather than treating an untested recipe as validated.

All tasks in a VM share the guest account's trust domain. Private file modes do not hide credentials from another process or agent allowed to execute as that account, and environment injection does not make values inaccessible to such code. Use separate VMs for distinct guest task trust domains; host-agent permissions remain independent. These guest credentials do not configure Codex model authentication, host tmux, host credential helpers or installed HTML applications.

### Private repository authentication

In **Clone Repository**, review the selected VM and clean repository URL, then open its **Guest Terminal** for authentication. AXV guest images run Git as `axv`, with home `/home/axv`; custom guests must configure the actual guest-service account. Browser login and host Git credentials do not grant guest repository access. Existing HTTPS credential helpers and SSH configurations remain usable. AXV does not store Git tokens, forward a host SSH agent or perform a separate automated access check.

Initialization runs independently of the terminal's login shell. Use persistent guest user configuration and helper executables available to the guest service; exporting `GH_TOKEN`, changing PATH or starting an SSH agent in one terminal does not configure a later clone. All projects in one VM share that account and its credentials; projects are not separate security boundaries.

#### GitHub HTTPS

This recipe requires a compatible guest with GitHub CLI (`gh`) installed. The current AXV guest image recipe includes Git but does not install `gh`; VM Start never installs it. Use an explicitly prepared image or install a reviewed [official GitHub CLI release](https://github.com/cli/cli/releases) for the guest architecture under the guest user's home, for example `/home/axv/.local/bin/gh`. Verify the release checksum before installation. Do not add broad sudo access or install credential tools onto the retained project volume. Invoke a user-installed `gh` by its absolute path so its Git helper does not depend on a terminal-only PATH.

For a home-directory installation at `/home/axv/.local/bin/gh`, run the following in Guest Terminal. Use your actual executable's absolute path if it differs:

```bash
umask 077
mkdir -p /home/axv/.config/gh
chmod 700 /home/axv/.config/gh
/home/axv/.local/bin/gh auth login --hostname github.com --git-protocol https --web
/home/axv/.local/bin/gh auth setup-git --hostname github.com
/home/axv/.local/bin/gh auth status --hostname github.com
```

Open the displayed device-login URL in your browser and enter its one-time code. Review the account and access requested by GitHub CLI. [Login](https://cli.github.com/manual/gh_auth_login) authenticates the guest; [setup-git](https://cli.github.com/manual/gh_auth_setup-git) persists Git's credential helper for that guest user. Organization policies and the account's repository permissions still apply. Keep tokens out of repository URLs, AXV task commands, documents and diagnostics.

Without an available OS credential store, `gh` can fall back to a plaintext token file. Use `gh auth status` to find its location; keep that file owned by the guest-service account with mode `0600` and its configuration directory private (`0700`). The default AXV guest location is `/home/axv/.config/gh/hosts.yml`. Do not print the file or use `gh auth token` in shared output. These credentials and the helper configuration live on the VM root disk and may be lost when replacing the VM, even while project files survive on the retained volume. Do not copy them onto that volume to preserve them.

To remove local authentication, use `gh auth logout --hostname github.com`. [Logout does not revoke the token](https://cli.github.com/manual/gh_auth_logout). To revoke browser-login credentials, use GitHub **Settings → Applications → Authorized OAuth Apps → GitHub CLI → Revoke access**; this affects other tokens authorized through that application too.

#### SSH

Configure a dedicated guest-owned key and the repository provider's host trust in `/home/axv/.ssh`, outside the project volume. Add only its public key to the provider account or a suitably scoped deploy key. Keep the directory private (`0700`), and private keys and configuration at `0600`. Select the key in the guest user's SSH configuration with an explicit `IdentityFile` and, where needed, `IdentitiesOnly yes`.

For GitHub, follow its [key setup](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/adding-a-new-ssh-key-to-your-github-account) and [connection test](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/testing-your-ssh-connection). Compare the presented host fingerprint with [GitHub's published fingerprints](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/githubs-ssh-key-fingerprints) before accepting it. A key obtained through `ssh-keyscan` alone does not establish trust. Initialization uses strict host-key checking and cannot prompt for a key passphrase. The key or an explicitly configured credential mechanism must be available to the independent guest service; a terminal-only agent is insufficient. Host-agent forwarding remains disabled.

#### Clone and recover

Return to **Clone Repository**, review the same VM and a new directory, then submit the clean HTTPS or SSH URL. Authentication setup does not itself create or register a project. Follow the independent initialization task to completion and choose **Open Project**.

After a lost response or reload, choose **Recover** to inspect the original request, attempt and environment before taking further action. After an accepted clone fails, inspect the preserved directory in **Volume workspace** and fix authentication in its original VM. **Recover** and task **Retry** do not rerun that initialization. Explicitly start a new initialization with a new request and unused directory, leaving the partial clone and original receipt available for inspection. GitHub may [report a private repository as missing](https://docs.github.com/en/repositories/creating-and-managing-repositories/troubleshooting-cloning-errors#error-repository-not-found) when the account lacks access, so that error alone cannot distinguish permissions from an incorrect URL.

After VM replacement, explicitly review and reconnect to the new environment and configure its credentials and host trust again. Old attempts retain their original environment; neither credential setup nor a surviving project volume retargets them.

## Virtual Machines

**Virtual Machines** manages VMs on the machine running AXV. Start in **VM Host** to set up the service. Use **Machines → Refresh** from the application menu to check the host and inventory.

### Connect the VM host

- **macOS** Review storage, allocation limits, installation and downloads before preparing the host. Reopen the window to check progress or retry using verified downloads. **Cancel** is available before configuration is committed. VM controls become available when preparation completes.
- **Linux** Choose **Connect** when AXV detects Firecracker. Use **Check connection** to refresh detection. For a custom socket or configuration file, choose **Edit connection**, then **Check connection** and **Connect**. **Cancel** preserves the previous connection settings.
- **Missing service on Linux amd64** Choose **Copy & Open Terminal** under **Install the VM service**. Paste, review and run the command in the host shell. It installs Firecracker and the default Guest image and starts `axv-host.service`. Return to **Virtual Machines** and choose **Connect** when AXV detects the service. Use **Copy command** or select the command to run it in an existing host shell.
- **Service recovery** Run the displayed start or status command for a stopped or unhealthy service. [Linux service setup](startup.md#linux-privilege-boundary)

**VM Host** shows connection status and host limits. Stopped VMs consume VM slots. Choose **Diagnostics → Copy diagnostics** for configuration and log information. **Request details** identifies an interrupted request. [Configuration and logs](startup.md#configuration-and-storage)

### Create and manage VMs

Choose **New VM** when the host has a compatible image. Enter a unique **Name**, select the **VM type** and review the configuration. **Advanced** lets you change resources, project storage and the Task's auto-delete time. Choose **Create & Start** or **Create stopped**.

Development VMs retain their project volumes on deletion. A Task can expire while running. Check its expiry and volume retention before creating it. VM deletion removes its root disk. A retained volume remains available separately. Use a [project-volume backup](vm-platform.md#project-volume-backups) for a separate copy.

Select a VM for its current operations. Use **Start VM** for a stopped VM and **Stop** to confirm shutdown when power state is uncertain. **Inspect** or the VM desktop icon opens its Inspector. **Back to Virtual Machines** returns to the inventory. The Inspector closes when its host confirms deletion. An offline host retains the last known inventory with changes disabled. Use **Retry connection** when available.

### Volumes, images and Activity

- **Volumes** shows capacity, retention, attachment and backup source when known. On Lima, use **New Volume** or select a volume for actions such as **Copy**. **Attach** connects a detached working volume to a stopped VM. On Firecracker, create a project volume with a new VM or select a compatible detached volume in **New VM → Advanced → Project volume**. For permanent removal of an eligible volume, choose **Delete** and confirm with **Delete Volume**.
- **Images** shows compatibility and status. Open **Details** for an ID, checksum and source. On Lima, use **Add Image** or **More → Add Version**. For eligible images, choose **More → Remove** on Lima or **Details → Remove** on Firecracker, then confirm with **Remove Image**. The host administrator registers [additional Firecracker Guest images](startup.md#linux-guest-bundle-installation). Source files supplied by the administrator stay on disk.
- **Activity** records the operation, target, time, state and useful phase or error. **View Activity** opens the matching record. Review recent deletion results here. Use the [VM CLI](startup.md#vm-command-line) for operation and resource IDs.

### Guest Terminal

On a ready VM, **Open Terminal** resumes the only live session, opens the session list in Inspector if several are live, or starts a shell if none are live. Inspector offers **Resume Terminal** and **New Terminal**. The terminal title and Inspector share the same session label. **More → Details** shows full identities.

Closing a Guest Terminal window detaches. **Reconnect** returns to the same surviving shell and **More → End Session** stops it. VM shutdown or guest-service restart ends live guest shells. Host Terminal and Codex run on the host. For independent SSH, file transfer or forwarding, use [`axv vm ssh`](startup.md#ssh-file-transfer-and-forwarding).

## Host terminal

Open **Terminal** for a shell in the service user's home directory. Codex's **Host Terminal** opens a separate host shell in the run's recorded working directory. **More → Details** shows the initial directory. Host Terminal requires [local same-origin access](api/host-terminal.md).

AXV asks before closing a live window. Confirming ends its shell and foreground job. Disconnecting or stopping the service also ends it. **New Session** starts a fresh shell after exit or failure. After reload, restored windows start fresh shells in their initial directories.

Host and Guest Terminal offer **Copy**, **Paste** and **Interrupt**. Their **More** menus give access to **Details**, **Clear** and **Paste text**. Shell control keys and desktop navigation shortcuts both remain available.

### Terminal clipboard

**Paste** uses the browser clipboard when allowed. Complete any native confirmation. If AXV cannot read the clipboard, it opens **Paste text**, also available from the terminal's menu.

Review **Text to paste** and choose **Send**. Enter adds a line. **Close** discards unsent text. An accepted send keeps the text available to copy and disables **Send**. A rejected send keeps the text and shows an error.

The dialog targets the original terminal. If the terminal disconnects, changes or closes, copy the text you need and open a new paste operation. Line breaks and control characters can execute commands. Bracketed paste protection depends on the receiving program.

## Codex

AXV runs Codex as the service user on macOS/Linux. Install the [supported Codex CLI and tmux versions](api/host-codex.md#availability-and-environment), then run `codex` in Host Terminal to sign in and complete project trust. AXV uses the CLI's existing credentials, approvals and settings.

If starting is unavailable, open **Sessions → Environment** for readiness and **Refresh** to recheck dependencies. **CLI ready** and **Terminal connected** describe the local CLI and transport. The provider's model request runs separately. [Host agent settings](startup.md#host-agent-defaults)

### Start in a folder or VM project

- **Default folder** expands to show the full path. **New Session** starts a separate conversation there.
- **New in Folder** opens the picker for another host directory. Browse, enter a path and choose **Go**, or choose a previously used folder from **Recent Projects**. Review the confirmed directory and choose **Start Here**. **Cancel** or Escape keeps the typed path for later. A failed read marks retained contents **Outdated** and disables navigation in that listing until a successful read.
- **Project Files → Codex** opens sessions for a registered VM project. Its **New Session** starts a host Codex conversation bound to that project, with files on the VM volume. The AXV Skill supplies tools scoped to that VM for submitted messages. Provider approvals and the CLI's host permissions still apply. The run's **Project** menu opens its **Project Files** or **Tasks**. [VM work context](codex.md#vm-project-work-context)

### Reopen, end or continue work

| Action | Effect |
| --- | --- |
| **Open / Reconnect** | Attach to the existing run |
| **End Session** | Confirm termination of the displayed run for all its attached clients |
| **Continue** | Confirm a new process for the same project and saved conversation |
| **Open Running Session** | Open the newer active run from an old window |
| **Host Terminal** | Open a separate shell in the run's recorded host directory |
| **More → Details** | Inspect full paths, run and terminal identities, process state or the tmux attach command |

Closing a run window, reloading or stopping AXV detaches while tmux keeps the run alive. Ending a run leaves accepted VM tasks under their own execution. Reconnect targets the same run and drops unsent input. **Continue** restores conversation context in a fresh process using the original provider history and account. If history or the VM project is unavailable, AXV shows the reason. Use **New Session** to create another conversation.

If a start or continuation loses its response, choose **Resolve Start** or **Resolve Continue** to check the original request before starting again. The recovery attempt stays bound to its database and browser client across reload. Review any recovery error or changed state before retrying. Switching away during continuation checks cancels its confirmation. If the manager reports completion after you leave, open the exact run from **Sessions**.

### Read activity and organize Sessions

**All Sessions** returns to the host manager. Choose **Unarchived** or **Archived** to browse. **Rename**, **Archive** and **Unarchive** update persistent session records while active work continues.

Activity describes the provider's work separately from the process state. **Activity unavailable** marks a missing observation. **Outdated** marks retained data after a failed refresh. **Action needed** lists pending requests and undismissed failures or interruptions in the current scope, including archived sessions. Use **Open** to answer in the exact run. **Dismiss alert** clears only the displayed outcome's marker. When the last item is handled, Sessions returns to its previous mode. [Work notifications](#work-notifications) can retain and deliver related events independently.

Use the history controls to browse output and **Live** to return to current output. On a metadata conflict, review the current values before retrying. [Codex architecture](codex.md) · [Settings reload](startup.md#agent-settings-and-background-lifecycle)
