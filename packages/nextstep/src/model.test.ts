import { describe, expect, it } from 'vitest';
import {
  activateLayoutWindow,
  defaultWorkspaces,
  desktopLayoutMode,
  inWorkspace,
  reconcileWorkspaceWindows,
  switchWorkspaceLayout,
  unsupportedLayout,
  initialIconPosition,
  iconWorkArea,
  fitDesktopIcons,
  ICON_SIZE,
  intersects,
  marqueeBounds,
  mergeMenuGroups,
  minimizedWindows,
  moveIconGroup,
  overviewTransform,
  resizeBounds,
  sanitizeLayout,
  selectionAfterClick,
  toggleWindowZoom,
  visibleBounds,
  windowIdentity,
  windowWorkArea,
} from './model';
import type { AppDefinition, DesktopWindow } from './types';

const app: AppDefinition = {
  id: 'files',
  title: 'Files',
  icon: 'folder',
  component: () => null,
  windowKey: (params) => params.folder ?? 'documents',
};
const bounds = { x: 70, y: 80, width: 500, height: 400 };
const viewport = { width: 1024, height: 768 };
const workArea = windowWorkArea(viewport, false);

describe('application menus', () => {
  it('keeps optional utility launchers out of a new Dock while preserving explicit user pins', () => {
    const utility = { ...app, id: 'utility', defaultPinned: false };
    const apps = [app, utility];
    const initial = sanitizeLayout(undefined, apps);
    expect(initial.dock).toEqual([{ appId: app.id, pinned: true }]);
    const saved = {
      ...initial,
      dock: [{ appId: utility.id, pinned: true }],
    };
    expect(sanitizeLayout(saved, apps).dock).toEqual(saved.dock);
  });
  it('replaces built-in menus and inserts app menus without duplicate IDs', () => {
    const defaults = ['desktop', 'file', 'view', 'window'].map((id) => ({
      id,
      label: id,
      items: [],
    }));
    const file = {
      id: 'file',
      label: 'File',
      items: [{ label: 'New Folder' }],
    };
    const result = mergeMenuGroups(defaults, [
      file,
      { id: 'edit', label: 'Edit', items: [] },
    ]);
    expect(result.map((group) => group.id)).toEqual([
      'desktop',
      'file',
      'edit',
      'view',
      'window',
    ]);
    expect(result[1]).toEqual(file);
    expect(defaults.map((group) => group.id)).toEqual([
      'desktop',
      'file',
      'view',
      'window',
    ]);
  });
});

