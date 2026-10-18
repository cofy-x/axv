import type { ComponentType, ReactNode } from 'react';

export type IconName =
  | 'computer'
  | 'folder'
  | 'project'
  | 'document'
  | 'notes'
  | 'trash'
  | 'appearance'
  | 'gallery'
  | 'image'
  | 'network'
  | 'virtual-machines'
  | 'virtual-machine'
  | 'info'
  | 'save'
  | 'inspector'
  | 'workspace'
  | 'terminal'
  | 'companion'
  | 'clock';

export type WindowParams = Record<string, string>;
export type Point = { x: number; y: number };
export type Size = { width: number; height: number };
export type Bounds = Point & Size;

export interface DesktopWindow extends Bounds {
  id: string;
  appId: string;
  title: string;
  params: WindowParams;
  workspaceId: string;
  sticky: boolean;
  minimized: boolean;
  viewState: Record<string, unknown>;
  shaded: boolean;
  zoomed: boolean;
  restoreBounds?: Bounds;
}

export interface ClipShortcut {
  id: string;
  appId: string;
  title: string;
  params: WindowParams;
}

export interface Workspace {
  id: string;
  name: string;
  order: string[];
  activeWindowId: string | null;
  clip: ClipShortcut[];
}

export interface PinnedMenu extends Point {
  id: string;
  kind: 'desktop' | 'application' | 'window';
  groupId?: string;
  ownerWindowId?: string;
  title: string;
}

export interface NextstepLayout {
  version: 2;
  windows: DesktopWindow[];
  activeWindowId: string | null;
  icons: Record<string, Point>;
  compactIcons: Record<string, Point>;
  workspaces: Workspace[];
  currentWorkspaceId: string;
  dock: { appId: string; pinned: boolean }[];
  pinnedMenus: PinnedMenu[];
  clipPosition?: Point;
}

export type DesktopLayout = NextstepLayout;

export interface MenuOptions {
  title?: string;
  pin?: Omit<PinnedMenu, 'id' | 'x' | 'y'>;
}

export interface MenuItem {
  id?: string;
  label?: string;
  separator?: boolean;
  disabled?: boolean;
  checked?: boolean;
  shortcut?: string;
  action?: () => void;
  children?: MenuItem[];
}

export interface MenuGroup {
  id: string;
  label: string;
  items: MenuItem[];
}

export interface DialogOptions {
  title: string;
  message?: ReactNode;
  input?: { label: string; value?: string };
  buttons?: {
    label: string;
    value: string;
    default?: boolean;
    cancel?: boolean;
  }[];
}

export type DialogResult = { button: string; value: string } | null;

/** A pure updater is evaluated against the latest queued window state. */
export type WindowStateUpdate =
  | Record<string, unknown>
  | ((current: Readonly<Record<string, unknown>>) => Record<string, unknown>);

export interface DesktopAPI {
  windows: DesktopWindow[];
  activeWindowId: string | null;
  workspaces: Workspace[];
  currentWorkspaceId: string;
  launchApp: (appId: string) => void;
  updateWindowState: (id: string, update: WindowStateUpdate) => void;
  switchWorkspace: (id: string) => void;
  renameWorkspace: (id: string, name: string) => void;
  moveWindow: (id: string, workspaceId: string) => void;
  setWindowSticky: (id: string, sticky: boolean) => void;
  minimizeWindow: (id: string) => void;
  toggleShade: (id: string) => void;
  toggleZoom: (id: string) => void;
  addClipShortcut: (shortcut: Omit<ClipShortcut, 'id'>) => void;
  removeClipShortcut: (id: string) => void;
  openApp: (
    appId: string,
    params?: WindowParams,
    options?: { title?: string },
  ) => string;
  closeWindow: (id: string) => Promise<boolean>;
  focusWindow: (id: string) => void;
  setTitle: (id: string, title: string) => void;
  showAllWindows: () => void;
  arrangeIcons: () => void;
  contextMenu: (items: MenuItem[], point: Point, options?: MenuOptions) => void;
  dialog: (options: DialogOptions) => Promise<DialogResult>;
  beforeClose: (
    id: string,
    handler: () => boolean | Promise<boolean>,
  ) => () => void;
}

export interface AppWindowProps {
  window: DesktopWindow;
  desktop: DesktopAPI;
  /** Keyboard input is suspended for inactive, hidden, folded or covered content. */
  interactive?: boolean;
}

export interface AppDefinition {
  id: string;
  title: string;
  icon: IconName;
  /** Whether to offer this application in the system launcher; defaults to true. */
  launchable?: boolean;
  launch?: (desktop: DesktopAPI) => void | Promise<void>;
  kind?: 'application' | 'panel';
  defaultSize?: Size;
  minSize?: Size;
  windowKey?: (params: WindowParams) => string;
  component: ComponentType<AppWindowProps>;
  menu?: (window: DesktopWindow, desktop: DesktopAPI) => MenuGroup[];
}

export interface DesktopIcon {
  id: string;
  label: string;
  icon: IconName;
  appId?: string;
  params?: WindowParams;
  isTrash?: boolean;
  trashable?: boolean;
  status?: {
    label: string;
    tone: 'neutral' | 'success' | 'warning' | 'error';
  };
  onOpen?: () => void;
}

export interface DesktopProps {
  apps: AppDefinition[];
  icons: DesktopIcon[];
  initialLayout?: DesktopLayout;
  initialLayoutError?: string;
  initialWindows?: { appId: string; params?: WindowParams; title?: string }[];
  onLayoutChange?: (layout: DesktopLayout) => void;
  onTrashIcons?: (ids: string[]) => void;
  onDesktopMenu?: (ids: string[], desktop: DesktopAPI) => MenuItem[];
  wallpaper?: 'slate' | 'dither' | 'solid';
  clock24Hour?: boolean;
  isShortcutAvailable?: (shortcut: ClipShortcut) => boolean;
  iconSnap?: boolean;
  connectionStatus?: 'connected' | 'connecting' | 'offline';
  onAppearance?: () => void;
  onAbout?: () => void;
  onHelp?: () => void;
  helpLabel?: string;
  onReady?: (desktop: DesktopAPI) => void;
}
