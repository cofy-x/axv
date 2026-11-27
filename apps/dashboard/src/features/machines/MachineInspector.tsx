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
  canCopyLocalLimaShell,
  guestTerminalReason,
  humanize,
  latestVMFailure,
  machineGuestStatus,
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
  const [copyingLimaShell, setCopyingLimaShell] = useState(false);
  const [limaShellNotice, setLimaShellNotice] = useState<{
    copied: boolean;
    command: string;
  }>();
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

  const showCopyLimaShell =
    connected &&
    !transition &&
    canCopyLocalLimaShell(machine, Boolean(data.preparation.check));
  const guestStatus = machine ? machineGuestStatus(machine) : undefined;

  async function copyLimaShell() {
    if (!machine || !showCopyLimaShell) return;
    setCopyingLimaShell(true);
    setError('');
    try {
      const info = await machinesApi.limaShell(machine.id);
      let copied = false;
      try {
        if (navigator.clipboard?.writeText) {
          await navigator.clipboard.writeText(info.command);
          copied = true;
        }
      } catch {
        copied = false;
      }
      setLimaShellNotice({ copied, command: info.command });
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      setCopyingLimaShell(false);
    }
  }

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
            <section
              className="machines-inspector-hero"
              data-status={transition ? 'transitioning' : machine.status}
            >
              <header className="machines-section-heading machines-inspector-heading">
                <div className="machines-inspector-identity">
                  <PixelIcon name="virtual-machine" size={44} />
                  <div>
                    <h2
                      className="machines-inspector-status"
                      data-status={
                        transition ? 'transitioning' : machine.status
                      }
                      aria-live="polite"
                    >
                      {transition || humanize(machine.status)}
                    </h2>
                    <p className="machines-inspector-specs">
                      {machine.spec.resources.cpus} CPU ·{' '}
                      {machine.spec.resources.memoryMiB} MiB ·{' '}
                      {machine.spec.resources.diskGiB} GiB root ·{' '}
                      {machine.spec.projectGiB} GiB project
                    </p>
                    {!transition && guestStatus && (
                      <p className="machines-form-help">{guestStatus}</p>
                    )}
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
                    active
                      ? 'Wait for the current VM operation.'
                      : terminalReason
                  }
                  terminalLabel={
                    !roster || roster.error ? 'New Terminal' : undefined
                  }
                  onAction={(action) => void lifecycle(action)}
                  onTerminal={() => void newTerminal()}
                  onResume={liveTerminals.length ? resumeTerminal : undefined}
                  onCopyLimaShell={
                    showCopyLimaShell ? () => void copyLimaShell() : undefined
                  }
                  limaShellPending={copyingLimaShell}
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
            </section>
            {!transition &&
              machine.reason &&
              !(failure && machine.status === 'unknown') &&
              machine.reason !== (failure?.error || guidance) && (
                <p className="machines-error">{machine.reason}</p>
              )}
            {guidance && <p className="machines-form-help">{guidance}</p>}
            {limaShellNotice && (
              <p className="machines-form-help" role="status">
                {limaShellNotice.copied
                  ? 'Copied local Lima shell command: '
                  : 'Run on this Mac: '}
                <code>{limaShellNotice.command}</code>
              </p>
            )}
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
              <summary className="ns-disclosure">Guest terminals</summary>
              <div className="machines-inspector-panel">
                <p className="fine-print">
                  Closing a terminal window detaches it. Stopping the VM ends
                  its running sessions.
                </p>
                <div className="machines-session-list">
                  {terminals.map((session) => {
                    const title = guestTerminalLabel(session);
                    const running = session.status === 'running';
                    const label = running ? 'Resume' : 'Open';
                    return (
                      <div
                        key={session.id}
                        className="machines-session"
                        data-running={running ? 'true' : 'false'}
                      >
                        <div className="machines-session-label">
                          <strong>{title}</strong>
                          {!running && (
                            <span className="machines-session-state">
                              {' '}
                              · {humanize(session.status)}
                            </span>
                          )}
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
                </div>
              </div>
            </details>
            <details className="machines-technical">
              <summary className="ns-disclosure">Configuration</summary>
              <div className="machines-inspector-panel">
                <dl className="info-list machines-details machines-summary-grid">
                  <div>
                    <dt>VM type</dt>
                    <dd>{humanize(machine.spec.kind)}</dd>
                  </div>
                  <div>
                    <dt>Provider</dt>
                    <dd>{humanize(machine.spec.provider)}</dd>
                  </div>
                  <div>
                    <dt>Resources</dt>
                    <dd>
                      {machine.spec.resources.cpus} CPU ·{' '}
                      {machine.spec.resources.memoryMiB} MiB memory
                    </dd>
                  </div>
                  <div>
                    <dt>Storage</dt>
                    <dd>
                      {machine.spec.resources.diskGiB} GiB root ·{' '}
                      {machine.spec.projectGiB} GiB project
                    </dd>
                  </div>
                  <div>
                    <dt>Project volume</dt>
                    <dd>
                      {volume?.name || machine.spec.volumeId || 'Detached'}
                    </dd>
                  </div>
                  <div>
                    <dt>Retention</dt>
                    <dd>
                      {machine.spec.retainVolume
                        ? 'Keep project volume after deletion'
                        : 'Delete project volume with VM'}
                    </dd>
                  </div>
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
              </div>
            </details>
            <details className="machines-technical">
              <summary className="ns-disclosure">Recent operations</summary>
              <div className="machines-inspector-panel">
                {recent.length ? (
                  <div className="machines-operations-list">
                    {recent.map((op) => {
                      const phase = operationPhase(op);
                      return (
                        <p
                          key={op.id}
                          className="machines-operation-item"
                          data-status={op.status}
                        >
                          {humanize(op.action)} · {humanize(op.status)}
                          {phase && ` · ${phase}`}
                          {op.error && (
                            <span className="machines-failure">
                              {' '}
                              — {op.error}
                            </span>
                          )}
                        </p>
                      );
                    })}
                  </div>
                ) : (
                  <p className="fine-print">No operations for this VM yet.</p>
                )}
              </div>
            </details>
            <details className="machines-technical">
              <summary className="ns-disclosure">Technical details</summary>
              <div className="machines-inspector-panel">
                <dl className="info-list machines-details machines-tech-list">
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
              </div>
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
