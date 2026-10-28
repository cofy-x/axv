import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Button,
  IconButton,
  PixelIcon,
  StatusBar,
  Tabs,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useResource } from '../../shared/api/useResource';
import { APIError } from '../../shared/api/http';
import { CodeEditor } from '../../shared/code-editor';
import type { CodeEditorHandle } from '../../shared/code-editor';
import { useDraftNamespace } from '../../shared/drafts/context';
import { downloadDraft } from '../../shared/drafts/download';
import { nameDialog, runCommand } from '../../shared/desktop/dialogs';
import { canTransferWindowFocus } from '../../shared/desktop/focus-transfer';
import { vmProjectsAPI } from './vm-api';
import type { VMProjectBinding } from './vm-api';
import { openVMProjectCodex } from '../codex/definition';
import { VMProjectPreviews } from './VMProjectPreviews';
import { VMProjectBackups } from './VMProjectBackups';
import { useVMFileObserver } from './useVMFileObserver';
import {
  vmFileBindingError,
  vmFileBindingKey,
  vmFileTargetKey,
} from './vm-file-observer';

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

interface Feedback {
  kind: 'automatic' | 'action';
  message: string;
}

interface FileError {
  reason: string;
  message: string;
  operation: 'read' | 'save' | 'create';
  code?: string;
}

function fileError(
  operation: FileError['operation'],
  error: unknown,
): FileError {
  return {
    operation,
    reason: message(error),
    message: message(error),
    code: error instanceof APIError ? error.code : undefined,
  };
}

function needsReconnect(error: FileError | null) {
  return vmFileBindingError(error?.code);
}

interface ObservationNotice {
  target: string;
  kind: 'changed' | 'error' | 'binding';
  message: string;
  reason?: string;
  code?: string;
}

const message = (error: unknown) =>
  error instanceof Error ? error.message : String(error);

