import { useRef, useState } from 'react';
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
import type { useCompute } from './useCompute';

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
  onShowActivity,
}: {
  data: ReturnType<typeof useCompute>;
  desktop: DesktopAPI;
  onShowActivity?: (id: string) => void;
}) {
  const [selectedId, setSelected] = useState('');
  const [selectedHost, setSelectedHost] = useState('');
  const [editor, setEditor] = useState<VolumeEditor>();
  const [attachId, setAttach] = useState('');
  const [busy, setBusy] = useState(false);
  const [lastChanges, setLastChanges] = useState<Record<string, VolumeChange>>(
    {},
  );
  const [error, setError] = useState('');
  const [accepted, setAccepted] = useState<{
    operation: Operation;
    label: string;
  }>();
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
    selectedHost === hostId
      ? data.volumes.find((volume) => volume.id === selectedId)
      : undefined;
  const resourceKey = (id: string, host = hostId) => JSON.stringify([host, id]);
  const attached = data.machines.find(
    (machine) => machine.id === selected?.machineId,
  );
  const provider = data.info?.providers?.find(
    (item) => item.provider === selected?.provider,
  );
  const availableProvider = data.info?.providers?.find(
    (item) => item.available && item.volumes,
  );
  const locked = busy || !!requests.pending;
  const selectedBusy =
    !selected ||
    !['ready', 'failed'].includes(selected.status) ||
    resourceBusy(
      data.operations,
      selected.id,
      selected.machineId,
      selected.pendingMachineId,
    );
  const disabled = locked || !!data.error || selectedBusy || !!editor;
  const supported = provider?.available && provider.volumes;
  const stopped =
    !selected?.machineId ||
    (!!attached &&
      attached.status === 'stopped' &&
      attached.desired === 'stopped' &&
      !attached.pendingSpec);
  const canAction = (action: string) =>
    !disabled &&
    !!supported &&
    (selected?.status === 'ready' ||
      selected?.pendingAction === `${action}-volume`);
  const menuEnabled = (action: VolumeMenuAction) =>
    action === 'delete'
      ? !disabled && !selected?.machineId && !selected?.pendingMachineId
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
    setBusy(true);
    setError('');
    const requestLabel = label || requests.pending?.label || 'Volume operation';
    try {
      const result = input
        ? await requests.submit(input, requestLabel)
        : await requests.retry();
      setAccepted({ operation: result.operation, label: requestLabel });
      if (originalInput?.kind === 'action')
        setLastChanges((current) => ({
          ...current,
          [resourceKey(originalInput.volume.id, originalInput.binding.hostId)]:
            originalInput.change,
        }));
      if (result.volume) {
        setSelected(result.volume.id);
        setSelectedHost(originalInput.binding.hostId || '');
      }
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

  async function confirmAction(action: 'delete' | 'detach') {
    if (!selected) return;
    const answer = await desktop.dialog({
      title:
        action === 'delete'
          ? 'Delete project volume?'
          : 'Detach project volume?',
      message:
        action === 'delete'
          ? `Permanently delete “${selected.name || selected.id}” and all of its files?`
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
        { kind: 'delete', volume: selected, binding: { hostId } },
        `Delete “${selected.name}”`,
      );
    else if (attached)
      await run(
        {
          kind: 'action',
          binding: { hostId },
          volume: selected,
          change: {
            action: 'detach',
            machineId: attached.id,
            machineRevision: attached.revision,
          },
        },
        `Detach “${selected.name}”`,
      );
  }

  const current = useRef({
    hostId,
    selected,
    menuEnabled,
    openEditor,
    confirmAction,
  });
  current.current = {
    hostId,
    selected,
    menuEnabled,
    openEditor,
    confirmAction,
  };

  return (
    <div>
      <div className="app-toolbar machines-toolbar">
        <Button
          disabled={locked || !!editor || !!data.error || !availableProvider}
          onClick={() => openEditor('create')}
        >
          New Volume
        </Button>
        <Button
          disabled={
            disabled ||
            !supported ||
            selected?.status !== 'ready' ||
            !stopped ||
            !!selected?.pendingSizeGiB
          }
          onClick={() => openEditor('copy')}
        >
          Copy
        </Button>
        <IconButton
          icon="more"
          label="More volume actions"
          aria-haspopup="menu"
          disabled={!selected}
          onClick={(event) => {
            if (!selected) return;
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
                void latest.confirmAction(action);
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
              { title: selected.name || selected.id },
            );
          }}
        />
      </div>
      {error && (
        <p className="machines-error" role="alert">
          {error}
        </p>
      )}
      <OperationFeedback
        accepted={accepted}
        operations={data.operations}
        onShowActivity={onShowActivity}
      />
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
            Retry Original Request
          </Button>
          <RequestDetails
            requestId={requests.pending.requestId}
            hostId={requests.pending.input.binding.hostId}
          />
        </div>
      )}
      {!availableProvider && (
        <p className="machines-form-help">
          {data.info
            ? 'An available provider with project-volume support is required to create storage.'
            : 'Checking host storage support…'}
        </p>
      )}
      {selected && !supported && (
        <p className="machines-warning">
          {provider?.reason ||
            'This provider is unavailable or does not support volume changes.'}
        </p>
      )}
      {selected && !stopped && (
        <p className="machines-form-help">
          Stop the attached VM and finish its pending configuration before
          copying, growing or detaching this volume.
        </p>
      )}
      {selected?.status === 'failed' && (
        <p className="machines-warning">
          {selected.pendingAction === 'create-volume' ||
          selected.pendingAction === 'copy-volume'
            ? 'Remove this incomplete detached volume, then create a new volume or copy.'
            : `Retry the failed ${selected.pendingAction?.replace('-volume', '') || 'volume'} action. Other changes remain unavailable until it is resolved.`}
        </p>
      )}
      {editor && (
        <form
          className="app-pad machines-form"
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
                  Disk space shares the host budget. Disks cannot shrink; an
                  attached VM must be stopped before growth. Its filesystem
                  grows on the next boot.
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
                    ? 'Create Volume'
                    : editor.kind === 'rename'
                      ? 'Rename Volume'
                      : editor.kind === 'resize'
                        ? 'Grow Volume'
                        : 'Apply'}
              </Button>
              <Button onClick={() => setEditor(undefined)}>Cancel</Button>
            </div>
          </fieldset>
        </form>
      )}
      <table className="machines-table">
        <thead>
          <tr>
            <th>Project volume</th>
            <th>Size / state</th>
            <th>Attachment</th>
          </tr>
        </thead>
        <tbody>
          {data.volumes.map((volume) => (
            <tr key={volume.id} data-selected={selected?.id === volume.id}>
              <td>
                <button
                  className="machines-select"
                  aria-pressed={selected?.id === volume.id}
                  onClick={() => {
                    setSelected(volume.id);
                    setSelectedHost(hostId);
                    setAttach('');
                  }}
                >
                  {volume.name || volume.id}
                </button>
                <details>
                  <summary>Details</summary>
                  <code>{volume.id}</code>
                </details>
                <small>
                  {volume.retain ? 'Retained' : 'Delete with attached VM'}
                </small>
              </td>
              <td>
                {volume.sizeGiB} GiB<small>{volume.status}</small>
                {volume.pendingSizeGiB && (
                  <small>Pending {volume.pendingSizeGiB} GiB</small>
                )}
                {volume.transientGiB && (
                  <small>
                    {volume.transientGiB} GiB reserved for temporary copies
                  </small>
                )}
                {volume.reason && (
                  <small className="machines-failure">{volume.reason}</small>
                )}
                {volume.status === 'failed' && volume.pendingMachineId && (
                  <small>
                    Retry the pending attachment before deleting this volume or
                    VM.
                  </small>
                )}
              </td>
              <td>
                {volume.machineId
                  ? data.machines.find(
                      (machine) => machine.id === volume.machineId,
                    )?.spec.name || volume.machineId
                  : 'Detached'}
              </td>
            </tr>
          ))}
          {!data.volumes.length && (
            <tr>
              <td colSpan={3}>No project volumes.</td>
            </tr>
          )}
        </tbody>
      </table>
      {selectedId && !selected && (
        <p className="machines-warning">
          The selected volume is no longer present. Select another volume
          explicitly.
        </p>
      )}
      {selected && (
        <div className="app-pad">
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
      )}
      {selected && !selected.machineId && (
        <div className="app-pad machines-form">
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
                The selected VM is no longer eligible. A pending attachment must
                be retried against its original VM after that VM is stopped and
                available.
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
              Attach Volume
            </Button>
          </fieldset>
        </div>
      )}
      <p className="app-pad machines-form-help">
        A project volume attaches to one VM at a time. Development VMs require
        retained storage. Retained storage is not a backup.
      </p>
    </div>
  );
}
