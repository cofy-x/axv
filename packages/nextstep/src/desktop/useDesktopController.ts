import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import {
  activateLayoutWindow,
  adjacentWindowBounds,
  arrangeDesktopIconPositions,
  desktopApplicationIcons,
  iconWorkArea,
  inWorkspace,
  isApplicationOnDesktop as applicationOnDesktop,
  minimizedWindows,
  reconcileWorkspaceWindows,
  sanitizeLayout,
  showDesktopApplication as updateDesktopApplication,
  switchWorkspaceLayout,
  toggleWindowZoom,
  unsupportedLayout,
  validAppId,
  visibleBounds,
  windowIdentity,
  windowWorkArea,
} from '../model';
import type { MenuRequest } from '../overlays';
import type {
  ClipShortcut,
  DesktopAPI,
  DesktopLayout,
  DesktopProps,
  DesktopWindow,
  DialogOptions,
  DialogResult,
  MenuItem,
  MenuOptions,
  PinnedMenu,
  Point,
  WindowParams,
  WindowStateUpdate,
} from '../types';
import { viewportSize } from './useViewport';
import { updateWindowViewState } from './window-state';
import { launchApplication, resolveApplication } from './applications';

function sameParams(first: WindowParams, second: WindowParams): boolean {
  return (
    Object.keys(first).length === Object.keys(second).length &&
    Object.entries(first).every(([key, value]) => second[key] === value)
  );
}

interface OverlayOpener {
  element: HTMLElement;
  activeWindowId: string | null;
}

function menuOwnerAvailable(menu: MenuRequest, layout: DesktopLayout) {
  const id = menu.options?.ownerWindowId;
  if (!id) return true;
  const owner = layout.windows.find((win) => win.id === id);
  return (
    !!owner &&
    layout.activeWindowId === id &&
    !owner.minimized &&
    !owner.shaded &&
    inWorkspace(owner, layout.currentWorkspaceId) &&
    (!menu.opener ||
      (menu.opener.isConnected &&
        !!menu.opener.getClientRects().length &&
        getComputedStyle(menu.opener).visibility !== 'hidden' &&
        !menu.opener.matches(':disabled')))
  );
}

export function initialDesktopLayout(props: DesktopProps): DesktopLayout {
  let state = sanitizeLayout(props.initialLayout, props.apps);
  if (props.initialLayout && !unsupportedLayout(props.initialLayout))
    return state;
  state.desktopApplications = { ...props.initialDesktopApplications };
  const dockOrder = [...new Set(props.initialDockOrder ?? [])];
  state.dock = [
    ...dockOrder.flatMap((appId) =>
      state.dock.filter((entry) => entry.appId === appId),
    ),
    ...state.dock.filter((entry) => !dockOrder.includes(entry.appId)),
  ];
  for (const initial of props.initialWindows ?? []) {
    const app = props.apps.find((candidate) => candidate.id === initial.appId);
    if (!app) continue;
    const params = initial.params ?? {};
    const id = windowIdentity(app, params);
    if (state.windows.some((window) => window.id === id)) continue;
    const index = state.windows.length;
    state.windows.push({
      id,
      appId: app.id,
      title: initial.title ?? app.title,
      params,
      x: 190 + index * 32,
      y: 52 + index * 32,
      width: app.defaultSize?.width ?? 640,
      height: app.defaultSize?.height ?? 440,
      workspaceId: state.currentWorkspaceId,
      sticky: false,
      minimized: false,
      viewState: {},
      shaded: false,
      zoomed: false,
    });
    if (!state.dock.some((entry) => entry.appId === app.id))
      state.dock.push({ appId: app.id, pinned: false });
    state = activateLayoutWindow(reconcileWorkspaceWindows(state), id);
  }
  return state;
}