describe('window geometry and restoration', () => {
  it('makes per-folder windows stable and distinct', () => {
    expect(windowIdentity(app)).toBe('files:documents');
    expect(windowIdentity(app, { folder: 'pictures' })).toBe('files:pictures');
  });
  it('keeps title bars and content reachable when a display shrinks without mutating saved bounds', () => {
    const saved = { ...bounds, x: 1300, y: 900 };
    const result = visibleBounds(
      saved,
      windowWorkArea({ width: 390, height: 844 }, false),
    );
    expect(result.x).toBeGreaterThanOrEqual(4);
    expect(result.y).toBeGreaterThanOrEqual(26);
    expect(result.x + result.width).toBeLessThanOrEqual(390);
    expect(result.y + result.height).toBeLessThanOrEqual(844 - 24);
    expect(saved).toEqual({ ...bounds, x: 1300, y: 900 });
  });
  it('limits all eight resize directions and keeps minimum dimensions', () => {
    const minimum = { width: 240, height: 160 };
    for (const shelf of [false, true])
      for (const movement of [-2000, 2000])
        for (const edge of ['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw']) {
          const area = windowWorkArea(viewport, shelf);
          const result = resizeBounds(
            bounds,
            { x: movement, y: movement },
            edge,
            minimum,
            area,
          );
          expect(result.width).toBeGreaterThanOrEqual(minimum.width);
          expect(result.height).toBeGreaterThanOrEqual(minimum.height);
          expect(result.x).toBeGreaterThanOrEqual(area.x);
          expect(result.y).toBeGreaterThanOrEqual(area.y);
          expect(result.x + result.width).toBeLessThanOrEqual(
            area.x + area.width,
          );
          expect(result.y + result.height).toBeLessThanOrEqual(
            area.y + area.height,
          );
          if (edge.includes('w'))
            expect(result.x + result.width).toBe(bounds.x + bounds.width);
          else expect(result.x).toBe(bounds.x);
          if (edge.includes('n'))
            expect(result.y + result.height).toBe(bounds.y + bounds.height);
          else expect(result.y).toBe(bounds.y);
        }
  });
  it('resizing from the west preserves the east edge at the minimum width', () => {
    const result = resizeBounds(
      bounds,
      { x: 490, y: 0 },
      'w',
      { width: 240, height: 160 },
      workArea,
    );
    expect(result.width).toBe(240);
    expect(result.x + result.width).toBe(bounds.x + bounds.width);
  });
  it('overview derives transforms and never changes persisted geometry', () => {
    const saved = structuredClone(bounds);
    expect(overviewTransform(bounds, 2, 5, viewport)).toMatch(
      /^translate\(.+\) scale\(.+\)$/,
    );
    expect(bounds).toEqual(saved);
  });
  it('preserves unavailable applications while filtering malformed geometry from stored layouts', () => {
    const layout = {
      version: 1,
      activeWindowId: 'missing',
      icons: { good: { x: 20, y: 40 }, bad: { x: NaN, y: 20 } },
      windows: [
        {
          ...bounds,
          id: 'files:documents',
          appId: 'files',
          title: 'Documents',
          params: {},
          shaded: false,
          zoomed: false,
        },
        {
          ...bounds,
          id: 'old:main',
          appId: 'old',
          title: 'Removed app',
          params: {},
          shaded: false,
          zoomed: false,
        },
        {
          ...bounds,
          x: Infinity,
          id: 'files:invalid',
          appId: 'files',
          title: 'Invalid',
          params: {},
          shaded: false,
          zoomed: false,
        },
      ],
    };
    const restored = sanitizeLayout(layout, [app]);
    expect(restored.windows.map((win) => win.id)).toEqual([
      'files:documents',
      'old:main',
    ]);
    expect(restored.activeWindowId).toBe('old:main');
    expect(restored.icons).toEqual({ good: { x: 20, y: 40 } });
    const desktopFocused = sanitizeLayout(
      { ...restored, activeWindowId: null },
      [app],
    );
    expect(desktopFocused.activeWindowId).toBeNull();
    expect(desktopFocused.windows).toEqual(restored.windows);
  });
});

