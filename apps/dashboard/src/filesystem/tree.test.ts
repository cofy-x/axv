import { describe, expect, it } from 'vitest';
import type { Entry } from './types';
import { affectedEntries, isInTrash, selectedRoots } from './tree';

function entry(
  id: string,
  parentId: string,
  kind: Entry['kind'] = 'folder',
): Entry {
  return {
    id,
    parentId,
    kind,
    name: id,
    mime: '',
    size: 0,
    revision: 1,
    createdAt: '',
    updatedAt: '',
    editable: kind === 'file',
    previewable: false,
    system: false,
  };
}

const tree = [
  entry('documents', ''),
  entry('projects', 'documents'),
  entry('nested', 'projects'),
  entry('draft', 'nested', 'file'),
  entry('readme', 'projects', 'file'),
  entry('pictures', 'documents'),
  entry('photo', 'pictures', 'file'),
  entry('loose', 'documents', 'file'),
];
const ids = (entries: Entry[]) => entries.map((item) => item.id);

describe('selected filesystem roots', () => {
  it('moves or copies a selected folder and its selected descendants only once', () => {
    expect(
      ids(selectedRoots(tree, ['draft', 'projects', 'nested', 'readme'])),
    ).toEqual(['projects']);
  });

  it('preserves independent selections and their order across nested trees', () => {
    expect(
      ids(
        selectedRoots(tree, ['photo', 'loose', 'draft', 'pictures', 'nested']),
      ),
    ).toEqual(['loose', 'pictures', 'nested']);
    expect(ids(selectedRoots(tree, ['readme', 'draft']))).toEqual([
      'readme',
      'draft',
    ]);
  });

  it('ignores stale and duplicate identifiers without mutating the inputs', () => {
    const selection = ['missing', 'draft', 'draft', 'loose'];
    const before = structuredClone(tree);
    expect(ids(selectedRoots(tree, selection))).toEqual(['draft', 'loose']);
    expect(selection).toEqual(['missing', 'draft', 'draft', 'loose']);
    expect(tree).toEqual(before);
  });

  it('keeps one deterministic selected member of a corrupt parent cycle', () => {
    const cycle = [
      entry('a', 'b'),
      entry('b', 'c'),
      entry('c', 'a'),
      entry('child', 'a'),
      entry('independent', ''),
    ];
    expect(
      ids(selectedRoots(cycle, ['child', 'b', 'a', 'independent'])),
    ).toEqual(['b', 'independent']);
    expect(ids(selectedRoots(cycle, ['a', 'b']))).toEqual(['a']);
    expect(ids(selectedRoots(cycle, ['child']))).toEqual(['child']);
    expect(ids(selectedRoots([entry('self', 'self')], ['self']))).toEqual([
      'self',
    ]);
  });
});

describe('affected filesystem entries', () => {
  it('includes the selected folder and every nested document for close guards', () => {
    expect(new Set(ids(affectedEntries(tree, ['projects'])))).toEqual(
      new Set(['projects', 'nested', 'draft', 'readme']),
    );
  });

  it('includes independent trees and deduplicates overlapping selections', () => {
    const result = ids(
      affectedEntries(tree, [
        'nested',
        'projects',
        'photo',
        'loose',
        'missing',
        'projects',
      ]),
    );
    expect(new Set(result)).toEqual(
      new Set(['nested', 'projects', 'draft', 'readme', 'photo', 'loose']),
    );
    expect(result).toHaveLength(6);
    expect(affectedEntries(tree, [])).toEqual([]);
  });

  it('terminates on cycles and includes each reachable entry exactly once', () => {
    const cycle = [
      entry('a', 'b'),
      entry('b', 'a'),
      entry('draft', 'b', 'file'),
      entry('self', 'self'),
    ];
    expect(ids(affectedEntries(cycle, ['a', 'b', 'self']))).toEqual([
      'a',
      'b',
      'self',
      'draft',
    ]);
  });
});

describe('virtual folder visibility', () => {
  it('hides descendants of a trashed folder but terminates on malformed cycles', () => {
    const entries = [
      entry('root', 'root'),
      { ...entry('folder', 'root'), trashedAt: '2026-10-15' },
      entry('child', 'folder'),
      entry('active', 'root'),
    ];
    expect(isInTrash(entries[2]!, entries)).toBe(true);
    expect(isInTrash(entries[3]!, entries)).toBe(false);
    expect(isInTrash(entry('loop', 'loop'), [entry('loop', 'loop')])).toBe(
      false,
    );
  });
});
