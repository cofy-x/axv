import { describe, expect, it, vi } from 'vitest';
import { APIError } from '../../shared/api/http';
import { createNoteDocument } from './note-document';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((ok, fail) => {
    resolve = ok;
    reject = fail;
  });
  return { promise, resolve, reject };
}

function stored(content: string, revision: number) {
  return { content, entry: { revision } };
}

describe('note document lifecycle', () => {
  it('saves only the requested snapshot and leaves later typing dirty', async () => {
    const first = deferred<{ entry: { revision: number } }>();
    const write = vi
      .fn<
        (
          content: string,
          revision: number,
        ) => Promise<{ entry: { revision: number } }>
      >()
      .mockImplementationOnce(() => first.promise)
      .mockResolvedValue({ entry: { revision: 5 } });
    const document = createNoteDocument({
      read: async () => stored('original', 3),
      write,
      refresh: async () => {},
    });
    await document.load();
    document.edit('first draft');
    const firstSave = document.save();
    document.edit('latest draft');
    const explicitSave = document.save();
    expect(write.mock.calls).toEqual([['first draft', 3]]);
    first.resolve({ entry: { revision: 4 } });
    await expect(Promise.all([firstSave, explicitSave])).resolves.toEqual([
      false,
      false,
    ]);
    expect(write.mock.calls).toEqual([['first draft', 3]]);
    expect(document.getSnapshot()).toMatchObject({
      text: 'latest draft',
      saved: 'first draft',
      revision: 4,
      dirty: true,
    });
    await document.save();
    expect(write.mock.calls).toEqual([
      ['first draft', 3],
      ['latest draft', 4],
    ]);
    expect(document.getSnapshot()).toMatchObject({
      text: 'latest draft',
      saved: 'latest draft',
      revision: 5,
      dirty: false,
      status: 'Saved',
    });
  });

  it('retains a failed draft and retries with its last committed revision', async () => {
    const write = vi
      .fn<
        (
          content: string,
          revision: number,
        ) => Promise<{ entry: { revision: number } }>
      >()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue({ entry: { revision: 8 } });
    const document = createNoteDocument({
      read: async () => stored('original', 7),
      write,
      refresh: async () => {},
    });
    await document.load();
    document.edit('unsaved draft');
    await expect(document.save()).resolves.toBe(false);
    expect(document.getSnapshot()).toMatchObject({
      text: 'unsaved draft',
      saved: 'original',
      revision: 7,
      dirty: true,
      conflict: false,
      error: 'offline',
    });
    await expect(document.save()).resolves.toBe(true);
    expect(write.mock.calls).toEqual([
      ['unsaved draft', 7],
      ['unsaved draft', 7],
    ]);
    expect(document.getSnapshot().dirty).toBe(false);
  });

  it('blocks a conflicting draft until explicitly loading the saved revision', async () => {
    const read = vi
      .fn()
      .mockResolvedValueOnce(stored('original', 2))
      .mockResolvedValueOnce(stored('other window', 3));
    const write = vi
      .fn<
        (
          content: string,
          revision: number,
        ) => Promise<{ entry: { revision: number } }>
      >()
      .mockRejectedValueOnce(
        new APIError('Revision conflict', 409, 'revision_conflict'),
      )
      .mockResolvedValue({ entry: { revision: 4 } });
    const document = createNoteDocument({
      read,
      write,
      refresh: async () => {},
    });
    await document.load();
    document.edit('my draft');
    await expect(document.save()).resolves.toBe(false);
    await expect(document.save()).resolves.toBe(false);
    expect(write).toHaveBeenCalledTimes(1);
    expect(document.getSnapshot()).toMatchObject({
      text: 'my draft',
      saved: 'original',
      revision: 2,
      conflict: true,
      dirty: true,
    });
    await document.load();
    expect(document.getSnapshot()).toMatchObject({
      text: 'other window',
      revision: 3,
      conflict: false,
      dirty: false,
    });
    document.edit('merged draft');
    await expect(document.save()).resolves.toBe(true);
    expect(write.mock.calls[1]).toEqual(['merged draft', 3]);
  });

  it('does not report a committed save as failed when metadata refresh is offline', async () => {
    const document = createNoteDocument({
      read: async () => stored('original', 1),
      write: async () => ({ entry: { revision: 2 } }),
      refresh: async () => {
        throw new Error('metadata offline');
      },
    });
    await document.load();
    document.edit('committed');
    await expect(document.save()).resolves.toBe(true);
    expect(document.getSnapshot()).toMatchObject({
      saved: 'committed',
      revision: 2,
      dirty: false,
      status: 'Saved',
      error: '',
    });
  });

  it.each(['success', 'failure'])(
    'ignores a stale load %s after cleanup and a newer document load',
    async (outcome) => {
      const stale = deferred<ReturnType<typeof stored>>();
      const current = deferred<ReturnType<typeof stored>>();
      const read = vi
        .fn()
        .mockImplementationOnce(() => stale.promise)
        .mockImplementationOnce(() => current.promise);
      const document = createNoteDocument({
        read,
        write: async () => ({ entry: { revision: 3 } }),
        refresh: async () => {},
      });
      const firstLoad = document.load();
      document.cancelLoad();
      const secondLoad = document.load();
      current.resolve(stored('current', 2));
      await secondLoad;
      document.edit('keep this draft');
      if (outcome === 'success') stale.resolve(stored('stale', 1));
      else stale.reject(new Error('obsolete request failed'));
      await firstLoad;
      expect(document.getSnapshot()).toMatchObject({
        text: 'keep this draft',
        saved: 'current',
        revision: 2,
        dirty: true,
      });
    },
  );
});

