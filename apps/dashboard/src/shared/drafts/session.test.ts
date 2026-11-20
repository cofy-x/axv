import { describe, expect, it } from 'vitest';
import { claimEditorSession } from './session';

describe('cloned browser tab identity claims', () => {
  it('gives a duplicate tab an independent draft ID even when sessionStorage was copied', async () => {
    const held = new Set<string>();
    const locks = {
      request: async (
        name: string,
        _options: { ifAvailable: true },
        callback: (lock: object | null) => Promise<void>,
      ) => {
        if (held.has(name)) return callback(null);
        held.add(name);
        return callback({});
      },
    };
    const copiedStorage = () => ({
      getItem: () => 'copied-draft-id',
      setItem: () => {},
    });
    const namespace = { databaseId: 'extensions:store', clientId: 'browser' };
    const first = await claimEditorSession(namespace, 'extension.sample:main', {
      storage: copiedStorage(),
      locks,
      randomId: () => 'first-independent',
    });
    const second = await claimEditorSession(
      namespace,
      'extension.sample:main',
      {
        storage: copiedStorage(),
        locks,
        randomId: () => 'second-independent',
      },
    );
    expect(first.draftId).toBe('copied-draft-id');
    expect(second.draftId).toBe('second-independent');
    expect(second.recoveryId).toBeUndefined();
  });

  it('uses independent records when exclusive browser locks are unavailable', async () => {
    const session = { getItem: () => 'older-draft', setItem: () => {} };
    const claim = await claimEditorSession(
      { databaseId: 'extensions:store', clientId: 'browser' },
      'extension.sample:main',
      { storage: session, randomId: () => 'new-draft' },
    );
    expect(claim).toEqual({ draftId: 'new-draft', recoveryId: 'older-draft' });
  });
});
