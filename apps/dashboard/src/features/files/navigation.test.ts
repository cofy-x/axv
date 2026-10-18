import { describe, expect, it } from 'vitest';
import type { Entry } from '../../filesystem/types';
import {
  availableFolder,
  entryPath,
  folderPath,
  reconcileNavigation,
} from './navigation';

function folder(
  id: string,
  parentId: string,
  name = id,
  trashedAt?: string,
): Entry {
  return {
    id,
    parentId,
    name,
    trashedAt,
    kind: 'folder',
    mime: '',
    size: 0,
    revision: 1,
    createdAt: '',
    updatedAt: '',
    editable: false,
    previewable: false,
    system: false,
  };
}

describe('column navigation by stable folder identity', () => {
  it('rebuilds paths after moving or renaming an ancestor', () => {
    const entries = [
      folder('root', ''),
      folder('documents', 'root'),
      folder('other', 'root'),
      folder('nested', 'other', 'Renamed'),
    ];
    expect(folderPath(entries, 'nested').map((entry) => entry.id)).toEqual([
      'root',
      'other',
      'nested',
    ]);
    expect(entryPath(entries, entries[3]!)).toBe('/root/other/Renamed');
  });
  it('recovers the closest live ancestor after trash or deletion', () => {
    const entries = [
      folder('root', ''),
      folder('documents', 'root'),
      folder('nested', 'documents', 'Nested', 'now'),
    ];
    expect(
      availableFolder(
        entries,
        'missing',
        ['root', 'documents', 'nested'],
        'missing',
      ),
    ).toBe('documents');
    expect(folderPath(entries, 'nested')).toEqual([]);
  });
  it('terminates on a malformed cycle without inventing infinite columns', () => {
    expect(folderPath([folder('a', 'b'), folder('b', 'a')], 'a')).toHaveLength(
      2,
    );
  });

  it('reconciles a newly selected child after its ancestor moves', () => {
    const entries = [
      folder('root', ''),
      folder('documents', 'root'),
      folder('destination', 'documents'),
      folder('parent', 'destination'),
      folder('child', 'parent'),
    ];
    const state = {
      folderId: 'child',
      pathIds: ['root', 'documents', 'parent', 'child'],
      scrollLeft: 38,
    };
    expect(reconcileNavigation(entries, state, 'documents')).toEqual({
      folderId: 'child',
      pathIds: ['root', 'documents', 'destination', 'parent', 'child'],
    });
    expect(state.pathIds).toEqual(['root', 'documents', 'parent', 'child']);
  });

  it('keeps a recovered location when a deleted ancestor returns', () => {
    const entries = [
      folder('root', ''),
      folder('documents', 'root'),
      folder('parent', 'documents'),
      folder('child', 'parent'),
    ];
    const state = {
      folderId: 'child',
      pathIds: ['root', 'documents', 'parent', 'child'],
    };
    const recovered = reconcileNavigation(
      entries.slice(0, 2),
      state,
      'documents',
    );
    expect(recovered).toEqual({
      folderId: 'documents',
      pathIds: ['root', 'documents'],
    });
    expect(reconcileNavigation(entries, recovered, 'documents')).toEqual(
      recovered,
    );
  });
});
