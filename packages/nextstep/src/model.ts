import type {
  AppDefinition,
  Bounds,
  DesktopLayout,
  DesktopIcon,
  DesktopWindow,
  MenuGroup,
  Point,
  Size,
  WindowParams,
  Viewport,
  Workspace,
} from './types';

export const MENU_HEIGHT = 26;
export const STRIP_HEIGHT = 76;
export const DOCK_SIZE = 56;
export const DOCK_WIDTH = DOCK_SIZE + 12;
export const DOCK_HEIGHT = DOCK_SIZE + 8;
export const ICON_SIZE = { width: 80, height: 76 };

/** The visual viewport, including an on-screen keyboard, determines shell presentation. */
export function desktopLayoutMode(viewport: Size) {
  const mobile = viewport.width < 600;
  const short = viewport.height <= 480;
  return {
    mobile,
    short,
    sideDock:
      !mobile ||
      (short && viewport.width >= 480 && viewport.width > viewport.height),
  };
}

/** Applications replace matching menus and insert additional menus after their preceding contribution. */
export function mergeMenuGroups(
  defaults: MenuGroup[],
  contributions: MenuGroup[],
): MenuGroup[] {
  const groups = [...defaults];
  let insertion = Math.max(
    0,
    groups.findIndex((group) => group.id === 'window'),
  );
  if (!insertion) insertion = groups.length;
  for (const group of contributions) {
    const existing = groups.findIndex((candidate) => candidate.id === group.id);
    if (existing >= 0) {
      groups[existing] = group;
      insertion = existing + 1;
    } else {
      groups.splice(insertion, 0, group);
      insertion += 1;
    }
  }
  return groups;
}

export function windowIdentity(
  app: AppDefinition,
  params: WindowParams = {},
): string {
  return `${app.id}:${app.windowKey?.(params) ?? 'main'}`;
}

/** A miniwindow shelf reserves space only while this workspace has restore icons. */
export function windowWorkArea(
  viewport: Viewport,
  hasMiniwindows: boolean,
): Bounds {
  const { mobile, short, sideDock } = desktopLayoutMode(viewport);
  const x = 4;
  const y = short ? 64 : mobile ? 90 : MENU_HEIGHT + 2;
  const right = sideDock ? DOCK_WIDTH : 4;
  const bottom =
    (sideDock ? 8 : DOCK_HEIGHT) + (hasMiniwindows ? STRIP_HEIGHT : 0);
  return {
    x,
    y,
    width: Math.max(1, viewport.width - x - right),
    height: Math.max(1, viewport.height - y - bottom),
  };
}

/** Icons share the shell's reserved edges, with a small inset for their labels. */
export function iconWorkArea(
  viewport: Viewport,
  hasMiniwindows: boolean,
): Bounds {
  const area = windowWorkArea(viewport, hasMiniwindows);
  const y = Math.max(40, area.y + 4);
  return {
    x: area.x + 4,
    y,
    width: Math.max(1, area.width - 8),
    height: Math.max(1, area.y + area.height - y - 4),
  };
}

/** Prefer either free side of a source, then clamp on the side with more space. */
export function adjacentWindowBounds(
  bounds: Bounds,
  source: Bounds,
  workArea: Bounds,
  minSize?: Size,
): Bounds {
  const fitted = visibleBounds(bounds, workArea, minSize);
  const gap = 12;
  const right = source.x + source.width + gap;
  const left = source.x - fitted.width - gap;
  const rightSpace = workArea.x + workArea.width - right;
  const leftSpace = source.x - gap - workArea.x;
  const x =
    rightSpace >= fitted.width
      ? right
      : leftSpace >= fitted.width
        ? left
        : rightSpace >= leftSpace
          ? right
          : left;
  const placed = visibleBounds(
    { ...bounds, x, y: source.y },
    workArea,
    minSize,
  );
  // A short display may fit presentation, but must not shrink normal saved size.
  return { ...bounds, x: placed.x, y: placed.y };
}

/** Fit presentation without changing saved normal or restorable geometry. */
export function visibleBounds(
  bounds: Bounds,
  workArea: Bounds,
  minSize: Size = { width: 240, height: 160 },
): Bounds {
  const width = Math.min(workArea.width, Math.max(minSize.width, bounds.width));
  const height = Math.min(
    workArea.height,
    Math.max(minSize.height, bounds.height),
  );
  return {
    x: Math.round(
      Math.min(
        Math.max(workArea.x, bounds.x),
        workArea.x + workArea.width - width,
      ),
    ),
    y: Math.round(
      Math.min(
        Math.max(workArea.y, bounds.y),
        workArea.y + workArea.height - height,
      ),
    ),
    width: Math.round(width),
    height: Math.round(height),
  };
}