describe('local draft recovery and metadata ordering', () => {
  it('restores a draft without rebasing it onto a changed server revision', async () => {
    const write = vi.fn(async () => ({ entry: { revision: 10 } }));
    const document = createNoteDocument(
      {
        read: async () => stored('remote text', 9),
        write,
        refresh: async () => {},
      },
      {
        read: async () => ({
          text: 'my recovered draft',
          saved: 'original',
          baseRevision: 3,
        }),
        write: async () => {},
        remove: async () => {},
      },
    );
    await document.load();
    expect(document.getSnapshot()).toMatchObject({
      text: 'remote text',
      recoveryAvailable: true,
    });
    document.restoreDraft();
    expect(document.getSnapshot()).toMatchObject({
      text: 'my recovered draft',
      revision: 3,
      conflict: true,
      dirty: true,
      recovered: true,
    });
    expect(await document.save()).toBe(false);
    expect(write).not.toHaveBeenCalled();
  });

  it('retains a newer draft after an earlier save completes', async () => {
    const pending = deferred<{ entry: { revision: number } }>();
    const local = {
      read: async () => undefined,
      write: vi.fn(
        async (_draft: {
          text: string;
          saved: string;
          baseRevision: number;
        }) => {},
      ),
      remove: vi.fn(async () => {}),
    };
    const document = createNoteDocument(
      {
        read: async () => stored('original', 1),
        write: () => pending.promise,
        refresh: async () => {},
      },
      local,
    );
    await document.load();
    document.edit('first');
    const save = document.save();
    document.edit('newer');
    pending.resolve({ entry: { revision: 2 } });
    await save;
    await document.flushDraft();
    expect(local.write).toHaveBeenLastCalledWith({
      text: 'newer',
      saved: 'first',
      baseRevision: 2,
    });
    expect(local.remove).not.toHaveBeenCalled();
  });

  it('adopts only a known metadata mutation based on its loaded revision', async () => {
    const document = createNoteDocument({
      read: async () => stored('original', 4),
      write: async () => ({ entry: { revision: 7 } }),
      refresh: async () => {},
    });
    await document.load();
    document.edit('draft');
    document.metadataChanged(3, 5);
    expect(document.getSnapshot().revision).toBe(4);
    document.metadataChanged(4, 5);
    expect(document.getSnapshot()).toMatchObject({
      revision: 5,
      text: 'draft',
      dirty: true,
    });
  });

  it('does not mislabel an unavailable item as a revision conflict', async () => {
    const document = createNoteDocument({
      read: async () => stored('original', 1),
      write: async () => {
        throw new APIError('In the Recycler', 409, 'unavailable');
      },
      refresh: async () => {},
    });
    await document.load();
    document.edit('draft');
    await document.save();
    expect(document.getSnapshot()).toMatchObject({
      conflict: false,
      text: 'draft',
      dirty: true,
      error: 'In the Recycler',
    });
  });

  it('reports unavailable local storage without discarding the draft or stopping explicit saves', async () => {
    const fail = async () => {
      throw new Error('quota exceeded');
    };
    const document = createNoteDocument(
      {
        read: async () => stored('original', 1),
        write: async () => ({ entry: { revision: 2 } }),
        refresh: async () => {},
      },
      { read: async () => undefined, write: fail, remove: fail },
    );
    await document.load();
    document.edit('draft');
    await document.flushDraft();
    expect(document.getSnapshot().localStatus).toContain('unavailable');
    expect(document.getSnapshot().text).toBe('draft');
    expect(await document.save()).toBe(true);
  });

  it('does not replace typing that happens while a reload is pending', async () => {
    const pending = deferred<ReturnType<typeof stored>>();
    const read = vi
      .fn()
      .mockResolvedValueOnce(stored('original', 1))
      .mockImplementationOnce(() => pending.promise);
    const document = createNoteDocument({
      read,
      write: async () => ({ entry: { revision: 2 } }),
      refresh: async () => {},
    });
    await document.load();
    const loading = document.reload();
    document.edit('new typing');
    pending.resolve(stored('remote', 2));
    await loading;
    expect(document.getSnapshot()).toMatchObject({
      text: 'new typing',
      revision: 1,
      dirty: true,
    });
  });
});

