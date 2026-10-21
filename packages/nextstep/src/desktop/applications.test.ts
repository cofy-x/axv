import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { sanitizeLayout, windowWorkArea } from '../model';
import type {
  AppDefinition,
  AppWindowProps,
  DesktopAPI,
  DesktopWindow,
} from '../types';
import { Clip } from './Clip';
import { Dock } from './Dock';
import { WindowFrame } from './WindowFrame';
import { launchApplication, resolveApplication } from './applications';

const savedWindow: DesktopWindow = {
  id: 'org.example.tool:document-7',
  appId: 'org.example.tool',
  title: 'Retained document',
  params: { documentId: 'document-7' },
  workspaceId: 'workspace-1',
  sticky: false,
  minimized: false,
  shaded: false,
  zoomed: false,
  viewState: { selection: 'retained' },
  x: 80,
  y: 90,
  width: 500,
  height: 400,
};
const viewport = { width: 1024, height: 768 };

function desktop(): DesktopAPI {
  const layout = sanitizeLayout(
    { version: 2, windows: [savedWindow], activeWindowId: savedWindow.id },
    [],
  );
  return {
    ...layout,
    launchApp: vi.fn(),
    isApplicationOnDesktop: vi.fn(() => false),
    showDesktopApplication: vi.fn(),
    openApp: vi.fn(() => ''),
    closeWindow: vi.fn(async () => true),
    focusWindow: vi.fn(),
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
    setTitle: vi.fn(),
    showAllWindows: vi.fn(),
    arrangeIcons: vi.fn(),
    contextMenu: vi.fn(),
    dialog: vi.fn(async () => null),
    beforeClose: vi.fn(() => () => {}),
  };
}

function renderWindow(
  app: AppDefinition,
  options: Partial<Parameters<typeof WindowFrame>[0]> = {},
) {
  return renderToStaticMarkup(
    createElement(WindowFrame, {
      win: savedWindow,
      app,
      index: 0,
      api: desktop(),
      viewport,
      workArea: windowWorkArea(viewport, false),
      mobile: false,
      overview: false,
      inputBlocked: false,
      activateWindow: vi.fn(),
      beginWindowGesture: vi.fn(),
      toggleShade: vi.fn(),
      toggleZoom: vi.fn(),
      ...options,
    }),
  );
}

afterEach(() => vi.unstubAllGlobals());

describe('application launch boundaries', () => {
  const app: AppDefinition = {
    id: savedWindow.appId,
    title: 'Example Tool',
    icon: 'document',
    component: () => null,
  };

  it('restores the latest existing window even when the application cannot create new windows', () => {
    const api = desktop();
    const newer = { ...savedWindow, id: `${app.id}:newer` };
    api.windows = [savedWindow, newer];
    launchApplication([{ ...app, launchable: false }], api, app.id);
    expect(api.focusWindow).toHaveBeenCalledExactlyOnceWith(newer.id);
    expect(api.openApp).not.toHaveBeenCalled();
    expect(api.dialog).not.toHaveBeenCalled();
  });

  it('rejects new launches of missing, disabled, uninstalled, and contextual applications', async () => {
    const launch = vi.fn();
    for (const apps of [
      [],
      [{ ...app, unavailable: 'Disabled by the user', launch }],
      [{ ...app, launchable: false, launch }],
      [{ ...app, launchable: false }],
    ]) {
      const api = { ...desktop(), windows: [] };
      launchApplication(apps, api, app.id);
      expect(api.dialog).toHaveBeenCalledExactlyOnceWith(
        expect.objectContaining({ title: 'Application unavailable' }),
      );
      expect(api.openApp).not.toHaveBeenCalled();
      expect(api.focusWindow).not.toHaveBeenCalled();
    }
    await Promise.resolve();
    expect(launch).not.toHaveBeenCalled();
  });

  it('uses owner launch actions and reports failures while default applications open normally', async () => {
    const api = { ...desktop(), windows: [] };
    launchApplication([app], api, app.id);
    expect(api.openApp).toHaveBeenCalledExactlyOnceWith(app.id);
    const launch = vi.fn(() => {
      throw new Error('The owner launch failed.');
    });
    launchApplication([{ ...app, launch }], api, app.id);
    await vi.waitFor(() => {
      expect(api.dialog).toHaveBeenCalledExactlyOnceWith(
        expect.objectContaining({
          title: `Cannot open ${app.title}`,
          message: 'The owner launch failed.',
        }),
      );
    });
    expect(launch).toHaveBeenCalledExactlyOnceWith(api);
    expect(api.openApp).toHaveBeenCalledTimes(1);
    launchApplication([app], api, '../invalid');
    expect(api.openApp).toHaveBeenCalledTimes(1);
    expect(api.dialog).toHaveBeenCalledTimes(1);
  });
});

