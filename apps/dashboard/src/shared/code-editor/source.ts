import { invertedEffects } from '@codemirror/commands';
import { StateEffect, StateField } from '@codemirror/state';
import type { EditorState, Transaction } from '@codemirror/state';

const endings = ['\n', '\r\n', '\r'] as const;
const restoreEndings = StateEffect.define<string>();
const replaceEndings = StateEffect.define<LineEndings>();

interface LineEndings {
  codes: string;
  preferred: string;
}

function changedEndings(value: LineEndings, transaction: Transaction) {
  if (!transaction.docChanged) return value;
  const parts: string[] = [];
  let cursor = 0;
  transaction.changes.iterChanges((from, to, _from, _to, inserted) => {
    const start = transaction.startState.doc.lineAt(from).number - 1;
    const end = transaction.startState.doc.lineAt(to).number - 1;
    parts.push(value.codes.slice(cursor, start));
    parts.push(value.preferred.repeat(inserted.lines - 1));
    cursor = end;
  });
  parts.push(value.codes.slice(cursor));
  return { ...value, codes: parts.join('') };
}

const lineEndings = StateField.define<LineEndings>({
  create: () => ({ codes: '', preferred: '0' }),
  update(value, transaction) {
    let next = changedEndings(value, transaction);
    for (const effect of transaction.effects)
      if (effect.is(restoreEndings)) next = { ...next, codes: effect.value };
      else if (effect.is(replaceEndings)) next = effect.value;
    return next;
  },
});

// CodeMirror uses logical newlines. Retain each original separator separately
// so syntax rendering, edits and undo never rewrite unrelated file bytes.
function source(value: string) {
  const separators = value.match(/\r\n?|\n/g) || [];
  const codes = separators
    .map((separator) => endings.indexOf(separator as (typeof endings)[number]))
    .join('');
  return {
    doc: value.replace(/\r\n?/g, '\n'),
    endings: { codes, preferred: codes[0] || '0' },
  };
}

export function sourceReplacement(value: string) {
  const next = source(value);
  return { doc: next.doc, effect: replaceEndings.of(next.endings) };
}

export function sourceDocument(value: string) {
  const next = source(value);
  return {
    doc: next.doc,
    extensions: [
      lineEndings.init(() => next.endings),
      invertedEffects.of((transaction) => {
        const before = transaction.startState.field(lineEndings).codes;
        return transaction.docChanged &&
          before !== transaction.state.field(lineEndings).codes
          ? [restoreEndings.of(before)]
          : [];
      }),
    ],
  };
}

export function sourceText(state: EditorState) {
  const text = state.doc.toString();
  const { codes } = state.field(lineEndings);
  if (!/[12]/.test(codes)) return text;
  let index = 0;
  return text.replace(/\n/g, () => endings[Number(codes[index++])] || '\n');
}
