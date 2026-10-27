import type { PointerEvent } from 'react';
import { inWorkspace, overviewTransform, visibleBounds } from '../model';
import type {
  AppDefinition,
  Bounds,
  DesktopAPI,
  DesktopWindow,
  Size,
} from '../types';
import { createWindowMenu } from './menus';
import { UnavailableApplication } from './applications';
import { WindowControlIcon } from '../controls';

interface WindowFrameProps {
  win: DesktopWindow;
  app: AppDefinition;
  index: number;
  api: DesktopAPI;
  viewport: Size;
  workArea: Bounds;
  mobile: boolean;
  overview: boolean;
  inputBlocked: boolean;
  preview?: Bounds;
  activateWindow: (id: string) => void;
  beginWindowGesture: (
    event: PointerEvent,
    win: DesktopWindow,
    edge?: string,
  ) => void;
  toggleShade: (id: string) => void;
  toggleZoom: (id: string) => void;
}

/** Stable keys and mounted children preserve drafts, scroll and app state across visibility changes. */
export function WindowFrame({
  win,
  app,
  index,
  api,
  viewport,
  workArea,
  mobile,
  overview,
  inputBlocked,
  preview,
  activateWindow,
  beginWindowGesture,
  toggleShade,
  toggleZoom,
}: WindowFrameProps) {
  const bounds =
    mobile || win.zoomed
      ? workArea
      : visibleBounds(preview ?? win, workArea, app.minSize);
  const active = api.activeWindowId === win.id;
  const visible = inWorkspace(win, api.currentWorkspaceId) && !win.minimized;
  const hidden = !overview && (!visible || (mobile && !active));
  const shaded = win.shaded && !overview;
  const Content = app.component;
  const menu = (point: { x: number; y: number }) =>
    api.contextMenu(createWindowMenu(win, api), point, {
      title: 'Window',
      pin: { kind: 'window', ownerWindowId: win.id, title: 'Window' },
    });
  return (
    <section
      data-window-id={win.id}
      data-app-id={win.appId}
      data-workspace-id={win.workspaceId}
      data-minimized={win.minimized || undefined}
      role="region"
      aria-label={win.title}
      tabIndex={-1}
      inert={hidden || undefined}
      aria-hidden={hidden || undefined}
      className={`ns-window ${active ? 'is-active' : ''} ${shaded ? 'is-shaded' : ''} ${app.kind === 'panel' ? 'is-panel' : ''}`}
      style={{
        left: bounds.x,
        top: bounds.y,
        width: bounds.width,
        height: shaded
          ? 'calc(var(--ns-titlebar-height) + 2px)'
          : bounds.height,
        zIndex: (overview ? 400 : 100) + index,
        // Keeping layout boxes alive also preserves native control scroll state
        // in Firefox, which display:none can discard even with stable React keys.
        visibility: hidden ? 'hidden' : undefined,
        pointerEvents: hidden ? 'none' : undefined,
        transform: overview
          ? overviewTransform(bounds, index, api.windows.length, viewport)
          : undefined,
      }}
      onPointerDownCapture={() => {
        if (!active && !overview) activateWindow(win.id);
      }}
    >
      <div
        className="ns-window-titlebar"
        onPointerDown={(event) => {
          if (!(event.target as HTMLElement).closest('button'))
            beginWindowGesture(event, win);
        }}
        onDoubleClick={(event) => {
          if (!(event.target as HTMLElement).closest('button'))
            toggleShade(win.id);
        }}
        onContextMenu={(event) => {
          event.preventDefault();
          menu({ x: event.clientX, y: event.clientY });
        }}
      >
        <button
          type="button"
          className="ns-window-control ns-minimize"
          aria-label={`Miniaturize ${win.title}`}
          title={`Miniaturize ${win.title}`}
          onClick={() => api.minimizeWindow(win.id)}
        >
          <WindowControlIcon icon="minimize" />
        </button>
        <button
          type="button"
          className="ns-window-menu"
          aria-label={`Window menu for ${win.title}`}
          title={`Window menu for ${win.title}`}
          aria-haspopup="menu"
          onClick={(event) => {
            event.currentTarget.focus();
            const rect = event.currentTarget.getBoundingClientRect();
            menu({ x: rect.left, y: rect.bottom });
          }}
        >
          <WindowControlIcon icon="menu" />
        </button>
        <span className="ns-window-title">{win.title}</span>
        <button
          type="button"
          className="ns-window-control ns-shade"
          aria-label={`${win.shaded ? 'Unshade' : 'Shade'} ${win.title}`}
          title={`${win.shaded ? 'Unshade' : 'Shade'} ${win.title}`}
          onClick={() => toggleShade(win.id)}
        >
          <WindowControlIcon icon={win.shaded ? 'unshade' : 'shade'} />
        </button>
        <button
          type="button"
          className="ns-window-control ns-zoom"
          aria-label={`${win.zoomed ? 'Restore' : 'Maximize'} ${win.title}`}
          title={`${win.zoomed ? 'Restore' : 'Maximize'} ${win.title}`}
          onClick={() => toggleZoom(win.id)}
        >
          <WindowControlIcon icon={win.zoomed ? 'restore' : 'maximize'} />
        </button>
        <button
          type="button"
          className="ns-window-control ns-close"
          aria-label={`Close ${win.title}`}
          title={`Close ${win.title}`}
          onClick={(event) => {
            event.currentTarget.focus({ preventScroll: true });
            void api.closeWindow(win.id);
          }}
        >
          <WindowControlIcon icon="close" />
        </button>
      </div>
      <div
        className="ns-window-content"
        inert={shaded || undefined}
        aria-hidden={shaded || undefined}
        style={{
          // Reserve the unshaded body size at the current control size so
          // shading does not collapse app layout or reset nested scroll areas.
          flex: '0 0 auto',
          height: `max(0px, calc(${bounds.height}px - var(--ns-titlebar-height) - 2px))`,
          visibility: hidden || shaded ? 'hidden' : undefined,
          pointerEvents: shaded ? 'none' : undefined,
        }}
      >
        {app.unavailable !== undefined ? (
          <UnavailableApplication message={app.unavailable} />
        ) : (
          <Content
            window={win}
            desktop={api}
            visible={!hidden && !shaded}
            interactive={
              active && !hidden && !shaded && !overview && !inputBlocked
            }
          />
        )}
      </div>
      {!win.shaded && !mobile && !overview && (
        <>
          <div className="ns-resizebar" aria-hidden="true" />
          {['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw'].map((edge) => (
            <div
              key={edge}
              className={`ns-resize ns-resize-${edge}`}
              data-resize={edge}
              aria-hidden="true"
              onPointerDown={(event) => beginWindowGesture(event, win, edge)}
            />
          ))}
        </>
      )}
      {overview && (
        <button
          type="button"
          className="ns-overview-pick"
          aria-label={`Switch to ${win.title}`}
          onClick={() => api.focusWindow(win.id)}
        />
      )}
    </section>
  );
}
