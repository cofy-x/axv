import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { Button, IconButton, PixelIcon } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { machinesApi } from './api';
import type { GuestTerminal } from './api';
import { useCompute } from './useCompute';
import { RequestDetails, VMOperationNotice } from './OperationFeedback';
import {
  currentGuestTerminalTarget,
  guestTerminalLabel,
  guestTerminalRequests,
  openGuestTerminal,
} from './open-terminal';
import { computeStore } from './compute-store';
import { MachineActions, MachineExpiry } from './MachineActions';
import { MachineSettings } from './MachineSettings';
import { canOpenVMProjectVolume } from '../project-files/vm-project-volume';
import {
  activeVMOperation,
  confirmVMAction,
  machineActionRequests,
  machineActionReason,
} from './lifecycle';
import type { VMAction } from './lifecycle';
import {
  guestTerminalReason,
  humanize,
  latestVMFailure,
  machineGuidance,
  machineTransition,
  operationPhase,
} from './presentation';

function showGuestTerminals(details: HTMLDetailsElement | null) {
  if (!details) return;
  details.open = true;
  details.querySelector('summary')?.focus();
}

export function MachineInspector({
  desktop,
  window,
  visible,
  interactive,
}: AppWindowProps) {
  const { setTitle, updateWindowState } = desktop;
  const data = useCompute(visible);
  const id = window.params.machineId || '';
  const hostId = window.params.hostId || '';
  const originalSnapshot = useRef(data);
  if (hostId && data.info?.hostId === hostId) originalSnapshot.current = data;
  const snapshot =
    data.info?.hostId === hostId ? data : originalSnapshot.current;
  const machine = snapshot.machines.find(
    (item) =>
      !!hostId &&
      item.id === id &&
      item.hostId === hostId &&
      item.status !== 'deleted',
  );
  const name = machine?.spec.name;
  useEffect(() => {
    if (name) setTitle(window.id, `${name} — Inspector`);
  }, [name, setTitle, window.id]);
  const connected = !!hostId && data.info?.hostId === hostId && !data.error;
  const hasIdentity = !!hostId && !!id;
  const otherHost =
    !!hostId && !!data.info?.hostId && data.info.hostId !== hostId;
  const terminalReason = !connected
    ? 'Reconnect to the original host before opening a guest terminal.'
    : guestTerminalReason(machine);
  const ready = !!machine && !terminalReason;
  const [terminalInventory, setTerminalInventory] = useState<{
    scope: string;
    terminals: GuestTerminal[];
    error?: string;
  }>();
  const terminalDetails = useRef<HTMLDetailsElement>(null);
  const [error, setError] = useState('');
  const [terminalRefresh, setTerminalRefresh] = useState(0);
  const [busy, setBusy] = useState(false);
  const pending = useSyncExternalStore(machineActionRequests.subscribe, () =>
    machineActionRequests.getPending(hostId, id),
  );
  const activeOperation =
    machine && activeVMOperation(machine, snapshot.operations);
  const transition =
    machine &&
    machineTransition(
      machine,
      activeOperation,
      pending && !pending.uncertain ? pending.action : undefined,
    );
  const active = busy || !!pending || !!transition;
  const failure =
    machine && !transition
      ? latestVMFailure(machine, snapshot.operations)
      : undefined;
  const notice = activeOperation || failure;
  const terminalScope =
    ready && !transition && visible !== false
      ? JSON.stringify([
          hostId,
          id,
          machine?.revision,
          data.environment?.runtime?.generation,
        ])
      : '';
  const roster =
    terminalScope && terminalInventory?.scope === terminalScope
      ? terminalInventory
      : undefined;
  const terminals = roster?.terminals ?? [];
  const liveTerminals = terminals.filter(
    (session) => session.status === 'running',
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
  const guidance =
    machine &&
    connected &&
    !transition &&
    !pending &&
    (!failure || machine.status === 'unknown')
      ? (machine.status === 'stopped' ? reason('start') : undefined) ||
        machineGuidance(machine)
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
      computeStore.receiveMachineOperation(result.machine, result.operation);
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }

  async function newTerminal() {
    const target = currentGuestTerminalTarget({ id, hostId });
    if (!target || !ready || active) return;
    setBusy(true);
    setError('');
    try {
      const session = await guestTerminalRequests.execute(
        target.id,
        target.hostId,
      );
      openGuestTerminal(desktop, target, session);
      setTerminalRefresh((value) => value + 1);
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }

  function openSession(session: GuestTerminal) {
    const target = currentGuestTerminalTarget({ id, hostId });
    if (!target || target.revision !== machine?.revision || !roster || active)
      return;
    openGuestTerminal(desktop, target, session);
  }

  function resumeTerminal() {
    if (liveTerminals.length === 1) {
      openSession(liveTerminals[0]);
    } else showGuestTerminals(terminalDetails.current);
  }

  const revealGuestTerminals = window.viewState.revealGuestTerminals === true;
  useEffect(() => {
    if (!revealGuestTerminals || interactive === false || !roster) return;
    if (terminals.length) showGuestTerminals(terminalDetails.current);
    updateWindowState(window.id, { revealGuestTerminals: false });
  }, [
    revealGuestTerminals,
    interactive,
    roster,
    terminals.length,
    updateWindowState,
    window.id,
  ]);

  useEffect(() => {
    if (!terminalScope) {
      setTerminalInventory(undefined);
      return;
    }
    let disposed = false;
    let timer: ReturnType<typeof setTimeout>;
    let abort: AbortController;

    async function load() {
      abort = new AbortController();
      const timeout = setTimeout(() => abort.abort(), 15000);
      try {
        const result = await machinesApi.terminals(id, hostId, abort.signal);
        if (!disposed)
          setTerminalInventory({
            scope: terminalScope,
            terminals: [...result.terminals].sort(
              (a, b) =>
                (a.sequence ?? 0) - (b.sequence ?? 0) ||
                a.id.localeCompare(b.id),
            ),
          });
      } catch (error) {
        if (!disposed)
          setTerminalInventory({
            scope: terminalScope,
            terminals: [],
            error: error instanceof Error ? error.message : String(error),
          });
      } finally {
        clearTimeout(timeout);
        if (!disposed) {
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
  }, [id, hostId, terminalScope, terminalRefresh]);

  function showActivity(operationId: string) {
    if (computeStore.getSnapshot().info?.hostId !== hostId) return;
    const managerId = desktop.openApp('machines');
    desktop.updateWindowState(managerId, {
      vmTab: 'operations',
      vmEnvironment: false,
      operationId,
      operationHostId: hostId,
      revealActivity: true,
    });
  }

  return (
    <div className="machines-app">
      <div className="app-toolbar machines-toolbar">
        <IconButton
          icon="back"
          label="Back to Virtual Machines"
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
      <div className="app-pad machines-scroll machines-inspector-body">
        {machine && data.error && (
          <p className="machines-error" role="alert">
            VM service unavailable. {data.error} Showing the last known VM
            state.
          </p>
        )}
        {machine && otherHost && (
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
                  <h2 aria-live="polite">
                    {transition || humanize(machine.status)}
                  </h2>
                  <MachineExpiry expiresAt={machine.expiresAt} />
                </div>
              </div>
            </header>
            <div className="machines-inspector-actions">
              <MachineActions
                desktop={desktop}
                windowId={window.id}
                machine={machine}
                reason={reason}
                transition={transition}
                terminalReason={
                  active ? 'Wait for the current VM operation.' : terminalReason
                }
                terminalLabel={
                  !roster || roster.error ? 'New Terminal' : undefined
                }
                onAction={(action) => void lifecycle(action)}
                onTerminal={() => void newTerminal()}
                onResume={liveTerminals.length ? resumeTerminal : undefined}
                onProjectFiles={
                  connected &&
                  !transition &&
                  volume &&
                  canOpenVMProjectVolume(machine, volume)
                    ? () =>
                        desktop.openApp(
                          'project-files',
                          {
                            hostId,
                            machineId: id,
                            vmProjectVolumeId: volume.id,
                          },
                          { title: `${machine.spec.name} — Project Files` },
                        )
                    : undefined
                }
              />
            </div>
            {!transition &&
              machine.reason &&
              !(failure && machine.status === 'unknown') &&
              machine.reason !== (failure?.error || guidance) && (
                <p className="machines-error">{machine.reason}</p>
              )}
            {guidance && <p className="machines-form-help">{guidance}</p>}
            {error && (
              <p className="machines-error" role="alert">
                {error}
              </p>
            )}
            {notice && (
              <VMOperationNotice
                className="machines-inspector-notice"
                operation={notice}
                onShowActivity={connected ? showActivity : undefined}
              />
            )}
            {roster?.error && (
              <p className="machines-error" role="alert">
                Guest terminals could not be listed. {roster.error}
              </p>
            )}
            <details
              ref={terminalDetails}
              hidden={!terminals.length}
              inert={!terminals.length}
              className="machines-technical"
            >
              <summary>Guest terminals</summary>
              <p className="fine-print">
                Closing a terminal window detaches it. Stopping the VM ends its
                running sessions.
              </p>
              {terminals.map((session) => {
                const title = guestTerminalLabel(session);
                const running = session.status === 'running';
                const label = running ? 'Resume' : 'Open';
                return (
                  <div key={session.id} className="machines-session">
                    <div>
                      <strong>{title}</strong>
                      {!running && <span> · {humanize(session.status)}</span>}
                    </div>
                    <Button
                      disabled={active}
                      aria-label={`${label} ${title}`}
                      onClick={() => openSession(session)}
                    >
                      {label}
                    </Button>
                  </div>
                );
              })}
            </details>
            <details className="machines-technical">
              <summary>Configuration</summary>
              <dl className="info-list machines-details">
                <dt>VM type</dt>
                <dd>{humanize(machine.spec.kind)}</dd>
                <dt>Provider</dt>
                <dd>{humanize(machine.spec.provider)}</dd>
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
              </dl>
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
                recent.map((op) => {
                  const phase = operationPhase(op);
                  return (
                    <p key={op.id}>
                      {humanize(op.action)} · {humanize(op.status)}
                      {phase && ` · ${phase}`}
                      {op.error && (
                        <span className="machines-failure"> — {op.error}</span>
                      )}
                    </p>
                  );
                })
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
                {machine.reason && (
                  <>
                    <dt>Provider observation</dt>
                    <dd>{machine.reason}</dd>
                  </>
                )}
              </dl>
            </details>
          </>
        ) : (
          <div className="machines-empty-status">
            <h2>
              {!hasIdentity
                ? 'VM identity unavailable'
                : otherHost
                  ? 'Original host unavailable'
                  : data.loading && !data.error
                    ? 'Loading VM…'
                    : connected
                      ? 'VM not found'
                      : 'VM host unavailable'}
            </h2>
            {!hasIdentity && <p>Open the VM from Virtual Machines.</p>}
            {!otherHost && hasIdentity && data.error && (
              <p role="alert">{data.error}</p>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
