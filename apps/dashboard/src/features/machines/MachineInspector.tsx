import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { Button, IconButton, PixelIcon, StatusBar } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { machinesApi } from './api';
import type { GuestTerminal } from './api';
import { useCompute } from './useCompute';
import { RequestDetails } from './OperationFeedback';
import { guestTerminalRequests, openGuestTerminal } from './open-terminal';
import { computeStore } from './compute-store';
import { MachineActions } from './MachineActions';
import { MachineSettings } from './MachineSettings';
import {
  confirmVMAction,
  machineActionRequests,
  machineActionReason,
} from './lifecycle';
import type { VMAction } from './lifecycle';
import { guestTerminalReason, humanize, machineGuidance } from './presentation';
import { RelatedDocuments } from '../document-workflow/RelatedDocuments';

export function MachineInspector({ desktop, window }: AppWindowProps) {
  const data = useCompute();
  const id = window.params.machineId || '';
  const hostId = window.params.hostId || '';
  const originalSnapshot = useRef(data);
  if (hostId && data.info?.hostId === hostId) originalSnapshot.current = data;
  const snapshot =
    data.info?.hostId === hostId ? data : originalSnapshot.current;
  const machine = snapshot.machines.find(
    (item) => !!hostId && item.id === id && item.hostId === hostId,
  );
  const connected = !!hostId && data.info?.hostId === hostId && !data.error;
  const terminalReason = !connected
    ? 'Reconnect to the original host before opening a guest terminal.'
    : guestTerminalReason(machine);
  const ready = !!machine && !terminalReason;
  const [terminals, setTerminals] = useState<GuestTerminal[]>([]);
  const [error, setError] = useState('');
  const [terminalError, setTerminalError] = useState('');
  const [terminalLoading, setTerminalLoading] = useState(false);
  const [terminalRefresh, setTerminalRefresh] = useState(0);
  const [busy, setBusy] = useState(false);
  const [operationId, setOperationId] = useState('');
  const pending = useSyncExternalStore(machineActionRequests.subscribe, () =>
    machineActionRequests.getPending(hostId, id),
  );
  const active =
    busy ||
    !!pending ||
    snapshot.operations.some(
      (op) =>
        (op.machineId === id || op.locks?.includes(id)) &&
        ['queued', 'running'].includes(op.status),
    );
  const provider = snapshot.info?.providers?.find(
    (item) => item.provider === machine?.spec.provider,
  );
  const volume = snapshot.volumes.find(
    (item) => item.id === machine?.spec.volumeId,
  );
  const recent = snapshot.operations
    .filter((op) => op.machineId === id || op.locks?.includes(id))
    .slice(0, 10);
  const reason = (action: VMAction) =>
    !machine
      ? 'VM unavailable.'
      : busy
        ? 'Waiting for the current request.'
        : pending
          ? 'Resolve the original request before changing this VM.'
          : !connected
            ? 'Reconnect to the host before changing this VM.'
            : machineActionReason(
                machine,
                action,
                snapshot.operations,
                snapshot.volumes,
                provider,
              );
  const guidance = machine
    ? !connected
      ? 'Reconnect to the original host before changing this VM.'
      : (machine.status === 'stopped' ? reason('start') : undefined) ||
        machineGuidance(machine, snapshot.operations)
    : undefined;

  async function lifecycle(action: VMAction, retry = false) {
    const target = retry ? pending?.machine : machine;
    if (
      !target ||
      !connected ||
      target.hostId !== hostId ||
      target.id !== id ||
      (!retry &&
        (reason(action) || !(await confirmVMAction(desktop, target, action))))
    )
      return;
    const latest = computeStore.getSnapshot();
    if (latest.error || latest.info?.hostId !== hostId) return;
    setBusy(true);
    setError('');
    try {
      const result = await machineActionRequests.execute(target, action);
      setOperationId(result.operation.id);
      data.refresh();
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }

  async function newTerminal() {
    if (!machine || !ready || active) return;
    setBusy(true);
    setError('');
    try {
      const session = await guestTerminalRequests.execute(
        machine.id,
        machine.hostId,
      );
      openGuestTerminal(desktop, machine, session);
      setTerminalRefresh((value) => value + 1);
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    if (!ready) return;
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    let abort: AbortController;

    async function load() {
      abort = new AbortController();
      const timeout = setTimeout(() => abort.abort(), 15000);
      setTerminalLoading(true);
      try {
        const result = await machinesApi.terminals(id, hostId, abort.signal);
        if (!disposed) {
          setTerminals(result.terminals);
          setTerminalError('');
        }
      } catch (error) {
        if (!disposed)
          setTerminalError(
            error instanceof Error ? error.message : String(error),
          );
      } finally {
        clearTimeout(timeout);
        if (!disposed) {
          setTerminalLoading(false);
          timer = setTimeout(() => void load(), 5000);
        }
      }
    }

    void load();
    return () => {
      disposed = true;
      abort?.abort();
      clearTimeout(timer);
    };
  }, [id, hostId, ready, terminalRefresh]);

  function showActivity() {
    const managerId = desktop.openApp('machines');
    desktop.updateWindowState(managerId, { vmTab: 'operations', operationId });
  }

  return (
    <div className="machines-app">
      <div className="app-toolbar machines-toolbar">
        <IconButton
          icon="back"
          label="Virtual Machines"
          onClick={() => desktop.openApp('machines')}
        />
        <div className="machines-toolbar-actions">
          <IconButton
            icon="refresh"
            label="Refresh"
            disabled={data.refreshing}
            onClick={() => {
              data.refresh();
              setTerminalRefresh((value) => value + 1);
            }}
          />
        </div>
      </div>
      <div className="app-pad machines-scroll">
        {data.error && (
          <p className="machines-error" role="alert">
            VM service unavailable. {data.error} Showing the last known VM
            state.
          </p>
        )}
        {hostId && data.info?.hostId && data.info.hostId !== hostId && (
          <p className="machines-warning" role="alert">
            Original host unavailable. This window is read-only until its host
            reconnects.
          </p>
        )}
        {pending?.uncertain && !busy && (
          <div className="machines-operation-feedback" role="status">
            <span>
              The response to {humanize(pending.action)} for “
              {pending.machine.spec.name}” is unresolved. Retry the original
              request before making another change.
            </span>
            <Button
              disabled={!connected}
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
        {machine ? (
          <>
            <header className="machines-section-heading machines-inspector-heading">
              <div className="machines-inspector-identity">
                <PixelIcon name="virtual-machine" size={48} />
                <div>
                  <span className="machines-eyebrow">
                    {humanize(machine.spec.kind)} VM · {machine.spec.provider}
                  </span>
                  <h2>{machine.spec.name}</h2>
                  <p>
                    <strong>{humanize(machine.status)}</strong> ·{' '}
                    {machine.guestState === 'not-installed'
                      ? 'Guest not installed'
                      : machine.guestState === 'incompatible'
                        ? 'Guest incompatible'
                        : machine.ready
                          ? 'Guest ready'
                          : 'Guest unavailable'}
                  </p>
                </div>
              </div>
            </header>
            <div className="machines-inspector-actions">
              <MachineActions
                desktop={desktop}
                machine={machine}
                reason={reason}
                terminalReason={
                  active ? 'Wait for the current VM operation.' : terminalReason
                }
                onAction={(action) => void lifecycle(action)}
                onTerminal={() => void newTerminal()}
              />
            </div>
            {machine.guestReason && (
              <p className="machines-form-help">{machine.guestReason}</p>
            )}
            {machine.reason && (
              <p className="machines-error">{machine.reason}</p>
            )}
            {guidance && guidance !== machine.reason && (
              <p className="machines-form-help">{guidance}</p>
            )}
            {error && (
              <p className="machines-error" role="alert">
                {error}
              </p>
            )}
            {operationId && (
              <div className="machines-operation-feedback" role="status">
                <span>
                  {(() => {
                    const op = data.operations.find(
                      (item) => item.id === operationId,
                    );
                    return op
                      ? `${humanize(op.action)} · ${humanize(op.status)}`
                      : 'Operation accepted.';
                  })()}
                </span>
                <Button onClick={showActivity}>View Activity</Button>
              </div>
            )}
            <dl className="info-list machines-details">
              <dt>Resources</dt>
              <dd>
                {machine.spec.resources.cpus} CPU ·{' '}
                {machine.spec.resources.memoryMiB} MiB memory
              </dd>
              <dt>Storage</dt>
              <dd>
                {machine.spec.resources.diskGiB} GiB root ·{' '}
                {machine.spec.projectGiB} GiB project
              </dd>
              <dt>Project volume</dt>
              <dd>{volume?.name || machine.spec.volumeId || 'Detached'}</dd>
              <dt>Retention</dt>
              <dd>
                {machine.spec.retainVolume
                  ? 'Keep project volume after deletion'
                  : 'Delete project volume with VM'}
              </dd>
              <dt>Expiry</dt>
              <dd>
                {machine.expiresAt
                  ? new Date(machine.expiresAt).toLocaleString()
                  : 'No expiry'}
              </dd>
            </dl>
            <details className="machines-technical">
              <summary>Guest terminals</summary>
              <p className="fine-print">
                Closing a terminal window detaches it. Stopping the VM ends its
                running sessions.
              </p>
              {ready && terminalError && (
                <p className="machines-error" role="alert">
                  {terminalError}
                </p>
              )}
              {!ready ? (
                <p>
                  {data.error
                    ? 'Reconnect to the host to list guest terminals.'
                    : terminalReason}
                </p>
              ) : terminals.length ? (
                terminals.map((session) => (
                  <div key={session.id} className="machines-session">
                    <code>{session.id.slice(0, 12)}</code>
                    <span>{humanize(session.status)}</span>
                    <Button
                      disabled={active || !!terminalError || terminalLoading}
                      onClick={() =>
                        openGuestTerminal(desktop, machine, session)
                      }
                    >
                      Open
                    </Button>
                  </div>
                ))
              ) : (
                <p>
                  {terminalLoading
                    ? 'Loading guest terminals…'
                    : 'No terminals in this guest boot.'}
                </p>
              )}
            </details>
            <details className="machines-technical">
              <summary>Configuration</summary>
              <MachineSettings
                key={machine.id}
                machine={machine}
                machines={snapshot.machines}
                info={snapshot.info}
                active={active}
                operations={snapshot.operations}
                unavailable={!connected}
                onSaved={data.refresh}
              />
            </details>
            <details className="machines-technical">
              <summary>Recent operations</summary>
              {recent.length ? (
                recent.map((op) => (
                  <p key={op.id}>
                    {humanize(op.action)} · {humanize(op.status)}
                    {op.phase && ` · ${humanize(op.phase)}`}
                    {op.error && (
                      <span className="machines-failure"> — {op.error}</span>
                    )}
                  </p>
                ))
              ) : (
                <p>No operations for this VM yet.</p>
              )}
            </details>
            <details className="machines-technical">
              <summary>Technical details</summary>
              <dl className="info-list machines-details">
                <dt>VM ID</dt>
                <dd>
                  <code>{machine.id}</code>
                </dd>
                <dt>Host</dt>
                <dd>
                  <code>{machine.hostId}</code>
                </dd>
                <dt>Desired state</dt>
                <dd>{humanize(machine.desired)}</dd>
                <dt>Revision</dt>
                <dd>{machine.revision}</dd>
                <dt>Image</dt>
                <dd>
                  <code>{machine.spec.imageId}</code>
                </dd>
              </dl>
            </details>
          </>
        ) : (
          <div className="machines-empty">
            <h2>
              {!hostId
                ? 'VM identity unavailable'
                : data.info?.hostId && data.info.hostId !== hostId
                  ? 'Original host unavailable'
                  : data.loading || data.refreshing
                    ? 'Loading VM…'
                    : !connected
                      ? 'VM details unavailable'
                      : 'This VM is no longer available'}
            </h2>
            <p>
              {!hostId
                ? 'Open this VM again from Virtual Machines to bind it to its host.'
                : data.info?.hostId && data.info.hostId !== hostId
                  ? 'This window belongs to a different host. Open Virtual Machines to choose a VM on the current host.'
                  : data.error
                    ? 'Refresh to reconnect to the host.'
                    : 'Open Virtual Machines to view the current inventory.'}
            </p>
          </div>
        )}
      </div>
      {hostId && id && (
        <RelatedDocuments
          target={{ hostId, machineId: id }}
          desktop={desktop}
        />
      )}
      <StatusBar>
        {machine
          ? `${machine.spec.name} · ${!connected ? 'Unavailable' : humanize(machine.status)}`
          : 'VM Inspector'}
      </StatusBar>
    </div>
  );
}
