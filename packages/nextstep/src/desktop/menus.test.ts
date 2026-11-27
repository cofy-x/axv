import { describe, expect, it, vi } from 'vitest';
import { defaultWorkspaces } from '../model';
import type {
  AppDefinition,
  DesktopAPI,
  DesktopProps,
  DesktopWindow,
} from '../types';
import { resolveApplication } from './applications';
import {
  createDesktopContextMenu,
  createApplicationMenus,
  createWindowMenu,
} from './menus';

function desktopAPI(): DesktopAPI {
  return {
    windows: [],
    activeWindowId: null,
    workspaces: defaultWorkspaces(),
    currentWorkspaceId: 'workspace-1',
    launchApp: vi.fn(),
    isApplicationOnDesktop: vi.fn(() => false),
    showDesktopApplication: vi.fn(),
    openApp: vi.fn(() => 'opened'),
    closeWindow: vi.fn(async () => true),
    focusWindow: vi.fn(),
    setTitle: vi.fn(),
    updateWindowState: vi.fn(),
    switchWorkspace: vi.fn(),
    renameWorkspace: vi.fn(),
    moveWindow: vi.fn(),
    setWindowSticky: vi.fn(),
    minimizeWindow: vi.fn(),
    toggleShade: vi.fn(),
    toggleZoom: vi.fn(),
    snapWindow: vi.fn(),
    addClipShortcut: vi.fn(),
    removeClipShortcut: vi.fn(),
    showAllWindows: vi.fn(),
    arrangeIcons: vi.fn(),
    contextMenu: vi.fn(),
    dialog: vi.fn(async () => null),
    beforeClose: vi.fn(() => () => {}),
  };
}

function app(id: string, launchable?: boolean): AppDefinition {
  return { id, title: id, icon: 'document', component: () => null, launchable };
}

function win(id: string): DesktopWindow {
  return {
    id,
    appId: 'editor',
    title: id,
    params: { entry: id },
    viewState: {},
    workspaceId: 'workspace-1',
    sticky: false,
    minimized: false,
    shaded: false,
    zoomed: false,
    x: 0,
    y: 0,
    width: 500,
    height: 400,
  };
}