export function resizeBounds(
  bounds: Bounds,
  delta: Point,
  edge: string,
  minSize: Size,
  workArea: Bounds,
): Bounds {
  const start = visibleBounds(bounds, workArea, minSize);
  const minimumWidth = Math.min(minSize.width, workArea.width);
  const minimumHeight = Math.min(minSize.height, workArea.height);
  let left = start.x;
  let top = start.y;
  let right = left + start.width;
  let bottom = top + start.height;
  // Constrain the moving edge rather than moving the whole window at a boundary.
  if (edge.includes('e'))
    right = Math.min(
      workArea.x + workArea.width,
      Math.max(left + minimumWidth, right + delta.x),
    );
  if (edge.includes('s'))
    bottom = Math.min(
      workArea.y + workArea.height,
      Math.max(top + minimumHeight, bottom + delta.y),
    );
  if (edge.includes('w'))
    left = Math.max(workArea.x, Math.min(right - minimumWidth, left + delta.x));
  if (edge.includes('n'))
    top = Math.max(workArea.y, Math.min(bottom - minimumHeight, top + delta.y));
  return {
    x: Math.round(left),
    y: Math.round(top),
    width: Math.round(right - left),
    height: Math.round(bottom - top),
  };
}

/** Maximization changes presentation; the normal rectangle survives work-area changes. */
export function toggleWindowZoom(win: DesktopWindow): DesktopWindow {
  return win.zoomed
    ? {
        ...win,
        ...(win.restoreBounds ?? win),
        zoomed: false,
        restoreBounds: undefined,
      }
    : {
        ...win,
        restoreBounds: {
          x: win.x,
          y: win.y,
          width: win.width,
          height: win.height,
        },
        shaded: false,
        zoomed: true,
      };
}

export function selectionAfterClick(
  current: string[],
  id: string,
  additive: boolean,
): string[] {
  if (!additive) return [id];
  return current.includes(id)
    ? current.filter((item) => item !== id)
    : [...current, id];
}

export function intersects(a: Bounds, b: Bounds): boolean {
  return (
    a.x < b.x + b.width &&
    a.x + a.width > b.x &&
    a.y < b.y + b.height &&
    a.y + a.height > b.y
  );
}

export function marqueeBounds(start: Point, end: Point): Bounds {
  return {
    x: Math.min(start.x, end.x),
    y: Math.min(start.y, end.y),
    width: Math.abs(end.x - start.x),
    height: Math.abs(end.y - start.y),
  };
}

/** Clamp one shared movement vector so selected icons retain their spacing. */
export function moveIconGroup(
  positions: Record<string, Point>,
  ids: string[],
  delta: Point,
  area: Bounds,
  snap = false,
): Record<string, Point> {
  const selected = ids.filter((id) => positions[id]);
  if (!selected.length) return {};
  const points = selected.map((id) => positions[id]);
  const anchor = points[0];
  const dx = snap
    ? Math.round((anchor.x + delta.x) / 8) * 8 - anchor.x
    : delta.x;
  const dy = snap
    ? Math.round((anchor.y + delta.y) / 8) * 8 - anchor.y
    : delta.y;
  const x = Math.min(
    Math.max(dx, area.x - Math.min(...points.map((point) => point.x))),
    area.x +
      area.width -
      ICON_SIZE.width -
      Math.max(...points.map((point) => point.x)),
  );
  const y = Math.min(
    Math.max(dy, area.y - Math.min(...points.map((point) => point.y))),
    area.y +
      area.height -
      ICON_SIZE.height -
      Math.max(...points.map((point) => point.y)),
  );
  return Object.fromEntries(
    selected.map((id) => [
      id,
      { x: positions[id].x + x, y: positions[id].y + y },
    ]),
  );
}

export function initialIconPosition(index: number, area: Bounds): Point {
  const rows = Math.max(1, Math.floor(area.height / ICON_SIZE.height));
  const columnWidth = ICON_SIZE.width + 6;
  const columns = Math.max(1, Math.floor((area.width + 6) / columnWidth));
  const column = Math.floor(index / rows) % columns;
  const page = Math.floor(index / (rows * columns));
  return {
    x:
      area.x + Math.max(0, area.width - ICON_SIZE.width) - column * columnWidth,
    y: area.y + ((index % rows) + page * rows) * ICON_SIZE.height,
  };
}

