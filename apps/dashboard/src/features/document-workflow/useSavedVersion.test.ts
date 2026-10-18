import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import type { DocumentSnapshot } from '../../filesystem/documents';
import { APIError } from '../../shared/api/http';
import { SavedVersionPicker } from './SavedVersionPicker';
import {
  currentSavedVersion,
  loadedSavedVersion,
  resolveSavedVersion,
  scopedSavedVersion,
  selectedSavedVersion,
} from './useSavedVersion';
import type { SavedVersionData, SavedVersionState } from './useSavedVersion';

function saved(entryId = 'document-a', revision = 1): SavedVersionData {
  return {
    entry: {
      id: entryId,
      parentId: 'documents',
      name: `${entryId}.md`,
      kind: 'file',
      mime: 'text/markdown',
      size: 12,
      revision,
      createdAt: '',
      updatedAt: '',
      editable: true,
      previewable: true,
      system: false,
    },
    content: `Saved revision ${revision}`,
    snapshots: [],
    warnings: [],
  };
}

function snapshot(
  entryId = 'document-a',
  revision = 1,
  databaseId = 'database-a',
): DocumentSnapshot {
  return {
    id: `${databaseId}-${entryId}-${revision}`,
    databaseId,
    entryId,
    revision,
    name: `${entryId}.md`,
    content: `Retained revision ${revision}`,
    sha256: 'digest',
    createdAt: '2026-10-18T01:00:00Z',
  };
}

const scope = JSON.stringify(['database-a', 'document-a']);
const loading: SavedVersionState = { scope, loading: true };

describe('saved version selection', () => {
  it('defaults to available saved content, including an empty document', () => {
    expect(currentSavedVersion(saved())).toMatchObject({
      kind: 'saved',
      revision: 1,
      content: 'Saved revision 1',
    });
    expect(currentSavedVersion({ ...saved(), content: '' })).toMatchObject({
      content: '',
    });
    expect(
      currentSavedVersion({ ...saved(), content: undefined }),
    ).toBeUndefined();
    const trashed = saved();
    trashed.entry!.trashedAt = '2026-10-18';
    expect(currentSavedVersion(trashed)).toBeUndefined();
    expect(currentSavedVersion({ ...saved(), entry: null })).toBeUndefined();
  });

  it('refreshes metadata without changing the reviewed revision until explicitly selected', () => {
    const initial = loadedSavedVersion(scope, loading, saved());
    const refreshed = loadedSavedVersion(
      scope,
      initial,
      saved('document-a', 2),
    );
    expect(refreshed.data?.entry?.revision).toBe(2);
    expect(refreshed.choice).toBe(initial.choice);
    expect(selectedSavedVersion(refreshed, 'reviewed')).toBe(initial.choice);
    expect(selectedSavedVersion(refreshed, 'saved')).toMatchObject({
      revision: 2,
      content: 'Saved revision 2',
    });
  });

  it('hides previous data immediately and resets selection for another document or database', () => {
    const previous = loadedSavedVersion(scope, loading, saved());
    for (const [databaseId, entryId] of [
      ['database-a', 'document-b'],
      ['database-b', 'document-a'],
    ]) {
      const nextScope = JSON.stringify([databaseId, entryId]);
      expect(scopedSavedVersion(nextScope, previous)).toEqual({
        scope: nextScope,
        loading: true,
      });
      const next = loadedSavedVersion(nextScope, previous, saved(entryId, 3));
      expect(next.choice).toMatchObject({ entryId, revision: 3 });
      expect(next.choice).not.toBe(previous.choice);
    }
  });

  it('retains a reviewed choice after deletion without automatically choosing historical content', () => {
    const retained = snapshot();
    const deleted: SavedVersionData = {
      entry: null,
      snapshots: [retained],
      warnings: [],
    };
    const initial = loadedSavedVersion(scope, loading, saved());
    expect(loadedSavedVersion(scope, initial, deleted).choice).toBe(
      initial.choice,
    );
    expect(loadedSavedVersion(scope, loading, deleted).choice).toBeUndefined();
    expect(
      selectedSavedVersion({ ...loading, data: deleted }, retained.id),
    ).toEqual({
      kind: 'snapshot',
      snapshot: retained,
    });
  });
});