describe('dynamic window work areas', () => {
  it('reserves the desktop Dock while reclaiming an empty miniwindow shelf', () => {
    expect(windowWorkArea(viewport, false)).toEqual({
      x: 4,
      y: 28,
      width: 952,
      height: 732,
    });
    expect(windowWorkArea(viewport, true)).toEqual({
      x: 4,
      y: 28,
      width: 952,
      height: 656,
    });
  });

  it('keeps the narrow-screen menus and actual bottom Dock space without an empty shelf row', () => {
    const mobile = { width: 390, height: 844 };
    expect(windowWorkArea(mobile, false)).toEqual({
      x: 4,
      y: 90,
      width: 382,
      height: 690,
    });
    expect(windowWorkArea(mobile, true)).toEqual({
      x: 4,
      y: 90,
      width: 382,
      height: 614,
    });
  });

  it('reclaims short landscape space with a side Dock while keeping narrower keyboards full width', () => {
    for (const { viewport, sideDock, area } of [
      {
        viewport: { width: 568, height: 320 },
        sideDock: true,
        area: { x: 4, y: 64, width: 496, height: 248 },
      },
      {
        viewport: { width: 844, height: 390 },
        sideDock: true,
        area: { x: 4, y: 64, width: 772, height: 318 },
      },
      {
        viewport: { width: 390, height: 320 },
        sideDock: false,
        area: { x: 4, y: 64, width: 382, height: 192 },
      },
      {
        viewport: { width: 320, height: 260 },
        sideDock: false,
        area: { x: 4, y: 64, width: 312, height: 132 },
      },
    ]) {
      expect(desktopLayoutMode(viewport)).toMatchObject({
        short: true,
        sideDock,
      });
      expect(windowWorkArea(viewport, false)).toEqual(area);
      expect(windowWorkArea(viewport, true)).toEqual({
        ...area,
        height: area.height - 76,
      });
      for (const shelf of [false, true]) {
        const windowArea = windowWorkArea(viewport, shelf);
        const icons = iconWorkArea(viewport, shelf);
        expect(icons.x).toBeGreaterThan(windowArea.x);
        expect(icons.y).toBeGreaterThan(windowArea.y);
        expect(icons.x + icons.width).toBeLessThan(
          windowArea.x + windowArea.width,
        );
        expect(icons.y + icons.height).toBeLessThan(
          windowArea.y + windowArea.height,
        );
      }
    }
    expect(desktopLayoutMode({ width: 568, height: 481 })).toEqual({
      mobile: true,
      short: false,
      sideDock: false,
    });
    expect(desktopLayoutMode({ width: 480, height: 480 }).sideDock).toBe(false);
  });

  it('counts only minimized windows belonging to the visible workspace, including sticky windows', () => {
    const normal = testWindow('normal');
    const local = { ...testWindow('local'), minimized: true };
    const remote = { ...testWindow('remote', 'workspace-2'), minimized: true };
    const sticky = {
      ...testWindow('sticky', 'workspace-3'),
      minimized: true,
      sticky: true,
    };
    const windows = [normal, local, remote, sticky];
    expect(minimizedWindows(windows, 'workspace-1')).toEqual([local, sticky]);
    expect(minimizedWindows(windows, 'workspace-2')).toEqual([remote, sticky]);
    expect(minimizedWindows(windows, 'workspace-4')).toEqual([sticky]);
    expect(minimizedWindows([normal, remote], 'workspace-1')).toEqual([]);
    expect(
      minimizedWindows([{ ...sticky, minimized: false }], 'workspace-4'),
    ).toEqual([]);
  });

  it('temporarily clamps normal windows when the shelf appears and recovers their saved geometry when it leaves', () => {
    const saved = {
      ...testWindow('bottom'),
      x: 100,
      y: 460,
      width: 500,
      height: 300,
    };
    const snapshot = structuredClone(saved);
    expect(visibleBounds(saved, workArea)).toEqual({
      x: 100,
      y: 460,
      width: 500,
      height: 300,
    });
    expect(visibleBounds(saved, windowWorkArea(viewport, true))).toEqual({
      x: 100,
      y: 384,
      width: 500,
      height: 300,
    });
    expect(visibleBounds(saved, workArea)).toEqual({
      x: 100,
      y: 460,
      width: 500,
      height: 300,
    });
    expect(saved).toEqual(snapshot);
  });

  it('retains normal and restore bounds while maximizing across shelf and display changes', () => {
    const normal = {
      ...testWindow('zoom'),
      x: 90,
      y: 400,
      width: 700,
      height: 360,
    };
    const zoomed = toggleWindowZoom(normal);
    const snapshot = structuredClone(zoomed);
    expect(zoomed).toMatchObject({
      x: 90,
      y: 400,
      width: 700,
      height: 360,
      zoomed: true,
    });
    expect(zoomed.restoreBounds).toEqual({
      x: 90,
      y: 400,
      width: 700,
      height: 360,
    });
    for (const area of [
      workArea,
      windowWorkArea(viewport, true),
      windowWorkArea({ width: 390, height: 844 }, false),
      windowWorkArea({ width: 568, height: 320 }, true),
      windowWorkArea({ width: 390, height: 320 }, false),
    ]) {
      visibleBounds(zoomed.restoreBounds!, area);
      expect(zoomed).toEqual(snapshot);
    }
    expect(toggleWindowZoom(zoomed)).toEqual({
      ...normal,
      restoreBounds: undefined,
    });
    const legacy = { ...zoomed, x: 4, y: 28, width: 944, height: 656 };
    expect(toggleWindowZoom(legacy)).toEqual({
      ...normal,
      restoreBounds: undefined,
    });
  });

  it('caps minimum sizes at a smaller available area without moving the fixed resize edges', () => {
    const area = windowWorkArea({ width: 240, height: 200 }, false);
    const minimum = { width: 300, height: 180 };
    expect(visibleBounds(bounds, area, minimum)).toEqual(area);
    for (const edge of ['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw'])
      expect(
        resizeBounds(bounds, { x: 1000, y: 1000 }, edge, minimum, area),
      ).toEqual(area);
    const tiny = windowWorkArea({ width: 3, height: 100 }, true);
    expect(tiny.width).toBe(1);
    expect(tiny.height).toBe(1);
  });
});

