import { describe, expect, it } from 'vitest';
import { history, redo, undo } from '@codemirror/commands';
import { EditorState } from '@codemirror/state';
import type { Transaction } from '@codemirror/state';
import { sourceDocument, sourceText } from './source';

function document(value: string) {
  const source = sourceDocument(value);
  let state = EditorState.create({
    doc: source.doc,
    extensions: [source.extensions, history()],
  });
  return {
    get state() {
      return state;
    },
    dispatch(transaction: Transaction) {
      state = transaction.state;
    },
  };
}

describe('source text and editor history', () => {
  it('retains mixed line endings and Unicode outside edited ranges', () => {
    const editor = document('α😀\r\nbeta\rgamma\n');
    expect(sourceText(editor.state)).toBe('α😀\r\nbeta\rgamma\n');
    editor.dispatch(
      editor.state.update({
        changes: [
          { from: 1, insert: '!' },
          { from: 4, to: 8, insert: 'B' },
        ],
      }),
    );
    expect(sourceText(editor.state)).toBe('α!😀\r\nB\rgamma\n');
    expect(undo(editor)).toBe(true);
    expect(sourceText(editor.state)).toBe('α😀\r\nbeta\rgamma\n');
    expect(redo(editor)).toBe(true);
    expect(sourceText(editor.state)).toBe('α!😀\r\nB\rgamma\n');
  });

  it('uses the document separator for new lines and restores exact bytes on undo', () => {
    const original = 'one\r\ntwo\rthree\n';
    const editor = document(original);
    editor.dispatch(
      editor.state.update({
        changes: { from: 2, to: 11, insert: 'X\nY\n' },
        userEvent: 'input.type',
      }),
    );
    expect(sourceText(editor.state)).toBe('onX\r\nY\r\nee\n');
    expect(undo(editor)).toBe(true);
    expect(sourceText(editor.state)).toBe(original);
    expect(redo(editor)).toBe(true);
    expect(sourceText(editor.state)).toBe('onX\r\nY\r\nee\n');
  });

  it('restores line separators across grouped edits and redo', () => {
    const original = 'a\r\nb\rc\n';
    const editor = document(original);
    editor.dispatch(
      editor.state.update({
        changes: { from: 0, to: 4, insert: 'x' },
        userEvent: 'input.type',
      }),
    );
    editor.dispatch(
      editor.state.update({
        changes: { from: 1, insert: '\ny' },
        userEvent: 'input.type',
      }),
    );
    expect(sourceText(editor.state)).toBe('x\r\nyc\n');
    expect(undo(editor)).toBe(true);
    expect(sourceText(editor.state)).toBe(original);
    expect(redo(editor)).toBe(true);
    expect(sourceText(editor.state)).toBe('x\r\nyc\n');
  });
});
