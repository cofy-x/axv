# AXV visual and interaction design

Use this guide for UI changes. It owns enduring presentation, interaction and accessibility rules. [Product direction](product.md) owns identity and goals; [desktop architecture](desktop.md) owns implementation; [usage](usage.md) owns action workflows.

## Brand and product voice

Use **AXV** for the visible product name and `axv` for technical identifiers. The tagline and name interpretation belong in [product identity](product.md#identity). Welcome, About and page metadata describe the enduring product purpose, without release notes, validation matrices or roadmap lists. Keep their claims consistent with [project status](../README.md#project-status), which owns those changing details.

Use direct action labels, identify the execution target and explain capability limits where they affect a choice. Product branding belongs in the dashboard and public assets; generic desktop controls remain reusable.

Keep buttons brief, usually one or two words. Use surrounding context instead of repeating object names. Put consequences in nearby guidance or confirmations, preserving execution targets and lifecycle distinctions.

Keep frequent actions visible and group secondary commands in one accessible menu. Name action menus for their view or selected target, including a short identifier when names can repeat. Use the shared icon button for familiar actions such as copy, paste and paging; keep lifecycle and destructive actions clearly named. Icon buttons retain accessible names, tooltips, focus feedback and touch hit areas.

Use short action labels without trailing ellipses, including commands that open a picker, panel or confirmation. Reserve ellipses for loading or in-progress text and actual truncation. The standalone More icon remains a menu affordance.

## Design sources and goals

[Window Maker](https://github.com/window-maker/wmaker/tree/535288cec55e99371f0996da9a12c1744a8a0f62) informs window chrome, Dock, Clip and menus. [GWorkspace](https://github.com/gnustep/apps-gworkspace/tree/6c49cd9071ffca4dd385bb3184a0ceb087a9b09c) informs column navigation and Inspector; [GNUstep GUI](https://github.com/gnustep/libs-gui/tree/fdc5cfe833f23ae1fd50e9ba6ce389c50f97c30f) informs compact raised and recessed controls. These references pin the inspected revisions. AXV bundles no native code, artwork or fonts from them; external material follows [AGENTS.md](../AGENTS.md).

Preserve the compact workstation language: dark active titles, square gray controls, crisp bevels, substantial application tiles and overlapping windows. Dock, Clip, miniwindows, fixed menus and Inspector have distinct roles. Readability, Unicode, keyboard access, touch and modern display scales take priority when adapting a historical detail.

## Sources of truth

Exact colors, font stacks, dimensions, breakpoints and states live in shared implementation. Change those definitions for a system-wide decision instead of copying values into feature CSS or this guide.

| Concern | Source |
| --- | --- |
| Tokens, frames, surfaces, controls and responsive styling | [Nextstep styles](../packages/nextstep/src/styles.css) |
| Shared control markup and generic SVG icons | [Controls](../packages/nextstep/src/controls.tsx) |
| Titles, folding, utility panels and visibility | [WindowFrame](../packages/nextstep/src/desktop/WindowFrame.tsx) |
| Dock, Clip and retained menus | [Desktop components](../packages/nextstep/src/desktop) |
| Shared application layout, status and errors | [Application styles](../apps/dashboard/src/shared/styles/apps.css) |
| Interactive examples | [Component Gallery](../apps/dashboard/src/features/gallery/Gallery.tsx) |

Reuse shared controls and tokens. Keep feature styling scoped to its application; promote a primitive used by multiple applications to the shared system and demonstrate it in Component Gallery.

## Visual rules

- **Surfaces:** Use gray hierarchy, square corners, thin dark outlines and small hard shadows. Raised surfaces have light upper/left edges and dark lower/right edges; editable areas are recessed. Use the shared neutral and bevel tokens rather than inventing equivalents.
- **Color:** Preserve dark selection and a distinct focus outline. Active titles are nearly black with white text; inactive application titles are gray, while utility panels keep their darker treatment. Use stronger color for meaning and pair status color with text or another cue. Wallpaper must leave windows and labels legible.
- **Typography:** Use the shared sans-serif UI stack and monospace where alignment matters. Keep compact bold titles and readable body text, including fallback glyphs for non-Latin content. Do not shrink text to hide overflow.
- **Icons:** Reuse the SVG system and application-tile proportions. Match silhouettes, detail and apparent size across launchers, miniwindows, menus and toolbars. Distinguish applications from their content: text files use a plain document, Text Editor adds a pencil, and Project Files adds a code panel to its folder. Sample documents use normal file icons independent of their names. Add labels where icons are ambiguous; check thin edges at integer and fractional display scales.
- **Spacing:** Keep related toolbar actions compact and aligned. Truncate long titles, wrap explanations and place lengthy errors with their recovery actions in a scrollable or wrapping content area.

## Window and control presentation

Applications use the shared title bar, optional toolbar, scrolling content and optional status bar. Keep the resize strip visible on resizable desktop windows. Minimize belongs on the left and close on the right; folding and maximizing remain separate.

| State | Presentation |
| --- | --- |
| Available | Clear label with the shared raised or inset treatment |
| Pressed | Visible surface or bevel change while engaged |
| Selected or checked | Persistent highlight or mark |
| Keyboard focus | Visible outline distinguishable from selection |
| Default dialog action | Shared stronger border and label, without hiding focus |
| Disabled | Muted but legible control and label |
| Error or unavailable | Nearby explanation and useful recovery action |

Empty, loading, failed and unsaved states are part of the design. Preserve visible work during recovery and report success only after the action succeeds.

Keep browser/server connectivity and application remote-access readiness distinct. The Tailscale utility controls AXV’s embedded remote entrypoint and does not display system VPN state. Status lamps need text labels, and a failed status query must not retain a current-looking success lamp. Connection diagnostics use an on-demand non-modal utility panel, reached through existing application launchers or Preferences. Optional Dock and Clip shortcuts follow their existing application roles.

## Interaction conventions

- **Windows:** Activation raises a window; title dragging moves it and edge dragging resizes it. Title double-click folds it. Folded, minimized and off-workspace content retains state without receiving input. Keep visible menu alternatives for these operations.
- **Available space:** Moving, resizing and maximizing use one window area. Reserve the miniwindow strip only while it contains current-workspace or sticky windows; keep Dock accessible. Temporary fitting preserves normal and restore geometry.
- **Desktop roles:** Dock represents applications and running state; miniwindows represent individual windows; Clip switches workspaces and holds shortcuts. Keep them visually distinct. Fixed menus retain their original owner even after another window activates.
- **Focus:** Modal dialogs contain focus only while open and return it on dismissal. Inspector and fixed menus remain non-modal. Inspector shows whether it follows selection or is pinned. Hidden terminals must not capture input or steal dialog focus.
- **Keyboard and touch:** Preserve editing shortcuts, clipboard, selection and composition. Menus support arrows, Enter and Escape; narrow submenus use a single panel with Back navigation. Essential actions need visible touch controls and must not depend on hover, right-click or double-click. Desktop shortcuts are listed in [usage](usage.md#desktop).

## Application design boundaries

Start from an existing application with similar tasks. Reuse its frame, menu, dialog and toolbar patterns; vary the content surface to fit the work.

- **Files and documents:** Column navigation keeps the selected path and next directory visible. Use one selection action area rather than repeating preview and footer buttons; group creation and secondary commands in menus. Editors keep save, unsaved status and explicit conflict/recovery choices visible. Inspector shows a clear target, with Open and applicable Restore actions directly available. Secondary menus retain their original object and never redirect to a later selection.
- **VM project editing:** Keep the VM project and connection state visible; full volume/root/runtime identities belong in Details. Save retains the reviewed content version and environment binding, with errors beside the draft. Directory refresh does not replace open text. Reload/reconnect that would discard an edit requires an explicit choice; recovered drafts retain their original version/binding. A hidden editor cannot receive focus or input. [Usage](usage.md#saved-versions-and-project-files) owns current actions.
- **VM project tasks:** Task windows retain their project identity independently of file editors and show commands, status, attempt history and plain-text output. Cancellation identifies the displayed attempt; retry explicitly creates another attempt after a final outcome. Unknown outcomes and failed refreshes stay visible, with retained data marked stale. Keep execution identities and requested resource limits in expandable details. Hidden views pause polling and input without stopping accepted work; unconfirmed submissions preserve their original request and environment for recovery.
- **Document exchange:** Keep saved-version selection, preview, destination and the primary action together. A task opened from a Codex run retains that exact target. Put origins, associations and full technical identifiers in expandable details; keep the selected revision, delivery uncertainty and recovery actions visible. Narrow project browsers give reading the full content width and restore directory selection on return.
- **Terminal surfaces:** Use a dark monospaced emulator inside shared gray chrome. Keep execution target and a compact initial-project label visible, with full paths and runtime identifiers available in accessible details. Keep interruption, reconnecting, continuing and ending distinct. Retain shell control keys alongside desktop navigation, and make wheel/touch scrolling browse history without inserting input.
- **Codex manager:** Keep the session list compact, with a clear start action, readable names/projects and context-bound row menus with touch access. Normal readiness belongs in the status bar; failures add explanation and repair actions. **Environment** is one independent non-modal diagnostics panel with its own scrolling. Distinguish CLI availability and terminal connectivity from model readiness.
- **Persistent-session actions:** Make detaching, ending and continuing visually distinct. End confirmations identify affected work. Continuation explains the new process and creates nothing on cancellation. Reconnection identifies existing work; recovery errors stay visible. The [usage guide](usage.md#codex) owns exact action effects.
- **Virtual Machines:** Identify the deployment machine and review storage, downloads and installation changes before confirmation. Keep blockers, progress and recovery actions visible; put successful checks and advanced diagnostics in expandable details. Preparation remains observable after reopening, and completion leads directly to VM controls. Inventory icons have readable names and textual states; actions retain their selected host and machine. Distinguish loading, offline, missing dependencies and empty or filtered inventories with useful next actions. The non-modal Inspector puts state, resources and guest access before configuration details. Preserve unfinished forms when switching views. Machine deletion and permanent volume deletion need distinct confirmations. The [usage guide](usage.md#virtual-machines) owns workflows, action labels and platform-specific behavior.

Long paths and diagnostics wrap or scroll within their content area. Session organization must keep active work visible. Terminal close/end messaging must match the distinct [host](usage.md#host-terminal), [Codex](usage.md#codex) and [guest](usage.md#virtual-machines) lifecycles.

## Narrow screens and accessibility

Fit the active window to the available area while retaining launch and workspace controls, and restore desktop geometry at wider sizes. Toolbars may wrap; menus and dialogs remain bounded; content scrolls within its own area.

Desktop icons align to the right of usable canvas, leaving launch and workspace controls accessible. [Desktop geometry](desktop.md#geometry-and-desktop-icons) owns fitting, reserved space and independent compact/wide positions. Allow two label lines, with full names in accessible labels/tooltips. Overflow scrolls inside the canvas; touch swipes over icons and empty space scroll while taps and long presses retain activation and menus.

The Virtual Machines launcher and individual VM icons use distinct original virtualization drawings. Resource icons show names and textual state, and offer target-specific actions rather than virtual-file actions. [Desktop architecture](desktop.md#geometry-and-desktop-icons) owns their identity and routing.

Follow shared responsive control sizes and hit areas. Keep primary actions and recovery options reachable without page-wide horizontal scrolling. Use semantic labels and visible focus; active, selected, disabled and error states must not rely on color alone. Respect reduced motion.

## Acceptance checks

- Review changed scenes across the browser, display-scale and touch projects in [Playwright configuration](../playwright.config.ts), using the desktop, compact and narrow scenarios in [visual tests](../tests/visual.spec.ts). Add fractional scale, browser zoom or smaller application bounds when the change affects them; do not hard-code review dimensions into layouts.
- Exercise active/inactive, folded, minimized/restored and workspace-switch states. Verify retained content, selection and scroll position. For geometry changes, cover an empty and occupied miniwindow strip, sticky windows and restoration. Include applicable empty, loading, error, disabled, selected and unsaved states.
- Complete the primary flow with pointer, keyboard and touch. Check focus order/return, owner-bound menus, cancellation, scrolling and visible alternatives to gestures. Compare actual application screenshots with Component Gallery and established applications.
- Reject unrelated rounded cards, excessive spacing, decorative soft shadows, mismatched icons, unreadable text, missing focus, hover-only actions and clipped controls.

Record viewport, scale, browser and interaction state with visual-review results. Keep screenshots/reports outside versioned source. Review behavior as well as images: a screenshot cannot prove focus return or preserved state. Commands and test isolation belong in [development](development.md#verification); update this guide only when an enduring design rule changes.
