import { describe, expect, it, vi } from 'vitest';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { createDesktopIcons } from './desktop-items';

describe('dashboard desktop application launchers', () => {
  it('provides the core application launchers for the desktop', () => {
    const icons = createDesktopIcons(() => null);
    expect(
      icons
        .filter((icon) => icon.applicationLauncher)
        .map((icon) => icon.appId),
    ).toEqual([
      'terminal',
      'project-files',
      'remote-access',
      'appearance',
      'codex',
      'machines',
      'gallery',
    ]);
  });

  it('preserves Terminal launcher session creation after adding its application identity', () => {
    const openApp = vi.fn();
    const desktop = {
      windows: [{ title: 'Terminal' }],
      openApp,
    } as unknown as DesktopAPI;
    const terminal = createDesktopIcons(() => desktop).find(
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
