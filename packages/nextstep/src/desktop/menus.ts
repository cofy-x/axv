import {
  DEFAULT_WORKSPACE_COUNT,
  isApplicationLauncher,
  workspaceLabel,
} from '../model';
import type {
  AppDefinition,
  DesktopAPI,
  DesktopProps,
  DesktopWindow,
  ClipShortcut,
  MenuItem,
} from '../types';
import { resolveApplication } from './applications';

export function showClock(api: DesktopAPI, clock24Hour = true) {
  void api.dialog({
    title: 'Clock',
    message: new Date().toLocaleString(undefined, {
      dateStyle: 'full',
      timeStyle: 'short',
      hour12: !clock24Hour,
    }),
    buttons: [{ label: 'OK', value: 'ok', default: true }],
  });
}

function createClipShortcutMenu(
  props: DesktopProps,
  api: DesktopAPI,
  shortcut: ClipShortcut,
): MenuItem[] {
  const app = resolveApplication(props.apps, shortcut.appId);
  const bound = Object.keys(shortcut.params).length > 0;
  const available =
    app.unavailable === undefined && (bound || app.launchable !== false);
  return [
    {
      label: 'Open',
      disabled: !available,
      action: () => {
        if (!available) return;
        if (bound) api.openApp(shortcut.appId, shortcut.params);
        else api.launchApp(shortcut.appId);
      },
    },
    {
      label: 'Remove Shortcut',
      action: () => api.removeClipShortcut(shortcut.id),
    },
  ];
}

function canSaveWindowShortcut(window: DesktopWindow, app: AppDefinition) {
  return app.launchable !== false || Object.keys(window.params).length > 0;
}

function createWorkspaceMenu(props: DesktopProps, api: DesktopAPI): MenuItem[] {
  const workspace = api.workspaces.find(
    (item) => item.id === api.currentWorkspaceId,
  )!;
  const extraWorkspaces = api.workspaces.slice(DEFAULT_WORKSPACE_COUNT);
  const active = api.windows.find((item) => item.id === api.activeWindowId);
  const canSaveActive =
    !!active &&
    canSaveWindowShortcut(active, resolveApplication(props.apps, active.appId));
  return [
    {
      label: 'Rename Workspace',
      action: () => {
        void api
          .dialog({
            title: 'Rename Workspace',
            message: 'Leave the name blank to use the workspace number.',
            input: { label: 'Workspace name', value: workspace.name },
          })
          .then((result) => {
            if (result) api.renameWorkspace(workspace.id, result.value);
          });
      },
    },
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
      disabled: !canSaveActive,
      action: () => {
        if (canSaveActive)
          api.addClipShortcut({
            appId: active.appId,
            title: active.title,
            params: active.params,
          });
      },
    },
    ...(extraWorkspaces.length
      ? [
          { separator: true },
          {
            label: 'Switch Workspace',
            children: extraWorkspaces.map((item, index) => ({
              label: workspaceLabel(item, index + DEFAULT_WORKSPACE_COUNT),
              checked: item.id === workspace.id,
              action: () => api.switchWorkspace(item.id),
            })),
          },
        ]
      : []),
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
  app: AppDefinition,
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
    {
      label: 'Tile Left',
      action: () => api.snapWindow(win.id, 'left'),
    },
    {
      label: 'Tile Right',
      action: () => api.snapWindow(win.id, 'right'),
    },
    { separator: true },
    {
      label: 'Move to Workspace',
      children: api.workspaces.map((workspace, index) => ({
        label: workspaceLabel(workspace, index),
        checked: win.workspaceId === workspace.id && !win.sticky,
        action: () => api.moveWindow(win.id, workspace.id),
      })),
    },
    {
      label: 'All Workspaces',
      checked: win.sticky,
      action: () => api.setWindowSticky(win.id, !win.sticky),
    },
    ...(canSaveWindowShortcut(win, app)
      ? [
          {
            label: 'Add Workspace Shortcut',
            action: () =>
              api.addClipShortcut({
                appId: win.appId,
                params: win.params,
                title: win.title.replace(/ \*$/, ''),
              }),
          },
        ]
      : []),
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
      children: createWorkspaceMenu(props, api),
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
    { label: 'Clock', action: () => showClock(api, props.clock24Hour) },
    ...(props.onAppearance
      ? [{ label: 'Preferences', action: props.onAppearance }]
      : []),
    ...(props.onHelp
      ? [{ label: props.helpLabel ?? 'Help', action: props.onHelp }]
      : []),
    ...(props.onAbout
      ? [{ label: 'About This Desktop', action: props.onAbout }]
      : []),
  ];
}

export function createApplicationMenus(props: DesktopProps, api: DesktopAPI) {
  const owner = api.windows.find((window) => window.id === api.activeWindowId);
  if (!owner || owner.shaded) return;
  const app = resolveApplication(props.apps, owner.appId);
  if (app.unavailable !== undefined || !app.menu) return;
  const groups = app
    .menu(owner, api)
    .filter(
      (group) =>
        group.id !== 'desktop' &&
        group.applicationMenu !== false &&
        group.items.some((item) => !item.separator),
    );
  if (groups.length) return { owner, title: app.title, groups };
}
