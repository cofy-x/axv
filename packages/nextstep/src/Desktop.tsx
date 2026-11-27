import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { CSSProperties } from 'react';
import { Button, PixelIcon, WindowControlIcon } from './controls';
import {
  APPLICATION_MENU_HEIGHT,
  APPLICATION_MENU_TOP,
  DEFAULT_WORKSPACE_COUNT,
  DOCK_SIZE,
  ICON_SIZE,
  desktopLayoutMode,
  iconWorkArea,
  minimizedWindows,
  windowWorkArea,
  workspaceLabel,
} from './model';
import { DialogOverlay, MenuOverlay } from './overlays';
import type { Bounds, DesktopProps, Point } from './types';
import { Dock } from './desktop/Dock';
import { DesktopCanvas } from './desktop/DesktopCanvas';
import { ApplicationMenu } from './desktop/MenuPanels';
import { WindowFrame } from './desktop/WindowFrame';
import {
  createApplicationMenus,
  createDesktopContextMenu,
} from './desktop/menus';
import { useDesktopController } from './desktop/useDesktopController';
import { useDesktopGestures } from './desktop/useDesktopGestures';
import { useViewport } from './desktop/useViewport';
import { resolveApplication } from './desktop/applications';

export function Desktop(props: DesktopProps) {
  const pointerPosition = useRef<Point | null>(null);
  const viewport = useViewport();
  const { mobile, short, sideDock } = desktopLayoutMode(viewport);
  const controller = useDesktopController(props);
  const { layout, setOverview, api, menu, dialogRequest } = controller;
  const windowCount = layout.windows.length;
  const overview = controller.overview && windowCount > 0;
  const compactOverview = overview && mobile;
  const titleCounts = compactOverview ? new Map<string, number>() : undefined;
  if (titleCounts)
    for (const { title } of layout.windows)
      titleCounts.set(title, (titleCounts.get(title) ?? 0) + 1);
  const overviewButton = useRef<HTMLButtonElement>(null);
  const dismissOverview = () => {
    setOverview(false);
    overviewButton.current?.focus({ preventScroll: true });
  };
  const desktopProps = { ...props, icons: controller.icons };
  const applicationMenus = overview
    ? undefined
    : createApplicationMenus(desktopProps, api);
  const minimized = minimizedWindows(layout.windows, layout.currentWorkspaceId);
  const hasMiniwindows = minimized.length > 0;
  const overlayReservesTopRow = overview || controller.persistenceBlocked;
  const areaWithTopRow = windowWorkArea(viewport, hasMiniwindows);
  const areaWithoutTopRow = windowWorkArea(viewport, hasMiniwindows, false);
  const baseWorkArea =
    applicationMenus || overlayReservesTopRow
      ? areaWithTopRow
      : areaWithoutTopRow;
  const statusRef = useRef<HTMLDivElement>(null);
  const [statusHeight, setStatusHeight] = useState(0);
  const [noticeFocusRevision, setNoticeFocusRevision] = useState(0);
  const hasStatus = !!props.statusOverlay;
  useLayoutEffect(() => {
    const element = statusRef.current;
    if (!element) {
      setStatusHeight(0);
      return;
    }
    const measure = () =>
      setStatusHeight(Math.ceil(element.getBoundingClientRect().height));
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [hasStatus]);
  useLayoutEffect(() => {
    const element = statusRef.current;
    if (!element) return;
    let focused: HTMLElement | null = null;
    const focus = (event: FocusEvent) => {
      focused =
        event.target instanceof HTMLElement && element.contains(event.target)
          ? event.target
          : null;
    };
    const pointer = (event: PointerEvent) => {
      if (!element.contains(event.target as Node)) focused = null;
    };
    const observer = new MutationObserver(() => {
      if (!focused || focused.isConnected) return;
      focused = null;
      setNoticeFocusRevision((revision) => revision + 1);
    });
    document.addEventListener('focusin', focus);
    document.addEventListener('pointerdown', pointer, true);
    observer.observe(element, { childList: true, subtree: true });
    return () => {
      document.removeEventListener('focusin', focus);
      document.removeEventListener('pointerdown', pointer, true);
      observer.disconnect();
    };
  }, [hasStatus]);
  const reservedStatusHeight = hasStatus ? statusHeight + 16 : 0;
  const reserveStatusSpace = (area: Bounds) => ({
    ...area,
    height: Math.max(1, area.height - reservedStatusHeight),
  });
  const workArea = reserveStatusSpace(baseWorkArea);
  const baseIconArea = iconWorkArea(viewport, hasMiniwindows);
  const desktopItems = (ids: string[]) =>
    createDesktopContextMenu(desktopProps, api, ids);
  const gestures = useDesktopGestures({
    props: desktopProps,
    layout,
    setLayout: controller.setLayout,
    workArea,
    iconArea: reserveStatusSpace(baseIconArea),
    mobile,
    overview,
    dismissOverview,
    overlayActive: !!menu || !!dialogRequest,
    api,
    activateWindow: controller.activateWindow,
    focusDesktop: controller.focusDesktop,
    desktopItems,
  });
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
      if (
        !(target instanceof HTMLElement) ||
        target.classList.contains('ns-overview-pick')
      )
        return;
      const frame = target.closest<HTMLElement>('[data-window-id]');
      if (frame?.dataset.windowId)
        focusedElements.current.set(frame.dataset.windowId, target);
    };
    document.addEventListener('focusin', capture);
    return () => document.removeEventListener('focusin', capture);
  }, []);
  useEffect(() => {
    for (const [id, element] of focusedElements.current)
      if (!element.isConnected) focusedElements.current.delete(id);
  }, [layout.windows]);
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
  }, [
    layout.activeWindowId,
    layout.currentWorkspaceId,
    dialogRequest,
    overview,
    hasStatus,
    noticeFocusRevision,
  ]);
  const openWorkspaceMenu = (element: HTMLElement) => {
    const rect = element.getBoundingClientRect();
    api.contextMenu(
      desktopItems([]),
      { x: rect.left, y: rect.bottom },
      { title: 'Workspace' },
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
          '--ns-application-menu-top': `${APPLICATION_MENU_TOP}px`,
          '--ns-application-menu-height': `${APPLICATION_MENU_HEIGHT}px`,
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
            className="ns-workspace-menu-button"
            aria-label="Workspace menu"
            title="Workspace menu"
            aria-haspopup="menu"
            onClick={(event) => {
              event.currentTarget.focus();
              openWorkspaceMenu(event.currentTarget);
            }}
          >
            {props.workspaceMenuIcon ?? <PixelIcon name="computer" size={17} />}
          </button>
          <div
            className="ns-workspace-switcher"
            role="group"
            aria-label="Workspaces"
          >
            {layout.workspaces.map((item, index) => {
              const current = item.id === workspace.id;
              if (index >= DEFAULT_WORKSPACE_COUNT && !current) return null;
              const label = workspaceLabel(item, index);
              return (
                <button
                  type="button"
                  key={item.id}
                  aria-label={label}
                  aria-pressed={current}
                  aria-keyshortcuts={index < 9 ? `Alt+${index + 1}` : undefined}
                  title={label}
                  onClick={() => api.switchWorkspace(item.id)}
                >
                  {index + 1}
                </button>
              );
            })}
          </div>
          <button
            ref={overviewButton}
            type="button"
            className="ns-window-overview"
            onClick={(event) => {
              event.currentTarget.focus({ preventScroll: true });
              api.showAllWindows();
            }}
            aria-label="Window Overview"
            aria-pressed={overview}
            disabled={windowCount === 0}
            title={`Window Overview · ${windowCount} open ${windowCount === 1 ? 'window' : 'windows'}`}
          >
            <WindowControlIcon icon="overview" />
          </button>
          {props.headerActions}
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
          gestures={gestures}
          inert={overview || undefined}
        />
        {applicationMenus && (
          <ApplicationMenu api={api} menus={applicationMenus} />
        )}
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
            role="region"
            aria-label="Window Overview"
            onClick={(event) => {
              if (event.target === event.currentTarget) dismissOverview();
            }}
          >
            <div className="ns-overview-header">
              <strong>Window Overview</strong>
              <Button
                aria-label="Close Window Overview"
                onClick={dismissOverview}
              >
                Done
              </Button>
            </div>
            {compactOverview && (
              <ul className="ns-overview-list" aria-label="Open windows">
                {layout.windows.map((win) => {
                  const app = resolveApplication(props.apps, win.appId);
                  const reference =
                    (titleCounts?.get(win.title) ?? 0) > 1
                      ? app.overviewReference?.(win)
                      : undefined;
                  const workspaceIndex = layout.workspaces.findIndex(
                    (item) => item.id === win.workspaceId,
                  );
                  const workspace = layout.workspaces[workspaceIndex];
                  const workspaceName =
                    workspace &&
                    !win.sticky &&
                    win.workspaceId !== layout.currentWorkspaceId
                      ? workspaceLabel(workspace, workspaceIndex)
                      : undefined;
                  const detail = [reference, workspaceName]
                    .filter(Boolean)
                    .join(' · ');
                  return (
                    <li key={win.id}>
                      <Button
                        className="ns-overview-window"
                        aria-label={
                          detail
                            ? `Switch to ${win.title} · ${detail}`
                            : `Switch to ${win.title}`
                        }
                        onClick={() => api.focusWindow(win.id)}
                      >
                        <PixelIcon name={app.icon} size={24} />
                        <span>{win.title}</span>
                        {detail && <small>{detail}</small>}
                      </Button>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        )}
        {layout.windows.map((win, index) => {
          const app = resolveApplication(props.apps, win.appId);
          // Keep mounted content fitted for its own application menu while
          // hidden or shaded so switching windows does not resize it.
          const inactiveArea = app.menu ? areaWithTopRow : areaWithoutTopRow;
          const frameArea =
            mobile &&
            !overlayReservesTopRow &&
            (win.id !== layout.activeWindowId || win.shaded)
              ? reserveStatusSpace(inactiveArea)
              : workArea;
          return (
            <WindowFrame
              key={win.id}
              win={win}
              app={app}
              index={overview ? index : workspace.order.indexOf(win.id)}
              api={api}
              workArea={frameArea}
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
        {minimized.length > 0 && !compactOverview && (
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
        {hasStatus && (
          <div
            ref={statusRef}
            className="ns-status-overlay"
            role="region"
            aria-label="Desktop notices"
            tabIndex={0}
            data-blocked={!!menu || !!dialogRequest || overview || undefined}
            inert={!!menu || !!dialogRequest || overview || undefined}
            style={{
              left: `calc(${baseWorkArea.x + 8}px + env(safe-area-inset-left, 0px))`,
              right: `calc(${viewport.width - baseWorkArea.x - baseWorkArea.width + 8}px + env(safe-area-inset-right, 0px))`,
              bottom:
                viewport.height - baseWorkArea.y - baseWorkArea.height + 8,
              maxHeight: Math.max(1, Math.floor(baseWorkArea.height / 3)),
            }}
          >
            {props.statusOverlay}
          </div>
        )}
        {menu && (
          <MenuOverlay
            key={menu.id}
            request={menu}
            pointerPosition={pointerPosition}
            close={controller.dismissMenu}
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
