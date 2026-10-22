import { expect, it } from 'vitest';
import { history, undo, undoDepth } from '@codemirror/commands';
import { Compartment, EditorState, Transaction } from '@codemirror/state';
import { externalSnapshot } from './external';
import { sourceDocument, sourceText } from './source';

it('maps reading selection through separate external edits and retains exact source separators', () => {
  const before = 'header\r\nkeep this selection\rfooter\n';
  const next = 'new header\rkeep this selection\r\nnew footer\r\n';
  const source = sourceDocument(before);
  const state = EditorState.create({
    doc: source.doc,
    selection: { anchor: 7, head: 26 },
    extensions: source.extensions,
  });
  const replacement = externalSnapshot(state, next);
  const updated = state.update({
    changes: replacement.changes,
    effects: replacement.effect,
  }).state;
  expect(
    updated.sliceDoc(updated.selection.main.from, updated.selection.main.to),
  ).toBe('keep this selection');
  expect(sourceText(updated)).toBe(next);
  const edited = updated.update({
    changes: { from: updated.doc.length, insert: 'last\n' },
  }).state;
  expect(sourceText(edited)).toBe(`${next}last\r`);
});

it('starts new local undo history after accepting an external snapshot', () => {
  const source = sourceDocument('first\r\nsecond\r\n');
  const localHistory = new Compartment();
  let state = EditorState.create({
    doc: source.doc,
    extensions: [source.extensions, localHistory.of(history())],
  });
  const editor = {
    get state() {
      return state;
    },
    dispatch(transaction: Transaction) {
      state = transaction.state;
    },
  };
  state = state.update({ changes: { from: 0, insert: 'saved local ' } }).state;
  expect(undoDepth(state)).toBe(1);
  const next = 'agent first\nsecond\r';
  const replacement = externalSnapshot(state, next);
  state = state.update({
    changes: replacement.changes,
    effects: [replacement.effect, localHistory.reconfigure([])],
    annotations: [
      Transaction.remote.of(true),
      Transaction.addToHistory.of(false),
    ],
  }).state;
  state = state.update({ effects: localHistory.reconfigure(history()) }).state;
  expect(undo(editor)).toBe(false);
  expect(sourceText(state)).toBe(next);
  state = state.update({ changes: { from: 0, insert: 'manual ' } }).state;
  expect(undo(editor)).toBe(true);
  expect(sourceText(state)).toBe(next);
});
