import { useEffect, useState, useSyncExternalStore } from 'react';
import {
  Button,
  Field,
  IconButton,
  PixelIcon,
  Select,
  StatusBar,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useCompute } from './useCompute';
import { RequestDetails } from './OperationFeedback';
import { guestTerminalRequests, openGuestTerminal } from './open-terminal';
import {
  confirmVMAction,
  machineActionRequests,
  machineActionReason,
} from './lifecycle';
import type { VMAction } from './lifecycle';
import { computeStore } from './compute-store';
import { MachineActions } from './MachineActions';
import { openMachineInspector } from './desktop-icons';
import { VolumeManager } from './VolumeManager';
import { ImageManager } from './ImageManager';
import { MachineEnvironment } from './MachineEnvironment';
import { preparationRunning } from './preparation-store';
import {
  guestTerminalReason,
  humanize,
  machineGuidance,
  operationTarget,
} from './presentation';

export function Machines({ window, desktop }: AppWindowProps) {
  const { setTitle } = desktop;
  const data = useCompute();
  const tab =
    (window.viewState.vmTab as
      'machines' | 'volumes' | 'images' | 'operations') || 'machines';
  const setTab = (value: string) =>
    desktop.updateWindowState(window.id, { vmTab: value });
  const [busy, setBusy] = useState(false);
  const [operationId, setOperationId] = useState('');
  const [error, setError] = useState('');
  const [search, setSearch] = useState('');
  const [stateFilter, setStateFilter] = useState('all');
  const [showHost, setShowHost] = useState(false);

  useEffect(
    () => setTitle(window.id, 'Virtual Machines'),
    [setTitle, window.id],
  );

  const visible = data.machines.filter(
    (machine) =>
      `${machine.spec.name} ${machine.id} ${machine.spec.provider}`
        .toLowerCase()
        .includes(search.toLowerCase()) &&
      (stateFilter === 'all' || machine.status === stateFilter),
  );
  const selected = visible.find(
    (machine) =>
      machine.id === window.viewState.machineId &&
      machine.hostId === window.viewState.machineHostId,
  );
  const pending = useSyncExternalStore(machineActionRequests.subscribe, () =>
    machineActionRequests.getPending(
      selected?.hostId || '',
      selected?.id || '',
    ),
  );
  const configured =
    data.preparation.check?.configured ||
    data.info?.configured ||
    data.environment?.configured ||
    data.machines.length > 0;
  const provider = data.info?.providers?.find(
    (item) => item.provider === selected?.spec.provider,
  );
  const availableProviders =
    data.info?.providers?.filter((item) => item.available) || [];
  const availableImages =
    data.info?.images?.filter(
      (item) =>
        !item.status || item.status === 'ready' || item.status === 'configured',
    ) || [];
  const canCreate =
    !!data.info?.configured &&
    !data.error &&
    availableProviders.some((item) =>
      availableImages.some((image) => image.provider === item.provider),
    );
  const operation = data.operations.find((item) => item.id === operationId);
  const preparing = preparationRunning(data.preparation.job);

  useEffect(() => {
    if (preparing) setShowHost(true);
  }, [preparing]);

  const actionReason = (action: VMAction) =>
    !selected
      ? 'Select a VM first.'
      : busy
        ? 'Waiting for the current request.'
        : pending
          ? 'Resolve the original request before changing this VM.'
          : data.error
            ? 'Refresh the host connection before changing a VM.'
            : machineActionReason(
                selected,
                action,
                data.operations,
                data.volumes,
                provider,
              );
  const terminalReason = !selected
    ? 'Select a VM first.'
    : busy || data.error || pending
      ? 'Wait for the host connection.'
      : guestTerminalReason(selected) ||
        (data.operations.some(
          (op) =>
            (op.machineId === selected.id || op.locks?.includes(selected.id)) &&
            ['queued', 'running'].includes(op.status),
        )
          ? 'Wait for the current VM operation.'
          : undefined);

  function showActivity(id?: string) {
    desktop.updateWindowState(window.id, {
      vmTab: 'operations',
      operationId: id || '',
    });
  }

  function inspect() {
    if (selected) openMachineInspector(desktop, selected);
  }

  async function run(action: () => Promise<void>) {
    setBusy(true);
    setError('');
    try {
      await action();
      data.refresh();
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }

  async function lifecycle(action: VMAction, retry = false) {
    const target = retry ? pending?.machine : selected;
    if (
      !target ||
      data.error ||
      data.info?.hostId !== target.hostId ||
      (!retry && actionReason(action))
    )
      return;
    if (!retry && !(await confirmVMAction(desktop, target, action))) return;
    const latest = computeStore.getSnapshot();
    if (latest.error || latest.info?.hostId !== target.hostId) return;
    await run(async () => {
      const result = await machineActionRequests.execute(target, action);
      setOperationId(result.operation.id);
    });
  }

  async function terminal() {
    if (!selected || terminalReason) return;
    await run(async () => {
      const session = await guestTerminalRequests.execute(
        selected.id,
        selected.hostId,
      );
      openGuestTerminal(desktop, selected, session);
    });
  }

  return (
    <div className="machines-app">
      <div className="app-toolbar machines-toolbar">
        {configured && (
          <Button
            disabled={!canCreate || busy}
            title={
              !canCreate
                ? 'An available provider and a ready image are required.'
                : undefined
            }
            onClick={() => desktop.openApp('machine-create')}
          >
            New VM
          </Button>
        )}
        {configured && (
          <Button
            aria-pressed={showHost}
            onClick={() => setShowHost(!showHost)}
          >
            Environment
          </Button>
        )}
        <IconButton
          icon="refresh"
          label="Refresh"
          disabled={data.refreshing}
          onClick={data.refresh}
        />
        {data.refreshing && (
          <span className="fine-print" role="status">
            Refreshing…
          </span>
        )}
      </div>
      {(error || (data.error && !preparing)) && (
        <div className="machines-error" role="alert">
          {data.error && <strong>VM service unavailable. </strong>}
          {error || data.error}
          {data.lastUpdated && data.error && (
            <span>
              {' '}
              Showing the last known state from{' '}
              {new Date(data.lastUpdated).toLocaleTimeString()}.
            </span>
          )}
        </div>
      )}
      {pending?.uncertain && !busy && (
        <div className="machines-operation-feedback" role="status">
          <span>
            The response to {humanize(pending.action)} for “
            {pending.machine.spec.name}” is unresolved. Retry the original
            request before making another change.
          </span>
          <Button
            disabled={
              !!data.error || data.info?.hostId !== pending.machine.hostId
            }
            onClick={() => void lifecycle(pending.action, true)}
          >
            Retry Original Request
          </Button>
          <RequestDetails
            requestId={pending.requestId}
            hostId={pending.machine.hostId}
          />
        </div>
      )}
      {!configured ? (
        <div className="machines-scroll">
          <MachineEnvironment
            data={data}
            onShowMachines={() => setShowHost(false)}
          />
        </div>
      ) : (
        <>
          <div className="machines-tabs" aria-label="VM views">
            {(['machines', 'volumes', 'images', 'operations'] as const).map(
              (name) => (
                <Button
                  key={name}
                  aria-pressed={tab === name}
                  onClick={() => {
                    setTab(name);
                    setShowHost(false);
                  }}
                >
                  {name === 'machines'
                    ? 'VMs'
                    : name === 'volumes'
                      ? 'Project Volumes'
                      : name === 'images'
                        ? 'Images'
                        : 'Activity'}
                </Button>
              ),
            )}
          </div>
          <div className="machines-scroll">
            <div hidden={!showHost} inert={!showHost}>
              <MachineEnvironment
                data={data}
                onShowMachines={() => setShowHost(false)}
              />
            </div>
            <div
              hidden={showHost || tab !== 'machines'}
              inert={showHost || tab !== 'machines'}
            >
              {data.machines.length > 0 && (
                <>
                  <div className="machines-filter">
                    <Field
                      label="Search VMs"
                      value={search}
                      onChange={(event) => setSearch(event.target.value)}
                    />
                    <Select
                      label="State"
                      value={stateFilter}
                      onChange={(event) => setStateFilter(event.target.value)}
                    >
                      <option value="all">All states</option>
                      {[
                        ...new Set([
                          ...data.machines.map((machine) => machine.status),
                          ...(stateFilter === 'all' ? [] : [stateFilter]),
                        ]),
                      ]
                        .sort()
                        .map((state) => (
                          <option key={state} value={state}>
                            {humanize(state)}
                          </option>
                        ))}
                    </Select>
                  </div>
                  <div
                    className="machines-selection"
                    aria-label="Selected VM actions"
                  >
                    <strong>
                      {selected?.spec.name || 'Select a VM to manage it'}
                    </strong>
                    <MachineActions
                      desktop={desktop}
                      machine={selected}
                      reason={actionReason}
                      terminalReason={terminalReason}
                      onAction={(action) => void lifecycle(action)}
                      onTerminal={() => void terminal()}
                      onInspect={inspect}
                    />
                    {selected && (
                      <p className="machines-form-help">
                        {data.error
                          ? 'Reconnect to the host to change this VM. Its last known details are available in the Inspector.'
                          : selected.status === 'stopped' &&
                              actionReason('start')
                            ? actionReason('start')
                            : machineGuidance(selected, data.operations)}
                      </p>
                    )}
                  </div>
                </>
              )}
              {visible.length ? (
                <div className="machines-grid" aria-label="Virtual machines">
                  {visible.map((machine) => (
                    <button
                      key={`${machine.hostId}:${machine.id}`}
                      className="machines-tile"
                      aria-label={`${machine.spec.name} ${machine.spec.provider}`}
                      aria-pressed={
                        selected?.id === machine.id &&
                        selected.hostId === machine.hostId
                      }
                      onClick={() =>
                        desktop.updateWindowState(window.id, {
                          machineId: machine.id,
                          machineHostId: machine.hostId,
                        })
                      }
                      onDoubleClick={() =>
                        openMachineInspector(desktop, machine)
                      }
                    >
                      <PixelIcon name="virtual-machine" size={48} />
                      <strong>{machine.spec.name}</strong>
                      <span>{humanize(machine.status)}</span>
                    </button>
                  ))}
                </div>
              ) : (
                <div className="machines-empty">
                  <h2>
                    {data.machines.length
                      ? 'No VMs match your filters'
                      : data.loading
                        ? 'Loading virtual machines…'
                        : data.error
                          ? 'VM service unavailable'
                          : 'Create your first VM'}
                  </h2>
                  <p>
                    {data.machines.length
                      ? 'Try another name or show all states.'
                      : data.error
                        ? 'Reconnect to the host to load your VMs.'
                        : !availableProviders.length
                          ? 'The VM provider needs attention before you can create a VM.'
                          : !availableImages.length
                            ? 'Add a compatible Linux image, then choose the resources for your VM.'
                            : 'Choose New VM to select an image and resources.'}
                  </p>
                  {data.machines.length ? (
                    <Button
                      onClick={() => {
                        setSearch('');
                        setStateFilter('all');
                      }}
                    >
                      Clear filters
                    </Button>
                  ) : canCreate ? null : !data.error &&
                    availableProviders.length ? (
                    <Button onClick={() => setTab('images')}>
                      Open Images
                    </Button>
                  ) : (
                    <Button onClick={() => setShowHost(true)}>
                      View environment
                    </Button>
                  )}
                </div>
              )}
            </div>
            <div
              hidden={showHost || tab !== 'volumes'}
              inert={showHost || tab !== 'volumes'}
            >
              <VolumeManager
                data={data}
                desktop={desktop}
                onShowActivity={showActivity}
              />
            </div>
            <div
              hidden={showHost || tab !== 'images'}
              inert={showHost || tab !== 'images'}
            >
              <ImageManager
                data={data}
                desktop={desktop}
                onShowActivity={showActivity}
              />
            </div>
            <div
              hidden={showHost || tab !== 'operations'}
              inert={showHost || tab !== 'operations'}
            >
              <table className="machines-table">
                <thead>
                  <tr>
                    <th>Operation</th>
                    <th>Target</th>
                    <th>State</th>
                  </tr>
                </thead>
                <tbody>
                  {data.operations.map((op) => (
                    <tr
                      key={op.id}
                      data-selected={window.viewState.operationId === op.id}
                    >
                      <td>
                        {humanize(op.action)}
                        <small>{new Date(op.createdAt).toLocaleString()}</small>
                        <details>
                          <summary>Details</summary>
                          <code>{op.id}</code>
                        </details>
                      </td>
                      <td>
                        {operationTarget(op, data.machines, data.volumes)}
                      </td>
                      <td>
                        {humanize(op.status)}
                        {op.phase && <small>{humanize(op.phase)}</small>}
                        {op.error && (
                          <small className="machines-failure">{op.error}</small>
                        )}
                      </td>
                    </tr>
                  ))}
                  {!data.operations.length && (
                    <tr>
                      <td colSpan={3}>
                        No activity yet. VM, image and volume operations appear
                        here.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </>
      )}
      {operationId && (
        <div className="machines-operation-feedback" role="status">
          <span>
            {operation
              ? `${humanize(operation.action)} · ${humanize(operation.status)}${operation.phase ? ` · ${humanize(operation.phase)}` : ''}`
              : 'Operation accepted.'}
          </span>
          <Button onClick={() => showActivity(operationId)}>
            View Activity
          </Button>
          <Button onClick={() => setOperationId('')}>Dismiss</Button>
        </div>
      )}
      <StatusBar>
        {preparing
          ? `Preparing VM environment · ${humanize(data.preparation.job?.phase || 'queued')}`
          : !configured
            ? data.loading
              ? 'Checking VM environment…'
              : !data.environment
                ? 'VM environment unavailable'
                : 'VM hosting is not configured'
            : `${data.machines.length} VMs · ${data.machines.filter((m) => m.status === 'running').length} running · ${data.machines.filter((m) => m.reserved).length} resource reservations${data.error ? ' · Offline' : ''}`}
      </StatusBar>
    </div>
  );
}
