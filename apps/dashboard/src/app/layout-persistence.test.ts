import { describe, expect, it, vi } from 'vitest';
import type { DesktopLayout } from '@cofy-x/axv-nextstep';
import { createLayoutPersistence } from './layout-persistence';

function layout(x: number): DesktopLayout {
  return {
    version: 2,
    workspaces: [
      {
        id: 'workspace-1',
        name: 'Workspace 1',
        order: [],
        activeWindowId: null,
        clip: [],
      },
    ],
    currentWorkspaceId: 'workspace-1',
    dock: [],
    pinnedMenus: [],
    windows: [],
    activeWindowId: null,
    icons: { document: { x, y: 0 } },
    compactIcons: {},
  };
}

function deferred() {
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<void>((ok, fail) => {
    resolve = ok;
    reject = fail;
  });
  return { promise, resolve, reject };
}

describe('desktop layout persistence', () => {
  it('serializes writes and keeps only the latest movement while saving', async () => {
    const first = deferred();
    const write = vi
      .fn()
      .mockImplementationOnce(() => first.promise)
      .mockResolvedValue(undefined);
    const saved = vi.fn();
    const failed = vi.fn();
    const persistence = createLayoutPersistence(write, saved, failed);
    persistence.enqueue(layout(10));
    const flushing = persistence.flush();
    persistence.enqueue(layout(20));
    persistence.enqueue(layout(30));
    await persistence.flush();
    expect(write).toHaveBeenCalledTimes(1);
    first.resolve();
    await flushing;
    expect(write.mock.calls).toEqual([[layout(10)], [layout(30)]]);
    expect(saved).toHaveBeenCalledTimes(2);
    expect(failed).not.toHaveBeenCalled();
    await persistence.flush();
    expect(write).toHaveBeenCalledTimes(2);
  });

  it('retries the latest layout rather than restoring a failed stale snapshot', async () => {
    const first = deferred();
    const write = vi
      .fn()
      .mockImplementationOnce(() => first.promise)
      .mockResolvedValue(undefined);
    const failed = vi.fn();
    const persistence = createLayoutPersistence(write, vi.fn(), failed);
    persistence.enqueue(layout(10));
    const flushing = persistence.flush();
    persistence.enqueue(layout(40));
    const error = new Error('offline');
    first.reject(error);
    await flushing;
    expect(failed).toHaveBeenCalledWith(error);
    expect(write).toHaveBeenCalledTimes(1);
    await persistence.flush();
    expect(write.mock.calls).toEqual([[layout(10)], [layout(40)]]);
  });

  it('keeps a failed snapshot when no newer movement exists', async () => {
    const write = vi
      .fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue(undefined);
    const persistence = createLayoutPersistence(write, vi.fn(), vi.fn());
    persistence.enqueue(layout(20));
    await persistence.flush();
    await persistence.flush();
    expect(write.mock.calls).toEqual([[layout(20)], [layout(20)]]);
  });
});
