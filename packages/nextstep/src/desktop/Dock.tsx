import { useEffect, useRef, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import { PixelIcon } from '../controls';
import type {
  AppDefinition,
  DesktopAPI,
  DesktopLayout,
  DesktopProps,
  MenuItem,
} from '../types';
import { usePointerGesture } from './usePointerGesture';
import { resolveApplication } from './applications';
import { showClock } from './menus';

interface DockProps {
  props: DesktopProps;
  compact: boolean;
  layout: DesktopLayout;
  api: DesktopAPI;
  setLayout: Dispatch<SetStateAction<DesktopLayout>>;
}

/** Application launchers live here. Minimized windows have their own shelf. */
export function Dock({ props, layout, api, setLayout, compact }: DockProps) {
  const [now, setNow] = useState(() => new Date());
  const [dragged, setDragged] = useState<string | null>(null);
  const suppressClick = useRef<string | null>(null);
  const beginGesture = usePointerGesture();
  useEffect(() => {
    if (compact) return;
    setNow(new Date());
    const timer = globalThis.setInterval(() => setNow(new Date()), 1000);
    return () => globalThis.clearInterval(timer);
  }, [compact]);
  const entries = layout.dock.filter(
    (entry) =>
      entry.pinned || layout.windows.some((win) => win.appId === entry.appId),
  );
  const apps = entries.map((entry) =>
    resolveApplication(props.apps, entry.appId),
  );
  const setPinned = (appId: string, pinned: boolean) =>
    setLayout((current) => ({
      ...current,
      dock: current.dock.some((entry) => entry.appId === appId)
        ? current.dock.map((entry) =>
            entry.appId === appId ? { ...entry, pinned } : entry,
          )
        : [...current.dock, { appId, pinned }],
    }));
  const reorder = (appId: string, beforeId: string) =>
    setLayout((current) => {
      const entry = current.dock.find((item) => item.appId === appId);
      if (!entry || appId === beforeId) return current;
      const dock = current.dock.filter((item) => item.appId !== appId);
      const index = dock.findIndex((item) => item.appId === beforeId);
      dock.splice(index < 0 ? dock.length : index, 0, entry);
      return { ...current, dock };
    });
  const appMenu = (app: AppDefinition): MenuItem[] => {
    const windows = layout.windows.filter((win) => win.appId === app.id);
    const index = apps.findIndex((item) => item.id === app.id);
    const pinned =
      layout.dock.find((entry) => entry.appId === app.id)?.pinned ?? false;
    const shown = api.isApplicationOnDesktop(app.id);
    const launchable = app.launchable !== false;
    return [
      {
        label: windows.length ? 'Activate Application' : 'Launch Application',
        action: () => api.launchApp(app.id),
      },
      ...windows.map((win) => ({
        label: win.title,
        checked: win.id === api.activeWindowId,
        action: () => api.focusWindow(win.id),
      })),
      { separator: true },
      ...(launchable || pinned
        ? [
            {
              label: 'Keep in Dock',
              checked: pinned,
              action: () => setPinned(app.id, !pinned),
            },
          ]
        : []),
      ...(launchable || shown
        ? [
            {
              label: 'Show on Desktop',
              checked: shown,
              action: () => api.showDesktopApplication(app.id, !shown),
            },
          ]
        : []),
      {
        label: 'Move Earlier',
        disabled: index <= 0,
        action: () => {
          if (index > 0) reorder(app.id, apps[index - 1].id);
        },
      },
      {
        label: 'Move Later',
        disabled: index === apps.length - 1,
        action: () => {
          const next = apps[index + 2];
          if (next) reorder(app.id, next.id);
          else
            setLayout((current) => ({
              ...current,
              dock: [
                ...current.dock.filter((entry) => entry.appId !== app.id),
                ...current.dock.filter((entry) => entry.appId === app.id),
              ],
            }));
        },
      },
      ...(launchable
        ? [
            {
              label: 'Add Workspace Shortcut',
              action: () =>
                api.addClipShortcut({
                  appId: app.id,
                  title: app.title,
                  params: {},
                }),
            },
          ]
        : []),
    ];
  };
  const addItems: MenuItem[] = props.apps
    .filter((app) => app.launchable !== false)
    .map((app) => ({
      label: app.title,
      checked: layout.dock.some(
        (entry) => entry.appId === app.id && entry.pinned,
      ),
      action: () => setPinned(app.id, true),
    }));
  const controls: MenuItem[] = compact
    ? [
        { label: 'Add application', children: addItems },
        { separator: true },
        ...apps.map((app) => ({ label: app.title, children: appMenu(app) })),
      ]
    : addItems;
  const controlsLabel = compact ? 'Dock menu' : 'Add application to Dock';
  const openAppMenu = (app: AppDefinition, element: HTMLElement) => {
    element.focus({ preventScroll: true });
    const rect = element.getBoundingClientRect();
    api.contextMenu(
      appMenu(app),
      { x: rect.left, y: rect.top },
      { title: app.title, align: 'end' },
    );
  };
  return (
    <aside
      className="ns-dock"
      aria-label="Application Dock"
      onPointerDownCapture={() => {
        suppressClick.current = null;
      }}
    >
      <div className="ns-dock-scroll">
        {!compact && (
          <button
            type="button"
            className="ns-dock-tile ns-clock"
            aria-label="Clock and date"
            title={now.toLocaleString(undefined, {
              hour12: props.clock24Hour === false,
            })}
            onClick={() => showClock(api, props.clock24Hour)}
          >
            <span className="ns-clock-month">
              {now.toLocaleDateString('en', { month: 'short' }).toUpperCase()}
            </span>
            <strong>{now.getDate()}</strong>
            <time>
              {now.toLocaleTimeString('en', {
                hour: '2-digit',
                minute: '2-digit',
                hour12: props.clock24Hour === false,
              })}
            </time>
          </button>
        )}
        {apps.map((app) => {
          const windows = layout.windows.filter((win) => win.appId === app.id);
          const running = windows.length > 0;
          const active = windows.some((win) => win.id === api.activeWindowId);
          return (
            <div
              key={app.id}
              className={`ns-dock-entry ${dragged === app.id ? 'is-dragging' : ''}`}
              data-dock-app={app.id}
              data-app-title={app.title}
            >
              <button
                type="button"
                className={`ns-dock-tile ${active ? 'is-active' : ''}`}
                aria-label={`${app.title}${running ? ` · ${windows.length} running window${windows.length > 1 ? 's' : ''}` : ''}`}
                aria-haspopup={windows.length > 1 ? 'menu' : undefined}
                aria-keyshortcuts="Shift+F10 ContextMenu"
                title={`${app.title}${app.unavailable !== undefined ? ' — unavailable' : ''}`}
                onPointerDown={(event) => {
                  // Touch and pen keep native panning; their options menu orders entries.
                  if (event.button !== 0 || event.pointerType !== 'mouse')
                    return;
                  const origin = { x: event.clientX, y: event.clientY };
                  let moved = false;
                  let target: string | null = null;
                  beginGesture(
                    event,
                    (point) => {
                      if (
                        Math.abs(point.x - origin.x) +
                          Math.abs(point.y - origin.y) <
                          8 &&
                        !moved
                      )
                        return;
                      moved = true;
                      setDragged(app.id);
                      const element = document
                        .elementFromPoint(point.x, point.y)
                        ?.closest<HTMLElement>('[data-dock-app]');
                      target = element?.dataset.dockApp ?? null;
                    },
                    (_, cancelled) => {
                      setDragged(null);
                      if (moved && !cancelled && target)
                        reorder(app.id, target);
                      suppressClick.current =
                        moved && !cancelled ? app.id : null;
                    },
                  );
                }}
                onClick={(event) => {
                  const el = event.currentTarget;
                  el.focus({ preventScroll: true });
                  const draggedApp = suppressClick.current;
                  suppressClick.current = null;
                  if (draggedApp === app.id && event.detail > 0) return;
                  if (windows.length > 1) openAppMenu(app, el);
                  else api.launchApp(app.id);
                }}
                onKeyDown={(event) => {
                  if (
                    event.key !== 'ContextMenu' &&
                    !(event.shiftKey && event.key === 'F10')
                  )
                    return;
                  event.preventDefault();
                  event.stopPropagation();
                  openAppMenu(app, event.currentTarget);
                }}
                onContextMenu={(event) => {
                  event.preventDefault();
                  openAppMenu(app, event.currentTarget);
                }}
              >
                <PixelIcon name={app.icon} size={34} />
                {running && (
                  <span className="ns-dock-running" aria-hidden="true" />
                )}
                {windows.length > 1 && (
                  <span className="ns-dock-count" aria-hidden="true">
                    {windows.length}
                  </span>
                )}
              </button>
            </div>
          );
        })}
      </div>
      <button
        type="button"
        className={compact ? 'ns-dock-menu' : 'ns-dock-add'}
        aria-label={controlsLabel}
        title={controlsLabel}
        aria-haspopup="menu"
        onClick={(event) => {
          event.currentTarget.focus({ preventScroll: true });
          const rect = event.currentTarget.getBoundingClientRect();
          api.contextMenu(
            controls,
            { x: rect.right, y: rect.top },
            { title: compact ? 'Dock' : 'Add to Dock', align: 'end' },
          );
        }}
      >
        {compact ? (
          <svg
            width="18"
            height="18"
            viewBox="0 0 16 16"
            fill="currentColor"
            aria-hidden="true"
            focusable="false"
          >
            <path d="M2 7h2v2H2zM7 7h2v2H7zM12 7h2v2h-2z" />
          </svg>
        ) : (
          '+'
        )}
      </button>
    </aside>
  );
}
