import { useEffect, useRef } from 'react';
import type { CSSProperties } from 'react';
import { PixelIcon } from './controls';
import {
  DOCK_SIZE,
  ICON_SIZE,
  desktopLayoutMode,
  iconWorkArea,
  minimizedWindows,
  windowWorkArea,
} from './model';
import { DialogOverlay, MenuOverlay } from './overlays';
import type { DesktopProps, Point } from './types';
import { Clip } from './desktop/Clip';
import { Dock } from './desktop/Dock';
import { DesktopCanvas } from './desktop/DesktopCanvas';
import { ApplicationMenu, PinnedMenus } from './desktop/MenuPanels';
import { WindowFrame } from './desktop/WindowFrame';
import { createDesktopContextMenu, createDesktopMenus } from './desktop/menus';
import { useDesktopController } from './desktop/useDesktopController';
import { useDesktopGestures } from './desktop/useDesktopGestures';
import { useViewport } from './desktop/useViewport';
import { resolveApplication } from './desktop/applications';

export function Desktop(props: DesktopProps) {
  const pointerPosition = useRef<Point | null>(null);
  const viewport = useViewport();
  const { mobile, short, sideDock } = desktopLayoutMode(viewport);
  const controller = useDesktopController(props);
  const { layout, overview, setOverview, api, menu, dialogRequest } =
    controller;
  const desktopProps = { ...props, icons: controller.icons };
  const minimized = minimizedWindows(layout.windows, layout.currentWorkspaceId);
  const workArea = windowWorkArea(viewport, minimized.length > 0);
  const desktopItems = (ids: string[]) =>
    createDesktopContextMenu(desktopProps, api, ids);
  const gestures = useDesktopGestures({
    props: desktopProps,
    layout,
    setLayout: controller.setLayout,
    workArea,
    iconArea: iconWorkArea(viewport, minimized.length > 0),
    mobile,
    overview,
    setOverview,
    overlayActive: !!menu || !!dialogRequest,
    api,
    activateWindow: controller.activateWindow,
    focusDesktop: controller.focusDesktop,
    desktopItems,
  });
  const { groups } = createDesktopMenus(desktopProps, api, gestures.selection);
  const workspace = layout.workspaces.find(
    (item) => item.id === layout.currentWorkspaceId,
  )!;
  const focusedElements = useRef(new Map<string, HTMLElement>());
  const focusedContext = useRef({
    windowId: layout.activeWindowId,
    workspaceId: layout.currentWorkspaceId,
  });
  useEffect(() => {
    const capture = (event: FocusEvent) => {
      const target = event.target;
      if (!(target instanceof HTMLElement)) return;
      const frame = target.closest<HTMLElement>('[data-window-id]');
      if (frame?.dataset.windowId)
        focusedElements.current.set(frame.dataset.windowId, target);
    };
    document.addEventListener('focusin', capture);
    return () => document.removeEventListener('focusin', capture);
  }, []);
  useEffect(() => {
    const contextChanged =
      focusedContext.current.windowId !== layout.activeWindowId ||
      focusedContext.current.workspaceId !== layout.currentWorkspaceId;
    focusedContext.current = {
      windowId: layout.activeWindowId,
      workspaceId: layout.currentWorkspaceId,
    };
    if (dialogRequest) return;
    const active = document.activeElement;
    // Dialog dismissal may already have restored its opener outside the window.
    // Explicit window/workspace activation still restores that window's focus.
    if (
      !contextChanged &&
      active instanceof HTMLElement &&
      active !== document.body &&
      active.isConnected &&
      !active.matches(':disabled') &&
      !active.closest('[inert], [hidden], .ns-dialog-layer') &&
      active.getClientRects().length > 0 &&
      getComputedStyle(active).visibility === 'visible'
    )
      return;
    const id = layout.activeWindowId;
    if (!id) return;
    const frame = Array.from(
      document.querySelectorAll<HTMLElement>('[data-window-id]'),
    ).find((item) => item.dataset.windowId === id);
    if (!frame || frame.inert) return;
    if (frame.contains(document.activeElement)) return;
    const saved = focusedElements.current.get(id);
    if (
      saved?.isConnected &&
      saved.offsetParent !== null &&
      !saved.closest('[inert]')
    )
      saved.focus({ preventScroll: true });
    else frame.focus({ preventScroll: true });
  }, [layout.activeWindowId, layout.currentWorkspaceId, dialogRequest]);
  const openWorkspaceMenu = (element: HTMLElement) => {
    const rect = element.getBoundingClientRect();
    api.contextMenu(
      desktopItems([]),
      { x: rect.left, y: rect.bottom },
      { title: 'Workspace', pin: { kind: 'desktop', title: 'Workspace' } },
    );
  };
  const recordPointer = (event: { clientX: number; clientY: number }) => {
    pointerPosition.current = { x: event.clientX, y: event.clientY };
  };
  return (
    <div
      className={`ns-desktop ns-wallpaper-${props.wallpaper ?? 'slate'} ${mobile ? 'is-mobile' : ''} ${short ? 'is-short' : ''} ${sideDock ? 'has-side-dock' : ''} ${overview ? 'is-overview' : ''}`}
      onPointerDownCapture={recordPointer}
      onPointerMove={recordPointer}
      style={
        {
          '--ns-viewport-height': `${viewport.height}px`,
          '--ns-dock-size': `${DOCK_SIZE}px`,
          '--ns-dock-width': `${DOCK_SIZE}px`,
          '--ns-icon-width': `${ICON_SIZE.width}px`,
          '--ns-icon-height': `${ICON_SIZE.height}px`,
        } as CSSProperties
      }
    >
      <div className="ns-shell" inert={!!dialogRequest || undefined}>
        <header className="ns-desktop-status">
          <button
            type="button"
            aria-label="Workspace menu"
            onClick={(event) => {
              event.currentTarget.focus();
              openWorkspaceMenu(event.currentTarget);
            }}
          >
            ◆ Workspace
          </button>
          <span>{workspace.name}</span>
          <button
            type="button"
            onClick={api.showAllWindows}
            aria-label="Show all windows"
            title="Switch windows: Ctrl+F6"
          >
            {layout.windows.length}{' '}
            {layout.windows.length === 1 ? 'window' : 'windows'}
          </button>
          <span
            className={`ns-connection ${props.connectionStatus ?? 'connected'}`}
            role="status"
          >
            {props.connectionStatus === 'offline'
              ? 'Offline'
              : props.connectionStatus === 'connecting'
                ? 'Connecting…'
                : 'Connected'}
          </span>
        </header>
        <DesktopCanvas
          icons={desktopProps.icons}
          onTrashIcons={props.onTrashIcons}
          gestures={gestures}
        />
        <Clip
          props={desktopProps}
          api={api}
          layout={layout}
          setLayout={controller.setLayout}
          compact={short}
        />
        <ApplicationMenu groups={groups} api={api} props={desktopProps} />
        {controller.persistenceBlocked && (
          <div className="ns-recovery" role="alert">
            This desktop layout is damaged or uses a newer version. Changes are
            not being saved.
            <button type="button" onClick={() => void controller.resetLayout()}>
              Reset Layout
            </button>
          </div>
        )}
        {overview && (
          <div
            className="ns-overview-backdrop"
            onClick={() => setOverview(false)}
          >
            <span>Choose a window · Escape to return</span>
          </div>
        )}
        {layout.windows.map((win, index) => {
          const app = resolveApplication(props.apps, win.appId);
          return (
            <WindowFrame
              key={win.id}
              win={win}
              app={app}
              index={overview ? index : workspace.order.indexOf(win.id)}
              api={api}
              viewport={viewport}
              workArea={workArea}
              mobile={mobile}
              overview={overview}
              inputBlocked={!!menu || !!dialogRequest}
              preview={gestures.windowPreview[win.id]}
              activateWindow={controller.activateWindow}
              beginWindowGesture={gestures.beginWindowGesture}
              toggleShade={controller.toggleShade}
              toggleZoom={controller.toggleZoom}
            />
          );
        })}
        <PinnedMenus
          props={desktopProps}
          api={api}
          layout={layout}
          setLayout={controller.setLayout}
        />
        {minimized.length > 0 && (
          <div
            className="ns-miniwindow-shelf"
            aria-label="Miniaturized windows"
          >
            {minimized.map((win) => {
              const app = resolveApplication(props.apps, win.appId);
              return (
                <button
                  type="button"
                  key={win.id}
                  className="ns-miniwindow"
                  aria-label={`Restore ${win.title}`}
                  onClick={() => api.focusWindow(win.id)}
                >
                  <span className="ns-miniwindow-caption">{win.title}</span>
                  <PixelIcon name={app?.icon ?? 'document'} size={28} />
                  <span className="ns-miniwindow-app">{app?.title}</span>
                </button>
              );
            })}
          </div>
        )}
        <Dock
          props={desktopProps}
          api={api}
          layout={layout}
          setLayout={controller.setLayout}
          compact={mobile || short || viewport.coarsePointer === true}
        />
        {menu && (
          <MenuOverlay
            key={menu.id}
            request={menu}
            pointerPosition={pointerPosition}
            close={controller.dismissMenu}
            pin={controller.pinMenu}
          />
        )}
      </div>
      {dialogRequest && (
        <DialogOverlay
          key={dialogRequest.id}
          options={dialogRequest.options}
          resolve={controller.resolveDialog}
        />
      )}
    </div>
  );
}
