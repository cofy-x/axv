import {
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { Compartment, EditorState, Transaction } from '@codemirror/state';
import {
  EditorView,
  drawSelection,
  highlightActiveLine,
  highlightActiveLineGutter,
  highlightSpecialChars,
  keymap,
  lineNumbers,
} from '@codemirror/view';
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
import {
  bracketMatching,
  defaultHighlightStyle,
  LanguageDescription,
  syntaxHighlighting,
} from '@codemirror/language';
import { languages } from '@codemirror/language-data';
import { search, searchKeymap } from '@codemirror/search';
import type { Extension } from '@codemirror/state';
import { Button } from '@cofy-x/axv-nextstep';
import { PlainCodeEditor } from './PlainCodeEditor';
import { sourceDocument, sourceText } from './source';
import { externalSnapshot } from './external';
import type { CodeEditorProps } from './types';

const theme = EditorView.theme({
  '&': {
    height: '100%',
    color: 'var(--ns-text)',
    backgroundColor: '#fff',
    fontSize: '13px',
  },
  '&.cm-focused': { outline: '2px solid #205a8a', outlineOffset: '-2px' },
  '.cm-scroller': {
    fontFamily: 'var(--ns-mono)',
    lineHeight: '1.5',
    overflow: 'auto',
  },
  '.cm-content': { padding: '6px 0', caretColor: 'var(--ns-text)' },
  '.cm-line': { padding: '0 8px' },
  '.cm-gutters': {
    backgroundColor: '#f1f1ed',
    color: '#666',
    borderRight: '1px solid #ccc',
  },
  '.cm-lineNumbers .cm-gutterElement': { padding: '0 6px', minWidth: '3ch' },
  '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: '#f5f3e9' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection':
    { backgroundColor: '#c4d8eb' },
});

function access(props: CodeEditorProps) {
  return [
    EditorState.readOnly.of(!!props.disabled || !!props.readOnly),
    EditorView.editable.of(!props.disabled && !props.readOnly),
    EditorView.contentAttributes.of({
      'aria-label': props.label,
      'aria-multiline': 'true',
      'aria-readonly': String(!!props.readOnly),
      'aria-disabled': String(!!props.disabled),
      tabindex: props.disabled ? '-1' : '0',
      spellcheck: 'false',
      autocapitalize: 'off',
      autocorrect: 'off',
    }),
  ];
}

export default function HighlightedCodeEditor(props: CodeEditorProps) {
  const { ref, value, documentId, filename, label, disabled, readOnly } = props;
  const mount = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const latest = useRef(props);
  const current = useRef({ documentId, value });
  const language = useRef(new Compartment());
  const permissions = useRef(new Compartment());
  const undoHistory = useRef(new Compartment());
  const composing = useRef(false);
  const admittedComposition = useRef<string | null>(null);
  const deferredAccess = useRef(false);
  const compositionEvents = useRef<{
    reset: () => void;
    sync: (editor: EditorView) => void;
  } | null>(null);
  const loadedLanguage = useRef<{
    filename: string;
    extension: Extension;
  } | null>(null);
  const createState = useRef<
    ((text: string, filename: string, documentId: string) => EditorState) | null
  >(null);
  const [languageError, setLanguageError] = useState('');
  const [retry, setRetry] = useState(0);
  const [failed, setFailed] = useState(false);
  const [accessVersion, setAccessVersion] = useState(0);

  useLayoutEffect(() => {
    latest.current = props;
  });
  useImperativeHandle(
    ref,
    () => ({
      focus: () => {
        if (!latest.current.disabled) view.current?.focus();
      },
      blur: () => view.current?.contentDOM.blur(),
      isComposing: () => view.current?.compositionStarted ?? false,
    }),
    [],
  );

  useLayoutEffect(() => {
    if (!mount.current) return;
    let compositionFrame: number | undefined;
    let editContext: EventTarget | null = null;
    const reportComposition = (
      active: boolean,
      owner = current.current.documentId,
    ) => {
      if (active === composing.current) return;
      composing.current = active;
      if (active) {
        const currentProps = latest.current;
        admittedComposition.current =
          currentProps.documentId === owner &&
          !currentProps.disabled &&
          !currentProps.readOnly
            ? owner
            : null;
      } else {
        admittedComposition.current = null;
        if (deferredAccess.current) setAccessVersion((value) => value + 1);
      }
      latest.current.onCompositionChange?.(active);
    };
    const settleComposition = (editor: EditorView) => {
      if (compositionFrame !== undefined)
        cancelAnimationFrame(compositionFrame);
      // Allow the browser's final input and CodeMirror's DOM observer to settle
      // before the owner may accept another external snapshot.
      compositionFrame = requestAnimationFrame(() => {
        compositionFrame = requestAnimationFrame(() => {
          compositionFrame = undefined;
          if (view.current !== editor) return;
          editor.requestMeasure({
            key: reportComposition,
            read: () => editor.compositionStarted,
            write: (active) => {
              if (!active) reportComposition(false);
            },
          });
        });
      });
    };
    const contextStarted = () => reportComposition(true);
    const contextEnded = () => {
      if (view.current) settleComposition(view.current);
    };
    const removeContextEvents = () => {
      editContext?.removeEventListener('compositionstart', contextStarted);
      editContext?.removeEventListener('compositionend', contextEnded);
      editContext = null;
    };
    const syncContextEvents = (editor: EditorView) => {
      // EditContext composition events do not bubble through contentDOM.
      const next = (
        editor.contentDOM as HTMLElement & {
          editContext?: EventTarget | null;
        }
      ).editContext;
      if (next === editContext) return;
      removeContextEvents();
      editContext = next || null;
      editContext?.addEventListener('compositionstart', contextStarted);
      editContext?.addEventListener('compositionend', contextEnded);
    };
    const resetComposition = () => {
      if (compositionFrame !== undefined)
        cancelAnimationFrame(compositionFrame);
      compositionFrame = undefined;
      removeContextEvents();
      reportComposition(false);
    };
    compositionEvents.current = {
      reset: resetComposition,
      sync: syncContextEvents,
    };
    const build = (text: string, name: string, owner: string) => {
      const source = sourceDocument(text);
      return EditorState.create({
        doc: source.doc,
        extensions: [
          source.extensions,
          undoHistory.current.of(history()),
          lineNumbers(),
          highlightSpecialChars(),
          drawSelection(),
          highlightActiveLine(),
          highlightActiveLineGutter(),
          bracketMatching(),
          syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
          EditorView.lineWrapping,
          theme,
          search({ top: true }),
          language.current.of(
            loadedLanguage.current?.filename === name
              ? loadedLanguage.current.extension
              : [],
          ),
          permissions.current.of(access(latest.current)),
          EditorView.domEventObservers({
            compositionstart: () => reportComposition(true, owner),
            compositionend: (_event, editor) => settleComposition(editor),
            blur: (_event, editor) => {
              if (composing.current) settleComposition(editor);
            },
          }),
          keymap.of([
            {
              key: 'Mod-s',
              run: () => {
                const next = latest.current;
                if (!next.onSave) return false;
                if (!next.disabled && !next.readOnly) next.onSave();
                return true;
              },
            },
            ...defaultKeymap,
            ...historyKeymap,
            ...searchKeymap,
          ]),
          EditorView.updateListener.of((update) => {
            syncContextEvents(update.view);
            if (update.view.compositionStarted) reportComposition(true, owner);
            else if (composing.current) settleComposition(update.view);
            const composition = admittedComposition.current === owner;
            if (
              !update.docChanged ||
              update.transactions.some((transaction) =>
                transaction.annotation(Transaction.remote),
              ) ||
              latest.current.documentId !== owner ||
              update.startState.readOnly ||
              (!composition &&
                (latest.current.disabled || latest.current.readOnly))
            )
              return;
            const text = sourceText(update.state);
            current.current = {
              documentId: owner,
              value: text,
            };
            latest.current.onChange?.(text, {
              documentId: owner,
              source: composition ? 'composition' : 'input',
            });
          }),
        ],
      });
    };
    createState.current = build;
    try {
      const initial = latest.current;
      const editor = new EditorView({
        state: build(initial.value, initial.filename, initial.documentId),
        parent: mount.current,
      });
      view.current = editor;
      current.current = {
        documentId: initial.documentId,
        value: initial.value,
      };
      syncContextEvents(editor);
      return () => {
        resetComposition();
        compositionEvents.current = null;
        view.current = null;
        createState.current = null;
        editor.destroy();
      };
    } catch {
      setFailed(true);
    }
  }, []);

  useLayoutEffect(() => {
    const editor = view.current;
    if (!editor || !createState.current) return;
    if (
      current.current.documentId !== documentId ||
      current.current.value !== value
    ) {
      const sameDocument = current.current.documentId === documentId;
      current.current = { documentId, value };
      if (!sameDocument) {
        // A native composition belongs to the focused document. Reusing its
        // DOM with another state must not route a pending commit into that file.
        editor.contentDOM.blur();
        const selection = editor.contentDOM.ownerDocument.getSelection();
        if (
          selection?.anchorNode &&
          editor.contentDOM.contains(selection.anchorNode)
        )
          selection.removeAllRanges();
        compositionEvents.current?.reset();
        editor.setState(createState.current(value, filename, documentId));
        compositionEvents.current?.sync(editor);
      } else {
        const replacement = externalSnapshot(editor.state, value);
        const scroll = editor.scrollSnapshot().map(replacement.changes);
        editor.dispatch({
          changes: replacement.changes,
          effects: [
            replacement.effect,
            undoHistory.current.reconfigure([]),
            ...(scroll ? [scroll] : []),
          ],
          annotations: [
            Transaction.remote.of(true),
            Transaction.addToHistory.of(false),
          ],
        });
        editor.dispatch({
          effects: undoHistory.current.reconfigure(history()),
        });
      }
    }
  }, [value, documentId, filename]);

  useLayoutEffect(() => {
    const editor = view.current;
    if (!editor) return;
    const pendingComposition =
      composing.current && admittedComposition.current === documentId;
    // Stop new focus/input immediately, retaining the already admitted native
    // composition until its final DOM/EditContext transaction has settled.
    editor.contentDOM.inert = !!disabled || (!!readOnly && pendingComposition);
    if ((disabled || readOnly) && pendingComposition) {
      deferredAccess.current = true;
      editor.contentDOM.setAttribute('aria-disabled', String(!!disabled));
      editor.contentDOM.setAttribute('aria-readonly', 'true');
      editor.contentDOM.tabIndex = -1;
      editor.contentDOM.blur();
      return;
    }
    deferredAccess.current = false;
    editor.dispatch({
      effects: permissions.current.reconfigure(access(latest.current)),
    });
    // A deferred transition changed these attributes outside CodeMirror's
    // attribute diff; restore them even when the resulting facet is unchanged.
    editor.contentDOM.setAttribute('aria-disabled', String(!!disabled));
    editor.contentDOM.setAttribute('aria-readonly', String(!!readOnly));
    editor.contentDOM.tabIndex = disabled ? -1 : 0;
    compositionEvents.current?.sync(editor);
    if (disabled) editor.contentDOM.blur();
    editor.requestMeasure();
  }, [disabled, readOnly, label, documentId, accessVersion]);

  useEffect(() => {
    const editor = view.current;
    if (!editor) return;
    let cancelled = false;
    const description = LanguageDescription.matchFilename(languages, filename);
    loadedLanguage.current = null;
    editor.dispatch({ effects: language.current.reconfigure([]) });
    setLanguageError('');
    if (description) {
      void description
        .load()
        .then((extension) => {
          if (
            cancelled ||
            view.current !== editor ||
            latest.current.filename !== filename
          )
            return;
          loadedLanguage.current = { filename, extension };
          editor.dispatch({ effects: language.current.reconfigure(extension) });
        })
        .catch(() => {
          if (
            !cancelled &&
            view.current === editor &&
            latest.current.filename === filename
          )
            setLanguageError(
              `${description.name} highlighting unavailable. Text remains available.`,
            );
        });
    }
    return () => {
      cancelled = true;
    };
  }, [filename, retry]);

  if (failed) return <PlainCodeEditor {...props} />;
  return (
    <div className="code-editor">
      {languageError && (
        <div className="code-editor-error" role="alert">
          <span>{languageError}</span>
          <Button
            disabled={disabled}
            onClick={() => setRetry((value) => value + 1)}
          >
            Retry
          </Button>
        </div>
      )}
      <div ref={mount} className="code-editor-surface" />
    </div>
  );
}
