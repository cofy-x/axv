import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, StatusBar, Tabs } from '@cofy-x/axv-nextstep';
import { useResource } from '../../shared/api/useResource';
import { APIError } from '../../shared/api/http';
import { CodeEditor } from '../../shared/code-editor';
import type { CodeEditorHandle } from '../../shared/code-editor';
import { downloadDraft } from '../../shared/drafts/download';
import { nameDialog, runCommand } from '../../shared/desktop/dialogs';
import { canTransferWindowFocus } from '../../shared/desktop/focus-transfer';
import { vmProjectsAPI } from './vm-api';
import type { VMProjectBinding } from './vm-api';
import type { VMProjectFileProps } from './VMProjectWindows';
import { sameVMProjectVolume, vmProjectVolumeScope } from './vm-project-volume';
import {
  guestTerminalRequests,
  openGuestTerminal,
} from '../machines/open-terminal';
import { openCodexSession } from '../codex/definition';
import { vmCodexAPI } from '../codex/vm-api';
import { VMProjectPreviews } from './VMProjectPreviews';
import { VMProjectBackups } from './VMProjectBackups';
import { VMProjectGitHub } from './VMProjectGitHub';
import { VMProjectLocationDetails } from './VMProjectLocationDetails';
import { VMProjectDirectoryPane } from './VMProjectDirectoryPane';
import { VMProjectFileHeader } from './VMProjectFileHeader';
import { VMProjectRecoveryBanners } from './VMProjectRecoveryBanners';
import { VMProjectToolbar } from './VMProjectToolbar';
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
  recoveryKey,
  registerFileGuard,
}: VMProjectFileProps) {
  const id = window.params.vmProjectId!;
  const hostId = window.params.hostId || '';
  const section =
    window.viewState.projectSection === 'previews' ||
    window.viewState.projectSection === 'backups' ||
    window.viewState.projectSection === 'github'
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
  const [githubNotice, setGitHubNotice] = useState<'' | 'error' | 'working'>(
    '',
  );
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
  const { machineId, vmProjectVolumeId } = window.params;
  const loadBinding = useCallback(
    async (signal: AbortSignal) => {
      if (!hostId)
        throw new Error(
          'This VM project has no recorded host identity. Open it from Projects.',
        );
      const result = await vmProjectsAPI.binding(id, hostId, signal);
      const scope = vmProjectVolumeScope({
        hostId,
        machineId,
        vmProjectVolumeId,
      });
      if (scope && !sameVMProjectVolume(scope, result.binding))
        throw new Error(
          'The project volume is unavailable on the original VM. Close this window and reopen the project from Projects to use its current VM.',
        );
      return result;
    },
    [id, hostId, machineId, vmProjectVolumeId],
  );
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
      !['files', 'previews', 'backups', 'github'].includes(next)
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

  useEffect(
    () =>
      registerFileGuard(() => ({
        busy: savingRef.current || latest.current.saving || inputPending(),
        dirty:
          latest.current.dirty ||
          !!latest.current.pendingRecovery ||
          latest.current.blockedRecovery,
        keep: () =>
          latest.current.editor ? persist(latest.current.editor) : true,
        discard: () => persist(null),
      })),
    [registerFileGuard, persist],
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
  const [openingTerminal, setOpeningTerminal] = useState(false);
  const [openingCodex, setOpeningCodex] = useState(false);
  const openProjectCodex = async () => {
    if (!binding || openingCodex) return;
    setOpeningCodex(true);
    setFeedback(null);
    const relativePath = [project?.root, folder].filter(Boolean).join('/');
    const target = {
      hostId: binding.hostId,
      machineId: binding.machineId,
      machineName: project?.name || binding.machineId,
    };
    try {
      const info = await vmCodexAPI.info(target);
      if (!mounted.current) return;
      if (!info.ready) {
        desktop.openApp('codex', {
          newVmHostId: binding.hostId,
          newVmMachineId: binding.machineId,
          newVmProjectPath: relativePath,
        });
        return;
      }
      const session = await vmCodexAPI.create(
        target,
        crypto.randomUUID(),
        relativePath ? `/projects/${relativePath}` : undefined,
      );
      if (mounted.current) openCodexSession(desktop, session);
    } catch {
      if (mounted.current) {
        desktop.openApp('codex', {
          newVmHostId: binding.hostId,
          newVmMachineId: binding.machineId,
          newVmProjectPath: relativePath,
        });
      }
    } finally {
      if (mounted.current) setOpeningCodex(false);
    }
  };
  const openProjectTerminal = async () => {
    if (!binding || openingTerminal) return;
    setOpeningTerminal(true);
    setFeedback(null);
    try {
      const directory =
        [project?.root, folder].filter(Boolean).join('/') || undefined;
      const session = await guestTerminalRequests.execute(
        binding.machineId,
        binding.hostId,
        directory,
      );
      if (mounted.current) {
        openGuestTerminal(
          desktop,
          {
            id: binding.machineId,
            hostId: binding.hostId,
            spec: { name: project?.name || binding.machineId },
          },
          session,
        );
      }
    } catch (err) {
      if (mounted.current) {
        setFeedback({ kind: 'action', message: message(err) });
      }
    } finally {
      if (mounted.current) setOpeningTerminal(false);
    }
  };
  return (
    <div className="project-browser vm-project-browser">
      <VMProjectToolbar
        desktop={desktop}
        projectId={id}
        hostId={hostId}
        project={project}
        binding={binding}
        section={section}
        folder={folder}
        windowInteractive={windowInteractive}
        interactive={interactive}
        unavailable={unavailable}
        saving={saving}
        composing={composing}
        openingTerminal={openingTerminal}
        openingCodex={openingCodex}
        onOpenTerminal={() => void openProjectTerminal()}
        onOpenCodex={() => void openProjectCodex()}
        onOpenMoreMenu={(rect) => {
          const target = latest.current.binding;
          const generation = navigationGeneration.current;
          const path = editor?.path || '';
          desktop.contextMenu(
            [
              ...(['file', 'folder'] as const).map((kind) => ({
                label: kind === 'file' ? 'New File' : 'New Folder',
                disabled: unavailable || saving || composing || !interactive,
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
        onNavigateUp={() =>
          void navigate(folder.split('/').slice(0, -1).join('/'))
        }
        onRefresh={() => setRefresh((value) => value + 1)}
      />
      <VMProjectLocationDetails
        section={section}
        folder={folder}
        id={id}
        hostId={hostId}
        project={project}
        binding={binding}
      />
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
            {
              id: 'github',
              label: 'GitHub',
              disabled: !windowInteractive || composing,
            },
          ]}
        >
          {((section !== 'files' && fileNotice) ||
            (section !== 'previews' && previewNotice) ||
            (section !== 'backups' && backupNotice) ||
            (section !== 'github' && githubNotice)) && (
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
              {section !== 'github' && githubNotice && (
                <Button
                  disabled={!windowInteractive}
                  onClick={() => selectSection('github')}
                >
                  {githubNotice === 'error' ? 'GitHub Error' : 'GitHub Status'}
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
            <VMProjectRecoveryBanners
              interactive={interactive}
              saving={saving}
              reading={reading}
              composing={composing}
              hasEditor={!!editor}
              fileErrors={fileErrors}
              errorOperation={error?.operation}
              errorNeedsReconnect={needsReconnect(error)}
              hasConnectionError={!!connection.error}
              hasListingError={!!listing.error}
              existingReconnect={existingReconnect}
              connecting={!connection.data && !connection.error}
              actionFeedback={
                feedback?.kind === 'action' ? feedback.message : undefined
              }
              blockedRecovery={blockedRecovery}
              pendingRecoveryPath={pendingRecovery?.editor.path}
              visibleNotice={visibleNotice}
              downloadAction={downloadAction}
              onRetryRead={() => {
                focusContent();
                setReadRequest((value) => value + 1);
              }}
              onReloadFile={() => {
                if (!editor) return;
                focusContent();
                void reloadFile(
                  binding,
                  editor.path,
                  navigationGeneration.current,
                );
              }}
              onRefreshListing={() => {
                focusContent();
                setRefresh((value) => value + 1);
              }}
              onReconnect={() => {
                focusContent();
                void reconnectProject();
              }}
              onDiscardBlockedRecovery={() =>
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
              onRestoreDraft={() => {
                if (!pendingRecovery) return;
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
              onDiscardDraft={() => {
                if (persist(null)) setPendingRecovery(null);
              }}
              onRetryCheck={checkFile}
            />
            <div className={`project-content${isReading ? ' is-reading' : ''}`}>
              <VMProjectDirectoryPane
                directoryRef={directory}
                binding={binding}
                pendingRecovery={!!pendingRecovery}
                listingData={listing.data}
                listingError={listing.error}
                folder={folder}
                file={file}
                cursor={cursor}
                interactive={interactive}
                saving={saving}
                composing={composing}
                onNavigate={(path, nextFile) => void navigate(path, nextFile)}
                onCursorChange={(nextCursor) =>
                  desktop.updateWindowState(window.id, { cursor: nextCursor })
                }
              />
              <section
                className="project-preview vm-project-editor"
                aria-label="VM project file editor"
                ref={preview}
                tabIndex={-1}
              >
                {(file || editor) && (
                  <VMProjectFileHeader
                    displayPath={
                      editor && !pendingRecovery ? editor.path : file
                    }
                    hasEditor={!!(editor && !pendingRecovery)}
                    dirty={dirty}
                    editing={editing}
                    saving={saving}
                    reading={reading}
                    composing={composing}
                    unavailable={unavailable}
                    interactive={interactive}
                    onBack={(event) => {
                      if (!filesActive.current || inputPending()) return;
                      event.currentTarget.focus({ preventScroll: true });
                      navigationGeneration.current++;
                      returnToDirectory.current = true;
                      desktop.updateWindowState(window.id, {
                        reading: false,
                      });
                    }}
                    onReload={() => {
                      if (editor)
                        void reloadFile(
                          binding,
                          editor.path,
                          navigationGeneration.current,
                        );
                    }}
                    onSave={() => void save()}
                    onFinishEditing={() => void finishEditing()}
                    onStartEditing={() => {
                      if (!filesActive.current || !editor) return;
                      setManualFile(editor.path);
                      latest.current = {
                        ...latest.current,
                        manualFile: editor.path,
                      };
                      editorInput.current?.focus();
                    }}
                  />
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
          <section
            className="vm-project-pane vm-project-tool-pane"
            aria-label="Project GitHub view"
            hidden={section !== 'github'}
            inert={!windowInteractive || section !== 'github'}
          >
            <VMProjectGitHub
              projectId={id}
              hostId={hostId}
              binding={binding}
              active={section === 'github' && windowVisible !== false}
              interactive={!!windowInteractive && section === 'github'}
              onNoticeChange={setGitHubNotice}
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
