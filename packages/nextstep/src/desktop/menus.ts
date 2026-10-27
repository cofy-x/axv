import { isApplicationLauncher, mergeMenuGroups } from '../model';
import type {
  DesktopAPI,
  DesktopProps,
  DesktopWindow,
  ClipShortcut,
  MenuGroup,
  MenuItem,
} from '../types';
import { resolveApplication } from './applications';

export function showClock(api: DesktopAPI) {
  void api.dialog({
    title: 'Clock',
    message: new Date().toLocaleString(undefined, {
      dateStyle: 'full',
      timeStyle: 'short',
    }),
    buttons: [{ label: 'OK', value: 'ok', default: true }],
  });
}

export function createClipShortcutMenu(
  props: DesktopProps,
  api: DesktopAPI,
  shortcut: ClipShortcut,
): MenuItem[] {
  const available =
    resolveApplication(props.apps, shortcut.appId).unavailable === undefined &&
    props.isShortcutAvailable?.(shortcut) !== false;
  return [
    {
      label: 'Open',
      disabled: !available,
      action: () => {
        if (!available) return;
        if (Object.keys(shortcut.params).length)
          api.openApp(shortcut.appId, shortcut.params);
        else api.launchApp(shortcut.appId);
      },
    },
    {
      label: 'Remove Shortcut',
      action: () => api.removeClipShortcut(shortcut.id),
    },
  ];
}

/** Workspace switching and shortcuts share the Clip menu. */
export function createClipMenu(
  props: DesktopProps,
  api: DesktopAPI,
): MenuItem[] {
  const workspace = api.workspaces.find(
    (item) => item.id === api.currentWorkspaceId,
  )!;
  return [
    ...api.workspaces.map((item, index) => ({
      label: `${index + 1}  ${item.name}`,
      checked: item.id === workspace.id,
      action: () => api.switchWorkspace(item.id),
    })),
    { separator: true },
    ...(workspace.clip.length
      ? [
          {
            label: 'Shortcuts',
            children: workspace.clip.map((shortcut) => ({
              label: shortcut.title,
              children: createClipShortcutMenu(props, api, shortcut),
            })),
          },
        ]
      : []),
    {
      label: 'Rename Workspace',
      action: () => {
        void api
          .dialog({
            title: 'Rename Workspace',
            input: { label: 'Workspace name', value: workspace.name },
          })
          .then((result) => {
            if (result?.value.trim())
              api.renameWorkspace(workspace.id, result.value);
          });
      },
    },
    {
      label: 'Add Application Shortcut',
      children: props.apps
        .filter((app) => app.launchable !== false)
        .map((app) => ({
          label: app.title,
          action: () =>
            api.addClipShortcut({
              appId: app.id,
              title: app.title,
              params: {},
            }),
        })),
    },
    {
      label: 'Add Active Window',
      disabled: !api.activeWindowId,
      action: () => {
        const win = api.windows.find((item) => item.id === api.activeWindowId);
        if (win)
          api.addClipShortcut({
            appId: win.appId,
            title: win.title,
            params: win.params,
          });
      },
    },
  ];
}

function openSelectedIcons(
  props: DesktopProps,
  api: DesktopAPI,
  ids: string[],
) {
  for (const icon of props.icons.filter((item) => ids.includes(item.id))) {
    if (icon.onOpen) icon.onOpen();
    else if (isApplicationLauncher(icon)) api.launchApp(icon.appId);
    else if (icon.appId) api.openApp(icon.appId, icon.params);
  }
}

export function createWindowMenu(
  win: DesktopWindow,
  api: DesktopAPI,
): MenuItem[] {
  return [
    {
      label: win.minimized ? 'Restore Window' : 'Miniaturize',
      action: () =>
        win.minimized ? api.focusWindow(win.id) : api.minimizeWindow(win.id),
    },
    {
      label: win.shaded ? 'Unshade' : 'Shade',
      action: () => api.toggleShade(win.id),
    },
    {
      label: win.zoomed ? 'Restore Size' : 'Maximize',
      action: () => api.toggleZoom(win.id),
    },
    { separator: true },
    {
      label: 'Move to Workspace',
      children: api.workspaces.map((workspace) => ({
        label: workspace.name,
        checked: win.workspaceId === workspace.id && !win.sticky,
        action: () => api.moveWindow(win.id, workspace.id),
      })),
    },
    {
      label: 'All Workspaces',
      checked: win.sticky,
      action: () => api.setWindowSticky(win.id, !win.sticky),
    },
    {
      label: 'Add to Clip',
      action: () =>
        api.addClipShortcut({
          appId: win.appId,
          params: win.params,
          title: win.title.replace(/ \*$/, ''),
        }),
    },
    { separator: true },
    {
      label: 'Close Window',
      shortcut: 'Ctrl/⌘ W',
      action: () => {
        void api.closeWindow(win.id);
      },
    },
  ];
}

