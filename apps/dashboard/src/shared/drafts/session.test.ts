import { describe, expect, it } from 'vitest';
import { editorDraftId } from './session';

function browserTab() {
  const values = new Map<string, string>();
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
  };
}

describe('editor recovery identities', () => {
  it('isolates normal same-document editors in separate tabs and preserves each across reload', () => {
    const first = browserTab();
    const second = browserTab();
    const namespace = { databaseId: 'same-database', clientId: 'same-browser' };
    const firstId = editorDraftId(
      namespace,
      'notes:document',
      first,
      () => 'first-draft',
    );
    const secondId = editorDraftId(
      namespace,
      'notes:document',
      second,
      () => 'second-draft',
    );
    expect(firstId).not.toBe(secondId);
    expect(
      editorDraftId(namespace, 'notes:document', first, () => 'unused'),
    ).toBe(firstId);
    expect(
      editorDraftId(namespace, 'notes:document', second, () => 'unused'),
    ).toBe(secondId);
  });

  it('isolates a database replaced on the same browser origin', () => {
    const session = browserTab();
    const first = editorDraftId(
      { databaseId: 'old', clientId: 'browser' },
      'notes:readme',
      session,
      () => 'old-draft',
    );
    const next = editorDraftId(
      { databaseId: 'new', clientId: 'browser' },
      'notes:readme',
      session,
      () => 'new-draft',
    );
    expect(first).not.toBe(next);
  });
});

describe('cloned browser tab identity claims', () => {
  it('gives a duplicate tab an independent draft ID even when sessionStorage was copied', async () => {
    const { claimEditorSession } = await import('./session');
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
    const namespace = { databaseId: 'database', clientId: 'browser' };
    const first = await claimEditorSession(namespace, 'notes:file', {
      storage: copiedStorage(),
      locks,
      randomId: () => 'first-independent',
    });
    const second = await claimEditorSession(namespace, 'notes:file', {
      storage: copiedStorage(),
      locks,
      randomId: () => 'second-independent',
    });
    expect(first.draftId).toBe('copied-draft-id');
    expect(second.draftId).toBe('second-independent');
    expect(second.recoveryId).toBeUndefined();
  });

  it('uses independent records when exclusive browser locks are unavailable', async () => {
    const { claimEditorSession } = await import('./session');
    const session = { getItem: () => 'older-draft', setItem: () => {} };
    const claim = await claimEditorSession(
      { databaseId: 'database', clientId: 'browser' },
      'notes:file',
      { storage: session, randomId: () => 'new-draft' },
    );
    expect(claim).toEqual({ draftId: 'new-draft', recoveryId: 'older-draft' });
  });
});
