import { describe, expect, it, vi } from 'vitest';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { FilesystemData } from '../filesystem/context';
import { createDesktopIcons } from './desktop-items';

const emptyFilesystem = { entries: [] } as unknown as FilesystemData;

describe('dashboard desktop application launchers', () => {
  it('marks pure application launchers independently of resource shortcuts', () => {
    const icons = createDesktopIcons(emptyFilesystem, () => null);
    expect(
      icons
        .filter((icon) => icon.applicationLauncher)
        .map((icon) => icon.appId),
    ).toEqual([
      'notes',
      'terminal',
      'project-files',
      'remote-access',
      'appearance',
      'codex',
      'machines',
      'gallery',
    ]);
    for (const id of ['system:computer', 'system:documents', 'system:trash'])
      expect(
        icons.find((icon) => icon.id === id)?.applicationLauncher,
      ).toBeUndefined();
  });

  it('preserves Terminal launcher session creation after adding its application identity', () => {
    const openApp = vi.fn();
    const desktop = {
      windows: [{ title: 'Terminal' }],
      openApp,
    } as unknown as DesktopAPI;
    const terminal = createDesktopIcons(emptyFilesystem, () => desktop).find(
      (icon) => icon.appId === 'terminal',
    );
    terminal?.onOpen?.();
    expect(openApp).toHaveBeenCalledWith(
      'terminal',
      { sessionId: expect.any(String) },
      { title: 'Terminal 2' },
    );
  });
});
