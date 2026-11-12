import { describe, expect, it, vi } from 'vitest';
import type { DesktopAPI, DesktopWindow } from '@cofy-x/axv-nextstep';
import { createWindowCommands } from '../../shared/desktop/window-commands';
import { createMachinesMenu, machineMenuCommand } from './menus';

describe('VM application menu ownership', () => {
  it('rejects stale selected-machine commands after selection or host changes', async () => {
    const commands = createWindowCommands();
    const desktop = { updateWindowState: vi.fn() } as unknown as DesktopAPI;
    const owner = {
      id: 'machines:main',
      viewState: { machineId: 'same-id', machineHostId: 'original-host' },
    } as unknown as DesktopWindow;
    const original = vi.fn(async () => true);
    const unregister = commands.register(
      owner.id,
      machineMenuCommand('start', 'original-host', 'same-id'),
      original,
    );
    const menu = createMachinesMenu(commands, owner, desktop);
    const start = menu
      .find((group) => group.id === 'machine')!
      .items.find((item) => item.label === 'Start')!;
    expect(start.disabled).toBe(false);
    start.action!();
    expect(original).toHaveBeenCalledOnce();
    unregister();
    const replacement = vi.fn(async () => true);
    commands.register(
      owner.id,
      machineMenuCommand('start', 'replacement-host', 'same-id'),
      replacement,
    );
    start.action!();
    expect(original).toHaveBeenCalledOnce();
    expect(replacement).not.toHaveBeenCalled();
    await expect(
      commands.execute(
        owner.id,
        machineMenuCommand('start', 'original-host', 'same-id'),
      ),
    ).resolves.toBe(false);
  });

  it('keeps view changes and disabled actions scoped to the owning window', () => {
    const commands = createWindowCommands();
    const desktop = {
      updateWindowState: vi.fn(),
      activeWindowId: 'another-window',
    } as unknown as DesktopAPI;
    const owner = {
      id: 'machines:main',
      viewState: { vmTab: 'images', vmEnvironment: true },
    } as unknown as DesktopWindow;
    commands.register(owner.id, 'machines:view:machines', async () => true);
    commands.register(owner.id, 'machines:view:volumes', async () => {
      desktop.updateWindowState(owner.id, {
        vmTab: 'volumes',
        vmEnvironment: false,
      });
      return true;
    });
    const groups = createMachinesMenu(commands, owner, desktop);
    expect(
      groups
        .find((group) => group.id === 'machine')!
        .items.filter((item) => !item.separator)
        .every((item) => item.disabled),
    ).toBe(true);
    const view = groups.find((group) => group.id === 'view')!.items;
    expect(view.find((item) => item.label === 'Environment')!.checked).toBe(
      true,
    );
    view.find((item) => item.label === 'Volumes')!.action!();
    expect(desktop.updateWindowState).toHaveBeenCalledWith(owner.id, {
      vmTab: 'volumes',
      vmEnvironment: false,
    });
    view.find((item) => item.label === 'Environment')!.action!();
    expect(desktop.updateWindowState).toHaveBeenLastCalledWith(owner.id, {
      vmEnvironment: false,
    });
  });
  it('keeps unconfigured views disabled while marking the environment as the visible view', () => {
    const owner = {
      id: 'machines:main',
      viewState: {},
    } as unknown as DesktopWindow;
    const desktop = { updateWindowState: vi.fn() } as unknown as DesktopAPI;
    const view = createMachinesMenu(
      createWindowCommands(),
      owner,
      desktop,
    ).find((group) => group.id === 'view')!.items;
    expect(view.find((item) => item.label === 'Environment')!.checked).toBe(
      true,
    );
    for (const label of ['VMs', 'Volumes', 'Images', 'Activity'])
      expect(view.find((item) => item.label === label)!.disabled).toBe(true);
  });
});
