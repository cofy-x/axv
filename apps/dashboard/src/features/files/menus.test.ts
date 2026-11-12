import { describe, expect, it, vi } from 'vitest';
import type { DesktopAPI, DesktopWindow } from '@cofy-x/axv-nextstep';
import type { FilesystemData } from '../../filesystem/context';
import { createFilesMenu } from './menus';

describe('file application view commands', () => {
  it('uses file sorting and refresh rather than desktop arrangement, bound to its original window', async () => {
    const refresh = vi.fn(async () => {});
    const data = {
      selections: {},
      entries: [],
      refresh,
    } as unknown as FilesystemData;
    const owner = {
      id: 'files:documents',
      params: {},
      viewState: { sort: 'modified', descending: true },
    } as unknown as DesktopWindow;
    const desktop = {
      activeWindowId: 'files:other',
      updateWindowState: vi.fn(),
      dialog: vi.fn(),
    } as unknown as DesktopAPI;
    const items = createFilesMenu(data, owner, desktop).find(
      (group) => group.id === 'view',
    )!.items;
    expect(items.some((item) => item.label === 'Arrange Icons')).toBe(false);
    expect(
      items.find((item) => item.label === 'Sort by Modified')!.checked,
    ).toBe(true);
    expect(items.find((item) => item.label === 'Descending')!.checked).toBe(
      true,
    );
    items.find((item) => item.label === 'Sort by Name')!.action!();
    expect(desktop.updateWindowState).toHaveBeenCalledWith(owner.id, {
      sort: 'name',
    });
    items.find((item) => item.label === 'Refresh Folder')!.action!();
    await Promise.resolve();
    expect(refresh).toHaveBeenCalledOnce();
  });
});