describe('unavailable application presentation', () => {
  it('retains a missing application window and replaces disabled code with a generic placeholder', () => {
    const missing = resolveApplication([], savedWindow.appId);
    const markup = renderWindow(missing);
    expect(markup).toContain('data-window-id="org.example.tool:document-7"');
    expect(markup).toContain('Application unavailable');
    expect(markup).toContain('Close Retained document');
    const component = vi.fn((_props: AppWindowProps) =>
      createElement('p', null, 'Application content'),
    );
    const unavailable = {
      ...missing,
      component,
      unavailable: 'Enable this application to continue.',
    };
    expect(renderWindow(unavailable)).toContain(unavailable.unavailable);
    expect(component).not.toHaveBeenCalled();
    const available = { ...unavailable, unavailable: undefined };
    const restored = renderWindow(
      resolveApplication([available], savedWindow.appId),
    );
    expect(restored).toContain('Application content');
    expect(restored).toContain('data-window-id="org.example.tool:document-7"');
    expect(component.mock.calls[0][0]).toMatchObject({
      window: savedWindow,
      visible: true,
      interactive: true,
    });
  });

  it('reports content visibility separately from active input and keeps hidden content inert', () => {
    let captured: AppWindowProps | undefined;
    const app: AppDefinition = {
      id: savedWindow.appId,
      title: 'Example Tool',
      icon: 'document',
      component: (props) => {
        captured = props;
        return createElement('p', null, 'Mounted content');
      },
    };
    renderWindow(app, { inputBlocked: true });
    expect(captured).toMatchObject({ visible: true, interactive: false });
    const hidden = renderWindow(app, {
      api: { ...desktop(), currentWorkspaceId: 'workspace-2' },
    });
    expect(captured).toMatchObject({ visible: false, interactive: false });
    expect(hidden).toContain('Mounted content');
    expect(hidden).toContain('inert=""');
    const shaded = renderWindow(app, { win: { ...savedWindow, shaded: true } });
    expect(captured).toMatchObject({ visible: false, interactive: false });
    expect(shaded).toContain('Mounted content');
    expect(shaded).toContain('aria-hidden="true"');
  });

  it('shows saved Dock and Clip entries while their definitions are absent', () => {
    vi.stubGlobal('window', { innerWidth: 1024, innerHeight: 768 });
    const api = desktop();
    const layout = sanitizeLayout(
      {
        ...sanitizeLayout(undefined, []),
        windows: [savedWindow],
        dock: [
          { appId: savedWindow.appId, pinned: false },
          { appId: 'another.tool', pinned: true },
        ],
        workspaces: [
          {
            id: 'workspace-1',
            name: 'Workspace',
            clip: [
              {
                id: 'document-shortcut',
                appId: savedWindow.appId,
                title: savedWindow.title,
                params: savedWindow.params,
              },
            ],
          },
        ],
      },
      [],
    );
    const props = { apps: [], icons: [] };
    const dock = renderToStaticMarkup(
      createElement(Dock, { props, layout, api, setLayout: vi.fn() }),
    );
    expect(dock).toContain('data-dock-app="org.example.tool"');
    expect(dock).toContain('data-dock-app="another.tool"');
    expect(dock).toContain('org.example.tool — unavailable');
    const clip = renderToStaticMarkup(
      createElement(Clip, {
        props,
        layout,
        api: { ...api, workspaces: layout.workspaces },
        setLayout: vi.fn(),
      }),
    );
    expect(clip).toContain('Clip: Retained document unavailable');
    expect(clip).toContain('Options for Clip shortcut Retained document');
  });
});
