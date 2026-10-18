import { describe, expect, it, vi } from 'vitest';
import type {
  DesktopAPI,
  DesktopWindow,
  MenuGroup,
  WindowParams,
} from '@cofy-x/axv-nextstep';
import { createWindowCommands } from '../../shared/desktop/window-commands';
import { createTerminalApp, openTerminalWindow } from './definition';

function menu(groups: MenuGroup[], label: string) {
  return groups
    .flatMap((group) => group.items)
    .find((item) => item.label === label)!;
}

describe('terminal application integration', () => {
  it('stores independent window identities and gives their Dock choices distinct titles', () => {
    const app = createTerminalApp(createWindowCommands());
    const opened: { params: WindowParams; title: string }[] = [];
    const desktop = {
      windows: [],
      openApp: vi.fn(
        (_appId: string, params: WindowParams, options: { title: string }) => {
          opened.push({ params, title: options.title });
          return `terminal:${params.sessionId}`;
        },
      ),
    } as unknown as DesktopAPI;
    const first = openTerminalWindow(desktop);
    desktop.windows = [{ title: opened[0].title } as DesktopWindow];
    const second = openTerminalWindow(desktop);
    expect(first).not.toBe(second);
    expect(opened.map((item) => item.title)).toEqual([
      'Terminal',
      'Terminal 2',
    ]);
    expect(`terminal:${app.windowKey!(opened[0].params)}`).toBe(first);
    expect(
      `terminal:${app.windowKey!(JSON.parse(JSON.stringify(opened[1].params)))}`,
    ).toBe(second);
    expect(app.windowKey!({})).toBe('main');
  });

  it('keeps fixed commands bound to their live owner after activation and handler changes', async () => {
    const commands = createWindowCommands();
    const app = createTerminalApp(commands);
    const owner = { id: 'terminal:first' } as DesktopWindow;
    const desktop = {
      activeWindowId: 'terminal:second',
      closeWindow: vi.fn(async () => true),
    } as unknown as DesktopAPI;
    const first = vi.fn(async () => true);
    const second = vi.fn(async () => true);
    commands.register(owner.id, 'terminal:clear', first);
    commands.register('terminal:second', 'terminal:clear', second);
    const original = app.menu!(owner, desktop);
    menu(original, 'Clear').action!();
    expect(first).toHaveBeenCalledOnce();
    expect(second).not.toHaveBeenCalled();
    const latest = vi.fn(async () => true);
    const unregister = commands.register(owner.id, 'terminal:clear', latest);
    menu(original, 'Clear').action!();
    expect(latest).toHaveBeenCalledOnce();
    menu(original, 'Close Window').action!();
    expect(desktop.closeWindow).toHaveBeenCalledWith(owner.id);
    unregister();
    menu(original, 'Clear').action!();
    expect(latest).toHaveBeenCalledOnce();
    await expect(commands.execute(owner.id, 'terminal:clear')).resolves.toBe(
      false,
    );
  });

  it('preserves project metadata across layout serialization without sharing shell identity', () => {
    const projectPath = "/tmp/project's 中文";
    const opened: { params: WindowParams; title: string }[] = [];
    const desktop = {
      windows: [],
      openApp: vi.fn(
        (_appId: string, params: WindowParams, options: { title: string }) => {
          opened.push({ params, title: options.title });
          return `terminal:${params.sessionId}`;
        },
      ),
    } as unknown as DesktopAPI;
    const first = openTerminalWindow(desktop, projectPath);
    desktop.windows = [{ title: opened[0].title } as DesktopWindow];
    const second = openTerminalWindow(desktop, projectPath);
    expect(first).not.toBe(second);
    expect(opened.map((item) => item.title)).toEqual([
      "Terminal · project's 中文",
      "Terminal · project's 中文 2",
    ]);
    expect(JSON.parse(JSON.stringify(opened[0].params))).toEqual({
      sessionId: opened[0].params.sessionId,
      projectPath,
    });
    openTerminalWindow(desktop);
    expect(opened[2].title).toBe('Terminal');
    expect(opened[2].params).not.toHaveProperty('projectPath');
  });

  it('reflects each owner session availability without persisting runtime state', () => {
    const commands = createWindowCommands();
    const app = createTerminalApp(commands);
    const owner = { id: 'terminal:one' } as DesktopWindow;
    const desktop = {} as DesktopAPI;
    commands.register(
      owner.id,
      'terminal:new-session',
      async () => true,
      false,
    );
    commands.register(owner.id, 'terminal:interrupt', async () => true, true);
    expect(menu(app.menu!(owner, desktop), 'New Session').disabled).toBe(true);
    expect(menu(app.menu!(owner, desktop), 'Interrupt').disabled).toBe(false);
    commands.register(owner.id, 'terminal:new-session', async () => true, true);
    commands.register(owner.id, 'terminal:interrupt', async () => true, false);
    expect(menu(app.menu!(owner, desktop), 'New Session').disabled).toBe(false);
    expect(menu(app.menu!(owner, desktop), 'Interrupt').disabled).toBe(true);
  });
});
