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
  | 'applications'
  | 'checklist'
  | 'image'
  | 'network'
  | 'cloud-tunnel'
  | 'mesh-network'
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
export type Viewport = Size & { coarsePointer?: boolean };
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
  /** Explicit per-application visibility overrides; omitted entries inherit desktop defaults. */
  desktopApplications?: Record<string, boolean>;
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
  /** A content menu is valid only while this window remains interactive. */
  ownerWindowId?: string;
  align?: 'start' | 'end';
  width?: 'wide';
}

export interface MenuItem {
  id?: string;
  label?: string;
  description?: string;
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
  isApplicationOnDesktop: (appId: string) => boolean;
  showDesktopApplication: (appId: string, shown: boolean) => void;
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
  /** Whether the content is displayed, independently of keyboard activation. */
  visible?: boolean;
  /** Keyboard input is suspended for inactive, hidden, folded or covered content. */
  interactive?: boolean;
}

export interface AppDefinition {
  id: string;
  title: string;
  icon: IconName;
  /** Keep saved application references while preventing unavailable code from running. */
  unavailable?: string;
  /** Whether application launchers can create a window; explicit openApp calls remain available. Defaults to true. */
  launchable?: boolean;
  /** Pin a launchable application in a new layout's Dock; defaults to true. */
  defaultPinned?: boolean;
  /** Offer a desktop launcher by default without overriding saved visibility choices. */
  defaultDesktop?: boolean;
  launch?: (desktop: DesktopAPI) => void | Promise<void>;
  kind?: 'application' | 'panel';
  /** Place a newly opened window beside its active source when there is space. */
  initialPlacement?: 'beside-active';
  defaultSize?: Size;
  minSize?: Size;
  windowKey?: (params: WindowParams) => string;
  component: ComponentType<AppWindowProps>;
  menu?: (window: DesktopWindow, desktop: DesktopAPI) => MenuGroup[];
}

export interface DesktopIcon {
  /** Stable host identity. The application: prefix is reserved for generated application shortcuts. */
  id: string;
  label: string;
  icon: IconName;
  appId?: string;
  /** A pure application launcher, independent of resource icons opening the same application. */
  applicationLauncher?: boolean;
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
  /** New-layout defaults only; a saved layout always keeps its visibility choices. */
  initialDesktopApplications?: Record<string, boolean>;
  /** Order fresh-layout pinned entries without replacing a saved Dock. */
  initialDockOrder?: string[];
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