describe('immutable saved version resolution', () => {
  it('reuses the exact captured snapshot on retry and isolates equal revisions across documents and databases', async () => {
    const captured = new Map();
    const first = snapshot();
    const second = snapshot('document-b');
    const third = snapshot('document-a', 1, 'database-b');
    const capture = vi
      .fn()
      .mockResolvedValueOnce({ snapshot: first })
      .mockResolvedValueOnce({ snapshot: second })
      .mockResolvedValueOnce({ snapshot: third });
    const choice = currentSavedVersion(saved());
    await expect(
      resolveSavedVersion(
        'database-a',
        'document-a',
        choice,
        captured,
        capture,
      ),
    ).resolves.toBe(first);
    await expect(
      resolveSavedVersion(
        'database-a',
        'document-a',
        choice,
        captured,
        capture,
      ),
    ).resolves.toBe(first);
    await expect(
      resolveSavedVersion(
        'database-a',
        'document-b',
        currentSavedVersion(saved('document-b')),
        captured,
        capture,
      ),
    ).resolves.toBe(second);
    await expect(
      resolveSavedVersion(
        'database-b',
        'document-a',
        choice,
        captured,
        capture,
      ),
    ).resolves.toBe(third);
    expect(capture.mock.calls).toEqual([
      ['document-a', 1],
      ['document-b', 1],
      ['document-a', 1],
    ]);
  });

  it('never substitutes a newer revision after a capture conflict', async () => {
    const captured = new Map();
    const conflict = new APIError(
      'Review the updated revision.',
      409,
      'revision_conflict',
    );
    const capture = vi.fn().mockRejectedValue(conflict);
    const previous = loadedSavedVersion(scope, loading, saved());
    const refreshed = loadedSavedVersion(
      scope,
      previous,
      saved('document-a', 2),
    );
    for (let attempt = 0; attempt < 2; attempt += 1)
      await expect(
        resolveSavedVersion(
          'database-a',
          'document-a',
          refreshed.choice,
          captured,
          capture,
        ),
      ).rejects.toBe(conflict);
    expect(capture.mock.calls).toEqual([
      ['document-a', 1],
      ['document-a', 1],
    ]);
    expect(captured.size).toBe(0);
  });

  it('uses retained snapshots after source deletion and rejects another document or database', async () => {
    const retained = snapshot();
    const choice = { kind: 'snapshot' as const, snapshot: retained };
    const capture = vi.fn();
    await expect(
      resolveSavedVersion(
        'database-a',
        'document-a',
        choice,
        new Map(),
        capture,
      ),
    ).resolves.toBe(retained);
    await expect(
      resolveSavedVersion(
        'database-a',
        'document-b',
        choice,
        new Map(),
        capture,
      ),
    ).rejects.toThrow('this document');
    await expect(
      resolveSavedVersion(
        'database-b',
        'document-a',
        choice,
        new Map(),
        capture,
      ),
    ).rejects.toThrow('this document');
    await expect(
      resolveSavedVersion(
        'database-a',
        'document-b',
        currentSavedVersion(saved()),
        new Map(),
        capture,
      ),
    ).rejects.toThrow('this document');
    expect(capture).not.toHaveBeenCalled();
  });

  it('does not retain a capture response for a different database or revision', async () => {
    const captured = new Map();
    const capture = vi
      .fn()
      .mockResolvedValue({ snapshot: snapshot('document-a', 2) });
    await expect(
      resolveSavedVersion(
        'database-a',
        'document-a',
        currentSavedVersion(saved()),
        captured,
        capture,
      ),
    ).rejects.toThrow('does not match');
    expect(captured.size).toBe(0);
  });
});

describe('saved version picker', () => {
  it('keeps the selected historical option when refresh no longer lists it', () => {
    const retained = snapshot();
    const selected: SavedVersionState = {
      ...loading,
      data: { ...saved(), snapshots: [] },
      choice: { kind: 'snapshot', snapshot: retained },
    };
    expect(selectedSavedVersion(selected, retained.id)).toBe(selected.choice);
    const markup = renderToStaticMarkup(
      createElement(SavedVersionPicker, {
        version: {
          data: {
            ...saved('document-a', 102),
            snapshots: [],
            warnings: ['Versions: temporarily unavailable'],
          },
          choice: { kind: 'snapshot', snapshot: retained },
          loading: false,
          error: undefined,
          choose: vi.fn(),
          reload: vi.fn(),
          resolve: async () => retained,
        },
      }),
    );
    expect(markup).toContain(
      `<option value="${retained.id}" selected="">Revision 1`,
    );
    expect(markup).toContain(
      '<option value="saved">Saved revision 102</option>',
    );
  });
});