/** Fit saved positions for display only; displaced icons use free cells, never a shared edge. */
export function fitDesktopIcons(
  ids: string[],
  saved: Record<string, Point>,
  area: Bounds,
): { positions: Record<string, Point>; height: number } {
  const positions: Record<string, Point> = {};
  const occupied: Bounds[] = [];
  let height = Math.max(area.height, ICON_SIZE.height);
  for (let index = 0; index < ids.length; index++)
    height = Math.max(
      height,
      initialIconPosition(index, area).y + ICON_SIZE.height - area.y,
    );
  for (const id of ids) {
    const point = saved[id];
    if (
      point &&
      point.x >= area.x &&
      point.x + ICON_SIZE.width <= area.x + area.width &&
      point.y >= area.y &&
      point.y + ICON_SIZE.height <= area.y + height
    ) {
      positions[id] = point;
      occupied.push({ ...point, ...ICON_SIZE });
    }
  }
  let slot = 0;
  for (const [index, id] of ids.entries()) {
    if (positions[id]) continue;
    let point = initialIconPosition(index, area);
    while (
      occupied.some((rect) => intersects(rect, { ...point, ...ICON_SIZE }))
    ) {
      point = initialIconPosition(slot++, area);
    }
    positions[id] = point;
    occupied.push({ ...point, ...ICON_SIZE });
    height = Math.max(height, point.y + ICON_SIZE.height - area.y);
  }
  return { positions, height };
}

export function overviewTransform(
  bounds: Bounds,
  index: number,
  count: number,
  viewport: Size,
): string {
  const columns = Math.max(
    1,
    Math.ceil(Math.sqrt((count * viewport.width) / viewport.height)),
  );
  const rows = Math.ceil(count / columns);
  const cellWidth = (viewport.width - 24) / columns;
  const cellHeight = (viewport.height - 80) / rows;
  const scale = Math.min(
    (cellWidth - 20) / bounds.width,
    (cellHeight - 20) / bounds.height,
    1,
  );
  const x =
    12 + (index % columns) * cellWidth + (cellWidth - bounds.width * scale) / 2;
  const y =
    34 +
    Math.floor(index / columns) * cellHeight +
    (cellHeight - bounds.height * scale) / 2;
  return `translate(${Math.round(x - bounds.x)}px, ${Math.round(y - bounds.y)}px) scale(${Math.max(0.1, scale)})`;
}

/** Membership does not control mounting: hidden windows keep their component identity. */
export function inWorkspace(win: DesktopWindow, workspaceId: string): boolean {
  return win.sticky || win.workspaceId === workspaceId;
}

export function minimizedWindows(
  windows: DesktopWindow[],
  workspaceId: string,
) {
  return windows.filter(
    (win) => win.minimized && inWorkspace(win, workspaceId),
  );
}

