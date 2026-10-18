import type { Entry } from '../../filesystem/types';

export type FileView = 'icons' | 'list';
export type FileSort = 'name' | 'modified' | 'size' | 'kind';

export interface FileRow {
  entry: Entry;
  depth: number;
}

export function folderEntries(
  entries: Entry[],
  parentId: string,
  sort: FileSort,
  descending: boolean,
): Entry[] {
  return entries
    .filter((entry) => entry.parentId === parentId && entry.id !== 'root')
    .sort((a, b) => {
      const comparison =
        sort === 'size'
          ? a.size - b.size
          : sort === 'modified'
            ? a.updatedAt.localeCompare(b.updatedAt)
            : sort === 'kind'
              ? a.kind.localeCompare(b.kind) || a.mime.localeCompare(b.mime)
              : a.name.localeCompare(b.name, undefined, { numeric: true });
      return comparison * (descending ? -1 : 1);
    });
}

export function buildFileRows(
  entries: Entry[],
  folderId: string,
  view: FileView,
  sort: FileSort,
  descending: boolean,
  expanded: ReadonlySet<string>,
): FileRow[] {
  const rows: FileRow[] = [];

  function collect(parentId: string, depth: number) {
    for (const entry of folderEntries(entries, parentId, sort, descending)) {
      rows.push({ entry, depth });
      if (view === 'list' && expanded.has(entry.id) && depth < 30)
        collect(entry.id, depth + 1);
    }
  }

  collect(folderId, 0);
  return rows;
}
