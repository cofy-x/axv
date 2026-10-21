import { describe, expect, it, vi } from 'vitest';
import { defaultWorkspaces } from '../model';
import type {
  AppDefinition,
  DesktopAPI,
  DesktopProps,
  DesktopWindow,
} from '../types';
import {
  createDesktopContextMenu,
  createDesktopMenus,
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
  it('launches and removes application shortcuts without invoking file or host desktop actions', () => {
    const api = desktopAPI();
    const custom = vi.fn(() => [{ label: 'Trash' }]);
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
      onTrashIcons: vi.fn(),
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
    expect(props.onTrashIcons).not.toHaveBeenCalled();
  });

  it('preserves a host launcher action and refuses to remove mixed resource selections as shortcuts', () => {
    const api = desktopAPI();
    const onOpen = vi.fn();
    const custom = vi.fn(() => [{ label: 'Trash' }]);
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
  it('keeps host custom file menu actions and forwards icons by identity', () => {
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
  it('pins commands to their original owner after active focus changes', () => {
    const api = desktopAPI();
    const a = win('a');
    const b = win('b');
    api.windows = [a, b];
    api.activeWindowId = b.id;
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
    createDesktopMenus(props, api, [], a.id)
      .groups.find((group) => group.id === 'file')!
      .items[0].action?.();
    expect(invoke).toHaveBeenCalledExactlyOnceWith(a.id);
  });
  it('distinguishes document actions, app launchers, and workspace membership', () => {
    const api = desktopAPI();
    const a = win('a');
    const items = createWindowMenu(a, api);
    items.find((item) => item.label === 'Miniaturize')!.action?.();
    expect(api.minimizeWindow).toHaveBeenCalledWith(a.id);
    items
      .find((item) => item.label === 'Move to Workspace')!
      .children![1].action?.();
    expect(api.moveWindow).toHaveBeenCalledWith(a.id, 'workspace-2');
    items.find((item) => item.label === 'All Workspaces')!.action?.();
    expect(api.setWindowSticky).toHaveBeenCalledWith(a.id, true);
    items.find((item) => item.label === 'Add to Clip')!.action?.();
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
      const groups = createDesktopMenus(
        { apps, icons: [] },
        api,
        [],
        original.id,
      ).groups;
      groups
        .find((group) => group.id === 'file')!
        .items.find((item) => item.label === 'Close Window')!
        .action?.();
      expect(api.closeWindow).toHaveBeenLastCalledWith(original.id);
    }
    expect(menu).not.toHaveBeenCalled();
  });
});
