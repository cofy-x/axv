# Desktop and Host Terminal architecture

This reference owns application extension, mounted window state, layout persistence and ordinary Host Terminal lifetimes. Read [ownership and dependencies](architecture.md#ownership-and-dependencies) first. [Design](design.md) owns visual, focus and accessibility requirements; the [desktop](api/desktop.md) and [Host Terminal](api/host-terminal.md) APIs own serialized fields and transport contracts.

## Implementation map

| Change | Start here |
| --- | --- |
| Public desktop extension API | [`types.ts`](../packages/nextstep/src/types.ts) and [`index.ts`](../packages/nextstep/src/index.ts); dashboard [registry](../apps/dashboard/src/app/registry.ts) for composition |
| Window and workspace transitions | [`useDesktopController.ts`](../packages/nextstep/src/desktop/useDesktopController.ts), [`model.ts`](../packages/nextstep/src/model.ts), [`WindowFrame.tsx`](../packages/nextstep/src/desktop/WindowFrame.tsx) |
| Menus and owner-bound commands | Generic [`menus.ts`](../packages/nextstep/src/desktop/menus.ts) and [`MenuPanels.tsx`](../packages/nextstep/src/desktop/MenuPanels.tsx); dashboard [`window-commands.ts`](../apps/dashboard/src/shared/desktop/window-commands.ts) |
| Client layout persistence | [`useLayoutPersistence.ts`](../apps/dashboard/src/app/useLayoutPersistence.ts), [`layout-persistence.ts`](../apps/dashboard/src/app/layout-persistence.ts); server [`settings.go`](../apps/server/internal/storage/settings.go) |
| Host Terminal | Frontend [`terminal`](../apps/dashboard/src/features/terminal); server [`terminal`](../apps/server/internal/terminal), composed by [`server.go`](../apps/server/internal/server/server.go) |

Tests live beside these modules. For cross-layer behavior, start with [`desktop.spec.ts`](../tests/desktop.spec.ts), [`work-area.spec.ts`](../tests/work-area.spec.ts), [`files-navigation.spec.ts`](../tests/files-navigation.spec.ts) or [`terminal.spec.ts`](../tests/terminal.spec.ts). Required commands and visual checks belong to [development](development.md#verification).

## Register an application

1. Keep the `AppDefinition`, component, styles and tests under `apps/dashboard/src/features/<feature>`. Use [Preferences](../apps/dashboard/src/features/appearance/definition.ts) for a simple definition or [Terminal](../apps/dashboard/src/features/terminal/definition.ts) for resource creation and window-owned commands.
2. Import public controls and types from `@cofy-x/axv-nextstep`. The dashboard [entry point](../apps/dashboard/src/main.tsx) loads the package's `styles.css` once before [feature styles](../apps/dashboard/src/app/styles.css).
3. Add the definition to the dashboard registry. Dependencies flow from composition into the feature; features never import the registry or startup layer. Generic Nextstep behavior uses host callbacks and capabilities, never hard-coded application IDs.

`launchable` controls launcher visibility. Independently, `defaultPinned` controls whether a launchable application is pinned when creating an initial Dock; it defaults to true and does not override saved user pinning. Set it to false for utilities that should remain available through Applications and optional Dock pinning. An optional `launch(desktop)` selects or creates a resource before opening its window. Use `kind: 'panel'` for non-modal inspectors and tools. Package naming follows [product identity](product.md#identity).

Tailscale is a launchable singleton utility panel. Its [feature definition](../apps/dashboard/src/features/tailscale/definition.ts) supplies the view and identity; dashboard composition supplies its system desktop icon, and the existing Applications menu, optional Dock pinning and Clip shortcuts supply launch access. Preferences also opens the same panel. The display name is **Tailscale**; its stable application ID remains `remote-access`, preserving saved windows, Dock pins and Clip shortcuts. Connection queries and local enable/disable actions belong to the feature panel, and Nextstep owns no Tailscale state or API calls. [Remote desktop usage](usage.md#remote-desktop) owns its setup and shutdown workflow.

## Window identity and visibility

`windowKey(params)` defines identity within an application. Preferences uses a singleton key and Files an anchor directory ID. [Text Editor](../apps/dashboard/src/features/notes/definition.ts) uses a file ID for ordinary opens and a distinct draft ID for recovered drafts or **New View of Document**. Paths and display names are not document identities. Opening an existing identity restores and focuses its window, switching workspaces when needed. File browser navigation updates view state without changing the anchor; using **Open** on a directory opens or activates the window identified by that directory.

Use `DesktopAPI` for desktop actions and `beforeClose(windowId, guard)` for an asynchronous close guard with cleanup. Feature commands register against a live window ID in the dashboard's typed command registry and unregister on close.

All windows retain stable identity in the same React tree. Folded, minimized and off-workspace content stays mounted, preserving drafts, selection and scrolling, but cannot receive pointer or keyboard input. Features use `AppWindowProps.interactive` to suspend input and focus behavior without disposing their content. Modal dialogs constrain focus and block conflicting workspace actions; Inspector and fixed menus remain non-modal.

## Menus and workspaces

`MenuGroup` and `MenuItem` describe nested items, checked/disabled state, shortcuts and actions. Fixed menus persist descriptors containing their kind, menu group and optional owner window ID, never callback closures. Actions resolve against the live owner at execution time. Changing the active window cannot retarget a fixed menu; closing its owner invalidates it.

Stable workspace IDs have editable names. Each workspace owns Clip shortcuts, stacking order and an active window. A window belongs to one home workspace and may be sticky across workspaces. Dock state represents applications and stores pinning and order; live windows supply running state and the window chooser. Minimized windows have separate restore icons.

## Geometry and desktop icons

Moving, resizing, maximizing and narrow-screen fitting use one computed work area. The miniwindow strip reserves space only when the current workspace has visible minimized windows, including sticky windows. The Dock reserves its own space. Workspace or shelf changes adjust displayed bounds without overwriting normal geometry or `restoreBounds`.

Desktop icons use the same shell bounds. Default placement and arrangement fill columns from the right; drag limits reserve the menu, Dock and visible miniwindow strip. Compact bounds reserve the top menu/Clip and bottom Dock instead of the wide-screen Dock strip. Wide and compact layouts retain independent icon positions. Temporary fitting moves displaced icons into free cells without persisting those adjustments. A full grid adds rows reachable through the desktop canvas's own scroll area.

Dashboard composition combines filesystem entries, application launchers and feature-owned VM projections. VM icon IDs and Inspector targets bind the host and machine IDs, while names remain labels. Derived VM icons use layout positions keyed by that identity without copying machine records into desktop storage. A confirmed inventory removes deleted targets; connection failures retain the last complete inventory as unavailable. Inventory snapshots must belong to one verified host connection generation.

Generic `DesktopIcon` status text and tone provide resource state without application-specific branches in Nextstep. Its `trashable` capability is opt-in: only eligible virtual filesystem entries route to Recycler or keyboard deletion. System and VM selections have their own dashboard menu actions and cannot fall through to file operations. Opening a bound VM restores its existing Inspector across workspaces; a missing or mismatched target remains unavailable rather than rebinding the window.

## Persistent layout and recovery

`DesktopLayout` stores desktop presentation and resource references. Serializable `DesktopWindow.viewState` holds small view settings such as a column path or pinned Inspector target. Layouts contain no document drafts, terminal instances, scrollback or process state. The [desktop API](api/desktop.md) owns the serialized format and version.

`updateWindowState` accepts a patch or a pure function of the latest queued view state. Reconciliation uses the functional form so a delayed effect cannot overwrite newer navigation with an older render's snapshot. File browsers repair paths after metadata changes, preserving the current folder by ID after moves or recovering its closest surviving ancestor after deletion. Selection and scrolling do not rewrite the location.

The dashboard saves layouts with a stable browser client ID through a serialized queue and short debounce. Pending changes coalesce before writing; pointer previews do not write every frame. A failed request cannot replace a newer pending layout. Failures remain visible and retryable. Unknown layout versions retain the server record and suspend writes until explicit reset. Invalid fields and missing references are repaired independently of virtual documents; viewport fitting preserves usable desktop geometry.

Preferences are shared by the local user; layouts are isolated by browser client ID. Tabs sharing that identity share the saved layout, so the last successful writer wins. Recovery draft IDs remain distinct. The [storage reference](storage.md) owns document revisions and browser recovery; layout repair never resets files.

## Terminal sessions

`features/terminal` owns one xterm.js instance, FitAddon, WebSocket and resize observer per window. Its controller owns startup, byte queues, sizing and explicit restart after exit or failure. Terminal commands use the window-owned registry, so a fixed menu continues to address its original terminal.

Folding, minimizing or switching workspaces preserves the emulator and shell. `interactive` suspends input and blurs the emulator without disconnecting it; hidden content cannot consume keys or take focus from menus and dialogs. Closing a live window uses the shared confirmation dialog, then disposes the emulator and connection. Connection loss ends the shell; restoring a window after reload or choosing New Session creates a fresh shell rather than reconnecting a process.

The Go service owns `terminal.Manager` independently of storage. It validates local access and Origin, enforces capacity, and bridges WebSocket bytes to a host shell. Platform adapters in the server terminal package own PTY creation, resizing, exit detection and process cleanup; dependency versions belong to the [Go module](../apps/server/go.mod).

The shared [`nativepty`](../apps/server/internal/nativepty) adapter creates pollable PTY masters for Host Terminal, Codex attachments and Guest Terminal. It preserves nonblocking I/O through `SyscallConn`; each feature retains its own process waiting, termination and detach rules.

On macOS/Linux, the process waiter retains the exit code, signal, duration and requested-close state in [service diagnostics](startup.md#configuration-and-storage), without terminal streams or environment values. It publishes process completion before logging so diagnostics cannot delay session cleanup.

Service shutdown closes ordinary Terminal connections and reaps their shells explicitly: `http.Server.Shutdown` alone does not close upgraded WebSockets. [Codex attachments](codex.md#terminal-attachments) have a separate lifetime. Framing and same-origin checks belong to the [Host Terminal API](api/host-terminal.md); the development proxy must preserve browser Host and Origin.

### Project terminals

`openTerminalWindow(desktop, projectPath?)` accepts an optional real host directory. The initial path is serializable window metadata. The server resolves symlinks, checks directory access and assigns the canonical path through the process working-directory API. It never constructs a shell `cd` command or falls back to home after validation fails. HOME remains the service user's home.

New Session and restored windows start fresh shells in the selected initial directory, not a later `cd` location. Host paths remain separate from virtual document IDs. The generic desktop package supplies frames and controls; shell policy, terminal dependencies and host connections belong to the feature and server. See the [Host Terminal API](api/host-terminal.md) for protocol details.
