# AXV visual and interaction design

Use this guide for recurring UI decisions. [Usage](usage.md) owns visible workflows, [desktop architecture](desktop.md) owns implementation, and [product direction](product.md) owns identity and goals.

## Brand and product voice

Follow [product identity](product.md#identity) for names and wording. Welcome, About and page metadata describe the product's enduring purpose. Keep availability and validation in [project status](../README.md#project-status).

Use the [AX monogram](../apps/dashboard/public/favicon.svg) for installation icons, the favicon, Welcome, About and the **Workspace menu** button. Keep that button's accessible name. Dashboard composition supplies the branding to generic desktop controls. Match the standalone browser theme color to the status bar and retain the native window frame.

## Design sources and goals

[Window Maker](https://github.com/window-maker/wmaker/tree/535288cec55e99371f0996da9a12c1744a8a0f62) informs window chrome, Dock and workspaces. [GWorkspace](https://github.com/gnustep/apps-gworkspace/tree/6c49cd9071ffca4dd385bb3184a0ceb087a9b09c) informs navigation and Inspector. [GNUstep GUI](https://github.com/gnustep/libs-gui/tree/fdc5cfe833f23ae1fd50e9ba6ce389c50f97c30f) informs compact raised and recessed controls. These are visual references. Follow [asset rules](../AGENTS.md#repository-rules) for reuse.

Preserve square gray controls, crisp bevels and overlapping windows. Prioritize readability, Unicode, keyboard access, touch and modern display scales when adapting a historical detail.

## Sources of truth

Reuse shared controls and tokens. Keep feature styles local. When a primitive serves multiple applications, define it in the shared system and demonstrate it in Component Gallery.

| Concern | Source |
| --- | --- |
| Tokens, window chrome and responsive styles | [Nextstep styles](../packages/nextstep/src/styles.css) |
| Control markup and generic icons | [Controls](../packages/nextstep/src/controls.tsx) |
| Window titles, folding and visibility | [WindowFrame](../packages/nextstep/src/desktop/WindowFrame.tsx) |
| Dock, workspace controls and menus | [Desktop components](../packages/nextstep/src/desktop) |
| Application layout, status and errors | [Application styles](../apps/dashboard/src/shared/styles/apps.css) |
| Interactive examples | [Component Gallery](../apps/dashboard/src/features/gallery/Gallery.tsx) |

## Visual rules

- Use neutral surfaces, thin outlines and hard bevels. Raise actionable controls and recess editable areas. Match related window content to its shared surface.
- Keep dark selection and a separate focus outline. Active titles are dark with white text. Use text or a mark alongside meaningful color. Keep wallpaper behind readable windows and icon labels.
- Use shared type sizes, ordinary weight for labels and short, stronger section headings. Reserve monospace for paths, code and aligned data. Let text wrap at narrow widths.
- Use icons that identify an application, a state or a familiar action. Keep details and diagnostics text-first when the title already identifies the panel. Give icon-only controls accessible names, tooltips and touch targets. Check thin edges at integer and fractional display scales.
- Align related actions and give each decision point one primary action near its target. Let action groups wrap together. Use definition lists for one object's fields and tables for records users compare.

## Window and control presentation

Use the shared title bar, optional toolbar, scrolling content and optional status bar. Keep resize controls available on resizable desktop windows. Minimize belongs on the left and close on the right. Folding and maximizing are separate actions.

| State | Presentation |
| --- | --- |
| Available | Clear label and shared control treatment |
| Pressed, selected or checked | Visible bevel change, highlight or mark |
| Keyboard focus | Visible outline distinguishable from selection |
| Default dialog action | Shared stronger border and label |
| Disabled | Muted, readable label |
| Error or unavailable | Explanation beside an available recovery action |

Use shared Tabs to switch content and ButtonGroup for related modes or filters. Preserve work and keep focus on a visible control during a switch. Keep application registration and lazy loading with the [owning feature](desktop.md#register-an-application).

Give startup a stable frame, a named loading stage and a useful failure state. Report actual progress and enter the desktop as soon as it is ready. Load a view in its existing window, with a local **Retry** on failure. Preserve other windows and drafts. Keep unavailable commands disabled until their view registers them. [Recovery during updates](usage.md#installed-window-and-updates)

### Settings and disclosures

- Use a compact heading and thin divider to group related settings. Keep field labels and fieldset semantics, with the divider clear of the heading text. Use the window's available width to decide when fields stack. Keep form actions reachable while settings scroll.
- Use native `details` and the shared `ns-disclosure` summary. The whole row toggles it. The trailing chevron shows the state and receives local hover feedback. Keep keyboard focus visible inside the row and leave space before the first field or detail.
- Keep a short path or a few facts on the surrounding surface. For longer groups of expanded controls, a subtle neutral background, inset spacing and a fine boundary can show where the settings begin. **New VM → Advanced** uses this treatment.
- Put workflow choices and their consequences in the main form. Keep optional tuning and raw technical values in disclosures. Review settings that affect execution, access or stored data before committing.

## Interaction conventions

- **Windows** retain content when folded, minimized or off-workspace. Only visible eligible content receives input or focus. Give drag, double-click and context-menu actions visible alternatives. See [desktop operations](usage.md#desktop).
- **Menus** retain their original window and resource. The application menu holds contextual tools, the Workspace menu holds desktop commands, and the title bar and Window Overview hold window actions. Keep common actions and recovery in the content window.
- **Focus** stays within a modal dialog and returns on dismissal. Inspectors remain non-modal. Keep editing shortcuts, text selection, clipboard and composition available. Menus use arrows, Enter and Escape, with a visible Back action in a narrow submenu.
- **Asynchronous updates** preserve drafts, the selection and reading position. Mark retained data stale after a failed observation. Give a late result an entry point while respecting the user's current window, workspace and action. Recover against the original target.

### Interface copy

Avoid unnecessary secondary labels and descriptions. Let the layout do the explaining.

- Use short action labels. Reserve ellipses for progress or truncation. Use **More** for secondary actions scoped to the current item. Keep execution, destructive and recovery actions named.
- Let the title identify the window. Show routine status once near the relevant action. For empty states, use a compact message and point to an existing action if the next step needs explanation.
- Explain a restriction, stale result, recovery step or effect on running work and storage where the user decides. Keep errors with their affected drafts, fields or actions.
- Use a brief local confirmation for routine success. Put durable outcomes in history. Keep failures and uncertain outcomes visible until the user can address them.
- Keep accessible form labels, status announcements and clear names for icon-only actions as the visible copy gets shorter.

## Application design boundaries

Start with a similar AXV application. Reuse its shared frame and controls, then organize the content around the task.

### Preferences and Welcome

Use one scrolling Preferences page with titled, divided sections. Group desktop actions with their settings. Keep the status area quiet until a save starts, then show its outcome. Use an accessible name that identifies the destination for short actions such as **Open** in Notifications. In Welcome, prioritize the work launchers and **Show Desktop**. Open Preferences directly at **Remote access** and put the installation guide in **More**. [Desktop workflow](usage.md#desktop)

### Virtual Machines

- **Setup** shows the next action for the detected host and keeps progress and failures visible. Review installation, storage and downloads before preparation. Offer a selectable command and a copy-and-open Terminal action when a host command is required. Keep technical diagnostics available on demand. A verified reconnect should take the existing host back to usable controls.
- **New VM** shows name, type and defaults before **Advanced**. Show Task expiry and project-volume retention before creation. Keep the create actions visible while optional settings scroll.
- **Inventory** distinguishes loading, disconnected, empty and filtered views. Put status, expiry, progress and current recovery with the selected VM. Keep its actions visible while the list scrolls. Show capacity, compatibility or provenance with resource names. Use **Details** for image IDs and checksums. Put volume actions beside their resource or selection.
- **Inspector and Activity** use disclosures for guest sessions, configuration and technical details. Reuse the same guest-session label in Inspector and Terminal. Activity links go to the original operation and preserve reading position on refresh. Confirm VM removal and permanent project-volume deletion separately. [VM workflows](usage.md#virtual-machines)

### Codex and terminals

- **Session launch** groups **New Session**, **New in Folder** and the default folder above one divider before Sessions. The folder row reveals a selectable path. Choosing or browsing a different directory belongs in **New in Folder**, with one explicit **Start Here**. Use the project name and parent path to distinguish recent projects. Preserve typed drafts and mark retained listings stale after a failed read.
- **Sessions** show the name, project and activity with one main action and a target-bound **More** menu. Use a short run reference for repeated names. Put full identities and paths in **Details**. Keep abnormal process or recovery states visible. Normal completion is quiet. Show **Action needed** only for pending work or undismissed alerts in scope. Return focus to usable controls when that view becomes empty.
- **Terminal views** use dark monospace content and shared gray chrome. Identify the execution target in the title or nearby context. Keep common actions in one compact toolbar and put full paths and runtime identifiers in **Details**. Browsing scrollback preserves input. Keep the distinction between detaching, reconnecting, ending and continuing clear at the action. [Codex](usage.md#codex) · [Host Terminal](usage.md#host-terminal) · [Guest Terminal](usage.md#guest-terminal)

### Project work

- **Project Files** keeps project identity and Tasks and Codex access visible across sections. Show the path before its actions, with a narrow Back action that restores selection. Start with read-only source and explicit editing. Save a reviewed snapshot against the original file and environment. Keep later typing, dirty drafts and undo history through view changes. Pause hidden monitoring and input. Let composition settle before leaving a field or editor. Keep failed checks and conflict recovery near the draft. [Project Files workflow](usage.md#project-files)
- **Setup and results** retain the original request and environment during recovery. Ask for an explicit open, replacement or new initialization. Keep guest authentication in Guest Terminal. Previews identify the VM and distinguish access removal from process cancellation. Backups and restores review the new volume and binding. [Project and provider boundaries](vm-platform.md#vm-projects)
- **Tasks** keep history and output visible, with an explicit command form and a return to history on narrow screens. Show the current VM near input and the original VM beside each attempt. Preserve earlier output and list position during updates. Cancel the displayed attempt. Resolve uncertain results before retrying. [Task ownership](vm-platform.md#persistent-project-tasks)

### Applications and desktop utilities

- **Application Manager** opens on the installed list. Keep installation review on demand, with identity, version and access visible before confirmation. Make **Open** or **Enable** the row action. Keep launcher visibility independent of installation and execution. Distinguish saved-data export, draft recovery and permanent deletion. [Extension workflow](usage.md#applications-and-extensions)
- **Notifications** opens on History, with separate browser and local settings. Keep delivery uncertainty, stale observations and recovery visible. Save and test credentials through explicit actions. Open original work in a separate read-only view bound to the recorded event and target. [Notification workflow](usage.md#work-notifications)
- **GitHub Review** prioritizes **Review a PR** and the inbox. Keep routine scan information in **Automation status**, with failures and quotas in its summary. [Review workflow](usage.md#github-review)
- **Remote access** keeps desktop connectivity, private access, local listeners and public reachability distinct. Pair each lamp with text and show query failures. Keep privileged setup and diagnostics local. In Cloudflare, put refresh and recovery in the relevant tab. Review publication target, address and access before submission. An access-opening retry explicitly confirms that same publication, with **Cancel** as the default. Preserve draft and request identity across project switches. Show traffic scope and unavailable data explicitly. [Remote access workflow](usage.md#remote-desktop)

## Narrow screens and accessibility

Fit windows to the usable area and restore desktop geometry at wider sizes. Keep launch, workspace and primary actions reachable as the viewport shrinks or an on-screen keyboard opens. Constrain menus and dialogs to the visual viewport. Give long text and overflow an appropriate local scroll or wrap.

Keep the active workspace, Window Overview and each Dock add control reachable. Give Window Overview a stable position and a pressed state while open. Dock tiles keep a running indicator and show a count for multiple windows. Keep minimized windows in their own strip. Desktop icons keep readable names, text status and matching actions. Touch scrolling stays separate from activation. See [desktop geometry](desktop.md#geometry-and-desktop-icons) for sizes, placement and persisted positions.

Use semantic labels, visible focus and consistent hit areas. Indicate selection, errors and availability beyond color. Respect reduced motion.

## Acceptance checks

- Review collapsed and expanded states, narrow application widths, display scale and touch using the [browser projects](../playwright.config.ts). Confirm that wrapping, scrolling and keyboard focus keep actions reachable.
- Exercise loading, empty, error, disabled and unsaved states affected by the change. Check draft, selection and scroll retention after switching views, folding or minimizing. For geometry changes, test the miniwindow strip and restored sizes.
- Follow the normal flow using pointer, keyboard and touch. Review the focus return and the original target when dismissing a menu, cancelling, recovering or retrying.
- For VM flow and copy, run the [focused VM UI review](development.md#focused-vm-ui-review). The [New VM form](../tests/machines-forms.spec.ts), [VM Inspector](../tests/machines-experience.spec.ts), [Codex manager](../tests/codex-manager.spec.ts) and [desktop](../tests/desktop.spec.ts) browser tests exercise the corresponding flows.

Record viewport, scale, browser and interaction state with review evidence. See [verification](development.md#verification) for required checks and the [documentation map](README.md#maintain-the-map) for where evidence belongs.
