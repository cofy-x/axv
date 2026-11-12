import { describe, expect, it, vi } from 'vitest';
import type { DesktopAPI, DesktopWindow } from '@cofy-x/axv-nextstep';
import { createWindowCommands } from '../../shared/desktop/window-commands';
import { createCodexApp } from './definition';

describe('Codex application menus', () => {
  it('offers run tools only for the original run window, with no desktop view commands', async () => {
    const commands = createWindowCommands();
    const desktop = {
      openApp: vi.fn(),
      dialog: vi.fn(),
    } as unknown as DesktopAPI;
    const app = createCodexApp(commands);
    const manager = { id: 'codex:manager', params: {} } as DesktopWindow;
    expect(app.menu!(manager, desktop).map((group) => group.label)).toEqual([
      'Sessions',
    ]);
    const run = {
      id: 'codex:session:original-run',
      params: { sessionId: 'session', runId: 'original-run' },
    } as unknown as DesktopWindow;
    const original = vi.fn(async () => true);
    const other = vi.fn(async () => true);
    commands.register(run.id, 'codex:project-files', original);
    commands.register('codex:session:new-run', 'codex:project-files', other);
    const groups = app.menu!(run, desktop);
    expect(groups.map((group) => group.label)).toEqual([
      'Sessions',
      'Terminal',
      'Session',
      'Project',
    ]);
    const tools = groups.find((group) => group.id === 'project')!.items;
    expect(tools.find((item) => item.label === 'Tasks')!.disabled).toBe(true);
    tools.find((item) => item.label === 'Project Files')!.action!();
    await Promise.resolve();
    expect(original).toHaveBeenCalledOnce();
    expect(other).not.toHaveBeenCalled();
  });
});