describe('desktop selection', () => {
  it('supports replacement, additive selection, and toggling selected icons off', () => {
    expect(selectionAfterClick(['a', 'b'], 'c', false)).toEqual(['c']);
    expect(selectionAfterClick(['a'], 'b', true)).toEqual(['a', 'b']);
    expect(selectionAfterClick(['a', 'b'], 'a', true)).toEqual(['b']);
  });
  it('selects intersections in either marquee direction', () => {
    const box = marqueeBounds({ x: 300, y: 300 }, { x: 100, y: 100 });
    expect(box).toEqual({ x: 100, y: 100, width: 200, height: 200 });
    expect(intersects(box, { x: 110, y: 120, width: 88, height: 74 })).toBe(
      true,
    );
    expect(intersects(box, { x: 350, y: 120, width: 88, height: 74 })).toBe(
      false,
    );
  });
  it('aligns columns to the right of the available area, above the Dock and miniwindows', () => {
    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 500, height: 652 },
      { width: 390, height: 844 },
    ]) {
      for (const minimized of [false, true]) {
        const area = iconWorkArea(viewport, minimized);
        const points = Array.from({ length: 9 }, (_, index) =>
          initialIconPosition(index, area),
        );
        expect(points[0].x + ICON_SIZE.width).toBe(area.x + area.width);
        expect(points[0].y).toBe(area.y);
        expect(area.y).toBeGreaterThanOrEqual(viewport.width < 600 ? 90 : 26);
        expect(area.x + area.width).toBe(
          viewport.width - (viewport.width < 600 ? 8 : 72),
        );
        for (const point of points) {
          expect(point.x).toBeGreaterThanOrEqual(area.x);
          expect(point.y + ICON_SIZE.height).toBeLessThanOrEqual(
            area.y + area.height,
          );
        }
      }
    }
  });
  it('temporarily fits displaced icons without overwriting saved coordinates or stacking them', () => {
    const wide = iconWorkArea({ width: 1440, height: 900 }, false);
    const narrow = iconWorkArea({ width: 1024, height: 600 }, true);
    const ids = ['one', 'two', 'three'];
    const saved = Object.fromEntries(
      ids.map((id, i) => [id, initialIconPosition(i, wide)]),
    );
    const before = structuredClone(saved);
    const fitted = fitDesktopIcons(ids, saved, narrow);
    expect(
      new Set(Object.values(fitted.positions).map((p) => `${p.x},${p.y}`)).size,
    ).toBe(ids.length);
    expect(fitDesktopIcons(ids, saved, wide).positions).toEqual(before);
    expect(saved).toEqual(before);
  });
  it('keeps saved wider-spaced positions when adding launchers to the compact grid', () => {
    const saved = {
      first: { x: 1112, y: 40 },
      second: { x: 1112, y: 124 },
    };
    const before = structuredClone(saved);
    const area = iconWorkArea({ width: 1280, height: 720 }, false);
    const { positions } = fitDesktopIcons(
      ['first', 'second', 'new'],
      saved,
      area,
    );
    expect(positions.first).toEqual(before.first);
    expect(positions.second).toEqual(before.second);
    for (const point of Object.values(saved))
      expect(
        intersects(
          { ...point, ...ICON_SIZE },
          { ...positions.new, ...ICON_SIZE },
        ),
      ).toBe(false);
    expect(saved).toEqual(before);
  });
  it('keeps unmodified default positions while moving one icon and makes overflow scrollable', () => {
    const area = iconWorkArea({ width: 390, height: 400 }, false);
    const ids = Array.from({ length: 30 }, (_, i) => `icon-${i}`);
    const saved = { [ids[0]]: { x: area.x, y: area.y } };
    const { positions, height } = fitDesktopIcons(ids, saved, area);
    expect(positions[ids[0]]).toEqual(saved[ids[0]]);
    expect(positions[ids[1]]).toEqual(initialIconPosition(1, area));
    expect(height).toBeGreaterThan(area.height);
    const rects = Object.values(positions).map((point) => ({
      ...point,
      ...ICON_SIZE,
    }));
    for (const [index, rect] of rects.entries()) {
      expect(rect.x).toBeGreaterThanOrEqual(area.x);
      expect(rect.x + rect.width).toBeLessThanOrEqual(area.x + area.width);
      expect(rect.y + rect.height).toBeLessThanOrEqual(area.y + height);
      expect(
        rects.slice(index + 1).some((other) => intersects(rect, other)),
      ).toBe(false);
    }
  });
  it('sanitizes compact positions independently from wide desktop positions', () => {
    const result = sanitizeLayout(
      {
        icons: { icon: { x: 100, y: 120 } },
        compactIcons: { icon: { x: 200, y: 94 }, bad: { x: 'no', y: 0 } },
      },
      [],
    );
    expect(result.icons).toEqual({ icon: { x: 100, y: 120 } });
    expect(result.compactIcons).toEqual({ icon: { x: 200, y: 94 } });
    expect(sanitizeLayout({}, []).compactIcons).toEqual({});
  });
  it('keeps a dragged group spaced apart at every viewport edge and when snapping', () => {
    const positions = { first: { x: 100, y: 100 }, second: { x: 190, y: 184 } };
    const area = iconWorkArea(viewport, true);
    for (const delta of [
      { x: 2000, y: 2000 },
      { x: -2000, y: -2000 },
      { x: 23, y: 11 },
    ]) {
      for (const snap of [false, true]) {
        const result = moveIconGroup(
          positions,
          ['first', 'second'],
          delta,
          area,
          snap,
        );
        expect(result.second.x - result.first.x).toBe(90);
        expect(result.second.y - result.first.y).toBe(84);
        expect(result.first.x).toBeGreaterThanOrEqual(area.x);
        expect(result.first.y).toBeGreaterThanOrEqual(area.y);
        expect(result.second.x + ICON_SIZE.width).toBeLessThanOrEqual(
          area.x + area.width,
        );
        expect(result.second.y + ICON_SIZE.height).toBeLessThanOrEqual(
          area.y + area.height,
        );
      }
    }
    expect(positions).toEqual({
      first: { x: 100, y: 100 },
      second: { x: 190, y: 184 },
    });
    expect(
      moveIconGroup(positions, ['missing'], { x: 10, y: 10 }, area),
    ).toEqual({});
  });
});

