import type { Entry } from './types';

/** Keep one operation per selected tree, in the caller's selection order. */
export function selectedRoots(entries: Entry[], ids: string[]): Entry[] {
  const byId = new Map(entries.map((entry) => [entry.id, entry]));
  const selected = [...new Set(ids)]
    .map((id) => byId.get(id))
    .filter((entry): entry is Entry => Boolean(entry));
  const selectionOrder = new Map(
    selected.map((entry, index) => [entry.id, index]),
  );
  const ancestors = new Map<string, Set<string>>();

  for (const entry of selected) {
    const lineage = new Set<string>();
    const visited = new Set([entry.id]);
    let parent = byId.get(entry.parentId);
    while (parent && !visited.has(parent.id)) {
      visited.add(parent.id);
      lineage.add(parent.id);
      parent = byId.get(parent.parentId);
    }
    ancestors.set(entry.id, lineage);
  }

  return selected.filter((entry, index) => {
    for (const ancestorId of ancestors.get(entry.id) ?? []) {
      const ancestorIndex = selectionOrder.get(ancestorId);
      if (ancestorIndex === undefined) continue;
      // A corrupt parent cycle has no natural root. Keep its first selected
      // member instead of discarding every member or traversing indefinitely.
      const sameCycle = ancestors.get(ancestorId)?.has(entry.id);
      if (!sameCycle || ancestorIndex < index) return false;
    }
    return true;
  });
}

/** Collect every selected entry and descendant once, including open documents. */
export function affectedEntries(entries: Entry[], ids: string[]): Entry[] {
  const byId = new Map(entries.map((entry) => [entry.id, entry]));
  const children = new Map<string, Entry[]>();
  for (const entry of byId.values()) {
    const siblings = children.get(entry.parentId) ?? [];
    siblings.push(entry);
    children.set(entry.parentId, siblings);
  }

  const queue = ids
    .map((id) => byId.get(id))
    .filter((entry): entry is Entry => Boolean(entry));
  const visited = new Set<string>();
  const result: Entry[] = [];
  for (let index = 0; index < queue.length; index++) {
    const entry = queue[index]!;
    if (visited.has(entry.id)) continue;
    visited.add(entry.id);
    result.push(entry);
    queue.push(...(children.get(entry.id) ?? []));
  }
  return result;
}

export function isInTrash(entry: Entry, entries: Entry[]): boolean {
  const seen = new Set<string>();
  let current: Entry | undefined = entry;
  while (current && !seen.has(current.id)) {
    if (current.trashedAt) return true;
    seen.add(current.id);
    current = entries.find((item) => item.id === current?.parentId);
  }
  return false;
}