export function VMProjectFiles({
  window,
  desktop,
  visible: windowVisible,
  interactive: windowInteractive,
}: AppWindowProps) {
  const id = window.params.vmProjectId!;
  const hostId = window.params.hostId || '';
  const section =
    window.viewState.projectSection === 'previews' ||
    window.viewState.projectSection === 'backups'
      ? window.viewState.projectSection
      : 'files';
  const interactive = !!windowInteractive && section === 'files';
  const filesActive = useRef(interactive);
  filesActive.current = interactive;
  const filesVisible = useRef(section === 'files' && windowVisible !== false);
  filesVisible.current = section === 'files' && windowVisible !== false;
  const [previewNotice, setPreviewNotice] = useState<
    '' | 'recovery' | 'error' | 'working'
  >('');
  const [backupNotice, setBackupNotice] = useState<
    '' | 'recovery' | 'error' | 'working'
  >('');
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
  const [manualFile, setManualFile] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [reading, setReading] = useState(false);
  const [readRequest, setReadRequest] = useState(0);
  const [refresh, setRefresh] = useState(0);
  const [error, setError] = useState<FileError | null>(null);
  const [feedback, setFeedback] = useState<Feedback | null>(null);
  const [composing, setComposing] = useState(false);
  const composingRef = useRef(false);
  const [observationNotice, setObservationNotice] =
    useState<ObservationNotice | null>(null);
  const invalidObservedBinding = useRef('');
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
  const editing = !!editor && manualFile === editor.path;
  const latest = useRef({
    editor,
    manualFile,
    binding,
    saving,
    dirty,
    pendingRecovery,
    blockedRecovery,
    error,
    draftError,
  });
  latest.current = {
    editor,
    manualFile,
    binding,
    saving,
    dirty,
    pendingRecovery,
    blockedRecovery,
    error,
    draftError,
  };
  const mounted = useRef(false);
  const savingRef = useRef(false);
  const navigationGeneration = useRef(0);
  const fileCreationGeneration = useRef(0);
  const inputGeneration = useRef(0);
  const saveGeneration = useRef(0);
  const directory = useRef<HTMLElement>(null);
  const preview = useRef<HTMLElement>(null);
  const editorInput = useRef<CodeEditorHandle>(null);
  const returnToDirectory = useRef(false);
  const observedTarget =
    binding && editor ? vmFileTargetKey(id, binding, editor.path) : '';
  const currentNotice =
    observationNotice?.target === observedTarget ? observationNotice : null;
  const existingReconnect = !!(
    needsReconnect(error) ||
    connection.error ||
    listing.error
  );
  const visibleNotice =
    (currentNotice?.kind === 'changed' &&
      error?.operation === 'save' &&
      error.code === 'file_conflict') ||
    (currentNotice?.kind === 'binding' &&
      needsReconnect(error) &&
      currentNotice.code === error?.code &&
      currentNotice.reason === error?.reason)
      ? null
      : currentNotice;

  const checkFile = useVMFileObserver({
    enabled:
      section === 'files' &&
      windowVisible !== false &&
      (!binding ||
        invalidObservedBinding.current !== vmFileBindingKey(binding)),
    targetKey: observedTarget,
    activityKey: `${saving}:${composing}:${!!pendingRecovery}:${blockedRecovery}:${readRequest}:${reconnect}`,
    surface: preview,
    snapshot: () => {
      const current = latest.current;
      if (
        !mounted.current ||
        !filesVisible.current ||
        document.visibilityState !== 'visible' ||
        !preview.current?.getClientRects().length ||
        !current.editor ||
        !current.binding ||
        savingRef.current ||
        composingRef.current ||
        editorInput.current?.isComposing() ||
        current.pendingRecovery ||
        current.blockedRecovery ||
        invalidObservedBinding.current === vmFileBindingKey(current.binding)
      )
        return null;
      return {
        projectId: id,
        binding: current.binding,
        path: current.editor.path,
        navigation: navigationGeneration.current,
        input: inputGeneration.current,
        save: saveGeneration.current,
        sha256: current.editor.sha256,
        dirty: current.dirty,
        acceptBlocked:
          current.error?.operation === 'save' ||
          needsReconnect(current.error) ||
          !!current.draftError,
      };
    },
    read: (captured, signal) =>
      vmProjectsAPI.read(id, captured.binding, captured.path, signal),
    receive: (value, decision) => {
      if (decision === 'changed') {
        setObservationNotice({
          target: observedTarget,
          kind: 'changed',
          message: latest.current.dirty
            ? 'File changed in VM. Draft kept.'
            : 'File changed in VM. Reload to review.',
        });
        return;
      }
      setObservationNotice(null);
      if (decision === 'same') return;
      const next = {
        path: value.path,
        text: value.content,
        saved: value.content,
        sha256: value.sha256,
      };
      inputGeneration.current++;
      setEditor(next);
      latest.current = { ...latest.current, editor: next, dirty: false };
      setFeedback({ kind: 'automatic', message: 'Updated from VM.' });
    },
    fail: (error, captured) => {
      const reconnect =
        error instanceof APIError && vmFileBindingError(error.code);
      if (reconnect)
        invalidObservedBinding.current = vmFileBindingKey(captured.binding);
      setObservationNotice({
        target: vmFileTargetKey(id, captured.binding, captured.path),
        kind: reconnect ? 'binding' : 'error',
        reason: message(error),
        code: error instanceof APIError ? error.code : undefined,
        message: reconnect
          ? `${message(error)} Your text is preserved. Reconnect explicitly to check the current VM environment.`
          : `Could not check VM file changes. Your text is preserved. ${message(error)}`,
      });
    },
  });

  const compositionChanged = useCallback((active: boolean) => {
    composingRef.current = active;
    inputGeneration.current++;
    setComposing(active);
  }, []);

  function inputPending() {
    return composingRef.current || !!editorInput.current?.isComposing();
  }

  function selectSection(next: string) {
    if (
      !windowInteractive ||
      inputPending() ||
      !['files', 'previews', 'backups'].includes(next)
    )
      return;
    if (next !== section) navigationGeneration.current++;
    desktop.updateWindowState(window.id, { projectSection: next });
  }

  function focusContent() {
    if (!filesActive.current) return;
    const destination = preview.current?.getClientRects().length
      ? preview.current
      : directory.current;
    destination?.focus({ preventScroll: true });
  }

  function filesAreVisible() {
    // Menus and dialogs temporarily block input while their captured actions
    // still belong here. Window visibility separately excludes hidden bodies.
    return (
      filesVisible.current &&
      !!(
        directory.current?.getClientRects().length ||
        preview.current?.getClientRects().length
      )
    );
  }

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
    checkFile();
    return () => {
      mounted.current = false;
    };
  }, [checkFile]);

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
    setError(null);
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
          if (current) setError(fileError('read', error));
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
        inputPending() ||
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
        if (savingRef.current || inputPending()) return false;
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
    if (
      isReading &&
      directory.current?.getClientRects().length === 0 &&
      canTransferWindowFocus(preview.current, directory.current)
    )
      preview.current?.focus({ preventScroll: true });
    else if (!isReading && returnToDirectory.current) {
      const selected = directory.current?.querySelector<HTMLButtonElement>(
        '[aria-current="true"]',
      );
      const destination = selected || directory.current;
      if (canTransferWindowFocus(destination, preview.current)) {
        returnToDirectory.current = false;
        destination?.focus({ preventScroll: true });
      }
    }
  }, [interactive, file, isReading]);

  useEffect(() => {
    const surface = directory.current?.parentElement;
    if (!interactive || !surface) return;
    let compact = directory.current?.getClientRects().length === 0;
    const observer = new ResizeObserver(() => {
      const next = directory.current?.getClientRects().length === 0;
      if (
        next &&
        !compact &&
        isReading &&
        canTransferWindowFocus(preview.current, directory.current)
      )
        preview.current?.focus({ preventScroll: true });
      compact = next;
    });
    observer.observe(surface);
    return () => observer.disconnect();
  }, [interactive, isReading]);

  async function discardBefore(action: string, isCurrent = filesAreVisible) {
    if (savingRef.current || inputPending()) return false;
    if (!latest.current.dirty && !latest.current.pendingRecovery) return true;
    const answer = await desktop.dialog({
      title: 'Discard unsaved changes?',
      message: `${action} discards the current draft and its recovery copy.`,
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Discard', value: 'discard', default: true },
      ],
    });
    if (
      answer?.button !== 'discard' ||
      inputPending() ||
      !isCurrent() ||
      !persist(null)
    )
      return false;
    setPendingRecovery(null);
    return true;
  }

  async function navigate(path: string, nextFile = '') {
    if (!filesActive.current || inputPending()) return;
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
    setManualFile(null);
    setReadRequest((value) => value + 1);
    setError(null);
    setFeedback(null);
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
      !filesActive.current ||
      !snapshot.editor ||
      snapshot.manualFile !== snapshot.editor.path ||
      !snapshot.binding ||
      blockedRecovery ||
      inputPending() ||
      savingRef.current ||
      !snapshot.dirty
    )
      return;
    savingRef.current = true;
    saveGeneration.current++;
    setSaving(true);
    setError(null);
    setFeedback(null);
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
      setFeedback({
        kind: 'action',
        message:
          next.text === next.saved
            ? 'Saved in VM.'
            : 'Snapshot saved in VM. Newer typing is unsaved.',
      });
      setRefresh((value) => value + 1);
    } catch (error) {
      if (mounted.current)
        setError({
          ...fileError('save', error),
          message: `${message(error)} Your draft is preserved. Reload or reconnect requires discarding it explicitly.`,
        });
    } finally {
      savingRef.current = false;
      saveGeneration.current++;
      if (mounted.current) setSaving(false);
    }
  }

  function ownsLocation(
    target: VMProjectBinding | null,
    generation: number,
    path?: string,
  ) {
    return (
      mounted.current &&
      filesAreVisible() &&
      latest.current.binding === target &&
      navigationGeneration.current === generation &&
      (path === undefined || latest.current.editor?.path === path)
    );
  }

  function downloadFileDraft(
    target: VMProjectBinding | null,
    path: string,
    generation: number,
  ) {
    void runCommand(desktop, async () => {
      if (
        !ownsLocation(target, generation, path) ||
        inputPending() ||
        latest.current.pendingRecovery ||
        latest.current.blockedRecovery
      )
        throw new Error('This action is no longer available for this file.');
      downloadDraft(path, latest.current.editor!.text);
    });
  }

  async function finishEditing() {
    const snapshot = latest.current;
    if (
      !filesActive.current ||
      !snapshot.editor ||
      snapshot.manualFile !== snapshot.editor.path ||
      savingRef.current
    )
      return;
    const path = snapshot.editor.path;
    const generation = navigationGeneration.current;
    const isCurrent = () =>
      ownsLocation(snapshot.binding, generation, path) &&
      latest.current.manualFile === path;
    if (
      !(await discardBefore('Ending manual editing', isCurrent)) ||
      !isCurrent()
    )
      return;
    const current = latest.current.editor!;
    const next = { ...current, text: current.saved };
    inputGeneration.current++;
    setEditor(next);
    setManualFile(null);
    latest.current = {
      ...latest.current,
      editor: next,
      manualFile: null,
      dirty: false,
    };
    setFeedback(null);
    editorInput.current?.blur();
  }

  async function reloadFile(
    target: VMProjectBinding | null,
    path: string,
    generation: number,
  ) {
    const isCurrent = () => ownsLocation(target, generation, path);
    if (
      !interactive ||
      !isCurrent() ||
      !(await discardBefore('Reloading the file', isCurrent)) ||
      !isCurrent()
    )
      return;
    navigationGeneration.current++;
    setEditor(null);
    setManualFile(null);
    setError(null);
    setFeedback(null);
    setReadRequest((value) => value + 1);
    desktop.updateWindowState(window.id, { reading: true });
  }

  async function reconnectProject(
    target = latest.current.binding,
    generation = navigationGeneration.current,
  ) {
    const isCurrent = () => ownsLocation(target, generation);
    if (
      !interactive ||
      !isCurrent() ||
      !(await discardBefore('Reconnecting', isCurrent)) ||
      !isCurrent()
    )
      return;
    navigationGeneration.current++;
    setEditor(null);
    setManualFile(null);
    setOriginalBinding(null);
    invalidObservedBinding.current = '';
    setError(null);
    setFeedback(null);
    setReconnect((value) => value + 1);
  }

  async function create(kind: 'file' | 'folder') {
    const target = latest.current.binding;
    const generation = navigationGeneration.current;
    if (
      !target ||
      !interactive ||
      savingRef.current ||
      blockedRecovery ||
      inputPending()
    )
      return;
    const name = await nameDialog(
      desktop,
      kind === 'file' ? 'New File' : 'New Folder',
      '',
    );
    if (
      !name ||
      inputPending() ||
      !mounted.current ||
      !filesAreVisible() ||
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
      setError(fileError('create', 'Enter a single file or folder name.'));
      return;
    }
    const path = [folder, name].filter(Boolean).join('/');
    const creation =
      kind === 'file' ? ++fileCreationGeneration.current : undefined;
    setError(null);
    try {
      if (kind === 'folder') await vmProjectsAPI.mkdir(id, target, path);
      else await vmProjectsAPI.write(id, target, path, '', '');
      if (!mounted.current || latest.current.binding !== target) return;
      setRefresh((value) => value + 1);
      if (
        kind === 'file' &&
        filesActive.current &&
        creation === fileCreationGeneration.current &&
        navigationGeneration.current === generation &&
        !inputPending() &&
        !savingRef.current &&
        !latest.current.dirty &&
        !latest.current.pendingRecovery &&
        !latest.current.blockedRecovery
      ) {
        navigationGeneration.current++;
        setEditor(null);
        setManualFile(path);
        setReadRequest((value) => value + 1);
        desktop.updateWindowState(window.id, { file: path, reading: true });
      }
      setFeedback({ kind: 'action', message: `Created ${path} in VM.` });
    } catch (error) {
      if (mounted.current && latest.current.binding === target)
        setError(fileError('create', error));
    }
  }

  const project = listing.data?.project || connection.data?.project;
  const unavailable = !binding || !!pendingRecovery || blockedRecovery;
  const fileErrors = Array.from(
    new Set(
      [error?.message, connection.error, listing.error, draftError].filter(
        Boolean,
      ),
    ),
  );
  const fileNotice =
    pendingRecovery || blockedRecovery
      ? 'recovery'
      : fileErrors.length ||
          currentNotice?.kind === 'error' ||
          currentNotice?.kind === 'binding'
        ? 'error'
        : '';
  const menuTitle = `${project?.name || 'VM project'} · ${id.slice(0, 8)}`;
  const downloadAction = editor &&
    dirty &&
    !pendingRecovery &&
    !blockedRecovery && (
      <Button
        disabled={composing || !interactive}
        onClick={() =>
          downloadFileDraft(binding, editor.path, navigationGeneration.current)
        }
      >
        Download Draft
      </Button>
    );
  return (
    <div className="project-browser vm-project-browser">
      <div className="app-toolbar project-browser-toolbar">
        <div
          className="project-browser-actions"
          role="group"
          aria-label="VM project actions"
        >
          <Button
            primary
            disabled={!windowInteractive}
            onClick={() =>
              openVMProjectCodex(desktop, hostId, id, project?.name)
            }
          >
            Codex
          </Button>
          <Button
            disabled={!windowInteractive}
            onClick={() =>
              desktop.openApp(
                'project-files',
                { hostId, vmTasksProjectId: id },
                { title: `Tasks · ${project?.name || 'VM project'}` },
              )
            }
          >
            Tasks
          </Button>
          <IconButton
            icon="more"
            label="More VM project actions"
            aria-haspopup="menu"
            disabled={!windowInteractive}
            onClick={(event) => {
              const rect = event.currentTarget.getBoundingClientRect();
              const target = latest.current.binding;
              const generation = navigationGeneration.current;
              const path = editor?.path || '';
              desktop.contextMenu(
                [
                  ...(['file', 'folder'] as const).map((kind) => ({
                    label: kind === 'file' ? 'New File' : 'New Folder',
                    disabled:
                      unavailable || saving || composing || !interactive,
                    action: () => {
                      if (ownsLocation(target, generation)) void create(kind);
                    },
                  })),
                  { separator: true },
                  {
                    label: 'Download Draft',
                    description: path ? path : undefined,
                    disabled:
                      !interactive ||
                      !path ||
                      composing ||
                      !!pendingRecovery ||
                      blockedRecovery,
                    action: () => downloadFileDraft(target, path, generation),
                  },
                  {
                    label: 'Reload',
                    disabled:
                      !interactive ||
                      !path ||
                      saving ||
                      composing ||
                      !!pendingRecovery ||
                      blockedRecovery,
                    action: () => void reloadFile(target, path, generation),
                  },
                  {
                    label: 'Reconnect',
                    disabled: !interactive || saving || composing,
                    action: () => void reconnectProject(target, generation),
                  },
                  { separator: true },
                  {
                    label: 'Projects',
                    action: () => {
                      if (mounted.current) desktop.openApp('project-files');
                    },
                  },
                ],
                { x: rect.left, y: rect.bottom },
                { title: menuTitle },
              );
            }}
          />
        </div>
        <div
          className="project-browser-navigation"
          role="group"
          aria-label="Project navigation"
          hidden={section !== 'files'}
          inert={!interactive}
        >
          <IconButton
            icon="up"
            label="Up"
            disabled={!folder || saving || composing || !interactive}
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
        </div>
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
      <div className="vm-project-sections">
        <Tabs
          label="VM project view"
          value={section}
          onChange={selectSection}
          tabs={[
            {
              id: 'files',
              label: 'Files',
              disabled: !windowInteractive || composing,
            },
            {
              id: 'previews',
              label: 'Previews',
              disabled: !windowInteractive || composing,
            },
            {
              id: 'backups',
              label: 'Backups',
              disabled: !windowInteractive || composing,
            },
          ]}
        >
          {((section !== 'files' && fileNotice) ||
            (section !== 'previews' && previewNotice) ||
            (section !== 'backups' && backupNotice)) && (
            <div className="vm-project-notices" aria-label="Project notices">
              {section !== 'files' && fileNotice && (
                <Button
                  disabled={!windowInteractive}
                  onClick={() => selectSection('files')}
                >
                  {fileNotice === 'recovery' ? 'File Recovery' : 'File Error'}
                </Button>
              )}
              {section !== 'previews' && previewNotice && (
                <Button
                  disabled={!windowInteractive}
                  onClick={() => selectSection('previews')}
                >
                  {previewNotice === 'recovery'
                    ? 'Preview Recovery'
                    : previewNotice === 'error'
                      ? 'Preview Error'
                      : 'Preview Status'}
                </Button>
              )}
              {section !== 'backups' && backupNotice && (
                <Button
                  disabled={!windowInteractive}
                  onClick={() => selectSection('backups')}
                >
                  {backupNotice === 'recovery'
                    ? 'Backup Recovery'
                    : backupNotice === 'error'
                      ? 'Backup Error'
                      : 'Backup Status'}
                </Button>
              )}
            </div>
          )}
          <section
            className="vm-project-pane vm-project-files-pane"
            aria-label="Project files"
            hidden={section !== 'files'}
            inert={!interactive}
          >
            {fileErrors.length > 0 && (
              <div className="project-error" role="alert">
                <div>
                  {fileErrors.map((reason) => (
                    <p key={reason}>{reason}</p>
                  ))}
                </div>
                <div className="app-actions">
                  {downloadAction}
                  {error?.operation === 'read' && !needsReconnect(error) && (
                    <Button
                      disabled={reading || !interactive}
                      onClick={() => {
                        focusContent();
                        setReadRequest((value) => value + 1);
                      }}
                    >
                      Retry
                    </Button>
                  )}
                  {error?.operation === 'save' &&
                    !needsReconnect(error) &&
                    editor && (
                      <Button
                        disabled={saving || composing || !interactive}
                        onClick={() => {
                          focusContent();
                          void reloadFile(
                            binding,
                            editor.path,
                            navigationGeneration.current,
                          );
                        }}
                      >
                        Reload
                      </Button>
                    )}
                  {listing.error && (
                    <Button
                      disabled={!interactive}
                      onClick={() => {
                        focusContent();
                        setRefresh((value) => value + 1);
                      }}
                    >
                      Refresh
                    </Button>
                  )}
                  {(needsReconnect(error) ||
                    connection.error ||
                    listing.error) && (
                    <Button
                      disabled={saving || composing || !interactive}
                      onClick={() => {
                        focusContent();
                        void reconnectProject();
                      }}
                    >
                      Reconnect
                    </Button>
                  )}
                </div>
              </div>
            )}
            {feedback?.kind === 'action' && (
              <p className="project-feedback" role="status">
                {feedback.message}
              </p>
            )}
            {blockedRecovery && (
              <section
                className="project-recovery app-pad"
                aria-label="Unavailable VM project recovery"
              >
                <p>
                  The stored recovery record could not be read. Editing is
                  paused to preserve it. Copy or inspect that browser record
                  before choosing to discard it.
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
                  This tab kept your text and its original VM binding. Restore
                  keeps the original file version check.
                </p>
                <Button
                  disabled={!interactive}
                  onClick={() => {
                    navigationGeneration.current++;
                    setOriginalBinding(pendingRecovery.binding);
                    setEditor(pendingRecovery.editor);
                    setManualFile(pendingRecovery.editor.path);
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
            {visibleNotice && (
              <section
                className="project-file-observation"
                role="status"
                aria-label="VM file updates"
              >
                <p>{visibleNotice.message}</p>
                <div className="app-actions">
                  {visibleNotice.kind !== 'changed' &&
                    fileErrors.length === 0 &&
                    downloadAction}
                  {visibleNotice.kind === 'binding' && !existingReconnect ? (
                    <Button
                      disabled={saving || composing || !interactive}
                      onClick={() => void reconnectProject()}
                    >
                      Reconnect
                    </Button>
                  ) : visibleNotice.kind === 'error' ? (
                    <Button disabled={!interactive} onClick={checkFile}>
                      Retry check
                    </Button>
                  ) : null}
                </div>
              </section>
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
                        disabled={
                          !interactive ||
                          saving ||
                          composing ||
                          entry.kind === 'symlink'
                        }
                        className={entry.path === file ? 'is-selected' : ''}
                        aria-current={entry.path === file ? 'true' : undefined}
                        onClick={(event) => {
                          event.currentTarget.focus({ preventScroll: true });
                          void navigate(
                            entry.kind === 'folder' ? entry.path : folder,
                            entry.kind === 'file' ? entry.path : '',
                          );
                        }}
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
                {binding &&
                  !pendingRecovery &&
                  !listing.data &&
                  !listing.error && <p role="status">Loading directory…</p>}
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
                {(file || editor) && (
                  <div className="project-preview-heading vm-project-file-toolbar">
                    <div className="vm-project-file-name">
                      <code
                        title={editor && !pendingRecovery ? editor.path : file}
                      >
                        {editor && !pendingRecovery ? editor.path : file}
                      </code>
                      {dirty && <strong>Unsaved</strong>}
                    </div>
                    <div
                      className="vm-project-file-controls"
                      role="group"
                      aria-label="File actions"
                    >
                      <IconButton
                        className="project-back"
                        icon="back"
                        label="Back"
                        title="Back to files"
                        disabled={!interactive || composing}
                        onClick={(event) => {
                          if (!filesActive.current || inputPending()) return;
                          event.currentTarget.focus({ preventScroll: true });
                          navigationGeneration.current++;
                          returnToDirectory.current = true;
                          desktop.updateWindowState(window.id, {
                            reading: false,
                          });
                        }}
                      />
                      {editor && !pendingRecovery && (
                        <>
                          <IconButton
                            icon="refresh"
                            label="Reload file"
                            disabled={
                              saving ||
                              reading ||
                              composing ||
                              unavailable ||
                              !interactive
                            }
                            onClick={() =>
                              void reloadFile(
                                binding,
                                editor.path,
                                navigationGeneration.current,
                              )
                            }
                          />
                          {editing ? (
                            <>
                              <Button
                                primary
                                disabled={
                                  !dirty ||
                                  saving ||
                                  composing ||
                                  unavailable ||
                                  !interactive
                                }
                                onClick={() => void save()}
                              >
                                {saving ? 'Saving…' : 'Save'}
                              </Button>
                              <Button
                                disabled={saving || composing || !interactive}
                                onClick={() => void finishEditing()}
                              >
                                {dirty ? 'Cancel' : 'Done'}
                              </Button>
                            </>
                          ) : (
                            <Button
                              disabled={
                                reading ||
                                saving ||
                                composing ||
                                unavailable ||
                                !interactive
                              }
                              onClick={() => {
                                if (!filesActive.current) return;
                                setManualFile(editor.path);
                                latest.current = {
                                  ...latest.current,
                                  manualFile: editor.path,
                                };
                                editorInput.current?.focus();
                              }}
                            >
                              Edit
                            </Button>
                          )}
                        </>
                      )}
                    </div>
                  </div>
                )}
                {editor && !pendingRecovery ? (
                  <CodeEditor
                    ref={editorInput}
                    label="VM project file text"
                    filename={editor.path}
                    documentId={observedTarget}
                    value={editor.text}
                    readOnly={!editing}
                    disabled={!interactive || blockedRecovery}
                    onCompositionChange={compositionChanged}
                    onChange={(text, change) => {
                      const current = latest.current;
                      if (
                        (!filesActive.current &&
                          change.source !== 'composition') ||
                        current.blockedRecovery ||
                        !current.editor ||
                        !current.binding ||
                        current.manualFile !== current.editor.path ||
                        change.documentId !==
                          vmFileTargetKey(
                            id,
                            current.binding,
                            current.editor.path,
                          )
                      )
                        return;
                      inputGeneration.current++;
                      const next = { ...current.editor, text };
                      setEditor(next);
                      latest.current = {
                        ...latest.current,
                        editor: next,
                        dirty: next.text !== next.saved,
                      };
                      persist(next);
                      setFeedback(null);
                    }}
                    onSave={() => void save()}
                  />
                ) : reading ? (
                  <p role="status">Loading file…</p>
                ) : error?.operation === 'read' ? (
                  <p>
                    Could not open <code>{file}</code>.
                  </p>
                ) : (
                  <p>
                    Select a file to read inside the VM. Choose Edit to make
                    manual changes; saving checks the version you opened.
                  </p>
                )}
              </section>
            </div>
          </section>
          <section
            className="vm-project-pane vm-project-tool-pane"
            aria-label="Project previews view"
            hidden={section !== 'previews'}
            inert={!windowInteractive || section !== 'previews'}
          >
            <VMProjectPreviews
              desktop={desktop}
              projectId={id}
              hostId={hostId}
              binding={binding}
              active={section === 'previews'}
              interactive={!!windowInteractive && section === 'previews'}
              onNoticeChange={setPreviewNotice}
            />
          </section>
          <section
            className="vm-project-pane vm-project-tool-pane"
            aria-label="Project backups view"
            hidden={section !== 'backups'}
            inert={!windowInteractive || section !== 'backups'}
          >
            <VMProjectBackups
              projectId={id}
              hostId={hostId}
              desktop={desktop}
              active={section === 'backups'}
              interactive={!!windowInteractive && section === 'backups'}
              dirty={dirty || !!pendingRecovery || blockedRecovery || saving}
              onNoticeChange={setBackupNotice}
            />
          </section>
        </Tabs>
      </div>
      <StatusBar>
        {feedback?.kind === 'automatic'
          ? feedback.message
          : file || folder || 'Project root'}
        {dirty ? ' · Unsaved changes' : ''}
        {currentNotice?.kind === 'changed' ? ' · Changed in VM' : ''}
        {pendingRecovery || blockedRecovery ? ' · Draft recovery' : ''}
        {binding ? (
          <span title={binding.machineId}>
            {' '}
            · VM {binding.machineId.slice(0, 8)}
          </span>
        ) : (
          ' · VM unavailable'
        )}
      </StatusBar>
    </div>
  );
}