function testWindow(id: string, workspaceId = 'workspace-1'): DesktopWindow {
  return {
    ...bounds,
    id: `files:${id}`,
    appId: 'files',
    title: id,
    params: { folder: id },
    workspaceId,
    sticky: false,
    minimized: false,
    viewState: { draft: 'unsaved' },
    shaded: false,
    zoomed: false,
  };
}

describe('versioned workspace persistence', () => {
  it('round-trips missing and disabled apps without losing resource identity, view state or owner references', () => {
    const initial = sanitizeLayout(undefined, [app]);
    const savedWindow = {
      ...testWindow('retained'),
      appId: 'org.example.tool',
      id: 'org.example.tool:resource:original',
      params: { resourceId: 'original' },
      viewState: { selection: ['one'], location: 'retained' },
      minimized: true,
    };
    const saved = {
      ...initial,
      windows: [savedWindow],
      dock: [{ appId: savedWindow.appId, pinned: true }],
      workspaces: initial.workspaces.map((workspace, index) =>
        index
          ? workspace
          : {
              ...workspace,
              order: [savedWindow.id],
              clip: [
                {
                  id: 'retained-shortcut',
                  appId: savedWindow.appId,
                  title: 'Resource',
                  params: savedWindow.params,
                },
              ],
            },
      ),
      pinnedMenus: [
        {
          id: 'retained-menu',
          kind: 'application' as const,
          title: 'Actions',
          groupId: 'actions',
          ownerWindowId: savedWindow.id,
          x: 20,
          y: 30,
        },
      ],
    };
    const missing = sanitizeLayout(saved, []);
    expect(missing.windows).toEqual([savedWindow]);
    expect(missing.dock).toEqual(saved.dock);
    expect(missing.workspaces[0].clip).toEqual(saved.workspaces[0].clip);
    expect(missing.pinnedMenus).toEqual(saved.pinnedMenus);
    const definition = {
      ...app,
      id: savedWindow.appId,
      windowKey: () => 'a-new-default-does-not-replace-saved-identities',
    };
    const disabled = sanitizeLayout(missing, [
      { ...definition, unavailable: 'Enable the application to continue.' },
    ]);
    const available = sanitizeLayout(disabled, [definition]);
    expect(available).toEqual(missing);
  });

  it('rejects unsafe application IDs and unverifiable missing window identities', () => {
    const initial = sanitizeLayout(undefined, []);
    const invalidIds = [
      '',
      '../tool',
      'https://tool',
      'app:resource',
      'a'.repeat(129),
    ];
    const appIds = [...invalidIds, 'org.example.tool'];
    const restored = sanitizeLayout(
      {
        ...initial,
        windows: [
          ...appIds.map((appId) => ({
            ...testWindow('one'),
            appId,
            id: `${appId}:resource`,
          })),
          {
            ...testWindow('other'),
            appId: 'org.example.tool',
            id: 'other-application:resource',
          },
          {
            ...testWindow('control'),
            appId: 'org.example.tool',
            id: 'org.example.tool:control\ncharacter',
          },
        ],
        dock: appIds.map((appId) => ({ appId, pinned: true })),
        workspaces: [
          {
            ...initial.workspaces[0],
            clip: appIds.map((appId, index) => ({
              id: String(index),
              appId,
              params: {},
            })),
          },
        ],
      },
      [],
    );
    expect(restored.windows.map((win) => win.id)).toEqual([
      'org.example.tool:resource',
    ]);
    expect(restored.dock).toEqual([
      { appId: 'org.example.tool', pinned: true },
    ]);
    expect(
      restored.workspaces[0].clip.map((shortcut) => shortcut.appId),
    ).toEqual(['org.example.tool']);
  });

  it('creates four workspaces, registered launchers, and stable defaults', () => {
    const result = sanitizeLayout(undefined, [app]);
    expect(result.version).toBe(2);
    expect(result.workspaces).toHaveLength(4);
    expect(result.currentWorkspaceId).toBe('workspace-1');
    expect(result.dock).toEqual([{ appId: 'files', pinned: true }]);
  });
  it('recognizes future versions so the controller can suppress persistence until reset', () => {
    expect(unsupportedLayout({ version: 99 })).toBe(true);
    expect(unsupportedLayout({ version: 1 })).toBe(false);
    expect(unsupportedLayout({ version: 2 })).toBe(false);
    expect(unsupportedLayout(null)).toBe(false);
  });
  it('migrates legacy windows and rejects malformed nested settings without throwing', () => {
    const result = sanitizeLayout(
      { version: 1, windows: [testWindow('a')], icons: null, workspaces: 42 },
      [app],
    );
    expect(result.windows[0].id).toBe('files:a');
    expect(result.windows[0].viewState).toEqual({ draft: 'unsaved' });
    expect(result.workspaces).toHaveLength(4);
    const broken = sanitizeLayout(
      {
        version: 2,
        windows: [
          null,
          42,
          {
            ...testWindow('b'),
            restoreBounds: { x: NaN, y: 0, width: 1, height: 1 },
          },
        ],
        dock: [null, { appId: 'missing', pinned: true }],
        workspaces: [
          null,
          {
            id: 'workspace-1',
            name: '',
            order: ['missing', 'files:b'],
            clip: [{ id: 'bad', appId: 'missing' }],
          },
        ],
        pinnedMenus: [
          { id: 'bad', kind: 'window', ownerWindowId: 'missing', x: 0, y: 0 },
        ],
        icons: { bad: null },
      },
      [app],
    );
    expect(broken.windows).toHaveLength(1);
    expect(broken.windows[0].restoreBounds).toBeUndefined();
    expect(broken.workspaces[0].order).toEqual(['files:b']);
    expect(broken.workspaces[0].clip).toEqual([
      { id: 'bad', appId: 'missing', title: 'missing', params: {} },
    ]);
    expect(broken.pinnedMenus).toEqual([]);
  });
  it('repairs missing running Dock entries and malformed maximized restore data', () => {
    const layout = {
      ...sanitizeLayout(undefined, [app]),
      dock: [],
      windows: [
        {
          ...testWindow('a'),
          zoomed: true,
          restoreBounds: { x: NaN, y: 0, width: 0, height: 0 },
        },
      ],
    };
    const repaired = sanitizeLayout(layout, [app]);
    expect(repaired.dock).toEqual([{ appId: 'files', pinned: false }]);
    expect(repaired.windows[0].zoomed).toBe(false);
  });
  it('keeps independent order/focus and object identities when switching workspaces', () => {
    const base = sanitizeLayout(undefined, [app]);
    const a = testWindow('a');
    const b = testWindow('b', 'workspace-2');
    let layout = reconcileWorkspaceWindows({ ...base, windows: [a, b] });
    layout = activateLayoutWindow(layout, a.id);
    const mountedWindows = layout.windows;
    layout = switchWorkspaceLayout(layout, 'workspace-2');
    expect(layout.activeWindowId).toBe(b.id);
    expect(layout.windows).toBe(mountedWindows);
    expect(layout.windows[1]).toBe(b);
    layout = activateLayoutWindow(layout, b.id);
    layout = switchWorkspaceLayout(layout, 'workspace-1');
    expect(layout.activeWindowId).toBe(a.id);
    expect(layout.windows[1].viewState).toEqual({ draft: 'unsaved' });
  });
  it('restores exactly a minimized document and switches to its owning workspace', () => {
    const base = sanitizeLayout(undefined, [app]);
    const a = {
      ...testWindow('a', 'workspace-2'),
      minimized: true,
      shaded: true,
    };
    const b = testWindow('b', 'workspace-2');
    const result = activateLayoutWindow(
      reconcileWorkspaceWindows({ ...base, windows: [a, b] }),
      a.id,
      true,
    );
    expect(result.currentWorkspaceId).toBe('workspace-2');
    expect(result.activeWindowId).toBe(a.id);
    expect(result.windows[0].minimized).toBe(false);
    expect(result.windows[0].shaded).toBe(false);
    expect(result.workspaces[1].order).toEqual([b.id, a.id]);
  });
  it('removes closed window menu owners and restores a visible workspace focus', () => {
    const base = sanitizeLayout(undefined, [app]);
    const a = testWindow('a');
    const b = { ...testWindow('b'), minimized: true };
    const layout = reconcileWorkspaceWindows({
      ...base,
      windows: [a, b],
      activeWindowId: b.id,
      pinnedMenus: [
        {
          id: 'pin',
          kind: 'window',
          title: 'Window',
          ownerWindowId: 'missing',
          x: 0,
          y: 0,
        },
      ],
    });
    expect(layout.activeWindowId).toBe(a.id);
    expect(layout.pinnedMenus).toEqual([]);
    expect(inWorkspace({ ...a, sticky: true }, 'workspace-4')).toBe(true);
    expect(defaultWorkspaces()[0].order).toEqual([]);
  });
});
