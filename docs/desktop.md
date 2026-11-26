# Desktop and Host Terminal architecture

This reference owns application registration, lazy view loading, mounted window identity, menus/geometry, layout persistence, installed extensions host integration and Host Terminal lifetimes. Read [ownership and dependencies](architecture.md#ownership-and-dependencies) first. [Design](design.md) owns visual/focus rules; [Desktop API](api/desktop.md) and [Host Terminal API](api/host-terminal.md) own serialized payloads and transport.

## Implementation map

| Change | Start here |
| --- | --- |
| Public Nextstep API & dashboard registry | [`packages/nextstep/src/types.ts`](../packages/nextstep/src/types.ts), [`index.ts`](../packages/nextstep/src/index.ts), [`apps/dashboard/src/app/registry.ts`](../apps/dashboard/src/app/registry.ts) |
| Window, workspace & layout model | [`useDesktopController.ts`](../packages/nextstep/src/desktop/useDesktopController.ts), [`model.ts`](../packages/nextstep/src/model.ts), [`WindowFrame.tsx`](../packages/nextstep/src/desktop/WindowFrame.tsx) |
| Menus & window-owned commands | [`menus.ts`](../packages/nextstep/src/desktop/menus.ts), [`MenuPanels.tsx`](../packages/nextstep/src/desktop/MenuPanels.tsx), [`window-commands.ts`](../apps/dashboard/src/shared/desktop/window-commands.ts) |
| Lazy window loading & startup | [`entry.ts`](../apps/dashboard/src/entry.ts), [`defer-window.tsx`](../apps/dashboard/src/shared/desktop/defer-window.tsx) |
| Installed extensions host & broker | [`features/extensions/registry.ts`](../apps/dashboard/src/features/extensions/registry.ts), [`ExtensionWindow.tsx`](../apps/dashboard/src/features/extensions/ExtensionWindow.tsx), [`runtime.ts`](../apps/dashboard/src/features/extensions/runtime.ts) |
| Client layout & session probe | [`useLayoutPersistence.ts`](../apps/dashboard/src/app/useLayoutPersistence.ts), [`layout-persistence.ts`](../apps/dashboard/src/app/layout-persistence.ts), [`useDesktopSession.ts`](../apps/dashboard/src/app/useDesktopSession.ts), [`storage/settings.go`](../apps/server/internal/storage/settings.go) |
| Host Terminal & clipboard paste | [`features/terminal`](../apps/dashboard/src/features/terminal), [`shared/terminal`](../apps/dashboard/src/shared/terminal), [`internal/terminal`](../apps/server/internal/terminal), [`internal/nativepty`](../apps/server/internal/nativepty) |

Key cross-layer E2E specs: [`desktop.spec.ts`](../tests/desktop.spec.ts), [`work-area.spec.ts`](../tests/work-area.spec.ts), [`project-files.spec.ts`](../tests/project-files.spec.ts), [`terminal.spec.ts`](../tests/terminal.spec.ts), [`extensions.spec.ts`](../tests/extensions.spec.ts), [`feature-loading.spec.ts`](../tests/feature-loading.spec.ts).

## Register an application

1. Keep `AppDefinition`, views, styles and tests under `apps/dashboard/src/features/<feature>` (see [`appearance/definition.ts`](../apps/dashboard/src/features/appearance/definition.ts) or [`terminal/definition.ts`](../apps/dashboard/src/features/terminal/definition.ts)).
2. Import generic controls/types only from `@cofy-x/axv-nextstep`; features never import composition (`app/`) or other features' internal startup wiring.
3. Add the definition to [`registry.ts`](../apps/dashboard/src/app/registry.ts). Nextstep uses generic host callbacks/capabilities and never hard-codes AXV app IDs.

Key registration invariants:

- `launchable`, `defaultPinned` (default `true` for fresh Docks only), `defaultDesktop`, `initialPlacement: 'beside-active'` and `DesktopProps.initialDesktopApplications` / `initialDockOrder` apply only when creating or resetting a layout; restored layouts retain saved user choices.
- Nextstep reserves the desktop icon prefix `application:` for generated app launchers; host-supplied icons (such as VM projections) keep their own namespaces.
- Stable app IDs (e.g. `remote-access` for Tailscale, `cloudflare`, `notifications`, `applications`) must remain unchanged so saved windows, Dock pins and shortcuts keep resolving.

## Application content loading

- [`index.html`](../apps/dashboard/index.html) owns critical startup styles/status panel; [`entry.ts`](../apps/dashboard/src/entry.ts) loads the main bundle asynchronously with explicit Reload on failure; [`App.tsx`](../apps/dashboard/src/app/App.tsx) offers Retry on failed bootstrap without resetting state.
- Feature definitions, launch callbacks, window keys and menus stay synchronous. Heavy views wrap their component once at module scope with [`deferWindow`](../apps/dashboard/src/shared/desktop/defer-window.tsx).
- Restored views load when presented; once mounted, windows stay mounted across workspace/shade/minimize transitions with normal inertness rules.
- Each deferred view shares one in-flight module promise and cache. Closing a pending window unsubscribes it so late resolution cannot reopen it; Retry only reloads unmounted content and never reloads the desktop or clears drafts. See [bundle budgets](development.md#dashboard-loading-and-performance).

## Installed extensions

- Built-ins remain compiled React features; installed packages register under `extension.<package-id>` in [`features/extensions/registry.ts`](../apps/dashboard/src/features/extensions/registry.ts) and cannot override built-in IDs. Nextstep owns no extension state or API calls.
- [`ExtensionWindow.tsx`](../apps/dashboard/src/features/extensions/ExtensionWindow.tsx) renders packages inside `sandbox="allow-scripts"` iframes (opaque origin, no `allow-same-origin`, no workstation credentials/session IDs) and transfers a dedicated `MessagePort` via a nonce handshake. Navigation, timeout, installation change or window close invalidates the port and client generation.
- [`runtime.ts`](../apps/dashboard/src/features/extensions/runtime.ts) owns explicit save snapshots, close guards, per-tab recovery claims ([`shared/drafts/session.ts`](../apps/dashboard/src/shared/drafts/session.ts)) scoped to the extension store UUID, and rejected-draft protection (blocking save/close/install changes until fixed or discarded, with in-memory JSON export).
- Upgrading, disabling or uninstalling suspends the old port/iframe while keeping the trusted window host and draft open until explicit Reload; replacement stores retain seen metadata for open windows so drafts stay exportable. Package format, CSP and RPC contracts belong to [Extensions API](api/extensions.md).

## Window identity, menus, and geometry

- **Identity & visibility:** `windowKey(params)` defines per-app window identity; opening matching params restores the existing window across workspaces. All windows remain in one React tree. Hidden content is inert (`AppWindowProps.visible` controls observation/presentation; `interactive` controls input/focus). Use `beforeClose(windowId, guard)` for async close guards and `DesktopProps.statusOverlay` for host-owned notices (reserving height without rewriting saved geometry).
- **Menus & workspaces:** The top-bar **Workspace** button (accessible name **Workspace menu**) owns desktop commands; the application menu renders only when the active window supplies visible groups (`applicationMenu !== false`). Owner-bound commands capture window and resource identity when opened. [Dock](../packages/nextstep/src/desktop/Dock.tsx) keeps its add control outside `.ns-dock-scroll`, separates touch/pen scrolling from drag-reorder, and prevents touch double-tap activation from clicking into the newly opened window.
- **Geometry & desktop icons:** [`desktopLayoutMode`](../packages/nextstep/src/model.ts) drives wide/compact/short-height layout and work-area insets (64 px top inset when menu row present or width ≥ 600 px; 30 px on narrow windows without app menus; dynamic reservations for Dock and visible miniwindow strip) without mutating saved normal bounds or `restoreBounds`. Wide and compact layouts store independent icon positions. VM icons bind `(hostId, machineId)` and close/remove on verified inventory deletion while preserving last known state on offline errors.

## Persistent layout and recovery

- `DesktopLayout` stores presentation and serializable resource references (`DesktopWindow.viewState`) — never drafts, terminals, scrollback or process state ([Desktop API](api/desktop.md)). Use the functional form of `updateWindowState` during reconciliation to avoid overwriting newer navigation.
- [`useLayoutPersistence.ts`](../apps/dashboard/src/app/useLayoutPersistence.ts) writes layouts per browser client ID through a debounced serialized queue (coalescing pending writes, preserving newer drafts on failure, suspending writes on unknown layout versions until explicit reset).
- After bootstrap, [`shared/api/desktop.ts`](../apps/dashboard/src/shared/api/desktop.ts) binds once to the returned `databaseId`. A `database_changed` rejection halts further desktop transport and layout flushes while preserving mounted windows and drafts; the page never silently re-bootstraps into a replacement store.
- [`useDesktopSession.ts`](../apps/dashboard/src/app/useDesktopSession.ts) probes `/api/health` on focus, visibility, retry and timer (skipping overlapping ticks; latest probe owns status).
- Missing app definitions stay rendered as inert unavailable placeholders with original IDs/params/shortcuts so catalog failures never discard layout state. Installed browser windows follow the same client-ID rules as tabs ([browser recovery](storage.md#browser-recovery)).

## Terminal sessions

- `features/terminal` owns one xterm.js instance, FitAddon, resize observer and WebSocket per window. Availability is checked alongside module loading; the socket opens only after fitting initial dimensions. `interactive` blurs and suspends input when hidden.
- Connection loss, close confirmation or desktop shutdown ends the shell explicitly (`http.Server.Shutdown` does not close hijacked WebSockets); reloading or **New Session** always starts a fresh shell.
- Server [`terminal.Manager`](../apps/server/internal/terminal) validates local/origin access, enforces capacity and bridges WebSockets to platform PTY adapters. Shared [`nativepty`](../apps/server/internal/nativepty) provides pollable nonblocking PTY masters for Host Terminal, Codex attachments and Guest Terminal. Exit metadata logs to sanitized diagnostics after publishing completion.

### Clipboard input

Host Terminal, Guest Terminal and Codex share [`use-terminal-paste.tsx`](../apps/dashboard/src/shared/terminal/use-terminal-paste.tsx) and [`terminal-paste.ts`](../apps/dashboard/src/shared/terminal/terminal-paste.ts). Each paste captures `(emulator, connection, inputGeneration)`; disconnecting or replacing the connection invalidates pending clipboard reads and fallback dialogs so stale input can never reach a later session.

### Project terminals

`openTerminalWindow(desktop, projectPath?)` passes an optional host directory. The server resolves symlinks, validates directory access and sets the process working directory via OS APIs (never constructing a shell `cd` or falling back to home on failure).
