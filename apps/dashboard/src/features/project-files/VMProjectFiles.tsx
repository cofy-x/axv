import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Button,
  IconButton,
  PixelIcon,
  StatusBar,
  TextArea,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useResource } from '../../shared/api/useResource';
import { useDraftNamespace } from '../../shared/drafts/context';
import { nameDialog } from '../../shared/desktop/dialogs';
import { vmProjectsAPI } from './vm-api';
import type { VMProjectBinding } from './vm-api';

interface Editor {
  path: string;
  text: string;
  saved: string;
  sha256: string;
}

interface Recovery {
  binding: VMProjectBinding;
  editor: Editor;
}

const message = (error: unknown) =>
  error instanceof Error ? error.message : String(error);

export function VMProjectFiles({
  window,
  desktop,
  interactive,
}: AppWindowProps) {
  const id = window.params.vmProjectId!;
  const hostId = window.params.hostId || '';
  const namespace = useDraftNamespace();
  const recoveryKey = JSON.stringify([
    'axv-vm-project-draft',
    namespace.databaseId,
    namespace.clientId,
    hostId,
    id,
  ]);
  const [recovery] = useState(() => {
    try {
      const raw = sessionStorage.getItem(recoveryKey);
      const value = raw ? (JSON.parse(raw) as Recovery) : null;
      if (
        raw !== null &&
        (!value ||
          typeof value !== 'object' ||
          !value.binding ||
          !value.editor ||
          value.binding.hostId !== hostId ||
          value.binding.projectId !== id ||
          typeof value.binding.volumeId !== 'string' ||
          typeof value.binding.machineId !== 'string' ||
          typeof value.binding.bootId !== 'string' ||
          typeof value.binding.instanceId !== 'string' ||
          !Number.isSafeInteger(value.binding.projectRevision) ||
          !Number.isSafeInteger(value.binding.volumeRevision) ||
          typeof value.editor.text !== 'string' ||
          typeof value.editor.saved !== 'string' ||
          typeof value.editor.sha256 !== 'string' ||
          typeof value.editor.path !== 'string')
      )
        throw new Error(
          'The VM project recovery record is invalid. Keep this tab open and copy your draft before clearing browser data.',
        );
      return { value, error: '' };
    } catch (error) {
      return { value: null, error: message(error) };
    }
  });
  const [pendingRecovery, setPendingRecovery] = useState(recovery.value);
  const [blockedRecovery, setBlockedRecovery] = useState(!!recovery.error);
  const [draftError, setDraftError] = useState(recovery.error);
  const [reconnect, setReconnect] = useState(0);
  const [originalBinding, setOriginalBinding] =
    useState<VMProjectBinding | null>(null);
  const [editor, setEditor] = useState<Editor | null>(null);
  const [saving, setSaving] = useState(false);
  const [reading, setReading] = useState(false);
  const [readRequest, setReadRequest] = useState(0);
  const [refresh, setRefresh] = useState(0);
  const [error, setError] = useState('');
  const [feedback, setFeedback] = useState('');
  const folder =
    typeof window.viewState.folder === 'string' ? window.viewState.folder : '';
  const file =
    typeof window.viewState.file === 'string' ? window.viewState.file : '';
  const cursor =
    typeof window.viewState.cursor === 'string' ? window.viewState.cursor : '';
  const isReading = window.viewState.reading !== false && !!file;
  const loadBinding = useCallback(() => {
    if (!hostId)
      return Promise.reject(
        new Error(
          'This VM project has no recorded host identity. Open it from Projects.',
        ),
      );
    return vmProjectsAPI.binding(id, hostId);
  }, [id, hostId]);
  const connection = useResource(`${hostId}:${id}:${reconnect}`, loadBinding);
  const binding = originalBinding || connection.data?.binding || null;
  const loadDirectory = useCallback(
    () =>
      binding && !pendingRecovery
        ? vmProjectsAPI.files(id, binding, folder, cursor)
        : Promise.resolve(null),
    [id, binding, folder, cursor, pendingRecovery],
  );
  const listing = useResource(
    `${id}:${folder}:${cursor}:${refresh}:${JSON.stringify(binding)}:${!!pendingRecovery}`,
    loadDirectory,
  );
  const dirty = !!editor && editor.text !== editor.saved;
  const latest = useRef({
    editor,
    binding,
    saving,
    dirty,
    pendingRecovery,
    blockedRecovery,
  });
  latest.current = {
    editor,
    binding,
    saving,
    dirty,
    pendingRecovery,
    blockedRecovery,
  };
  const mounted = useRef(false);
  const savingRef = useRef(false);
  const navigationGeneration = useRef(0);
  const directory = useRef<HTMLElement>(null);
  const preview = useRef<HTMLElement>(null);
  const editorInput = useRef<HTMLTextAreaElement>(null);
  const returnToDirectory = useRef(false);

  const persist = useCallback(
    (next: Editor | null, nextBinding = latest.current.binding) => {
      try {
        if (next && nextBinding && next.text !== next.saved)
          sessionStorage.setItem(
            recoveryKey,
            JSON.stringify({ binding: nextBinding, editor: next }),
          );
        else sessionStorage.removeItem(recoveryKey);
        setDraftError('');
        return true;
      } catch {
        setDraftError(
          'Local draft recovery is unavailable. Keep this tab open and copy unsaved text before leaving.',
        );
        return false;
      }
    },
    [recoveryKey],
  );

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  useEffect(() => {
    if (
      !binding ||
      !file ||
      pendingRecovery ||
      latest.current.editor?.path === file
    )
      return;
    let current = true;
    setReading(true);
    setError('');
    void vmProjectsAPI
      .read(id, binding, file)
      .then(
        (value) => {
          if (current) {
            const next = {
              path: value.path,
              text: value.content,
              saved: value.content,
              sha256: value.sha256,
            };
            setEditor(next);
          }
        },
        (error: unknown) => {
          if (current) setError(message(error));
        },
      )
      .finally(() => {
        if (current) setReading(false);
      });
    return () => {
      current = false;
    };
  }, [id, binding, file, pendingRecovery, readRequest]);

  useEffect(() => {
    const prevent = (event: BeforeUnloadEvent) => {
      if (
        latest.current.dirty ||
        latest.current.saving ||
        latest.current.pendingRecovery ||
        latest.current.blockedRecovery
      ) {
        event.preventDefault();
        event.returnValue = '';
      }
    };
    globalThis.addEventListener('beforeunload', prevent);
    return () => globalThis.removeEventListener('beforeunload', prevent);
  }, []);

  useEffect(
    () =>
      desktop.beforeClose(window.id, async () => {
        if (latest.current.saving) return false;
        if (!latest.current.dirty && !latest.current.pendingRecovery)
          return true;
        const answer = await desktop.dialog({
          title: 'Close VM project?',
          message:
            'Unsaved changes remain in this tab’s recovery storage. Discard removes that recovery copy.',
          buttons: [
            { label: 'Cancel', value: 'cancel', cancel: true },
            { label: 'Keep Draft', value: 'keep', default: true },
            { label: 'Discard', value: 'discard' },
          ],
        });
        if (answer?.button === 'keep')
          return latest.current.editor ? persist(latest.current.editor) : true;
        return answer?.button === 'discard' && persist(null);
      }),
    [desktop, window.id, persist],
  );

  useEffect(() => {
    if (!interactive) {
      editorInput.current?.blur();
      return;
    }
    if (isReading && directory.current?.getClientRects().length === 0)
      preview.current?.focus({ preventScroll: true });
    else if (!isReading && returnToDirectory.current) {
      returnToDirectory.current = false;
      const selected = directory.current?.querySelector<HTMLButtonElement>(
        '[aria-current="true"]',
      );
      (selected || directory.current)?.focus({ preventScroll: true });
    }
  }, [interactive, file, isReading]);

  useEffect(() => {
    const surface = directory.current?.parentElement;
    if (!interactive || !surface) return;
    let compact = directory.current?.getClientRects().length === 0;
    const observer = new ResizeObserver(() => {
      const next = directory.current?.getClientRects().length === 0;
      if (next && !compact && isReading)
        preview.current?.focus({ preventScroll: true });
      compact = next;
    });
    observer.observe(surface);
    return () => observer.disconnect();
  }, [interactive, isReading]);

  async function discardBefore(action: string) {
    if (savingRef.current) return false;
    if (!latest.current.dirty && !latest.current.pendingRecovery) return true;
    const answer = await desktop.dialog({
      title: 'Discard unsaved changes?',
      message: `${action} discards the current draft and its recovery copy.`,
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Discard', value: 'discard', default: true },
      ],
    });
    if (answer?.button !== 'discard' || !persist(null)) return false;
    setPendingRecovery(null);
    return true;
  }

  async function navigate(path: string, nextFile = '') {
    if (!interactive) return;
    if (
      path === folder &&
      nextFile === file &&
      latest.current.editor?.path === nextFile
    ) {
      navigationGeneration.current++;
      desktop.updateWindowState(window.id, { reading: true });
      return;
    }
    if (!(await discardBefore('Opening another location'))) return;
    navigationGeneration.current++;
    setEditor(null);
    setReadRequest((value) => value + 1);
    setError('');
    setFeedback('');
    desktop.updateWindowState(window.id, {
      folder: path,
      file: nextFile,
      cursor: '',
      reading: !!nextFile,
    });
  }

  async function save() {
    const snapshot = latest.current;
    if (
      !interactive ||
      !snapshot.editor ||
      !snapshot.binding ||
      blockedRecovery ||
      savingRef.current ||
      !snapshot.dirty
    )
      return;
    savingRef.current = true;
    setSaving(true);
    setError('');
    setFeedback('');
    try {
      const value = await vmProjectsAPI.write(
        id,
        snapshot.binding,
        snapshot.editor.path,
        snapshot.editor.text,
        snapshot.editor.sha256,
      );
      if (!mounted.current) return;
      const current = latest.current.editor!;
      const next = {
        ...current,
        saved: snapshot.editor.text,
        sha256: value.sha256,
      };
      setEditor(next);
      latest.current = {
        ...latest.current,
        editor: next,
        dirty: next.text !== next.saved,
      };
      persist(next, snapshot.binding);
      setFeedback(
        next.text === next.saved
          ? 'Saved in VM.'
          : 'Snapshot saved in VM. Newer typing is unsaved.',
      );
      setRefresh((value) => value + 1);
    } catch (error) {
      if (mounted.current)
        setError(
          `${message(error)} Your draft is preserved. Reload or reconnect requires discarding it explicitly.`,
        );
    } finally {
      savingRef.current = false;
      if (mounted.current) setSaving(false);
    }
  }

  async function reconnectProject() {
    if (!interactive || !(await discardBefore('Reconnecting'))) return;
    navigationGeneration.current++;
    setEditor(null);
    setOriginalBinding(null);
    setError('');
    setFeedback('');
    setReconnect((value) => value + 1);
  }

  async function create(kind: 'file' | 'folder') {
    const target = latest.current.binding;
    const generation = navigationGeneration.current;
    if (!target || !interactive || savingRef.current || blockedRecovery) return;
    const name = await nameDialog(
      desktop,
      kind === 'file' ? 'New File' : 'New Folder',
      '',
    );
    if (
      !name ||
      !mounted.current ||
      navigationGeneration.current !== generation ||
      latest.current.binding !== target
    )
      return;
    if (
      name.includes('/') ||
      name.includes('\\') ||
      name === '.' ||
      name === '..'
    ) {
      setError('Enter a single file or folder name.');
      return;
    }
    const path = [folder, name].filter(Boolean).join('/');
    setError('');
    try {
      if (kind === 'folder') await vmProjectsAPI.mkdir(id, target, path);
      else await vmProjectsAPI.write(id, target, path, '', '');
      if (!mounted.current || latest.current.binding !== target) return;
      setRefresh((value) => value + 1);
      if (
        kind === 'file' &&
        navigationGeneration.current === generation &&
        !latest.current.dirty &&
        !latest.current.pendingRecovery
      ) {
        setEditor(null);
        setReadRequest((value) => value + 1);
        desktop.updateWindowState(window.id, { file: path, reading: true });
      }
      setFeedback(`Created ${path} in VM.`);
    } catch (error) {
      if (mounted.current && latest.current.binding === target)
        setError(message(error));
    }
  }

  const project = listing.data?.project || connection.data?.project;
  const unavailable = !binding || !!pendingRecovery || blockedRecovery;
  return (
    <div className="project-browser vm-project-browser">
      <div className="app-toolbar">
        <IconButton
          icon="up"
          label="Up"
          disabled={!folder || saving || !interactive}
          onClick={() =>
            void navigate(folder.split('/').slice(0, -1).join('/'))
          }
        />
        <IconButton
          icon="refresh"
          label="Refresh directory"
          disabled={unavailable || !interactive}
          onClick={() => setRefresh((value) => value + 1)}
        />
        <Button
          disabled={!dirty || saving || unavailable || !interactive}
          onClick={() => void save()}
          primary
        >
          {saving ? 'Saving…' : 'Save'}
        </Button>
        <Button
          disabled={unavailable || saving || !interactive}
          onClick={() => void create('file')}
        >
          New File
        </Button>
        <Button
          disabled={unavailable || saving || !interactive}
          onClick={() => void create('folder')}
        >
          New Folder
        </Button>
        <Button
          disabled={saving || !interactive}
          onClick={() => void reconnectProject()}
        >
          Reconnect
        </Button>
        <IconButton
          icon="more"
          label="More VM project actions"
          aria-haspopup="menu"
          disabled={!interactive}
          onClick={(event) => {
            const rect = event.currentTarget.getBoundingClientRect();
            desktop.contextMenu(
              [
                {
                  label: 'Projects',
                  action: () => {
                    if (mounted.current) desktop.openApp('project-files');
                  },
                },
              ],
              { x: rect.left, y: rect.bottom },
              { title: 'VM project actions' },
            );
          }}
        />
      </div>
      <div className="project-location">
        <div className="project-location-summary">
          <strong>VM · {project?.name || window.title}</strong>
          <span>{folder || 'Project root'}</span>
        </div>
        <details>
          <summary>Details</summary>
          <dl className="info-list">
            <dt>Project</dt>
            <dd>
              <code>{id}</code>
            </dd>
            <dt>Host</dt>
            <dd>
              <code>{hostId}</code>
            </dd>
            <dt>Volume</dt>
            <dd>
              <code>
                {binding?.volumeId || project?.volumeId || 'Unavailable'}
              </code>
            </dd>
            <dt>Directory in volume</dt>
            <dd>
              <code>{project?.root || 'Volume root'}</code>
            </dd>
            <dt>VM</dt>
            <dd>
              <code>{binding?.machineId || 'Unavailable'}</code>
            </dd>
            <dt>Boot / instance</dt>
            <dd>
              <code>
                {binding
                  ? `${binding.bootId} / ${binding.instanceId}`
                  : 'Unavailable'}
              </code>
            </dd>
          </dl>
        </details>
      </div>
      {(error || connection.error || listing.error || draftError) && (
        <p className="project-error" role="alert">
          {error || connection.error || listing.error || draftError}
        </p>
      )}
      {draftError && (error || connection.error || listing.error) && (
        <p className="project-error" role="alert">
          {draftError}
        </p>
      )}
      {feedback && (
        <p className="project-feedback" role="status">
          {feedback}
        </p>
      )}
      {blockedRecovery && (
        <section
          className="project-recovery app-pad"
          aria-label="Unavailable VM project recovery"
        >
          <p>
            The stored recovery record could not be read. Editing is paused to
            preserve it. Copy or inspect that browser record before choosing to
            discard it.
          </p>
          <Button
            disabled={!interactive}
            onClick={() =>
              void (async () => {
                const answer = await desktop.dialog({
                  title: 'Discard stored recovery?',
                  message:
                    'This removes the unreadable VM project recovery record from this tab. Its contents cannot be recovered by AXV afterward.',
                  buttons: [
                    { label: 'Cancel', value: 'cancel', cancel: true },
                    { label: 'Discard', value: 'discard', default: true },
                  ],
                });
                if (answer?.button === 'discard' && persist(null))
                  setBlockedRecovery(false);
              })()
            }
          >
            Discard Recovery
          </Button>
        </section>
      )}
      {pendingRecovery && (
        <section
          className="project-recovery app-pad"
          aria-label="VM project draft recovery"
        >
          <strong>Unsaved draft: {pendingRecovery.editor.path}</strong>
          <p>
            This tab kept your text and its original VM binding. Restore keeps
            the original file version check.
          </p>
          <Button
            disabled={!interactive}
            onClick={() => {
              navigationGeneration.current++;
              setOriginalBinding(pendingRecovery.binding);
              setEditor(pendingRecovery.editor);
              desktop.updateWindowState(window.id, {
                file: pendingRecovery.editor.path,
                folder: pendingRecovery.editor.path
                  .split('/')
                  .slice(0, -1)
                  .join('/'),
                reading: true,
              });
              setPendingRecovery(null);
            }}
          >
            Restore Draft
          </Button>
          <Button
            disabled={!interactive}
            onClick={() => {
              if (persist(null)) setPendingRecovery(null);
            }}
          >
            Discard Draft
          </Button>
        </section>
      )}
      {!connection.data && !connection.error && (
        <p className="project-feedback" role="status">
          Connecting to VM project…
        </p>
      )}
      <div className={`project-content${isReading ? ' is-reading' : ''}`}>
        <nav
          className="project-directory"
          aria-label="VM project directory"
          ref={directory}
          tabIndex={-1}
        >
          <ul>
            {listing.data?.entries.map((entry) => (
              <li key={entry.path}>
                <button
                  disabled={!interactive || saving || entry.kind === 'symlink'}
                  className={entry.path === file ? 'is-selected' : ''}
                  aria-current={entry.path === file ? 'true' : undefined}
                  onClick={() =>
                    void navigate(
                      entry.kind === 'folder' ? entry.path : folder,
                      entry.kind === 'file' ? entry.path : '',
                    )
                  }
                >
                  <PixelIcon
                    size={16}
                    name={entry.kind === 'folder' ? 'folder' : 'document'}
                  />
                  <span>
                    {entry.name}
                    {entry.kind === 'symlink' && ' (symlink)'}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          {binding && !pendingRecovery && !listing.data && !listing.error && (
            <p role="status">Loading directory…</p>
          )}
          {listing.data?.entries.length === 0 && (
            <p>This directory is empty.</p>
          )}
          <div className="project-pagination">
            {cursor && (
              <Button
                disabled={!interactive}
                onClick={() =>
                  desktop.updateWindowState(window.id, { cursor: '' })
                }
              >
                First page
              </Button>
            )}
            {listing.data?.nextCursor && (
              <Button
                disabled={!interactive}
                onClick={() =>
                  desktop.updateWindowState(window.id, {
                    cursor: listing.data!.nextCursor!,
                  })
                }
              >
                Next page
              </Button>
            )}
            {listing.data?.truncated && !listing.data.nextCursor && (
              <p>Directory limit reached.</p>
            )}
          </div>
        </nav>
        <section
          className="project-preview vm-project-editor"
          aria-label="VM project file editor"
          ref={preview}
          tabIndex={-1}
        >
          <Button
            className="project-back"
            disabled={!interactive}
            onClick={() => {
              navigationGeneration.current++;
              returnToDirectory.current = true;
              desktop.updateWindowState(window.id, { reading: false });
            }}
          >
            Back
          </Button>
          {editor && !pendingRecovery ? (
            <>
              <div className="project-preview-heading">
                <code>{editor.path}</code>
                <strong>{dirty ? 'Unsaved' : 'Saved'}</strong>
                <Button
                  disabled={saving || !interactive}
                  onClick={() =>
                    void (async () => {
                      if (await discardBefore('Reloading the file')) {
                        navigationGeneration.current++;
                        setEditor(null);
                        setReadRequest((value) => value + 1);
                        desktop.updateWindowState(window.id, {
                          reading: true,
                        });
                      }
                    })()
                  }
                >
                  Reload File
                </Button>
              </div>
              <TextArea
                ref={editorInput}
                aria-label="VM project file text"
                value={editor.text}
                disabled={!interactive || blockedRecovery}
                spellCheck={false}
                onChange={(event) => {
                  const next = { ...editor, text: event.target.value };
                  setEditor(next);
                  latest.current = {
                    ...latest.current,
                    editor: next,
                    dirty: next.text !== next.saved,
                  };
                  persist(next);
                  setFeedback('');
                }}
                onKeyDown={(event) => {
                  if (
                    (event.metaKey || event.ctrlKey) &&
                    event.key.toLowerCase() === 's'
                  ) {
                    event.preventDefault();
                    void save();
                  }
                }}
              />
            </>
          ) : reading ? (
            <p role="status">Loading file…</p>
          ) : (
            <p>
              Select a file to edit inside the VM. Saving checks the version you
              opened.
            </p>
          )}
        </section>
      </div>
      <StatusBar>
        {file || folder || 'Project root'}
        {dirty ? ' · Unsaved changes' : ''}
        {binding ? ` · VM ${binding.machineId}` : ' · VM unavailable'}
      </StatusBar>
    </div>
  );
}