describe('recovery choices and local storage failures', () => {
  it('offers recovery without changing displayed server text or writing to the server', async () => {
    const write = vi.fn(async () => ({ entry: { revision: 2 } }));
    const remove = vi.fn(async () => {});
    const document = createNoteDocument(
      { read: async () => stored('saved', 1), write, refresh: async () => {} },
      {
        read: async () => ({
          text: 'recovery',
          saved: 'saved',
          baseRevision: 1,
        }),
        write: async () => {},
        remove,
      },
    );
    await document.load();
    expect(document.getSnapshot()).toMatchObject({
      text: 'saved',
      recoveryAvailable: true,
      dirty: false,
    });
    document.edit('should not overwrite a pending recovery');
    expect(document.getSnapshot().text).toBe('saved');
    await document.keepSaved();
    expect(document.getSnapshot()).toMatchObject({
      text: 'saved',
      recoveryAvailable: false,
    });
    expect(remove).toHaveBeenCalledOnce();
    expect(write).not.toHaveBeenCalled();
  });

  it('keeps the dirty draft and reports failure if discard cannot remove the recovery record', async () => {
    const document = createNoteDocument(
      {
        read: async () => stored('saved', 1),
        write: async () => ({ entry: { revision: 2 } }),
        refresh: async () => {},
      },
      {
        read: async () => undefined,
        write: async () => {},
        remove: async () => {
          throw new Error('blocked');
        },
      },
    );
    await document.load();
    document.edit('draft to keep');
    await expect(document.discard()).rejects.toThrow('blocked');
    expect(document.getSnapshot()).toMatchObject({
      text: 'draft to keep',
      dirty: true,
      resolvingRecovery: false,
    });
    expect(document.getSnapshot().error).toContain('could not be removed');
  });

  it('coalesces pending local writes to the newest snapshot while a write is in flight', async () => {
    const first = deferred<void>();
    const localWrite = vi
      .fn()
      .mockImplementationOnce(() => first.promise)
      .mockResolvedValue(undefined);
    const document = createNoteDocument(
      {
        read: async () => stored('saved', 1),
        write: async () => ({ entry: { revision: 2 } }),
        refresh: async () => {},
      },
      {
        read: async () => undefined,
        write: localWrite,
        remove: async () => {},
      },
    );
    await document.load();
    document.edit('first');
    await Promise.resolve();
    document.edit('second');
    document.edit('third');
    document.edit('latest');
    expect(localWrite).toHaveBeenCalledTimes(1);
    first.resolve();
    await document.flushDraft();
    expect(localWrite.mock.calls.map(([draft]) => draft.text)).toEqual([
      'first',
      'latest',
    ]);
  });
});
