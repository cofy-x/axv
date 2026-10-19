import { useEffect, useRef, useState } from 'react';
import { Button, IconButton, PixelIcon, StatusBar } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { filesystemApi } from '../../filesystem/api';
import { isInTrash } from '../../filesystem/tree';
import { openEntry } from '../../filesystem/presentation';
import { newEntry } from '../../filesystem/commands';
import { useFilesystemData } from '../../filesystem/context';
import { nameDialog, runCommand } from '../../shared/desktop/dialogs';
import { useWindowCommands } from '../../shared/desktop/WindowCommandsContext';
import { useDraftNamespace } from '../../shared/drafts/context';
import { listDrafts } from '../../shared/drafts/storage';
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
        onClick={() =>
          void runCommand(desktop, () =>
            newEntry(data, desktop, 'documents', 'file'),
          )
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
}: AppWindowProps & { session: EditorSession }) {
  const data = useFilesystemData();
  const entryId = window.params.entryId!;
  const entry = data.entries.find((item) => item.id === entryId);
  const entryName = entry?.name || window.title.replace(/ \*$/, '');
  const markdown = isMarkdownFilename(entryName);
  const [view, setView] = useState<'edit' | 'preview'>('edit');
  const showPreview = markdown && view === 'preview';
  const editor = useRef<HTMLTextAreaElement>(null);
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
    unavailable,
    getSnapshot,
    saveCopy,
    loadSaved,
  });
  menuState.current = {
    entryId,
    unavailable,
    getSnapshot,
    saveCopy,
    loadSaved,
  };

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

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
        if (!getSnapshot().dirty) return true;
        const answer = await desktop.dialog({
          title: 'Save changes?',
          message: `Save changes to “${entryName}” before closing?`,
          buttons: [
            { label: 'Cancel', value: 'cancel', cancel: true },
            { label: 'Discard', value: 'discard' },
            { label: 'Save', value: 'save', default: true },
          ],
        });
        if (answer?.button === 'discard') {
          try {
            await discard();
            return true;
          } catch {
            return false;
          }
        }
        return answer?.button === 'save' ? save() : false;
      }),
    [desktop, window.id, entryName, save, getSnapshot, discard],
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
    () => commands.register(window.id, 'save', save),
    [commands, window.id, save],
  );

  async function saveCopy() {
    const name = await nameDialog(
      desktop,
      'Save Copy',
      entryName.replace(/(\.[^.]+)?$/, ' copy$1'),
    );
    if (!name) return;
    const result = await filesystemApi.create(
      !unavailable && entry ? entry.parentId : 'documents',
      name,
      'file',
      getSnapshot().text,
    );
    await data.refresh();
    openEntry(result.entry, desktop);
  }

  async function loadSaved() {
    const answer = await desktop.dialog({
      title: 'Load the saved document?',
      message:
        'This discards the draft in this window and its local recovery copy. Use Save Copy first to keep it.',
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Load Saved', value: 'load', default: true },
      ],
    });
    if (answer?.button === 'load') await reload();
  }

  function menuAction(
    kind: 'workflow' | 'copy' | 'reload',
    work: (current: typeof menuState.current) => void | Promise<void>,
  ) {
    return () => {
      if (!mounted.current) return;
      void runCommand(desktop, async () => {
        const current = menuState.current;
        const state = current.getSnapshot();
        if (
          current.entryId !== entryId ||
          !state.loaded ||
          (kind !== 'copy' && current.unavailable) ||
          (kind === 'reload' &&
            (state.saving ||
              state.recoveryAvailable ||
              state.resolvingRecovery))
        )
          throw new Error(
            'This action is no longer available for this document.',
          );
        await work(current);
      });
    };
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
          if (!unavailable) void save();
        }
      }}
    >
      <div className="app-toolbar">
        <PixelIcon name="notes" size={16} />
        <span className="toolbar-caption">
          {markdown ? 'Markdown' : 'Plain Text'}
        </span>
        {markdown && (
          <span
            className="notes-view-controls"
            role="group"
            aria-label="Document view"
          >
            <Button
              aria-pressed={!showPreview}
              onClick={() => {
                setView('edit');
                requestAnimationFrame(() => editor.current?.focus());
              }}
            >
              Edit
            </Button>
            <Button
              aria-pressed={showPreview}
              disabled={!loaded}
              onClick={() => setView('preview')}
            >
              Preview
            </Button>
          </span>
        )}
        <Button
          disabled={
            !loaded ||
            conflict ||
            !dirty ||
            saving ||
            unavailable ||
            note.recoveryAvailable ||
            note.resolvingRecovery
          }
          onClick={() => void save()}
        >
          {saving ? 'Saving…' : 'Save'}
        </Button>
        <IconButton
          icon="more"
          label="More document actions"
          aria-haspopup="menu"
          onClick={(event) => {
            const rect = event.currentTarget.getBoundingClientRect();
            desktop.contextMenu(
              [
                ...[
                  { task: 'attach', label: 'Attach' },
                  { task: 'export', label: 'Export' },
                  { task: 'details', label: 'Details' },
                ].map(({ task, label }) => ({
                  label,
                  disabled: !loaded || unavailable,
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
                  label: 'Save Copy',
                  disabled: !loaded,
                  action: menuAction('copy', (current) => current.saveCopy()),
                },
                {
                  label: 'Reload',
                  disabled:
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
              { title: entryName },
            );
          }}
        />
      </div>
      {unavailable && (
        <div className="app-error" role="status">
          This document was moved to the Recycler or deleted. Your draft remains
          available; use Save Copy to keep it.
        </div>
      )}
      {note.recoveryAvailable && (
        <div className="editor-recovery" role="alert">
          <strong>A local recovery copy is available.</strong>
          <p>
            Restore the draft, or choose Keep Saved to discard the recovery
            copy.
          </p>
          <div className="app-actions">
            <Button
              disabled={note.resolvingRecovery}
              onClick={() => note.restoreDraft()}
            >
              Restore Draft
            </Button>
            {loaded && (
              <Button
                disabled={note.resolvingRecovery}
                onClick={() => void runCommand(desktop, note.keepSaved)}
              >
                Keep Saved
              </Button>
            )}
            <Button
              disabled={note.resolvingRecovery}
              onClick={() => void desktop.closeWindow(window.id)}
            >
              Decide Later
            </Button>
          </div>
        </div>
      )}
      {note.recovered && (
        <div className="editor-recovery" role="status">
          Recovered a local draft. It has not been saved to the server.
        </div>
      )}
      {loaded && entry && entry.revision !== note.revision && !conflict && (
        <div className="editor-recovery" role="status">
          The saved document changed elsewhere. Your text is unchanged; reload
          it or save a copy.
        </div>
      )}
      {error && (
        <div className="app-error" role="alert">
          <span>{error}</span>
          <div className="app-actions">
            {conflict ? (
              <>
                <Button
                  disabled={unavailable}
                  onClick={() => void runCommand(desktop, loadSaved)}
                >
                  Load Saved
                </Button>
                <Button onClick={() => void runCommand(desktop, saveCopy)}>
                  Save Copy
                </Button>
              </>
            ) : (
              <Button
                disabled={unavailable && loaded}
                onClick={() => {
                  if (loaded) void save();
                  else void note.load().catch(note.setError);
                }}
              >
                Retry
              </Button>
            )}
          </div>
        </div>
      )}
      <div className="notes-document-surface">
        <textarea
          ref={editor}
          className={`notes-editor${showPreview ? ' is-preview-hidden' : ''}`}
          inert={showPreview}
          aria-hidden={showPreview}
          aria-label="Document text"
          spellCheck={false}
          value={text}
          disabled={!loaded || note.recoveryAvailable || note.resolvingRecovery}
          onChange={(event) => note.edit(event.target.value)}
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
