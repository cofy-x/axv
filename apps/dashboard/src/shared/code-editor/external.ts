import { diff } from '@codemirror/merge';
import type { EditorState } from '@codemirror/state';
import { sourceReplacement } from './source';

/** Map reading positions without putting external snapshots in local history. */
export function externalSnapshot(state: EditorState, value: string) {
  const replacement = sourceReplacement(value);
  const before = state.doc.toString();
  const changes = state.changes(
    diff(before, replacement.doc, { scanLimit: 500, timeout: 20 }).map(
      ({ fromA, toA, fromB, toB }) => ({
        from: fromA,
        to: toA,
        insert: replacement.doc.slice(fromB, toB),
      }),
    ),
  );
  return { changes, effect: replacement.effect };
}