export function createDesktopContextMenu(
  props: DesktopProps,
  api: DesktopAPI,
  ids: string[],
): MenuItem[] {
  const selected = props.icons.filter((icon) => ids.includes(icon.id));
  const applications = selected.filter(isApplicationLauncher);
  if (applications.length) {
    return [
      { label: 'Open', action: () => openSelectedIcons(props, api, ids) },
      {
        label: 'Remove from Desktop',
        disabled: applications.length !== ids.length,
        action: () => {
          if (applications.length !== ids.length) return;
          for (const icon of applications)
            api.showDesktopApplication(icon.appId, false);
        },
      },
    ];
  }
  const custom = props.onDesktopMenu?.(ids, api);
  if (custom && ids.length) return custom;
  return [
    ...(ids.length
      ? (custom ?? [
          { label: 'Open', action: () => openSelectedIcons(props, api, ids) },
        ])
      : []),
    {
      label: 'Applications',
      children: props.apps
        .filter((app) => app.launchable !== false)
        .map((app) => ({
          label: app.title,
          action: () => api.launchApp(app.id),
        })),
    },
    {
      label: 'Workspaces',
      children: api.workspaces.map((workspace) => ({
        label: workspace.name,
        checked: workspace.id === api.currentWorkspaceId,
        action: () => api.switchWorkspace(workspace.id),
      })),
    },
    {
      label: 'Windows',
      children: api.windows.map((win) => ({
        label: win.title,
        checked: win.id === api.activeWindowId,
        action: () => api.focusWindow(win.id),
      })),
      disabled: !api.windows.length,
    },
    { separator: true },
    { label: 'Arrange Icons', action: api.arrangeIcons },
    { label: 'Clock', action: () => showClock(api) },
    ...(props.onAppearance
      ? [{ label: 'Preferences', action: props.onAppearance }]
      : []),
    ...(props.onAbout
      ? [{ label: 'About This Desktop', action: props.onAbout }]
      : []),
  ];
}

/** Supply an explicit owner when reconstructing a pinned application menu. */
export function createDesktopMenus(
  props: DesktopProps,
  api: DesktopAPI,
  selection: string[],
  ownerWindowId: string | null = api.activeWindowId,
): { groups: MenuGroup[]; windowItems: MenuItem[] } {
  const activeWindow = api.windows.find((win) => win.id === ownerWindowId);
  const activeApp = props.apps.find((app) => app.id === activeWindow?.appId);
  const windowItems: MenuItem[] = [
    ...(activeWindow ? createWindowMenu(activeWindow, api) : []),
    ...(activeWindow ? [{ separator: true }] : []),
    {
      label: 'Show All Windows',
      disabled: !api.windows.length,
      action: api.showAllWindows,
    },
    ...api.windows.map((win) => ({
      id: win.id,
      label: win.title,
      checked: win.id === ownerWindowId,
      action: () => api.focusWindow(win.id),
    })),
  ];
  const defaults: MenuGroup[] = [
    {
      id: 'desktop',
      label: 'Workspace',
      items: createDesktopContextMenu(props, api, selection),
    },
    {
      id: 'file',
      label: 'File',
      items: [
        {
          label: 'Open',
          disabled: !selection.length,
          action: () => openSelectedIcons(props, api, selection),
        },
        {
          label: 'Close Window',
          disabled: !activeWindow,
          shortcut: 'Ctrl/⌘ W',
          action: () => {
            if (activeWindow) void api.closeWindow(activeWindow.id);
          },
        },
      ],
    },
    {
      id: 'view',
      label: 'View',
      items: [
        { label: 'Arrange Icons', action: api.arrangeIcons },
        {
          label: 'Show All Windows',
          disabled: !api.windows.length,
          action: api.showAllWindows,
        },
      ],
    },
    { id: 'window', label: 'Window', items: windowItems },
    ...(props.onHelp
      ? [
          {
            id: 'help',
            label: 'Help',
            items: [{ label: props.helpLabel ?? 'Help', action: props.onHelp }],
          },
        ]
      : []),
  ];
  return {
    groups: mergeMenuGroups(
      defaults,
      activeWindow && activeApp?.unavailable === undefined && activeApp?.menu
        ? activeApp.menu(activeWindow, api)
        : [],
    ),
    windowItems,
  };
}
