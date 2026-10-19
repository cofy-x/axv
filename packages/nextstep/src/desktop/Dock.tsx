import { useEffect, useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import { PixelIcon } from '../controls';
import type {
  AppDefinition,
  DesktopAPI,
  DesktopLayout,
  DesktopProps,
  MenuItem,
  Point,
} from '../types';
import { usePointerGesture } from './usePointerGesture';

interface DockProps {
  props: DesktopProps;
  layout: DesktopLayout;
  api: DesktopAPI;
  setLayout: Dispatch<SetStateAction<DesktopLayout>>;
}

/** Application launchers live here; document miniwindows have their own shelf. */
export function Dock({ props, layout, api, setLayout }: DockProps) {
  const [now, setNow] = useState(() => new Date());
  const [dragged, setDragged] = useState<string | null>(null);
  const beginGesture = usePointerGesture();
  useEffect(() => {
    const timer = globalThis.setInterval(() => setNow(new Date()), 1000);
    return () => globalThis.clearInterval(timer);
  }, []);
  const entries = layout.dock.filter(
    (entry) =>
      entry.pinned || layout.windows.some((win) => win.appId === entry.appId),
  );
  const apps = entries
    .map((entry) => props.apps.find((app) => app.id === entry.appId))
    .filter((app): app is AppDefinition => !!app);
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
      {
        label: 'Keep in Dock',
        checked: pinned,
        action: () => setPinned(app.id, !pinned),
      },
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
      {
        label: 'Add to Clip',
        action: () =>
          api.addClipShortcut({ appId: app.id, title: app.title, params: {} }),
      },
    ];
  };
  const pointFor = (element: HTMLElement): Point => {
    const rect = element.getBoundingClientRect();
    return { x: rect.left - 220, y: rect.top };
  };
  return (
    <aside className="ns-dock" aria-label="Application Dock">
      <button
        type="button"
        className="ns-dock-tile ns-clock"
        aria-label="Clock and date"
        title={now.toLocaleString()}
        onClick={() =>
          void api.dialog({
            title: 'Clock',
            message: now.toLocaleString(undefined, {
              dateStyle: 'full',
              timeStyle: 'short',
            }),
            buttons: [{ label: 'OK', value: 'ok', default: true }],
          })
        }
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
      {apps.map((app) => {
        const windows = layout.windows.filter((win) => win.appId === app.id);
        const running = windows.length > 0;
        const active = windows.some((win) => win.id === api.activeWindowId);
        return (
          <div
            key={app.id}
            className={`ns-dock-entry ${dragged === app.id ? 'is-dragging' : ''}`}
            data-dock-app={app.id}
          >
            <button
              type="button"
              className={`ns-dock-tile ${active ? 'is-active' : ''}`}
              aria-label={`${app.title}${running ? ` · ${windows.length} running window${windows.length > 1 ? 's' : ''}` : ''}`}
              title={app.title}
              onPointerDown={(event) => {
                if (event.button !== 0) return;
                const button = event.currentTarget;
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
                    if (moved && !cancelled && target) reorder(app.id, target);
                    if (moved) button.dataset.suppressClick = 'true';
                  },
                );
              }}
              onClick={(event) => {
                const el = event.currentTarget;
                el.focus({ preventScroll: true });
                if (el.dataset.suppressClick) {
                  delete el.dataset.suppressClick;
                  return;
                }
                if (windows.length > 1)
                  api.contextMenu(appMenu(app), pointFor(el), {
                    title: app.title,
                  });
                else api.launchApp(app.id);
              }}
              onContextMenu={(event) => {
                event.preventDefault();
                event.currentTarget.focus();
                api.contextMenu(appMenu(app), pointFor(event.currentTarget), {
                  title: app.title,
                });
              }}
            >
              <PixelIcon name={app.icon} size={42} />
              <span className="ns-dock-running" aria-hidden="true">
                {running ? '●' : ''}
              </span>
              {windows.length > 1 && (
                <span className="ns-dock-count" aria-hidden="true">
                  {windows.length}
                </span>
              )}
            </button>
            <button
              type="button"
              className="ns-dock-options"
              aria-label={`${app.title} Dock options`}
              title={`${app.title} Dock options`}
              aria-haspopup="menu"
              onClick={(event) =>
                api.contextMenu(appMenu(app), pointFor(event.currentTarget), {
                  title: app.title,
                })
              }
            >
              ▾
            </button>
          </div>
        );
      })}
      <button
        type="button"
        className="ns-dock-add"
        aria-label="Add application to Dock"
        title="Add application to Dock"
        aria-haspopup="menu"
        onClick={(event) =>
          api.contextMenu(
            props.apps
              .filter((app) => app.launchable !== false)
              .map((app) => ({
                label: app.title,
                checked: layout.dock.some(
                  (entry) => entry.appId === app.id && entry.pinned,
                ),
                action: () => setPinned(app.id, true),
              })),
            pointFor(event.currentTarget),
            { title: 'Add to Dock' },
          )
        }
      >
        +
      </button>
    </aside>
  );
}