export function defaultWorkspaces(): Workspace[] {
  return ['Workspace', 'Development', 'Documents', 'Studio'].map(
    (name, index) => ({
      id: `workspace-${index + 1}`,
      name,
      order: [],
      activeWindowId: null,
      clip: [],
    }),
  );
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function strings(value: unknown): Record<string, string> {
  return Object.fromEntries(
    Object.entries(record(value)).filter(
      (entry): entry is [string, string] => typeof entry[1] === 'string',
    ),
  );
}

/** Persisted application references are opaque identifiers, not paths or URLs. */
export function validAppId(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    value.length <= 128 &&
    /^[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(value)
  );
}

export function isApplicationLauncher(
  icon: DesktopIcon,
): icon is DesktopIcon & { appId: string } {
  return (
    icon.applicationLauncher === true && validAppId(icon.appId) && !icon.isTrash
  );
}

/** A stored choice takes precedence over host launchers and late application defaults. */
export function isApplicationOnDesktop(
  layout: Pick<DesktopLayout, 'desktopApplications'>,
  apps: AppDefinition[],
  icons: DesktopIcon[],
  appId: string,
): boolean {
  if (!validAppId(appId)) return false;
  const choice = layout.desktopApplications?.[appId];
  if (typeof choice === 'boolean') return choice;
  return (
    icons.some((icon) => isApplicationLauncher(icon) && icon.appId === appId) ||
    apps.some((app) => app.id === appId && app.defaultDesktop === true)
  );
}

/** Only presentation changes: keep window state and both sets of icon positions intact. */
export function showDesktopApplication(
  layout: DesktopLayout,
  appId: string,
  shown: boolean,
): DesktopLayout {
  if (!validAppId(appId) || typeof shown !== 'boolean') return layout;
  if (layout.desktopApplications?.[appId] === shown) return layout;
  return {
    ...layout,
    desktopApplications: { ...layout.desktopApplications, [appId]: shown },
  };
}

/** Combine resource icons with one host-owned or generated launcher for each shown application. */
export function desktopApplicationIcons(
  layout: Pick<DesktopLayout, 'desktopApplications'>,
  apps: AppDefinition[],
  icons: DesktopIcon[],
  launchApp: (appId: string) => void,
): DesktopIcon[] {
  const hostApplications = new Set(
    icons.filter(isApplicationLauncher).map((icon) => icon.appId),
  );
  const result = icons.flatMap((icon) => {
    if (!isApplicationLauncher(icon)) return [icon];
    if (!isApplicationOnDesktop(layout, apps, icons, icon.appId)) return [];
    const app = apps.find((candidate) => candidate.id === icon.appId);
    return [
      {
        ...icon,
        trashable: false,
        onOpen: icon.onOpen ?? (() => launchApp(icon.appId)),
        ...(!app || app.unavailable !== undefined || app.launchable === false
          ? { status: { label: 'Unavailable', tone: 'warning' as const } }
          : {}),
      },
    ];
  });
  const candidates = new Set([
    ...apps.filter((app) => app.defaultDesktop === true).map((app) => app.id),
    ...Object.keys(layout.desktopApplications ?? {}),
  ]);
  for (const appId of candidates) {
    if (
      hostApplications.has(appId) ||
      !isApplicationOnDesktop(layout, apps, icons, appId)
    )
      continue;
    const app = apps.find((candidate) => candidate.id === appId);
    result.push({
      id: `application:${appId}`,
      appId,
      applicationLauncher: true,
      label: app?.title ?? appId,
      icon: app?.icon ?? 'document',
      trashable: false,
      onOpen: () => launchApp(appId),
      ...(!app || app.unavailable !== undefined || app.launchable === false
        ? { status: { label: 'Unavailable', tone: 'warning' as const } }
        : {}),
    });
  }
  return result;
}

/** Arranging visible icons must not erase positions belonging to hidden launchers. */
export function arrangeDesktopIconPositions(
  positions: Record<string, Point>,
  icons: DesktopIcon[],
  area: Bounds,
): Record<string, Point> {
  return {
    ...positions,
    ...Object.fromEntries(
      icons.map((icon, index) => [icon.id, initialIconPosition(index, area)]),
    ),
  };
}

function savedWindowId(value: unknown, appId: string): value is string {
  return (
    typeof value === 'string' &&
    value.length <= 1024 &&
    value.startsWith(`${appId}:`) &&
    value.length > appId.length + 1 &&
    Array.from(value).every((character) => {
      const code = character.charCodeAt(0);
      return code >= 32 && code !== 127;
    })
  );
}

function point(value: unknown): Point | undefined {
  const candidate = record(value);
  return Number.isFinite(candidate.x) && Number.isFinite(candidate.y)
    ? { x: Number(candidate.x), y: Number(candidate.y) }
    : undefined;
}

function bounds(value: unknown): Bounds | undefined {
  const candidate = record(value);
  const position = point(value);
  return position &&
    Number.isFinite(candidate.width) &&
    Number.isFinite(candidate.height) &&
    Number(candidate.width) > 0 &&
    Number(candidate.height) > 0
    ? {
        ...position,
        width: Math.min(10000, Number(candidate.width)),
        height: Math.min(10000, Number(candidate.height)),
      }
    : undefined;
}

export function unsupportedLayout(value: unknown): boolean {
  const layout = record(value);
  return (
    layout.version !== undefined && layout.version !== 1 && layout.version !== 2
  );
}

/** Unknown versions stay read-only at the controller boundary. Version 1 is migrated. */
export function sanitizeLayout(
  value: unknown,
  apps: AppDefinition[],
): DesktopLayout {
  const raw = record(value);
  const valid =
    (raw.version === 1 || raw.version === 2) && Array.isArray(raw.windows);
  const rawWorkspaces =
    raw.version === 2 && Array.isArray(raw.workspaces) ? raw.workspaces : [];
  const workspaces: Workspace[] = [];
  for (const item of rawWorkspaces.slice(0, 16)) {
    const workspace = record(item);
    if (
      typeof workspace.id !== 'string' ||
      !workspace.id ||
      workspaces.some((w) => w.id === workspace.id)
    )
      continue;
    const clip: Workspace['clip'] = [];
    for (const item of Array.isArray(workspace.clip)
      ? workspace.clip.slice(0, 32)
      : []) {
      const shortcut = record(item);
      if (
        typeof shortcut.id !== 'string' ||
        clip.some((s) => s.id === shortcut.id) ||
        !validAppId(shortcut.appId)
      )
        continue;
      clip.push({
        id: shortcut.id,
        appId: shortcut.appId,
        title: String(shortcut.title ?? shortcut.appId).slice(0, 128),
        params: strings(shortcut.params),
      });
    }
    workspaces.push({
      id: workspace.id,
      name: String(workspace.name || 'Workspace').slice(0, 40),
      order: Array.isArray(workspace.order)
        ? workspace.order.filter((id): id is string => typeof id === 'string')
        : [],
      activeWindowId:
        typeof workspace.activeWindowId === 'string'
          ? workspace.activeWindowId
          : null,
      clip,
    });
  }
  if (!workspaces.length) workspaces.push(...defaultWorkspaces());
  const currentWorkspaceId = workspaces.some(
    (w) => w.id === raw.currentWorkspaceId,
  )
    ? String(raw.currentWorkspaceId)
    : workspaces[0].id;
  const windows: DesktopWindow[] = [];
  for (const item of valid ? (raw.windows as unknown[]).slice(0, 100) : []) {
    const candidate = record(item);
    const geometry = bounds(candidate);
    if (!validAppId(candidate.appId) || !geometry) continue;
    const app = apps.find((a) => a.id === candidate.appId);
    const params = strings(candidate.params);
    // A temporarily missing definition cannot recompute a resource's window key.
    // Preserve valid saved identities when that definition becomes available again.
    const id = savedWindowId(candidate.id, candidate.appId)
      ? candidate.id
      : app
        ? windowIdentity(app, params)
        : undefined;
    if (!id) continue;
    if (windows.some((win) => win.id === id)) continue;
    windows.push({
      ...geometry,
      id,
      appId: candidate.appId,
      title: String(candidate.title || app?.title || candidate.appId).slice(
        0,
        256,
      ),
      params,
      viewState: record(candidate.viewState),
      workspaceId: workspaces.some((w) => w.id === candidate.workspaceId)
        ? String(candidate.workspaceId)
        : workspaces[0].id,
      sticky: candidate.sticky === true,
      minimized: candidate.minimized === true,
      shaded: candidate.shaded === true,
      zoomed: candidate.zoomed === true && !!bounds(candidate.restoreBounds),
      restoreBounds: bounds(candidate.restoreBounds),
    });
  }
  for (const workspace of workspaces) {
    const members = windows.filter((win) => inWorkspace(win, workspace.id));
    workspace.order = [
      ...new Set([
        ...workspace.order.filter((id) => members.some((win) => win.id === id)),
        ...members.map((win) => win.id),
      ]),
    ];
    const requested =
      workspace.id === currentWorkspaceId
        ? raw.activeWindowId
        : workspace.activeWindowId;
    workspace.activeWindowId =
      requested === null
        ? null
        : members.some((win) => win.id === requested && !win.minimized)
          ? String(requested)
          : ([...workspace.order]
              .reverse()
              .find((id) =>
                members.some((win) => win.id === id && !win.minimized),
              ) ?? null);
  }
  const dock: DesktopLayout['dock'] = [];
  for (const item of raw.version === 2 && Array.isArray(raw.dock)
    ? raw.dock
    : apps
        .filter(
          (app) => app.launchable !== false && app.defaultPinned !== false,
        )
        .map((app) => ({ appId: app.id, pinned: true }))) {
    const entry = record(item);
    if (validAppId(entry.appId) && !dock.some((d) => d.appId === entry.appId))
      dock.push({ appId: entry.appId, pinned: entry.pinned === true });
  }
  for (const win of windows) {
    if (!dock.some((entry) => entry.appId === win.appId))
      dock.push({ appId: win.appId, pinned: false });
  }
  const pinnedMenus: DesktopLayout['pinnedMenus'] = [];
  for (const item of raw.version === 2 && Array.isArray(raw.pinnedMenus)
    ? raw.pinnedMenus.slice(0, 12)
    : []) {
    const menu = record(item);
    const position = point(menu);
    if (
      !position ||
      typeof menu.id !== 'string' ||
      pinnedMenus.some((m) => m.id === menu.id) ||
      !['desktop', 'application', 'window'].includes(String(menu.kind))
    )
      continue;
    if (
      menu.kind !== 'desktop' &&
      !windows.some((win) => win.id === menu.ownerWindowId)
    )
      continue;
    pinnedMenus.push({
      ...position,
      id: menu.id,
      kind: menu.kind as 'desktop' | 'application' | 'window',
      title: String(menu.title || 'Menu'),
      ownerWindowId:
        menu.kind !== 'desktop' && typeof menu.ownerWindowId === 'string'
          ? menu.ownerWindowId
          : undefined,
      groupId: typeof menu.groupId === 'string' ? menu.groupId : undefined,
    });
  }
  const iconPositions = (value: unknown): Record<string, Point> =>
    Object.fromEntries(
      Object.entries(record(value)).flatMap(([id, value]) => {
        const position = point(value);
        return position ? [[id, position]] : [];
      }),
    );
  const desktopApplications = Object.fromEntries(
    Object.entries(record(raw.desktopApplications)).filter(
      ([appId, shown]) => validAppId(appId) && typeof shown === 'boolean',
    ),
  ) as Record<string, boolean>;
  return {
    version: 2,
    windows,
    icons: iconPositions(raw.icons),
    compactIcons: iconPositions(raw.compactIcons),
    ...(raw.desktopApplications === undefined ? {} : { desktopApplications }),
    workspaces,
    currentWorkspaceId,
    dock,
    pinnedMenus,
    clipPosition: point(raw.clipPosition) ?? { x: 10, y: 42 },
    activeWindowId: workspaces.find((w) => w.id === currentWorkspaceId)!
      .activeWindowId,
  };
}

/** Restore workspace-local focus and order without moving or recreating windows. */
export function switchWorkspaceLayout(
  layout: DesktopLayout,
  id: string,
): DesktopLayout {
  const workspace = layout.workspaces.find((w) => w.id === id);
  if (!workspace || id === layout.currentWorkspaceId) return layout;
  const activeWindowId =
    workspace.activeWindowId &&
    layout.windows.some(
      (win) =>
        win.id === workspace.activeWindowId &&
        !win.minimized &&
        inWorkspace(win, id),
    )
      ? workspace.activeWindowId
      : ([...workspace.order]
          .reverse()
          .find((key) =>
            layout.windows.some(
              (win) => win.id === key && !win.minimized && inWorkspace(win, id),
            ),
          ) ?? null);
  return { ...layout, currentWorkspaceId: id, activeWindowId };
}

export function activateLayoutWindow(
  layout: DesktopLayout,
  id: string,
  reveal = false,
): DesktopLayout {
  const target = layout.windows.find((win) => win.id === id);
  if (!target) return layout;
  const workspaceId = target.sticky
    ? layout.currentWorkspaceId
    : target.workspaceId;
  return {
    ...layout,
    currentWorkspaceId: workspaceId,
    activeWindowId: id,
    windows: layout.windows.map((win) =>
      win.id === id
        ? { ...win, minimized: false, shaded: reveal ? false : win.shaded }
        : win,
    ),
    workspaces: layout.workspaces.map((workspace) =>
      workspace.id === workspaceId
        ? {
            ...workspace,
            activeWindowId: id,
            order: [...workspace.order.filter((key) => key !== id), id],
          }
        : workspace,
    ),
  };
}

export function reconcileWorkspaceWindows(
  layout: DesktopLayout,
): DesktopLayout {
  const workspaces = layout.workspaces.map((workspace) => {
    const members = layout.windows.filter((win) =>
      inWorkspace(win, workspace.id),
    );
    const order = [
      ...new Set([
        ...workspace.order.filter((id) => members.some((win) => win.id === id)),
        ...members.map((win) => win.id),
      ]),
    ];
    const activeWindowId = members.some(
      (win) => win.id === workspace.activeWindowId && !win.minimized,
    )
      ? workspace.activeWindowId
      : ([...order]
          .reverse()
          .find((id) =>
            members.some((win) => win.id === id && !win.minimized),
          ) ?? null);
    return { ...workspace, order, activeWindowId };
  });
  return {
    ...layout,
    workspaces,
    activeWindowId:
      workspaces.find((workspace) => workspace.id === layout.currentWorkspaceId)
        ?.activeWindowId ?? null,
    pinnedMenus: layout.pinnedMenus.filter(
      (menu) =>
        !menu.ownerWindowId ||
        layout.windows.some((win) => win.id === menu.ownerWindowId),
    ),
  };
}
