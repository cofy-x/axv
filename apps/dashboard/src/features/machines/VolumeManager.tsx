import {
  Fragment,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
} from 'react';
import {
  Button,
  Checkbox,
  Field,
  IconButton,
  Select,
} from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { machinesApi } from './api';
import type { ComputeBinding, Machine, Operation, Volume } from './api';
import { integer, nameError, resourceBusy } from './form-state';
import { OperationFeedback, RequestDetails } from './OperationFeedback';
import { computeStore } from './compute-store';
import { OperationRequest, operationError } from './operation-requests';
import { humanize } from './presentation';
import type { useCompute } from './useCompute';
import { isProjectBackup, volumeActionAllowed } from './volume-policy';
import type { VolumePolicyAction } from './volume-policy';

type VolumeChange = Parameters<typeof machinesApi.volumeAction>[1];
type VolumeMenuAction = 'rename' | 'resize' | 'detach' | 'delete';

type VolumeInput = { binding: ComputeBinding } & (
  | { kind: 'create'; spec: Parameters<typeof machinesApi.createVolume>[0] }
  | { kind: 'action'; volume: Volume; change: VolumeChange }
  | { kind: 'delete'; volume: Volume }
);

type VolumeEditor = {
  kind: 'create' | 'copy' | 'rename' | 'resize' | 'retain';
  provider: string;
  hostId: string;
  volume?: Volume;
  machine?: Machine;
  name: string;
  size: string;
  retain: boolean;
};

