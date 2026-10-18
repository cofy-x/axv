import type { Entry } from '../../filesystem/types';
import { isInTrash } from '../../filesystem/tree';

/** Rebuild the browser path from stable IDs after rename, move, trash or restore. */
export function folderPath(entries: Entry[], folderId: string): Entry[] {
  const byId = new Map(entries.map((entry) => [entry.id, entry]));
  const path: Entry[] = [];
  const seen = new Set<string>();
  let current = byId.get(folderId);
  while (current && current.kind === 'folder' && !seen.has(current.id)) {
    if (isInTrash(current, entries)) return [];
    seen.add(current.id);
    path.unshift(current);
    current = byId.get(current.parentId);
  }
  return path;
}

export function availableFolder(
  entries: Entry[],
  preferred: string,
  previousPath: string[],
  anchor: string,
): string {
  for (const id of [
    preferred,
    ...[...previousPath].reverse(),
    anchor,
    'documents',
    'root',
  ]) {
    if (folderPath(entries, id).length) return id;
  }
  return 'root';
}

/** Repair the current location against live entries, retaining its nearest ancestor. */
export function reconcileNavigation(
  entries: Entry[],
  state: Readonly<Record<string, unknown>>,
  anchor: string,
): { folderId: string; pathIds: string[] } {
  const previousPath = Array.isArray(state.pathIds)
    ? state.pathIds.filter((id): id is string => typeof id === 'string')
    : [];
  const folderId = availableFolder(
    entries,
    typeof state.folderId === 'string' ? state.folderId : anchor,
    previousPath,
    anchor,
  );
  return {
    folderId,
    pathIds: folderPath(entries, folderId).map((entry) => entry.id),
  };
}

export function entryPath(entries: Entry[], entry: Entry): string {
  const path = folderPath(
    entries,
    entry.kind === 'folder' ? entry.id : entry.parentId,
  );
  return (
    '/' +
    [
      ...path.map((item) => item.name),
      ...(entry.kind === 'file' ? [entry.name] : []),
    ].join('/')
  );
}