/** The single owner of persisted desktop state and stable application commands. */
export function useDesktopController(props: DesktopProps) {
  const [layout, setLayout] = useState<DesktopLayout>(() =>
    initialDesktopLayout(props),
  );
  const [persistenceBlocked, setPersistenceBlocked] = useState(
    () => !!props.initialLayoutError || unsupportedLayout(props.initialLayout),
  );
  const propsRef = useRef(props);
  propsRef.current = props;
  const apiRef = useRef<DesktopAPI | null>(null);
  const [overview, setOverview] = useState(false);
  const [menu, setMenu] = useState<MenuRequest | null>(null);
  const menuRef = useRef(menu);
  menuRef.current = menu;
  useLayoutEffect(() => {
    if (menu && !menuOwnerAvailable(menu, layout)) setMenu(null);
  }, [menu, layout, props.apps]);
  const menuCounter = useRef(0);
  const menuActionOpener = useRef<OverlayOpener | null>(null);
  const pendingFocusReturn = useRef<OverlayOpener | null>(null);
  const [dialogs, setDialogs] = useState<
    Array<{
      id: number;
      options: DialogOptions;
      resolve: (result: DialogResult) => void;
      opener: OverlayOpener | null;
    }>
  >([]);
  const dialogsRef = useRef(dialogs);
  dialogsRef.current = dialogs;
  const dialogCounter = useRef(0);
  useLayoutEffect(() => {
    if (menu || dialogs.length) return;
    // Window controls become interactive again only after the overlay unmounts.
    const opener = pendingFocusReturn.current;
    pendingFocusReturn.current = null;
    if (!opener || opener.activeWindowId !== layout.activeWindowId) return;
    const target = opener.element;
    const active = document.activeElement;
    if (
      !target.isConnected ||
      target.matches(':disabled') ||
      target.closest('[inert], [hidden]') ||
      !target.getClientRects().length ||
      getComputedStyle(target).visibility === 'hidden' ||
      (active instanceof HTMLElement &&
        active !== document.body &&
        active !== target)
    )
      return;
    target.focus({ preventScroll: true });
  }, [menu, dialogs, layout.activeWindowId]);
  const closeHandlers = useRef(
    new Map<string, () => boolean | Promise<boolean>>(),
  );
  const closing = useRef(new Set<string>());
  useEffect(() => {
    if (!persistenceBlocked) propsRef.current.onLayoutChange?.(layout);
  }, [layout, persistenceBlocked]);

  const activateWindow = useCallback((id: string, reveal = false) => {
    if (dialogsRef.current.length) return;
    setOverview(false);
    setLayout((current) => activateLayoutWindow(current, id, reveal));
  }, []);
  const focusWindow = useCallback(
    (id: string) => activateWindow(id, true),
    [activateWindow],
  );
  const focusDesktop = useCallback(() => {
    if (dialogsRef.current.length) return;
    setLayout((current) => ({
      ...current,
      activeWindowId: null,
      workspaces: current.workspaces.map((workspace) =>
        workspace.id === current.currentWorkspaceId
          ? { ...workspace, activeWindowId: null }
          : workspace,
      ),
    }));
  }, []);
  const openApp = useCallback(
    (
      appId: string,
      params: WindowParams = {},
      options?: { title?: string },
    ) => {
      if (!validAppId(appId)) return '';
      const app = resolveApplication(propsRef.current.apps, appId);
      const restored = apiRef.current?.windows.find(
        (win) => win.appId === appId && sameParams(win.params, params),
      );
      if (!restored && app.unavailable !== undefined) {
        void apiRef.current?.dialog({
          title: 'Application unavailable',
          message: app.unavailable,
          buttons: [{ label: 'OK', value: 'ok', default: true }],
        });
        return '';
      }
      const id = restored?.id ?? windowIdentity(app, params);
      setOverview(false);
      setLayout((current) => {
        const existing = current.windows.find((item) => item.id === id);
        if (!existing && app.unavailable !== undefined) return current;
        const index = current.windows.length % 6;
        const next: DesktopWindow = existing
          ? { ...existing, title: options?.title ?? existing.title }
          : {
              id,
              appId,
              params,
              title: options?.title ?? app.title,
              x: 190 + index * 26,
              y: 48 + index * 28,
              width: app.defaultSize?.width ?? 640,
              height: app.defaultSize?.height ?? 440,
              workspaceId: current.currentWorkspaceId,
              sticky: false,
              minimized: false,
              viewState: {},
              shaded: false,
              zoomed: false,
            };
        if (!existing && app.initialPlacement === 'beside-active') {
          const viewport = viewportSize();
          const source = current.windows.find(
            (win) =>
              win.id === current.activeWindowId &&
              !win.minimized &&
              inWorkspace(win, current.currentWorkspaceId),
          );
          if (source && viewport.width >= 600) {
            const area = windowWorkArea(
              viewport,
              minimizedWindows(current.windows, current.currentWorkspaceId)
                .length > 0,
            );
            const sourceApp = resolveApplication(
              propsRef.current.apps,
              source.appId,
            );
            const sourceBounds = source.zoomed
              ? area
              : visibleBounds(source, area, sourceApp.minSize);
            Object.assign(
              next,
              adjacentWindowBounds(next, sourceBounds, area, app.minSize),
            );
          }
        }
        return activateLayoutWindow(
          reconcileWorkspaceWindows({
            ...current,
            windows: [
              ...current.windows.filter((item) => item.id !== id),
              next,
            ],
            dock: current.dock.some((entry) => entry.appId === appId)
              ? current.dock
              : [...current.dock, { appId, pinned: false }],
          }),
          id,
          true,
        );
      });
      return id;
    },
    [],
  );
  const dialog = useCallback(
    (options: DialogOptions) =>
      new Promise<DialogResult>((resolve) => {
        const element =
          menuRef.current?.opener ??
          (document.activeElement instanceof HTMLElement
            ? document.activeElement
            : null);
        const opener =
          menuActionOpener.current ??
          dialogsRef.current.at(-1)?.opener ??
          (element?.closest('.ns-dialog-layer')
            ? pendingFocusReturn.current
            : element
              ? {
                  element,
                  activeWindowId: apiRef.current?.activeWindowId ?? null,
                }
              : null);
        setMenu(null);
        const request = {
          id: ++dialogCounter.current,
          options,
          resolve,
          opener,
        };
        dialogsRef.current = [...dialogsRef.current, request];
        setDialogs(dialogsRef.current);
      }),
    [],
  );
  const launchApp = useCallback((appId: string) => {
    if (!validAppId(appId) || !apiRef.current || dialogsRef.current.length)
      return;
    launchApplication(propsRef.current.apps, apiRef.current, appId);
  }, []);
  const closeWindow = useCallback(async (id: string): Promise<boolean> => {
    if (closing.current.has(id)) return false;
    closing.current.add(id);
    try {
      if ((await closeHandlers.current.get(id)?.()) === false) return false;
      setLayout((current) =>
        reconcileWorkspaceWindows({
          ...current,
          windows: current.windows.filter((win) => win.id !== id),
        }),
      );
      setMenu((current) =>
        current?.options?.ownerWindowId === id ||
        current?.options?.pin?.ownerWindowId === id
          ? null
          : current,
      );
      return true;
    } finally {
      closing.current.delete(id);
    }
  }, []);
  const setTitle = useCallback(
    (id: string, title: string) =>
      setLayout((current) =>
        current.windows.find((win) => win.id === id)?.title === title
          ? current
          : {
              ...current,
              windows: current.windows.map((win) =>
                win.id === id ? { ...win, title } : win,
              ),
            },
      ),
    [],
  );
  const updateWindowState = useCallback(
    (id: string, update: WindowStateUpdate) =>
      setLayout((current) => updateWindowViewState(current, id, update)),
    [],
  );
  const switchWorkspace = useCallback((id: string) => {
    if (dialogsRef.current.length) return;
    setMenu(null);
    setOverview(false);
    setLayout((current) => switchWorkspaceLayout(current, id));
  }, []);
  const renameWorkspace = useCallback((id: string, name: string) => {
    const clean = name.trim().slice(0, 40);
    if (!clean) return;
    setLayout((current) => ({
      ...current,
      workspaces: current.workspaces.map((workspace) =>
        workspace.id === id ? { ...workspace, name: clean } : workspace,
      ),
    }));
  }, []);
  const moveWindow = useCallback(
    (id: string, workspaceId: string) =>
      setLayout((current) =>
        !current.workspaces.some((workspace) => workspace.id === workspaceId)
          ? current
          : reconcileWorkspaceWindows({
              ...current,
              windows: current.windows.map((win) =>
                win.id === id ? { ...win, workspaceId, sticky: false } : win,
              ),
            }),
      ),
    [],
  );
  const setWindowSticky = useCallback(
    (id: string, sticky: boolean) =>
      setLayout((current) =>
        reconcileWorkspaceWindows({
          ...current,
          windows: current.windows.map((win) =>
            win.id === id ? { ...win, sticky } : win,
          ),
        }),
      ),
    [],
  );
  const minimizeWindow = useCallback(
    (id: string) =>
      setLayout((current) =>
        reconcileWorkspaceWindows({
          ...current,
          windows: current.windows.map((win) =>
            win.id === id ? { ...win, minimized: true } : win,
          ),
        }),
      ),
    [],
  );
  const toggleShade = useCallback(
    (id: string) =>
      setLayout((current) => ({
        ...current,
        windows: current.windows.map((win) =>
          win.id === id ? { ...win, shaded: !win.shaded } : win,
        ),
      })),
    [],
  );
  const toggleZoom = useCallback(
    (id: string) =>
      setLayout((current) => ({
        ...current,
        windows: current.windows.map((win) =>
          win.id === id ? toggleWindowZoom(win) : win,
        ),
      })),
    [],
  );
  const addClipShortcut = useCallback((shortcut: Omit<ClipShortcut, 'id'>) => {
    if (!validAppId(shortcut.appId)) return;
    setLayout((current) => ({
      ...current,
      workspaces: current.workspaces.map((workspace) =>
        workspace.id !== current.currentWorkspaceId ||
        workspace.clip.some(
          (item) =>
            item.appId === shortcut.appId &&
            JSON.stringify(item.params) === JSON.stringify(shortcut.params),
        )
          ? workspace
          : {
              ...workspace,
              clip: [
                ...workspace.clip,
                { ...shortcut, id: crypto.randomUUID() },
              ].slice(-32),
            },
      ),
    }));
  }, []);
  const removeClipShortcut = useCallback(
    (id: string) =>
      setLayout((current) => ({
        ...current,
        workspaces: current.workspaces.map((workspace) => ({
          ...workspace,
          clip: workspace.clip.filter((item) => item.id !== id),
        })),
      })),
    [],
  );
  const arrangeIcons = useCallback(
    () =>
      setLayout((current) => {
        const viewport = viewportSize();
        const area = iconWorkArea(
          viewport,
          minimizedWindows(current.windows, current.currentWorkspaceId).length >
            0,
        );
        const key = viewport.width < 600 ? 'compactIcons' : 'icons';
        return {
          ...current,
          [key]: arrangeDesktopIconPositions(
            current[key],
            desktopApplicationIcons(
              current,
              propsRef.current.apps,
              propsRef.current.icons,
              launchApp,
            ),
            area,
          ),
        };
      }),
    [launchApp],
  );
  const isApplicationOnDesktop = useCallback(
    (appId: string) =>
      applicationOnDesktop(
        { desktopApplications: layout.desktopApplications },
        propsRef.current.apps,
        propsRef.current.icons,
        appId,
      ),
    [layout.desktopApplications],
  );
  const showDesktopApplication = useCallback(
    (appId: string, shown: boolean) => {
      setLayout((current) => updateDesktopApplication(current, appId, shown));
    },
    [],
  );
  const icons = useMemo(
    () =>
      desktopApplicationIcons(
        { desktopApplications: layout.desktopApplications },
        props.apps,
        props.icons,
        launchApp,
      ),
    [layout.desktopApplications, props.apps, props.icons, launchApp],
  );
  const contextMenu = useCallback(
    (items: MenuItem[], point: Point, options?: MenuOptions) => {
      if (items.length)
        setMenu({
          id: ++menuCounter.current,
          items,
          point,
          options,
          opener:
            document.activeElement instanceof HTMLElement
              ? document.activeElement
              : null,
        });
    },
    [],
  );
  const beforeClose = useCallback(
    (id: string, handler: () => boolean | Promise<boolean>) => {
      closeHandlers.current.set(id, handler);
      return () => {
        if (closeHandlers.current.get(id) === handler)
          closeHandlers.current.delete(id);
      };
    },
    [],
  );
  const api = useMemo<DesktopAPI>(
    () => ({
      windows: layout.windows,
      activeWindowId: layout.activeWindowId,
      workspaces: layout.workspaces,
      currentWorkspaceId: layout.currentWorkspaceId,
      launchApp,
      isApplicationOnDesktop,
      showDesktopApplication,
      openApp,
      closeWindow,
      focusWindow,
      setTitle,
      updateWindowState,
      switchWorkspace,
      renameWorkspace,
      moveWindow,
      setWindowSticky,
      minimizeWindow,
      toggleShade,
      toggleZoom,
      addClipShortcut,
      removeClipShortcut,
      arrangeIcons,
      contextMenu,
      dialog,
      beforeClose,
      showAllWindows: () => setOverview((current) => !current),
    }),
    [
      layout.windows,
      layout.activeWindowId,
      layout.workspaces,
      layout.currentWorkspaceId,
      launchApp,
      isApplicationOnDesktop,
      showDesktopApplication,
      openApp,
      closeWindow,
      focusWindow,
      setTitle,
      updateWindowState,
      switchWorkspace,
      renameWorkspace,
      moveWindow,
      setWindowSticky,
      minimizeWindow,
      toggleShade,
      toggleZoom,
      addClipShortcut,
      removeClipShortcut,
      arrangeIcons,
      contextMenu,
      dialog,
      beforeClose,
    ],
  );
  apiRef.current = api;
  useEffect(() => {
    propsRef.current.onReady?.(api);
  }, [api]);
  const dialogRequest = dialogs[0];
  const dismissMenu = (action?: () => void) => {
    if (menu && !menuOwnerAvailable(menu, layout)) {
      setMenu(null);
      return;
    }
    const opener = menu?.opener
      ? { element: menu.opener, activeWindowId: layout.activeWindowId }
      : null;
    pendingFocusReturn.current = opener;
    setMenu(null);
    // A dialog opened by this action must inherit the original menu trigger.
    menuActionOpener.current = opener;
    try {
      action?.();
    } finally {
      menuActionOpener.current = null;
    }
  };
  const resolveDialog = (result: DialogResult) => {
    if (!dialogRequest) return;
    pendingFocusReturn.current = dialogRequest.opener;
    dialogsRef.current = dialogsRef.current.filter(
      (request) => request.id !== dialogRequest.id,
    );
    setDialogs(dialogsRef.current);
    dialogRequest.resolve(result);
  };
  const pinMenu = (point: Point) => {
    if (!menu?.options?.pin || !menuOwnerAvailable(menu, layout)) return;
    const descriptor: PinnedMenu = {
      ...menu.options.pin,
      ...point,
      id: crypto.randomUUID(),
    };
    setLayout((current) => ({
      ...current,
      pinnedMenus: [
        ...current.pinnedMenus.filter(
          (item) =>
            !(
              item.kind === descriptor.kind &&
              item.groupId === descriptor.groupId &&
              item.ownerWindowId === descriptor.ownerWindowId
            ),
        ),
        descriptor,
      ].slice(-12),
    }));
    dismissMenu();
  };
  const resetLayout = async () => {
    const result = await dialog({
      title: 'Reset desktop layout?',
      message:
        'The saved layout is unavailable. Reset only window, Dock, Clip and workspace positions? Your files and preferences will be kept.',
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Reset Layout', value: 'reset', default: true },
      ],
    });
    if (result?.button === 'reset') {
      setLayout(
        initialDesktopLayout({ ...propsRef.current, initialLayout: undefined }),
      );
      setPersistenceBlocked(false);
    }
  };
  return {
    layout,
    icons,
    setLayout,
    overview,
    setOverview,
    menu,
    dismissMenu,
    pinMenu,
    dialogRequest,
    resolveDialog,
    api,
    activateWindow,
    focusDesktop,
    toggleShade,
    toggleZoom,
    persistenceBlocked,
    resetLayout,
  };
}