export function VolumeManager({
  data,
  desktop,
  windowId,
  visible,
  onShowActivity,
}: {
  data: ReturnType<typeof useCompute>;
  desktop: DesktopAPI;
  windowId: string;
  visible: boolean;
  onShowActivity?: (id: string) => void;
}) {
  const [selectedVolume, selectVolume] = useState<{
    id: string;
    hostId: string;
  }>();
  const [editor, setEditor] = useState<VolumeEditor>();
  const [attachId, setAttach] = useState('');
  const [attachment, setAttachment] = useState('');
  const attachmentId = useId();
  const reveal = useCallback((element: HTMLElement | null) => {
    element?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  }, []);
  const revealAttachment = useCallback(
    (element: HTMLDivElement | null) => {
      if (!element) return;
      reveal(element);
      element
        .querySelector<HTMLSelectElement>('select:not(:disabled)')
        ?.focus({ preventScroll: true });
    },
    [reveal],
  );
  const [busy, setBusy] = useState(false);
  const [lastChanges, setLastChanges] = useState<Record<string, VolumeChange>>(
    {},
  );
  const [error, setError] = useState('');
  const [accepted, setAccepted] = useState<{
    operation: Operation;
    label: string;
    hostId: string;
    deletedVolumeId?: string;
  }>();
  const currentOperation =
    accepted &&
    (data.operations.find(
      (operation) => operation.id === accepted.operation.id,
    ) ||
      accepted.operation);
  const deleted =
    !!accepted?.deletedVolumeId &&
    currentOperation?.status === 'succeeded' &&
    data.info?.hostId === accepted.hostId &&
    !data.volumes.some((volume) => volume.id === accepted.deletedVolumeId);
  useEffect(() => {
    if (!deleted || !accepted) return;
    selectVolume((current) =>
      current?.id === accepted.deletedVolumeId &&
      current?.hostId === accepted.hostId
        ? undefined
        : current,
    );
    const clear = () =>
      setAccepted((current) => (current === accepted ? undefined : current));
    if (!visible) {
      clear();
      return;
    }
    const timer = setTimeout(clear, 5000);
    return () => clearTimeout(timer);
  }, [accepted, deleted, visible]);
  const [requests] = useState(
    () =>
      new OperationRequest<
        VolumeInput,
        { operation: Operation; volume?: Volume }
      >(async (input, id) => {
        if (input.kind === 'create')
          return machinesApi.createVolume(input.spec, id, input.binding);
        if (input.kind === 'action')
          return machinesApi.volumeAction(
            input.volume,
            input.change,
            id,
            input.binding,
          );
        return {
          operation: await machinesApi.deleteVolume(
            input.volume,
            id,
            input.binding,
          ),
        };
      }),
  );
  const hostId = data.info?.hostId || '';
  const selected =
    selectedVolume?.hostId === hostId
      ? data.volumes.find((volume) => volume.id === selectedVolume.id)
      : undefined;
  const resourceKey = (id: string, host = hostId) => JSON.stringify([host, id]);
  const selectedKey = selected && resourceKey(selected.id);
  const attachOpen = !!selectedKey && attachment === selectedKey;
  const showAttach =
    selected && !selected.machineId && volumeActionAllowed(selected, 'attach');
  const attached = data.machines.find(
    (machine) => machine.id === selected?.machineId,
  );
  const provider = data.info?.providers?.find(
    (item) => item.provider === selected?.provider,
  );
  const availableProvider = data.info?.providers?.find(
    (item) => item.available && item.volumes,
  );
  const createsVolumesWithVMs =
    !availableProvider &&
    data.info?.providers?.some(
      (item) => item.provider === 'firecracker' && item.persistentVolumes,
    );
  const locked = busy || !!requests.pending;
  const volumeBusy = (volume: Volume) =>
    !['ready', 'failed'].includes(volume.status) ||
    resourceBusy(
      data.operations,
      volume.id,
      volume.machineId,
      volume.pendingMachineId,
    );
  const writesBlocked = locked || !!data.error || !!editor;
  const disabled = writesBlocked || !selected || volumeBusy(selected);
  const supported = provider?.available && provider.volumes;
  const stopped =
    !selected?.machineId ||
    (!!attached &&
      attached.status === 'stopped' &&
      attached.desired === 'stopped' &&
      !attached.pendingSpec);
  const canAction = (action: VolumePolicyAction) =>
    !disabled &&
    !!supported &&
    !!selected &&
    volumeActionAllowed(selected, action) &&
    (selected?.status === 'ready' ||
      selected?.pendingAction === `${action}-volume`);
  const canDelete = (volume: Volume) =>
    !writesBlocked &&
    !volumeBusy(volume) &&
    !volume.machineId &&
    !volume.pendingMachineId;
  const showRowActions = createsVolumesWithVMs && data.volumes.some(canDelete);
  const menuEnabled = (action: VolumeMenuAction) =>
    action === 'delete'
      ? !!selected && canDelete(selected)
      : action === 'detach'
        ? canAction(action) && !!attached && stopped
        : canAction(action) && (action !== 'resize' || stopped);
  const eligible = data.machines.filter(
    (machine) =>
      machine.status === 'stopped' &&
      machine.desired === 'stopped' &&
      !machine.pendingSpec &&
      !machine.spec.volumeId &&
      machine.spec.provider === selected?.provider &&
      (machine.spec.kind !== 'development' || selected?.retain) &&
      !resourceBusy(data.operations, machine.id),
  );
  const targetId = selected?.pendingMachineId || attachId;
  const attachTarget = eligible.find((machine) => machine.id === targetId);
  const latestEditorVolume = data.volumes.find(
    (volume) => volume.id === editor?.volume?.id,
  );
  const latestEditorMachine = data.machines.find(
    (machine) => machine.id === editor?.machine?.id,
  );
  const editorChanged =
    !!editor?.volume &&
    (!latestEditorVolume ||
      editor.volume.revision !== latestEditorVolume.revision ||
      editor.machine?.revision !== latestEditorMachine?.revision);
  const editorProvider = data.info?.providers?.find(
    (item) => item.provider === editor?.provider,
  );
  const minimumSize = editor?.volume
    ? editor.volume.pendingSizeGiB || editor.volume.sizeGiB
    : 1;
  const editorReason = !editor
    ? ''
    : (editor.hostId !== hostId
        ? 'This draft belongs to another VM host. Cancel it and open a new form for the current host.'
        : '') ||
      data.error ||
      (latestEditorVolume &&
      editor.kind !== 'create' &&
      !volumeActionAllowed(latestEditorVolume, editor.kind)
        ? 'This protected project volume cannot use this action. Use the backup and restore workflow in Project Files.'
        : '') ||
      (!editorProvider?.available || !editorProvider.volumes
        ? editorProvider?.reason || 'This volume provider is unavailable.'
        : editorChanged
          ? 'The source or target changed. Review its current revision before applying this draft.'
          : editor.volume &&
              resourceBusy(
                data.operations,
                editor.volume.id,
                editor.volume.machineId,
              )
            ? 'Wait for the active volume or VM operation to finish.'
            : editor.kind === 'rename' ||
                editor.kind === 'create' ||
                editor.kind === 'copy'
              ? nameError(editor.name)
              : '') ||
      (['create', 'copy', 'resize'].includes(editor.kind) &&
      !integer(editor.size, minimumSize, data.info?.capacity?.diskGiB)
        ? `Enter a whole disk size of at least ${minimumSize} GiB within the host capacity.`
        : '');

  function openEditor(kind: VolumeEditor['kind'], retain?: boolean) {
    if (
      kind !== 'create' &&
      (!selected || !volumeActionAllowed(selected, kind))
    )
      return;
    const previous =
      selected?.status === 'failed' &&
      selected.pendingAction === `${kind}-volume`
        ? lastChanges[resourceKey(selected.id)]
        : undefined;
    setEditor({
      kind,
      hostId,
      provider:
        kind === 'create'
          ? availableProvider?.provider || ''
          : selected!.provider,
      ...(kind !== 'create'
        ? {
            volume: structuredClone(selected),
            machine: attached ? structuredClone(attached) : undefined,
          }
        : {}),
      name:
        kind === 'copy'
          ? `${selected?.name || 'Project'} copy`
          : kind === 'create'
            ? 'Project volume'
            : previous?.name || selected?.name || '',
      size: String(
        kind === 'create'
          ? 4
          : selected?.pendingSizeGiB || selected?.sizeGiB || 4,
      ),
      retain: retain ?? previous?.retain ?? selected?.retain ?? true,
    });
    setError('');
  }

  async function run(input?: VolumeInput, label?: string) {
    const originalInput = input || requests.pending?.input;
    const latest = computeStore.getSnapshot();
    if (
      !latest.info?.hostId ||
      originalInput?.binding.hostId !== latest.info.hostId
    ) {
      setError(
        'This request belongs to another VM host. Reconnect to its original host to resolve it.',
      );
      return;
    }
    if (latest.error) {
      setError('Reconnect to the host before making changes.');
      return;
    }
    if (input) {
      const sourceId =
        input.kind === 'create' ? input.spec.sourceVolumeId : input.volume.id;
      const action =
        input.kind === 'create'
          ? 'copy'
          : input.kind === 'delete'
            ? 'delete'
            : input.change.action;
      const source = latest.volumes.find((volume) => volume.id === sourceId);
      if (source && !volumeActionAllowed(source, action)) {
        setError(
          'This protected project volume cannot use this action. Use the backup and restore workflow in Project Files.',
        );
        return;
      }
    }
    setBusy(true);
    setError('');
    const requestLabel = label || requests.pending?.label || 'Volume operation';
    try {
      const result = input
        ? await requests.submit(input, requestLabel)
        : await requests.retry();
      setAccepted({
        operation: result.operation,
        label: requestLabel,
        hostId: latest.info.hostId,
        deletedVolumeId:
          originalInput.kind === 'delete' ? originalInput.volume.id : undefined,
      });
      if (originalInput?.kind === 'action')
        setLastChanges((current) => ({
          ...current,
          [resourceKey(originalInput.volume.id, originalInput.binding.hostId)]:
            originalInput.change,
        }));
      if (
        originalInput?.kind === 'action' &&
        originalInput.change.action === 'attach'
      )
        setAttachment('');
      if (result.volume)
        selectVolume({ id: result.volume.id, hostId: latest.info.hostId });
      setEditor(undefined);
      data.refresh();
    } catch (failure) {
      setError(operationError(failure));
    } finally {
      setBusy(false);
    }
  }

  function applyEditor() {
    if (!editor || editorReason) return;
    if (editor.kind === 'create' || editor.kind === 'copy') {
      void run(
        {
          kind: 'create',
          binding: { hostId: editor.hostId },
          spec: {
            name: editor.name.trim(),
            provider: editor.provider,
            sizeGiB: Number(editor.size),
            retain: editor.retain,
            ...(editor.volume ? { sourceVolumeId: editor.volume.id } : {}),
          },
        },
        `${editor.kind === 'copy' ? 'Copy' : 'Create'} “${editor.name.trim()}”`,
      );
    } else {
      const change: VolumeChange = {
        action: editor.kind,
        ...(editor.kind === 'rename'
          ? { name: editor.name.trim() }
          : editor.kind === 'resize'
            ? {
                sizeGiB: Number(editor.size),
                machineRevision: editor.machine?.revision,
              }
            : { retain: editor.retain }),
      };
      void run(
        {
          kind: 'action',
          volume: editor.volume!,
          change,
          binding: { hostId: editor.hostId },
        },
        `${editor.kind === 'rename' ? 'Rename' : editor.kind === 'resize' ? 'Grow' : 'Change retention for'} “${editor.volume!.name}”`,
      );
    }
  }

  async function confirmAction(action: 'delete' | 'detach', volume: Volume) {
    const answer = await desktop.dialog({
      title:
        action === 'delete'
          ? 'Delete project volume?'
          : 'Detach project volume?',
      message:
        action === 'delete'
          ? `Permanently delete “${volume.name || volume.id}” and all of its files?`
          : `Detach this disk from “${attached?.spec.name}”? Its data stays on the volume. The VM needs a project volume before it can start again.`,
      buttons: [
        { label: 'Cancel', value: 'cancel', default: true, cancel: true },
        {
          label: action === 'delete' ? 'Delete Volume' : 'Detach',
          value: 'continue',
        },
      ],
    });
    if (answer?.button !== 'continue') return;
    if (action === 'delete')
      await run(
        { kind: 'delete', volume, binding: { hostId } },
        `Delete “${volume.name}”`,
      );
    else if (attached)
      await run(
        {
          kind: 'action',
          binding: { hostId },
          volume,
          change: {
            action: 'detach',
            machineId: attached.id,
            machineRevision: attached.revision,
          },
        },
        `Detach “${volume.name}”`,
      );
  }

  const actions = {
    hostId,
    selected,
    volumes: data.volumes,
    canDelete,
    menuEnabled,
    openEditor,
    confirmAction,
  };
  const current = useRef(actions);
  current.current = actions;

  function openVolumeActions(anchor: HTMLButtonElement, volume: Volume) {
    const rect = anchor.getBoundingClientRect();
    desktop.contextMenu(
      [
        {
          label: 'Delete',
          action: () => {
            const latest = current.current;
            const target = latest.volumes.find((item) => item.id === volume.id);
            if (
              latest.hostId === hostId &&
              target &&
              target.revision === volume.revision &&
              latest.canDelete(target)
            )
              void latest.confirmAction('delete', target);
          },
        },
      ],
      { x: rect.right, y: rect.bottom },
      {
        ownerWindowId: windowId,
        align: 'end',
        title: volume.name || volume.id,
      },
    );
  }

  const volumeFeedback = (
    <>
      {error && (
        <p className="machines-error" role="alert">
          {error}
        </p>
      )}
      {deleted ? (
        <p className="machines-message" role="status">
          Volume deleted.
        </p>
      ) : (
        <OperationFeedback
          accepted={
            accepted?.hostId === data.info?.hostId ? accepted : undefined
          }
          operations={data.operations}
          onShowActivity={onShowActivity}
        />
      )}
      {requests.pending && !busy && (
        <div className="machines-operation-feedback">
          <p>
            The response to {requests.pending.label} was interrupted. Resolve
            that exact request before making another change.
          </p>
          {requests.pending.input.binding.hostId !== hostId && (
            <p>Reconnect to the original VM host to resolve this request.</p>
          )}
          <Button
            disabled={
              !!data.error || requests.pending.input.binding.hostId !== hostId
            }
            onClick={() => void run()}
          >
            Retry Original
          </Button>
          <RequestDetails
            requestId={requests.pending.requestId}
            hostId={requests.pending.input.binding.hostId}
          />
        </div>
      )}
    </>
  );
  const selection = selected && !createsVolumesWithVMs && (
    <>
      <div className="machines-volume-controls">
        {showAttach && (
          <Button
            aria-label={
              attachOpen
                ? `Close attachment picker for ${selected.name || selected.id}`
                : `Attach ${selected.name || selected.id}`
            }
            aria-expanded={attachOpen}
            aria-controls={attachOpen ? attachmentId : undefined}
            disabled={!attachOpen && !canAction('attach')}
            onClick={() => {
              setAttach('');
              setAttachment(attachOpen ? '' : resourceKey(selected.id));
            }}
          >
            {attachOpen ? 'Close' : 'Attach'}
          </Button>
        )}
        <Button
          disabled={
            disabled ||
            !supported ||
            !volumeActionAllowed(selected, 'copy') ||
            selected.status !== 'ready' ||
            !stopped ||
            !!selected.pendingSizeGiB
          }
          onClick={() => openEditor('copy')}
        >
          Copy
        </Button>
        <IconButton
          icon="more"
          label="More volume actions"
          aria-haspopup="menu"
          onClick={(event) => {
            const targetHost = hostId;
            const targetId = selected.id;
            const rect = event.currentTarget.getBoundingClientRect();
            const invoke = (action: VolumeMenuAction) => {
              const latest = current.current;
              if (
                latest.hostId !== targetHost ||
                latest.selected?.id !== targetId ||
                !latest.menuEnabled(action)
              )
                return;
              if (action === 'delete' || action === 'detach')
                void latest.confirmAction(action, latest.selected);
              else latest.openEditor(action);
            };
            desktop.contextMenu(
              [
                {
                  label: 'Rename',
                  disabled: !menuEnabled('rename'),
                  action: () => invoke('rename'),
                },
                {
                  label: 'Grow',
                  disabled: !menuEnabled('resize'),
                  action: () => invoke('resize'),
                },
                {
                  label: 'Detach',
                  disabled: !menuEnabled('detach'),
                  action: () => invoke('detach'),
                },
                { separator: true },
                {
                  label: 'Delete',
                  disabled: !menuEnabled('delete'),
                  action: () => invoke('delete'),
                },
              ],
              { x: rect.left, y: rect.bottom },
              {
                ownerWindowId: windowId,
                title: selected.name || selected.id,
              },
            );
          }}
        />
      </div>
      {volumeFeedback}
      {!supported && (
        <p className="machines-warning">
          {provider?.reason ||
            'This provider is unavailable or does not support volume changes.'}
        </p>
      )}
      {!stopped && (
        <p className="machines-form-help">
          Stop the attached VM and finish its pending configuration before
          copying, growing or detaching this volume.
        </p>
      )}
      {isProjectBackup(selected) && (
        <p className="machines-form-help">
          Restore this backup in Project Files to use its files in a VM.
        </p>
      )}
      {selected.restoredFrom && (
        <p className="machines-form-help">
          Use Project Files to back up this working volume.
        </p>
      )}
      {selected.status === 'failed' && (
        <p className="machines-warning">
          {selected.pendingAction === 'create-volume' ||
          selected.pendingAction === 'copy-volume'
            ? 'Remove this incomplete detached volume, then create a new volume or copy.'
            : `Retry the failed ${selected.pendingAction?.replace('-volume', '') || 'volume'} action. Other changes remain unavailable until it is resolved.`}
        </p>
      )}
      <div className="machines-volume-retention">
        <Checkbox
          label="Keep this volume after VM deletion"
          checked={selected.retain}
          disabled={
            !canAction('retain') || attached?.spec.kind === 'development'
          }
          onChange={(event) => openEditor('retain', event.target.checked)}
        />
        {selected.status === 'failed' &&
          selected.pendingAction === 'retain-volume' && (
            <Button
              disabled={!canAction('retain')}
              onClick={() => openEditor('retain')}
            >
              Retry Retention
            </Button>
          )}
      </div>
      {showAttach && attachOpen && (
        <div
          className="machines-form machines-volume-attachment"
          id={attachmentId}
          ref={revealAttachment}
        >
          {!eligible.length && !targetId ? (
            <p className="machines-form-help">
              No compatible stopped VM without a project volume.
            </p>
          ) : (
            <fieldset disabled={!canAction('attach')}>
              <legend>Attach to a stopped VM</legend>
              <Select
                label="VM without a project volume"
                value={targetId}
                disabled={!!selected.pendingMachineId}
                onChange={(event) => setAttach(event.target.value)}
              >
                <option value="">Choose a compatible stopped VM</option>
                {targetId && !attachTarget && (
                  <option value={targetId}>Selected VM unavailable</option>
                )}
                {eligible.map((machine) => (
                  <option key={machine.id} value={machine.id}>
                    {machine.spec.name}
                  </option>
                ))}
              </Select>
              {targetId && !attachTarget && (
                <p className="machines-warning">
                  The selected VM is no longer eligible. A pending attachment
                  must be retried against its original VM after that VM is
                  stopped and available.
                </p>
              )}
              <Button
                disabled={!attachTarget}
                onClick={() => {
                  if (attachTarget)
                    void run(
                      {
                        kind: 'action',
                        binding: { hostId },
                        volume: selected,
                        change: {
                          action: 'attach',
                          machineId: attachTarget.id,
                          machineRevision: attachTarget.revision,
                        },
                      },
                      `Attach “${selected.name}” to “${attachTarget.spec.name}”`,
                    );
                }}
              >
                Attach
              </Button>
            </fieldset>
          )}
        </div>
      )}
    </>
  );

  return (
    <div>
      {!createsVolumesWithVMs && (
        <div className="app-toolbar machines-toolbar">
          <Button
            disabled={locked || !!editor || !!data.error || !availableProvider}
            onClick={() => openEditor('create')}
          >
            New Volume
          </Button>
        </div>
      )}
      {!selection && volumeFeedback}
      {!availableProvider && !createsVolumesWithVMs && (
        <p className="machines-message">
          {data.info
            ? 'An available provider with project-volume support is required to create storage.'
            : 'Checking host storage support…'}
        </p>
      )}
      {editor && (
        <form
          ref={reveal}
          className="app-pad machines-form machines-volume-editor"
          onSubmit={(event) => {
            event.preventDefault();
            applyEditor();
          }}
        >
          <fieldset disabled={locked}>
            <legend>
              {editor.kind === 'create'
                ? 'New project volume'
                : `${editor.kind === 'copy' ? 'Copy' : editor.kind === 'rename' ? 'Rename' : editor.kind === 'resize' ? 'Grow' : 'Retention for'} “${editor.volume?.name || editor.volume?.id}”`}
            </legend>
            {['create', 'copy', 'rename'].includes(editor.kind) && (
              <Field
                label="Volume name"
                value={editor.name}
                required
                maxLength={80}
                onChange={(event) =>
                  setEditor({ ...editor, name: event.target.value })
                }
              />
            )}
            {['create', 'copy', 'resize'].includes(editor.kind) && (
              <>
                <Field
                  label={
                    editor.kind === 'resize' ? 'New size (GiB)' : 'Size (GiB)'
                  }
                  type="number"
                  min={minimumSize}
                  max={data.info?.capacity?.diskGiB}
                  value={editor.size}
                  required
                  onChange={(event) =>
                    setEditor({ ...editor, size: event.target.value })
                  }
                />
                <p className="machines-form-help">
                  {editor.kind === 'resize'
                    ? 'Disk space shares the host budget. Disks cannot shrink; an attached VM must be stopped before growth. Its filesystem grows on the next boot.'
                    : 'Disk space shares the host budget. Volumes cannot shrink.'}
                </p>
              </>
            )}
            {['create', 'copy', 'retain'].includes(editor.kind) && (
              <Checkbox
                label="Keep after VM deletion"
                checked={editor.retain}
                onChange={(event) =>
                  setEditor({ ...editor, retain: event.target.checked })
                }
              />
            )}
            {editorReason && <p className="machines-warning">{editorReason}</p>}
            {editorChanged &&
              latestEditorVolume &&
              editor.hostId === hostId && (
                <Button
                  onClick={() =>
                    setEditor({
                      ...editor,
                      volume: structuredClone(latestEditorVolume),
                      machine: latestEditorMachine
                        ? structuredClone(latestEditorMachine)
                        : undefined,
                    })
                  }
                >
                  Use Latest
                </Button>
              )}
            <div className="machines-form-actions">
              <Button type="submit" disabled={!!editorReason}>
                {editor.kind === 'copy'
                  ? 'Create Copy'
                  : editor.kind === 'create'
                    ? 'Create'
                    : editor.kind === 'rename'
                      ? 'Rename'
                      : editor.kind === 'resize'
                        ? 'Grow'
                        : 'Apply'}
              </Button>
              <Button onClick={() => setEditor(undefined)}>Cancel</Button>
            </div>
          </fieldset>
        </form>
      )}
      {data.volumes.length ? (
        <table className="machines-table machines-inventory-table">
          <colgroup>
            <col />
            <col className="machines-inventory-secondary-column" />
            {showRowActions && (
              <col className="machines-volume-actions-column" />
            )}
          </colgroup>
          <thead>
            <tr>
              <th scope="col">Project volume</th>
              <th scope="col">Attached to</th>
              {showRowActions && <th scope="col" aria-label="Volume actions" />}
            </tr>
          </thead>
          <tbody>
            {data.volumes.map((volume) => {
              const backup = isProjectBackup(volume);
              const sourceId =
                volume.restoredFrom || volume.backup?.sourceVolumeId;
              const sourceName = sourceId
                ? data.volumes.find((item) => item.id === sourceId)?.name
                : undefined;
              const machineName = data.machines.find(
                (machine) => machine.id === volume.machineId,
              )?.spec.name;
              return (
                <Fragment key={volume.id}>
                  <tr data-selected={selected === volume}>
                    <td>
                      {createsVolumesWithVMs ? (
                        <strong>{volume.name || volume.id}</strong>
                      ) : (
                        <button
                          className="machines-select"
                          aria-pressed={selected?.id === volume.id}
                          onClick={() => {
                            if (selected === volume) return;
                            selectVolume({ id: volume.id, hostId });
                            setAttach('');
                            setAttachment('');
                          }}
                        >
                          {volume.name || volume.id}
                        </button>
                      )}
                      <small>
                        {volume.sizeGiB} GiB ·{' '}
                        <span>
                          {backup
                            ? 'Immutable backup'
                            : volume.retain
                              ? 'Retained'
                              : 'Delete with VM'}
                        </span>
                      </small>
                      {backup && sourceName && (
                        <small>Backup of {sourceName}</small>
                      )}
                      {backup && createsVolumesWithVMs && !data.error && (
                        <small>Restore in Project Files</small>
                      )}
                      {volume.restoredFrom && (
                        <small>
                          {sourceName
                            ? `Restored from ${sourceName}`
                            : 'Restored working volume'}
                        </small>
                      )}
                      {volume.status !== 'ready' && (
                        <small>{humanize(volume.status)}</small>
                      )}
                      {volume.pendingSizeGiB && (
                        <small>Pending {volume.pendingSizeGiB} GiB</small>
                      )}
                      {volume.transientGiB && (
                        <small>
                          {volume.transientGiB} GiB reserved for temporary
                          copies
                        </small>
                      )}
                      {volume.reason && (
                        <small className="machines-failure">
                          {volume.reason}
                        </small>
                      )}
                      {volume.status === 'failed' &&
                        volume.pendingMachineId && (
                          <small>
                            Retry the pending attachment before deleting this
                            volume or VM.
                          </small>
                        )}
                    </td>
                    <td>
                      {backup
                        ? '—'
                        : machineName || volume.machineId || 'Detached'}
                    </td>
                    {showRowActions && (
                      <td className="machines-inventory-action machines-volume-actions-cell">
                        {canDelete(volume) && (
                          <IconButton
                            icon="more"
                            label={`Volume actions for ${volume.name || volume.id}`}
                            aria-haspopup="menu"
                            onClick={(event) =>
                              openVolumeActions(event.currentTarget, volume)
                            }
                          />
                        )}
                      </td>
                    )}
                  </tr>
                  {selected === volume && selection && (
                    <tr ref={reveal} className="machines-volume-selection">
                      <td colSpan={2}>{selection}</td>
                    </tr>
                  )}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      ) : !editor && !data.loading && !data.error ? (
        <div className="machines-empty-status">
          <h2>No project volumes yet</h2>
          {createsVolumesWithVMs && <p>Create a VM to add a project volume.</p>}
        </div>
      ) : null}
      {selectedVolume &&
        !selected &&
        !(
          deleted &&
          selectedVolume.id === accepted?.deletedVolumeId &&
          selectedVolume.hostId === accepted.hostId
        ) && (
          <p className="machines-warning">
            The selected volume is no longer present. Select another volume
            explicitly.
          </p>
        )}
    </div>
  );
}
