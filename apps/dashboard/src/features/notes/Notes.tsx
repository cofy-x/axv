import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import {
  Button,
  ButtonGroup,
  IconButton,
  PixelIcon,
  StatusBar,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { filesystemApi } from '../../filesystem/api';
import { isInTrash } from '../../filesystem/tree';
import { openEntry } from '../../filesystem/presentation';
import { newEntry } from '../../filesystem/commands';
import { useFilesystemData } from '../../filesystem/context';
import { nameDialog, runCommand } from '../../shared/desktop/dialogs';
import { useWindowCommands } from '../../shared/desktop/WindowCommandsContext';
import { desktopDatabase } from '../../shared/api/desktop';
import { useDraftNamespace } from '../../shared/drafts/context';
import { listDrafts } from '../../shared/drafts/storage';
import { downloadDraft } from '../../shared/drafts/download';
import { getEditorSession } from '../../shared/drafts/session';
import type { EditorSession } from '../../shared/drafts/session';
import type { DraftRecord } from '../../shared/drafts/storage';
import { isMarkdownFilename, MarkdownPreview } from '../../shared/markdown';
import { useNoteDocument } from './useNoteDocument';

export function Notes(props: AppWindowProps) {
  return props.window.params.entryId ? (
    <DocumentSession {...props} />
  ) : (
    <EditorLauncher {...props} />
  );
}

function EditorLauncher({ desktop }: AppWindowProps) {
  const data = useFilesystemData();
  const databaseFailure = useSyncExternalStore(
    desktopDatabase.subscribe,
    desktopDatabase.getSnapshot,
  );
  const { databaseId, clientId } = useDraftNamespace();
  const [drafts, setDrafts] = useState<DraftRecord[]>([]);
  const [error, setError] = useState('');
  useEffect(() => {
    let active = true;
    void listDrafts({ databaseId, clientId })
      .then((items) => {
        if (active) setDrafts(items);
      })
      .catch(() => {
        if (active)
          setError('Local draft recovery is unavailable in this browser.');
      });
    return () => {
      active = false;
    };
  }, [databaseId, clientId]);
  return (
    <div className="editor-launcher app-pad">
      <div className="panel-heading">
        <PixelIcon name="notes" />
        <div>
          <strong>Text Editor</strong>
          <p>Edit text or preview Markdown.</p>
        </div>
      </div>
      <Button
        primary
        disabled={!!databaseFailure}
        onClick={() =>
          void runCommand(desktop, async () => {
            desktopDatabase.assertActive();
            await newEntry(data, desktop, 'documents', 'file');
          })
        }
      >
        New Document
      </Button>
      {error && <p role="status">{error}</p>}
      {drafts.length > 0 && (
        <section aria-label="Recovered drafts">
          <h2>Recovered drafts</h2>
          <p>Stored in this browser. Not saved to the server.</p>
          <div className="editor-document-list">
            {drafts.map((draft) => (
              <button
                key={draft.key}
                onClick={() =>
                  desktop.openApp(
                    'notes',
                    { entryId: draft.entryId, draftId: draft.draftId },
                    { title: draft.name },
                  )
                }
              >
                <PixelIcon name="document" size={16} />
                <span>{draft.name}</span>
                <small>{new Date(draft.updatedAt).toLocaleString()}</small>
              </button>
            ))}
          </div>
        </section>
      )}
      <section aria-label="Text documents">
        <h2>Open a document</h2>
        <div className="editor-document-list">
          {data.entries
            .filter(
              (entry) => entry.editable && !isInTrash(entry, data.entries),
            )
            .map((entry) => (
              <button key={entry.id} onClick={() => openEntry(entry, desktop)}>
                <PixelIcon name="document" size={16} />
                <span>{entry.name}</span>
              </button>
            ))}
        </div>
      </section>
    </div>
  );
}

function DocumentSession(props: AppWindowProps) {
  const { databaseId, clientId } = useDraftNamespace();
  const { id, params } = props.window;
  const [session, setSession] = useState<EditorSession | null>(null);
  useEffect(() => {
    let active = true;
    void getEditorSession({ databaseId, clientId }, id, params.draftId).then(
      (value) => {
        if (active) setSession(value);
      },
    );
    return () => {
      active = false;
    };
  }, [databaseId, clientId, id, params.draftId]);
  return session ? (
    <DocumentEditor {...props} session={session} />
  ) : (
    <div className="app-pad">Opening your document…</div>
  );
}

function DocumentEditor({
  window,
  desktop,
  session,
  interactive = true,
  visible = true,
}: AppWindowProps & { session: EditorSession }) {
  const data = useFilesystemData();
  const databaseFailure = useSyncExternalStore(
    desktopDatabase.subscribe,
    desktopDatabase.getSnapshot,
  );
  const entryId = window.params.entryId!;
  const entry = data.entries.find((item) => item.id === entryId);
  const entryName = entry?.name || window.title.replace(/ \*$/, '');
  const markdown = isMarkdownFilename(entryName);
  const [view, setView] = useState<'edit' | 'preview'>('edit');
  const showPreview = markdown && view === 'preview';
  const editor = useRef<HTMLTextAreaElement>(null);
  const [composing, setComposing] = useState(false);
  const composingRef = useRef(false);
  const compositionOwner = useRef('');
  const compositionEnded = useRef(false);
  const compositionFrame = useRef<number | undefined>(undefined);
  const editorOwner = `${entryId}:${session.draftId}`;
  const inputState = useRef({ interactive, visible, showPreview, editorOwner });
  inputState.current = { interactive, visible, showPreview, editorOwner };
  const unavailable = !entry || isInTrash(entry, data.entries);
  const commands = useWindowCommands();
  const note = useNoteDocument(
    entryId,
    session.draftId,
    entryName,
    data.refresh,
    session.recoveryId,
  );
  const {
    text,
    status,
    loaded,
    error,
    conflict,
    dirty,
    saving,
    save,
    reload,
    getSnapshot,
    discard,
    flushDraft,
    metadataChanged,
  } = note;
  const mounted = useRef(false);
  const menuState = useRef({
    entryId,
    entryName,
    unavailable,
    visible,
    editorOwner,
    getSnapshot,
    saveCopy,
    loadSaved,
  });
  menuState.current = {
    entryId,
    entryName,
    unavailable,
    visible,
    editorOwner,
    getSnapshot,
    saveCopy,
    loadSaved,
  };

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      if (compositionFrame.current !== undefined)
        cancelAnimationFrame(compositionFrame.current);
    };
  }, []);

  const canSave =
    !databaseFailure &&
    !composing &&
    loaded &&
    !conflict &&
    dirty &&
    !saving &&
    !unavailable &&
    !note.recoveryAvailable &&
    !note.resolvingRecovery;
  const canDownload =
    loaded && !composing && !note.recoveryAvailable && !note.resolvingRecovery;
  const saveDocument = useCallback(async () => {
    const state = getSnapshot();
    if (
      !mounted.current ||
      composingRef.current ||
      desktopDatabase.getSnapshot() ||
      menuState.current.unavailable ||
      !state.loaded ||
      state.conflict ||
      !state.dirty ||
      state.saving ||
      state.recoveryAvailable ||
      state.resolvingRecovery
    )
      return false;
    return save();
  }, [getSnapshot, save]);

  useEffect(() => {
    desktop.setTitle(window.id, `${entryName}${dirty ? ' *' : ''}`);
  }, [entryName, dirty, desktop, window.id]);

  useEffect(
    () =>
      data.onMetadataChanged((updated, previousRevision) => {
        if (updated.id === entryId)
          metadataChanged(previousRevision, updated.revision);
      }),
    [data, entryId, metadataChanged],
  );

  useEffect(
    () =>
      desktop.beforeClose(window.id, async () => {
        if (composingRef.current) return false;
        if (!getSnapshot().dirty) return true;
        const protectedDraft = !!desktopDatabase.getSnapshot();
        const answer = await desktop.dialog({
          title: protectedDraft ? 'Keep your draft' : 'Save changes?',
          message: protectedDraft
            ? `“${entryName}” cannot be saved to this workspace. Download the draft or keep this window open before checking AXV in a new window.`
            : `Save changes to “${entryName}” before closing?`,
          buttons: protectedDraft
            ? [
                {
                  label: 'Cancel',
                  value: 'cancel',
                  cancel: true,
                  default: true,
                },
                { label: 'Download Draft', value: 'download' },
                { label: 'Discard', value: 'discard' },
              ]
            : [
                { label: 'Cancel', value: 'cancel', cancel: true },
                { label: 'Discard', value: 'discard' },
                { label: 'Save', value: 'save', default: true },
              ],
        });
        if (composingRef.current || !mounted.current) return false;
        if (answer?.button === 'download') {
          const state = getSnapshot();
          if (
            state.loaded &&
            !state.recoveryAvailable &&
            !state.resolvingRecovery
          )
            await runCommand(desktop, async () =>
              downloadDraft(entryName, state.text),
            );
          return false;
        }
        if (answer?.button === 'discard') {
          try {
            await discard();
            return true;
          } catch {
            return false;
          }
        }
        return answer?.button === 'save' ? saveDocument() : false;
      }),
    [desktop, window.id, entryName, saveDocument, getSnapshot, discard],
  );

  useEffect(() => {
    const prevent = (event: BeforeUnloadEvent) => {
      if (getSnapshot().dirty) {
        event.preventDefault();
        event.returnValue = '';
      }
    };
    const flush = () => {
      void flushDraft();
    };
    globalThis.addEventListener('beforeunload', prevent);
    globalThis.addEventListener('pagehide', flush);
    return () => {
      globalThis.removeEventListener('beforeunload', prevent);
      globalThis.removeEventListener('pagehide', flush);
    };
  }, [getSnapshot, flushDraft]);

  useEffect(
    () => commands.register(window.id, 'save', saveDocument, canSave),
    [commands, window.id, saveDocument, canSave],
  );

  function requireServerAction() {
    desktopDatabase.assertActive();
    const state = getSnapshot();
    if (
      !mounted.current ||
      composingRef.current ||
      menuState.current.editorOwner !== editorOwner ||
      !menuState.current.visible ||
      !state.loaded ||
      state.recoveryAvailable ||
      state.resolvingRecovery
    )
      throw new Error('This action is no longer available for this document.');
  }

  async function saveCopy() {
    requireServerAction();
    const name = await nameDialog(
      desktop,
      'Save Copy',
      entryName.replace(/(\.[^.]+)?$/, ' copy$1'),
    );
    if (!name) return;
    requireServerAction();
    const result = await filesystemApi.create(
      !unavailable && entry ? entry.parentId : 'documents',
      name,
      'file',
      getSnapshot().text,
    );
    desktopDatabase.assertActive();
    await data.refresh();
    desktopDatabase.assertActive();
    if (!mounted.current) return;
    openEntry(result.entry, desktop);
  }

  async function loadSaved() {
    requireServerAction();
    const answer = await desktop.dialog({
      title: 'Load the saved document?',
      message:
        'This discards the draft in this window and its local recovery copy. Use Save Copy first to keep it.',
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Load Saved', value: 'load', default: true },
      ],
    });
    if (answer?.button === 'load') {
      requireServerAction();
      await reload();
    }
  }

  function menuAction(
    kind: 'workflow' | 'copy' | 'download' | 'reload',
    work: (current: typeof menuState.current) => void | Promise<void>,
  ) {
    return () => {
      if (!mounted.current) return;
      void runCommand(desktop, async () => {
        const current = menuState.current;
        const state = current.getSnapshot();
        if (
          current.entryId !== entryId ||
          current.editorOwner !== editorOwner ||
          !current.visible ||
          composingRef.current ||
          !state.loaded ||
          ((kind === 'workflow' || kind === 'reload') && current.unavailable) ||
          (kind === 'download' &&
            (state.recoveryAvailable || state.resolvingRecovery)) ||
          (kind === 'reload' &&
            (state.saving ||
              state.recoveryAvailable ||
              state.resolvingRecovery))
        )
          throw new Error(
            'This action is no longer available for this document.',
          );
        if (kind !== 'download') desktopDatabase.assertActive();
        await work(current);
      });
    };
  }

  const downloadCurrent = menuAction('download', (current) =>
    downloadDraft(current.entryName, current.getSnapshot().text),
  );

  function finishComposition(value: string) {
    if (compositionOwner.current !== editorOwner || !compositionEnded.current)
      return;
    if (getSnapshot().text !== value) note.edit(value);
    if (compositionFrame.current !== undefined)
      cancelAnimationFrame(compositionFrame.current);
    compositionFrame.current = undefined;
    compositionOwner.current = '';
    compositionEnded.current = false;
    composingRef.current = false;
    setComposing(false);
  }

  return (
    <div
      className="notes-app"
      onKeyDown={(event) => {
        if (
          (event.metaKey || event.ctrlKey) &&
          event.key.toLowerCase() === 's'
        ) {
          event.preventDefault();
          event.stopPropagation();
          if (!interactive || event.nativeEvent.isComposing) return;
          // A new non-composing command follows the native commit even when
          // this engine delivers its final input before compositionend.
          if (composingRef.current && editor.current)
            finishComposition(editor.current.value);
          if (composingRef.current) return;
          void saveDocument();
        }
      }}
    >
      <div className="app-toolbar">
        <PixelIcon name="notes" size={16} />
        <span className="toolbar-caption">
          {markdown ? 'Markdown' : 'Plain Text'}
        </span>
        {markdown && (
          <ButtonGroup label="Document view">
            <Button
              aria-pressed={!showPreview}
              disabled={composing}
              onClick={() => {
                if (!interactive || composingRef.current) return;
                setView('edit');
                requestAnimationFrame(() => {
                  const current = inputState.current;
                  if (
                    mounted.current &&
                    current.interactive &&
                    current.visible &&
                    current.editorOwner === editorOwner &&
                    !current.showPreview
                  )
                    editor.current?.focus();
                });
              }}
            >
              Edit
            </Button>
            <Button
              aria-pressed={showPreview}
              disabled={!loaded || composing}
              onClick={() => {
                if (interactive && !composingRef.current) setView('preview');
              }}
            >
              Preview
            </Button>
          </ButtonGroup>
        )}
        <Button disabled={!canSave} onClick={() => void saveDocument()}>
          {saving ? 'Saving…' : 'Save'}
        </Button>
        <IconButton
          icon="more"
          label="More document actions"
          aria-haspopup="menu"
          onClick={(event) => {
            if (!interactive) return;
            const rect = event.currentTarget.getBoundingClientRect();
            desktop.contextMenu(
              [
                ...[
                  { task: 'attach', label: 'Attach' },
                  { task: 'export', label: 'Export' },
                  { task: 'details', label: 'Details' },
                ].map(({ task, label }) => ({
                  label,
                  disabled:
                    !!databaseFailure || !loaded || unavailable || composing,
                  action: menuAction('workflow', () => {
                    const workflowId = desktop.openApp('document-workflow', {
                      entryId,
                      task,
                    });
                    desktop.updateWindowState(workflowId, { task });
                  }),
                })),
                { separator: true },
                {
                  label: 'Download Draft',
                  description: 'Current text, including unsaved changes.',
                  disabled: !canDownload,
                  action: downloadCurrent,
                },
                {
                  label: 'Save Copy',
                  disabled:
                    !!databaseFailure ||
                    !loaded ||
                    composing ||
                    note.recoveryAvailable ||
                    note.resolvingRecovery,
                  action: menuAction('copy', (current) => current.saveCopy()),
                },
                {
                  label: 'Reload',
                  disabled:
                    !!databaseFailure ||
                    composing ||
                    !loaded ||
                    unavailable ||
                    saving ||
                    note.recoveryAvailable ||
                    note.resolvingRecovery,
                  action: menuAction('reload', (current) =>
                    current.loadSaved(),
                  ),
                },
              ],
              { x: rect.left, y: rect.bottom },
              { title: entryName, ownerWindowId: window.id },
            );
          }}
        />
      </div>
      {databaseFailure && (
        <div className="editor-recovery" role="status">
          <span>
            {loaded || note.recoveryAvailable
              ? 'Saving is unavailable in this window.'
              : 'This document is unavailable in this window.'}
          </span>
          {(loaded || note.recoveryAvailable) && (
            <div className="app-actions">
              <Button disabled={!canDownload} onClick={downloadCurrent}>
                Download Draft
              </Button>
            </div>
          )}
        </div>
      )}
      {unavailable && !databaseFailure && (
        <div className="app-error" role="status">
          <span>
            This document was moved to the Recycler or deleted. Your draft
            remains available; save a copy or download it to keep it.
          </span>
          {!conflict && (
            <div className="app-actions">
              <Button
                disabled={
                  !loaded ||
                  composing ||
                  note.recoveryAvailable ||
                  note.resolvingRecovery
                }
                onClick={() => void runCommand(desktop, saveCopy)}
              >
                Save Copy
              </Button>
              {!error && (
                <Button disabled={!canDownload} onClick={downloadCurrent}>
                  Download Draft
                </Button>
              )}
            </div>
          )}
        </div>
      )}
      {note.recoveryAvailable && (
        <div className="editor-recovery" role="alert">
          <strong>A local recovery copy is available.</strong>
          <p>
            {databaseFailure
              ? 'Restore the draft to edit or download it.'
              : 'Restore the draft, or choose Keep Saved to discard the recovery copy.'}
          </p>
          <div className="app-actions">
            <Button
              disabled={note.resolvingRecovery || composing}
              onClick={() => {
                if (!composingRef.current) note.restoreDraft();
              }}
            >
              Restore Draft
            </Button>
            {loaded && !databaseFailure && (
              <Button
                disabled={note.resolvingRecovery || composing}
                onClick={() => {
                  if (!composingRef.current && !desktopDatabase.getSnapshot())
                    void runCommand(desktop, note.keepSaved);
                }}
              >
                Keep Saved
              </Button>
            )}
            <Button
              disabled={note.resolvingRecovery || composing}
              onClick={() => void desktop.closeWindow(window.id)}
            >
              Decide Later
            </Button>
          </div>
        </div>
      )}
      {note.recovered && !databaseFailure && (
        <div className="editor-recovery" role="status">
          Recovered a local draft. It has not been saved to the server.
        </div>
      )}
      {loaded &&
        entry &&
        entry.revision !== note.revision &&
        !conflict &&
        !databaseFailure && (
          <div className="editor-recovery" role="status">
            The saved document changed elsewhere. Your text is unchanged; reload
            it or save a copy.
          </div>
        )}
      {error && !databaseFailure && (
        <div className="app-error" role="alert">
          <span>{error}</span>
          <div className="app-actions">
            {conflict ? (
              <>
                <Button
                  disabled={
                    unavailable ||
                    composing ||
                    note.recoveryAvailable ||
                    note.resolvingRecovery
                  }
                  onClick={() => void runCommand(desktop, loadSaved)}
                >
                  Load Saved
                </Button>
                <Button
                  disabled={
                    composing ||
                    note.recoveryAvailable ||
                    note.resolvingRecovery
                  }
                  onClick={() => void runCommand(desktop, saveCopy)}
                >
                  Save Copy
                </Button>
              </>
            ) : (
              <Button
                disabled={
                  composing ||
                  (unavailable && loaded) ||
                  note.recoveryAvailable ||
                  note.resolvingRecovery
                }
                onClick={() => {
                  if (composingRef.current || desktopDatabase.getSnapshot())
                    return;
                  if (loaded) void saveDocument();
                  else void note.load().catch(note.setError);
                }}
              >
                Retry
              </Button>
            )}
            {loaded && (
              <Button disabled={!canDownload} onClick={downloadCurrent}>
                Download Draft
              </Button>
            )}
          </div>
        </div>
      )}
      <div className="notes-document-surface">
        <textarea
          ref={editor}
          className={`notes-editor${showPreview ? ' is-preview-hidden' : ''}`}
          inert={showPreview || !interactive}
          aria-hidden={showPreview}
          aria-label="Document text"
          spellCheck={false}
          value={text}
          readOnly={!interactive && !composing}
          disabled={!loaded || note.recoveryAvailable || note.resolvingRecovery}
          onCompositionStart={() => {
            if (!interactive || showPreview) return;
            if (compositionFrame.current !== undefined)
              cancelAnimationFrame(compositionFrame.current);
            compositionOwner.current = editorOwner;
            compositionEnded.current = false;
            composingRef.current = true;
            setComposing(true);
          }}
          onCompositionEnd={(event) => {
            if (compositionOwner.current !== editorOwner) return;
            // The final native input can follow compositionend, even if a menu
            // or another window took focus while this composition was active.
            note.edit(event.currentTarget.value);
            compositionEnded.current = true;
            compositionFrame.current = requestAnimationFrame(() => {
              compositionFrame.current = requestAnimationFrame(() => {
                compositionFrame.current = undefined;
                compositionOwner.current = '';
                composingRef.current = false;
                setComposing(false);
              });
            });
          }}
          onInput={(event) => {
            const native = event.nativeEvent as InputEvent;
            if (
              compositionOwner.current !== editorOwner ||
              !compositionEnded.current ||
              native.isComposing ||
              ![
                'insertCompositionText',
                'insertFromComposition',
                'insertText',
              ].includes(native.inputType)
            )
              return;
            // Firefox can deliver this final input after compositionend has
            // already synchronized the value, so React need not emit onChange.
            finishComposition(event.currentTarget.value);
          }}
          onChange={(event) => {
            const native = event.nativeEvent as InputEvent;
            const authorizedComposition =
              compositionOwner.current === editorOwner &&
              (native.isComposing ||
                native.inputType === 'insertCompositionText' ||
                native.inputType === 'insertFromComposition' ||
                (compositionEnded.current &&
                  native.inputType === 'insertText'));
            if ((!interactive || showPreview) && !authorizedComposition) return;
            note.edit(event.target.value);
          }}
        />
        {showPreview && (
          <div
            className="notes-preview"
            role="region"
            aria-label="Markdown preview"
            tabIndex={0}
          >
            <MarkdownPreview content={text} />
          </div>
        )}
      </div>
      <div className="draft-status" role="status">
        {note.localStatus}
      </div>
      <StatusBar>
        <span data-testid="save-status">{status}</span>
        <span>
          {new TextEncoder().encode(text).byteLength.toLocaleString()} bytes
        </span>
      </StatusBar>
    </div>
  );
}