describe('host-independent desktop commands', () => {
  it('keeps Clip shortcut and workspace commands bound to their original targets', async () => {
    const api = desktopAPI();
    const owner = win('source');
    api.windows = [owner];
    api.activeWindowId = owner.id;
    api.workspaces[0].clip = [
      {
        id: 'saved',
        appId: 'editor',
        title: 'Saved document',
        params: { entry: 'saved' },
      },
      {
        id: 'missing',
        appId: 'missing',
        title: 'Missing application',
        params: {},
      },
    ];
    const props: DesktopProps = {
      apps: [app('editor')],
      icons: [],
      onAppearance: vi.fn(),
    };
    const workspaces = createDesktopContextMenu(props, api, []).find(
      (item) => item.label === 'Workspaces',
    )!.children!;
    expect(workspaces.some((item) => item.label === 'Switch Workspace')).toBe(
      false,
    );
    const shortcuts = workspaces.find(
      (item) => item.label === 'Shortcuts',
    )!.children!;
    shortcuts[0].children![0].action?.();
    expect(api.openApp).toHaveBeenCalledExactlyOnceWith('editor', {
      entry: 'saved',
    });
    expect(shortcuts[1].children![0].disabled).toBe(true);
    shortcuts[1].children![0].action?.();
    expect(api.launchApp).not.toHaveBeenCalled();
    shortcuts[0].children![1].action?.();
    expect(api.removeClipShortcut).toHaveBeenCalledExactlyOnceWith('saved');
    workspaces.find((item) => item.label === 'Add Active Window')!.action?.();
    expect(api.addClipShortcut).toHaveBeenCalledWith({
      appId: owner.appId,
      title: owner.title,
      params: owner.params,
    });
    workspaces
      .find((item) => item.label === 'Add Application Shortcut')!
      .children![0].action?.();
    expect(api.addClipShortcut).toHaveBeenCalledWith({
      appId: 'editor',
      title: 'editor',
      params: {},
    });
    vi.mocked(api.dialog).mockResolvedValueOnce({
      button: 'ok',
      value: 'Renamed',
    });
    workspaces.find((item) => item.label === 'Rename Workspace')!.action?.();
    api.currentWorkspaceId = 'workspace-2';
    await Promise.resolve();
    expect(api.renameWorkspace).toHaveBeenCalledExactlyOnceWith(
      'workspace-1',
      'Renamed',
    );
    vi.mocked(api.dialog).mockResolvedValueOnce({ button: 'ok', value: '' });
    workspaces.find((item) => item.label === 'Rename Workspace')!.action?.();
    await Promise.resolve();
    expect(api.renameWorkspace).toHaveBeenLastCalledWith('workspace-1', '');
  });

  it('offers a switch command for saved workspaces beyond the numbered controls', () => {
    const api = desktopAPI();
    api.workspaces.push({
      id: 'saved-extra',
      name: 'Sandbox',
      order: [],
      activeWindowId: null,
      clip: [],
    });
    const workspaces = createDesktopContextMenu(
      { apps: [], icons: [] },
      api,
      [],
    ).find((item) => item.label === 'Workspaces')!.children!;
    const extras = workspaces.find(
      (item) => item.label === 'Switch Workspace',
    )!.children!;
    expect(extras.map((item) => item.label)).toEqual(['Workspace 5 · Sandbox']);
    extras[0].action?.();
    expect(api.switchWorkspace).toHaveBeenCalledExactlyOnceWith('saved-extra');
  });

  it('saves contextual window shortcuts only when they address a target', () => {
    const api = desktopAPI();
    const contextual = app('editor', false);
    const unbound = { ...win('unbound'), params: {} };
    const bound = win('bound');
    const props: DesktopProps = { apps: [contextual], icons: [] };
    api.windows = [unbound, bound];
    api.activeWindowId = unbound.id;
    api.workspaces[0].clip = [unbound, bound].map((window) => ({
      id: window.id,
      title: window.title,
      appId: window.appId,
      params: window.params,
    }));
    const workspace = createDesktopContextMenu(props, api, []).find(
      (item) => item.label === 'Workspaces',
    )!.children!;
    expect(
      workspace.find((item) => item.label === 'Add Active Window')?.disabled,
    ).toBe(true);
    expect(createWindowMenu(unbound, api, contextual)).not.toContainEqual(
      expect.objectContaining({ label: 'Add Workspace Shortcut' }),
    );
    const shortcuts = workspace.find(
      (item) => item.label === 'Shortcuts',
    )!.children!;
    expect(shortcuts[0].children![0].disabled).toBe(true);
    shortcuts[0].children![1].action?.();
    expect(api.removeClipShortcut).toHaveBeenCalledExactlyOnceWith(unbound.id);
    shortcuts[1].children![0].action?.();
    expect(api.openApp).toHaveBeenCalledExactlyOnceWith(
      bound.appId,
      bound.params,
    );
    createWindowMenu(bound, api, contextual)
      .find((item) => item.label === 'Add Workspace Shortcut')
      ?.action?.();
    expect(api.addClipShortcut).toHaveBeenCalledExactlyOnceWith({
      appId: bound.appId,
      params: bound.params,
      title: bound.title,
    });
  });

  it('launches and removes application shortcuts without invoking host resource actions', () => {
    const api = desktopAPI();
    const custom = vi.fn(() => [{ label: 'Inspect' }]);
    const props: DesktopProps = {
      apps: [app('example')],
      icons: [
        {
          id: 'application:example',
          appId: 'example',
          applicationLauncher: true,
          label: 'Example',
          icon: 'document',
        },
      ],
      onDesktopMenu: custom,
    };
    const menu = createDesktopContextMenu(props, api, ['application:example']);
    expect(menu.map((item) => item.label)).toEqual([
      'Open',
      'Remove from Desktop',
    ]);
    menu[0].action?.();
    expect(api.launchApp).toHaveBeenCalledExactlyOnceWith('example');
    expect(api.openApp).not.toHaveBeenCalled();
    menu[1].action?.();
    expect(api.showDesktopApplication).toHaveBeenCalledExactlyOnceWith(
      'example',
      false,
    );
    expect(custom).not.toHaveBeenCalled();
  });

  it('preserves a host launcher action and refuses to remove mixed resource selections as shortcuts', () => {
    const api = desktopAPI();
    const onOpen = vi.fn();
    const custom = vi.fn(() => [{ label: 'Inspect' }]);
    const props: DesktopProps = {
      apps: [app('example')],
      icons: [
        {
          id: 'system:example',
          appId: 'example',
          applicationLauncher: true,
          label: 'Example',
          icon: 'document',
          onOpen,
        },
        {
          id: 'resource',
          appId: 'example',
          params: { resourceId: 'original' },
          label: 'Resource',
          icon: 'document',
        },
      ],
      onDesktopMenu: custom,
    };
    const mixed = createDesktopContextMenu(props, api, [
      'system:example',
      'resource',
    ]);
    mixed[0].action?.();
    expect(onOpen).toHaveBeenCalledOnce();
    expect(api.openApp).toHaveBeenCalledExactlyOnceWith('example', {
      resourceId: 'original',
    });
    expect(mixed[1].disabled).toBe(true);
    mixed[1].action?.();
    expect(api.showDesktopApplication).not.toHaveBeenCalled();
    expect(custom).not.toHaveBeenCalled();
    createDesktopContextMenu(props, api, ['resource']);
    expect(custom).toHaveBeenCalledExactlyOnceWith(['resource'], api);
  });

  it('launches arbitrary registered app IDs through their launch hooks', () => {
    const api = desktopAPI();
    const props: DesktopProps = {
      apps: [
        app('inventory'),
        app('hidden', false),
        { ...app('reporting'), defaultPinned: false },
      ],
      icons: [],
    };
    const menus = createDesktopContextMenu(props, api, []);
    const apps = menus.find((item) => item.label === 'Applications')!.children!;
    expect(apps.map((item) => item.label)).toEqual(['inventory', 'reporting']);
    apps[0].action?.();
    expect(api.launchApp).toHaveBeenCalledWith('inventory');
    expect(api.openApp).not.toHaveBeenCalled();
  });
  it('keeps host custom menu actions and forwards resource icons by identity', () => {
    const api = desktopAPI();
    const onOpen = vi.fn();
    const props: DesktopProps = {
      apps: [app('inventory')],
      icons: [
        {
          id: 'sku',
          label: 'SKU',
          icon: 'document',
          appId: 'inventory',
          params: { sku: '42' },
        },
        { id: 'custom', label: 'Custom', icon: 'folder', onOpen },
      ],
    };
    createDesktopContextMenu(props, api, ['sku', 'custom'])[0].action?.();
    expect(api.openApp).toHaveBeenCalledWith('inventory', { sku: '42' });
    expect(onOpen).toHaveBeenCalledOnce();
    expect(
      createDesktopContextMenu({ ...props, onDesktopMenu: () => [] }, api, [
        'sku',
      ]),
    ).toEqual([]);
  });
  it('uses the exact active window for application commands', () => {
    const api = desktopAPI();
    const a = win('a');
    const b = win('b');
    api.windows = [a, b];
    api.activeWindowId = a.id;
    const invoke = vi.fn();
    const editor: AppDefinition = {
      ...app('editor'),
      menu: (owner) => [
        {
          id: 'file',
          label: 'File',
          items: [{ label: 'Save', action: () => invoke(owner.id) }],
        },
      ],
    };
    const props: DesktopProps = { apps: [editor], icons: [] };
    createApplicationMenus(props, api)!
      .groups.find((group) => group.id === 'file')!
      .items[0].action?.();
    expect(invoke).toHaveBeenCalledExactlyOnceWith(a.id);
    api.activeWindowId = b.id;
    createApplicationMenus(props, api)!.groups[0].items[0].action?.();
    expect(invoke).toHaveBeenLastCalledWith(b.id);
  });
  it('distinguishes document actions, app launchers, and workspace membership', () => {
    const api = desktopAPI();
    const a = win('a');
    api.workspaces[1].name = 'Sandbox';
    const items = createWindowMenu(a, api, app('editor'));
    items.find((item) => item.label === 'Miniaturize')!.action?.();
    expect(api.minimizeWindow).toHaveBeenCalledWith(a.id);
    items
      .find((item) => item.label === 'Move to Workspace')!
      .children!.find((item) => item.label === 'Workspace 2 · Sandbox')!
      .action?.();
    expect(api.moveWindow).toHaveBeenCalledWith(a.id, 'workspace-2');
    items.find((item) => item.label === 'All Workspaces')!.action?.();
    expect(api.setWindowSticky).toHaveBeenCalledWith(a.id, true);
    items.find((item) => item.label === 'Add Workspace Shortcut')!.action?.();
    expect(api.addClipShortcut).toHaveBeenCalledWith({
      appId: 'editor',
      params: { entry: 'a' },
      title: 'a',
    });
  });
  it('keeps unavailable window commands bound to their owner without invoking application menus', () => {
    const api = desktopAPI();
    const original = win('editor:original');
    api.windows = [original, win('editor:active')];
    api.activeWindowId = 'editor:active';
    const menu = vi.fn(() => []);
    for (const apps of [
      [],
      [{ ...app('editor'), unavailable: 'Disabled', menu }],
    ]) {
      expect(createApplicationMenus({ apps, icons: [] }, api)).toBeUndefined();
      createWindowMenu(original, api, resolveApplication(apps, original.appId))
        .find((item) => item.label === 'Close Window')!
        .action?.();
      expect(api.closeWindow).toHaveBeenLastCalledWith(original.id);
    }
    expect(menu).not.toHaveBeenCalled();
  });
  it('shows only nonempty menus supplied by the exact active application', () => {
    const api = desktopAPI();
    const owner = win('a');
    api.windows = [owner];
    api.activeWindowId = owner.id;
    const session = {
      id: 'session',
      label: 'Session',
      items: [{ label: 'Reconnect' }],
    };
    const editor = {
      ...app('editor'),
      menu: () => [
        session,
        { id: 'unused', label: 'Unused', items: [] },
        { id: 'separator', label: 'Separator', items: [{ separator: true }] },
        {
          id: 'hidden',
          label: 'Hidden',
          items: [{ label: 'Hidden command' }],
          applicationMenu: false,
        },
      ],
    };
    const props = { apps: [editor], icons: [] };
    expect(createApplicationMenus(props, api)).toEqual({
      owner,
      title: 'editor',
      groups: [session],
    });
    api.windows = [{ ...owner, shaded: true }];
    expect(createApplicationMenus(props, api)).toBeUndefined();
    api.activeWindowId = null;
    expect(createApplicationMenus(props, api)).toBeUndefined();
    createDesktopContextMenu(props, api, [])
      .find((item) => item.label === 'Arrange Icons')!
      .action?.();
    expect(api.arrangeIcons).toHaveBeenCalledOnce();
  });
  it('keeps shared help in the workspace while allowing an application-specific guide', () => {
    const api = desktopAPI();
    const owner = win('a');
    api.windows = [owner];
    api.activeWindowId = owner.id;
    const onHelp = vi.fn();
    const props: DesktopProps = {
      apps: [app('editor')],
      icons: [],
      onHelp,
      helpLabel: 'Welcome',
    };
    createDesktopContextMenu(props, api, []).find(
      (item) => item.label === 'Welcome',
    )!.action!();
    expect(onHelp).toHaveBeenCalledOnce();
    expect(createApplicationMenus(props, api)).toBeUndefined();
    const guide = {
      id: 'help',
      label: 'Editor Guide',
      items: [{ label: 'Editing help' }],
    };
    const menus = createApplicationMenus(
      { ...props, apps: [{ ...app('editor'), menu: () => [guide] }] },
      api,
    );
    expect(menus?.groups).toEqual([guide]);
  });
});
