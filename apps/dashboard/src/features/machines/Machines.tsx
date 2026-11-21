import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import {
  Button,
  ButtonGroup,
  Field,
  PixelIcon,
  Select,
  StatusBar,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useWindowCommands } from '../../shared/desktop/WindowCommandsContext';
import { machineMenuCommand } from './menus';
import { useCompute } from './useCompute';
import { RequestDetails, VMOperationNotice } from './OperationFeedback';
import { machinesApi } from './api';
import type { GuestTerminal } from './api';
import {
  currentGuestTerminalTarget,
  guestTerminalRequests,
  openGuestTerminal,
} from './open-terminal';
import {
  activeVMOperation,
  confirmVMAction,
  machineActionRequests,
  machineActionReason,
} from './lifecycle';
import type { VMAction } from './lifecycle';
import { computeStore } from './compute-store';
import { MachineActions, MachineExpiry } from './MachineActions';
import { creationImages } from './form-state';
import { openMachineInspector } from './desktop-icons';
import { VolumeManager } from './VolumeManager';
import { ImageManager } from './ImageManager';
import { MachineEnvironment } from './MachineEnvironment';
import { hostInstallerCommand } from './HostServiceGuidance';
import { preparationRunning } from './preparation-store';
import {
  guestTerminalReason,
  humanize,
  latestVMFailure,
  linuxVMSetupGuide,
  machineGuidance,
  machineTransition,
  operationLabel,
  operationPhase,
  operationTarget,
} from './presentation';

export function Machines({ window, desktop, interactive }: AppWindowProps) {
  const { setTitle, updateWindowState } = desktop;
  const commands = useWindowCommands();
  const data = useCompute();
  const refreshData = data.refresh;
  const tab =
    (window.viewState.vmTab as
      'machines' | 'volumes' | 'images' | 'operations') || 'machines';
  const setTab = (value: string) =>
    desktop.updateWindowState(window.id, { vmTab: value });
  const [busy, setBusy] = useState(false);
  const [openingTerminal, setOpeningTerminal] = useState<string>();
  const [lastAction, setLastAction] = useState<{
    hostId: string;
    id: string;
  }>();
  const operationId =
    lastAction?.hostId === data.info?.hostId ? lastAction?.id : undefined;
  const [error, setError] = useState('');
  const [search, setSearch] = useState('');
  const [stateFilter, setStateFilter] = useState('all');
  const showHost = window.viewState.vmEnvironment === true;
  const setShowHost = useCallback(
    (value: boolean) => {
      updateWindowState(window.id, { vmEnvironment: value });
    },
    [updateWindowState, window.id],
  );
  const viewNav = useRef<HTMLDivElement>(null);
  const activityBody = useRef<HTMLTableSectionElement>(null);

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
  const terminalIntent =
    selected && JSON.stringify([selected.hostId, selected.id, tab, showHost]);
  const terminalScope = useRef<AbortController | null>(null);
  useLayoutEffect(() => {
    if (!terminalIntent || desktop.activeWindowId !== window.id) return;
    const controller = new AbortController();
    terminalScope.current = controller;
    return () => {
      terminalScope.current = null;
      controller.abort();
    };
  }, [desktop.activeWindowId, window.id, terminalIntent]);
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
  const hasReadyImage = creationImages(data.info).length > 0;
  const canCreate = !!data.info?.configured && !data.error && !!hasReadyImage;
  const canImportImages = availableProviders.some((item) => item.images);
  const imageSetupCommand =
    !hasReadyImage &&
    !canImportImages &&
    data.preparation.check?.defaultImageId &&
    data.info?.configured &&
    data.info.hostId &&
    availableProviders.some((item) => item.provider === 'firecracker')
      ? hostInstallerCommand(
          data.environment?.executablePath,
          'install-image',
          data.info.hostId,
        )
      : undefined;
  const firecrackerImageRequired =
    !data.machines.length &&
    !data.loading &&
    !data.error &&
    !hasReadyImage &&
    !canImportImages &&
    availableProviders.some((item) => item.provider === 'firecracker');
  const operation = data.operations.find((item) => item.id === operationId);
  const preparing = preparationRunning(data.preparation.job);
  const inventoryError = !data.reloading && !preparing ? data.error : undefined;
  const offlineActions = configured && !!inventoryError && !imageSetupCommand;
  const showingImageSetup =
    !!imageSetupCommand && !showHost && tab === 'images';
  const reconnecting =
    busy ||
    preparing ||
    !!data.preparation.pending ||
    !!data.preparation.mutation;

  useEffect(() => {
    if (preparing) setShowHost(true);
  }, [preparing, setShowHost]);

  const actionReason = useCallback(
    (action: VMAction) =>
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
                ),
    [
      selected,
      busy,
      pending,
      data.error,
      data.operations,
      data.volumes,
      provider,
    ],
  );
  const selectedOperation =
    selected && activeVMOperation(selected, data.operations);
  const transition =
    selected &&
    machineTransition(
      selected,
      selectedOperation,
      pending && !pending.uncertain ? pending.action : undefined,
    );
  const selectedFailure =
    selected && !transition
      ? latestVMFailure(selected, data.operations)
      : undefined;
  const terminalReason = !selected
    ? 'Select a VM first.'
    : busy || data.error || pending
      ? 'Wait for the host connection.'
      : transition
        ? 'Wait for the current VM operation.'
        : guestTerminalReason(selected);
  const guidance =
    selected &&
    !data.error &&
    !transition &&
    !pending &&
    (!selectedFailure || selected.status === 'unknown')
      ? (selected.status === 'stopped' ? actionReason('start') : undefined) ||
        machineGuidance(selected)
      : undefined;
  const notice = selectedFailure || selectedOperation;
  const activityId = window.viewState.operationId;
  const selectedActivity =
    window.viewState.operationHostId === data.info?.hostId &&
    typeof activityId === 'string'
      ? activityId
      : undefined;
  const viewingActivity = !showHost && tab === 'operations';
  const activityFound = data.operations.some(
    (item) => item.id === selectedActivity,
  );
  const revealActivity = window.viewState.revealActivity === true;
  useEffect(() => {
    if (!revealActivity || !viewingActivity || interactive === false) return;
    if (!activityFound && selectedActivity && (data.loading || data.refreshing))
      return;
    activityBody.current
      ?.querySelector('tr[data-selected="true"]')
      ?.scrollIntoView({ block: 'center', inline: 'nearest' });
    viewNav.current
      ?.querySelector<HTMLButtonElement>('button[data-view="operations"]')
      ?.focus({ preventScroll: true });
    updateWindowState(window.id, { revealActivity: false });
  }, [
    revealActivity,
    viewingActivity,
    interactive,
    activityFound,
    selectedActivity,
    data.loading,
    data.refreshing,
    updateWindowState,
    window.id,
  ]);
  const feedbackInSelection =
    !showHost &&
    tab === 'machines' &&
    selected &&
    operation?.machineId === selected.id;

  function showActivity(id: string) {
    const hostId = data.info?.hostId;
    if (!hostId || computeStore.getSnapshot().info?.hostId !== hostId) return;
    updateWindowState(window.id, {
      vmTab: 'operations',
      vmEnvironment: false,
      operationId: id,
      operationHostId: hostId,
      revealActivity: true,
    });
  }

  const inspect = useCallback(() => {
    if (selected) openMachineInspector(desktop, selected);
  }, [desktop, selected]);

  const run = useCallback(async (action: () => Promise<void>) => {
    setBusy(true);
    setError('');
    try {
      await action();
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }, []);

  const showImageSetup = useCallback(() => {
    updateWindowState(window.id, { vmEnvironment: false, vmTab: 'images' });
    viewNav.current
      ?.querySelector<HTMLButtonElement>('button[data-view="images"]')
      ?.focus({ preventScroll: true });
  }, [updateWindowState, window.id]);

  const newVM = useCallback(() => {
    if (imageSetupCommand) showImageSetup();
    else desktop.openApp('machine-create');
  }, [desktop, imageSetupCommand, showImageSetup]);

  function reconnectImageHost() {
    const check = data.prepare.getSnapshot().check;
    if (!check?.defaultImageId || check.lockedReason || reconnecting) return;
    void run(async () => {
      const plan = await data.prepare.review(check.defaults);
      if (!plan.canStart)
        throw new Error(
          plan.checks
            .filter((check) => check.status === 'blocked')
            .map((check) => check.detail)
            .join(' ') || 'The VM host could not be verified.',
        );
      setShowHost(true);
      await data.prepare.start(plan);
      refreshData();
    });
  }

  const lifecycle = useCallback(
    async (action: VMAction, retry = false) => {
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
        setLastAction({ hostId: target.hostId, id: result.operation.id });
        computeStore.receiveMachineOperation(result.machine, result.operation);
      });
    },
    [
      pending,
      selected,
      data.error,
      data.info?.hostId,
      actionReason,
      desktop,
      run,
    ],
  );

  const terminal = useCallback(async () => {
    const scope = terminalScope.current;
    if (!scope || !selected || !terminalIntent || terminalReason) return;
    const target = currentGuestTerminalTarget(selected);
    if (!target) return;
    const { id, hostId } = target;
    setOpeningTerminal(terminalIntent);
    try {
      await run(async () => {
        try {
          let session: GuestTerminal | Promise<GuestTerminal> | undefined =
            guestTerminalRequests.retryPending(id, hostId);
          if (!session) {
            const { terminals } = await machinesApi.terminals(
              id,
              hostId,
              AbortSignal.any([scope.signal, AbortSignal.timeout(15000)]),
            );
            if (scope.signal.aborted) return;
            const current = currentGuestTerminalTarget(target);
            if (!current)
              throw new Error(
                'The selected VM is no longer ready for terminal access.',
              );
            session = guestTerminalRequests.retryPending(id, hostId);
            if (!session) {
              const running = terminals.filter(
                (item) => item.status === 'running',
              );
              if (running.length > 1) {
                const inspectorId = openMachineInspector(desktop, current);
                updateWindowState(inspectorId, { revealGuestTerminals: true });
                return;
              }
              session = running[0] ?? guestTerminalRequests.execute(id, hostId);
            }
          }
          const result = await session;
          const current = currentGuestTerminalTarget(target);
          if (!scope.signal.aborted && current)
            openGuestTerminal(desktop, current, result);
          refreshData();
        } catch (error) {
          if (!scope.signal.aborted) throw error;
        }
      });
    } finally {
      setOpeningTerminal(undefined);
    }
  }, [
    selected,
    terminalIntent,
    terminalReason,
    run,
    desktop,
    refreshData,
    updateWindowState,
  ]);

  useEffect(() => {
    const unregister = [
      commands.register(
        window.id,
        'machines:new',
        async () => {
          newVM();
          return true;
        },
        (canCreate || (!!imageSetupCommand && !data.reloading)) && !busy,
      ),
      commands.register(window.id, 'machines:refresh', async () => {
        refreshData();
        return true;
      }),
    ];
    for (const view of [
      'machines',
      'volumes',
      'images',
      'operations',
    ] as const) {
      unregister.push(
        commands.register(
          window.id,
          `machines:view:${view}`,
          async () => {
            updateWindowState(window.id, { vmTab: view, vmEnvironment: false });
            return true;
          },
          configured,
        ),
      );
    }
    if (selected) {
      for (const action of [
        'start',
        'stop',
        'restart',
        'force-stop',
        'delete',
      ] as const) {
        unregister.push(
          commands.register(
            window.id,
            machineMenuCommand(action, selected.hostId, selected.id),
            async () => {
              await lifecycle(action);
              return true;
            },
            !actionReason(action),
          ),
        );
      }
      unregister.push(
        commands.register(
          window.id,
          machineMenuCommand('inspect', selected.hostId, selected.id),
          async () => {
            inspect();
            return true;
          },
        ),
        commands.register(
          window.id,
          machineMenuCommand('terminal', selected.hostId, selected.id),
          async () => {
            await terminal();
            return true;
          },
          !terminalReason,
        ),
      );
    }
    return () => {
      for (const cleanup of unregister) cleanup();
    };
  }, [
    commands,
    window.id,
    desktop,
    canCreate,
    imageSetupCommand,
    data.reloading,
    newVM,
    busy,
    refreshData,
    selected,
    lifecycle,
    actionReason,
    inspect,
    terminal,
    terminalReason,
    configured,
    updateWindowState,
  ]);

  const actionPhase = operation && operationPhase(operation);
  const actionFeedback = operationId && (
    <div
      className="machines-operation-feedback"
      role={
        operation?.status === 'failed' && feedbackInSelection
          ? 'alert'
          : 'status'
      }
    >
      <span
        className={
          operation?.status === 'failed' && feedbackInSelection
            ? 'machines-failure'
            : undefined
        }
      >
        {operation
          ? `${humanize(operation.action)} · ${humanize(operation.status)}${actionPhase ? ` · ${actionPhase}` : ''}`
          : 'Operation accepted.'}
        {operation?.error && feedbackInSelection && ` · ${operation.error}`}
      </span>
      {(!configured ||
        showHost ||
        tab !== 'operations' ||
        selectedActivity !== operationId) && (
        <Button onClick={() => showActivity(operationId)}>View Activity</Button>
      )}
      <Button onClick={() => setLastAction(undefined)}>Dismiss</Button>
    </div>
  );
  const hostButton = (
    <Button aria-pressed={showHost} onClick={() => setShowHost(!showHost)}>
      VM Host
    </Button>
  );

  return (
    <div className="machines-app">
      {configured && !offlineActions && (
        <div className="app-toolbar machines-toolbar">
          {data.reloading ? (
            <span className="machines-reload-status" role="status">
              Reconnecting after the VM image installation…
            </span>
          ) : imageSetupCommand ? (
            showingImageSetup ? (
              data.error && (
                <Button disabled={reconnecting} onClick={reconnectImageHost}>
                  {reconnecting ? 'Reconnecting…' : 'Reconnect to VM Host'}
                </Button>
              )
            ) : (
              <Button primary disabled={busy} onClick={showImageSetup}>
                Set Up VM Image
              </Button>
            )
          ) : (
            !data.error && (
              <Button
                disabled={!canCreate || busy}
                title={
                  !canCreate
                    ? 'An available provider and a ready image are required.'
                    : undefined
                }
                onClick={newVM}
              >
                New VM
              </Button>
            )
          )}
          <div className="machines-toolbar-actions">{hostButton}</div>
        </div>
      )}
      {(error || inventoryError) && (
        <div className="machines-error machines-inventory-error">
          <div role="alert">
            {inventoryError || error}
            {data.lastUpdated && inventoryError && (
              <span>
                {' '}
                Showing the last known state from{' '}
                {new Date(data.lastUpdated).toLocaleTimeString()}.
              </span>
            )}
          </div>
          {configured && inventoryError && !showingImageSetup && (
            <div className="machines-form-actions">
              <Button primary disabled={data.refreshing} onClick={refreshData}>
                Retry connection
              </Button>
              {offlineActions && hostButton}
            </div>
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
            desktop={desktop}
            onShowMachines={() => setShowHost(false)}
          />
        </div>
      ) : (
        <>
          <div className="machines-tabs" ref={viewNav}>
            <ButtonGroup label="VM views">
              {(['machines', 'volumes', 'images', 'operations'] as const).map(
                (name) => (
                  <Button
                    key={name}
                    data-view={name}
                    aria-pressed={!showHost && tab === name}
                    onClick={() => {
                      setTab(name);
                      setShowHost(false);
                    }}
                  >
                    {name === 'machines'
                      ? 'VMs'
                      : name === 'volumes'
                        ? 'Volumes'
                        : name === 'images'
                          ? 'Images'
                          : 'Activity'}
                  </Button>
                ),
              )}
            </ButtonGroup>
          </div>
          <div className="machines-scroll">
            <div hidden={!showHost} inert={!showHost}>
              <MachineEnvironment
                data={data}
                desktop={desktop}
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
                </>
              )}
              {selected && (
                <div
                  className="machines-selection"
                  role="group"
                  aria-label="Selected VM actions"
                >
                  <div className="machines-selection-heading">
                    <div className="machines-selection-identity">
                      <strong title={selected.spec.name}>
                        {selected.spec.name}
                      </strong>
                      <MachineExpiry expiresAt={selected.expiresAt} />
                    </div>
                    <MachineActions
                      desktop={desktop}
                      windowId={window.id}
                      machine={selected}
                      reason={actionReason}
                      transition={transition}
                      terminalReason={terminalReason}
                      terminalPending={
                        openingTerminal === terminalIntent && !data.error
                      }
                      onAction={(action) => void lifecycle(action)}
                      onTerminal={() => void terminal()}
                      onInspect={inspect}
                    />
                  </div>
                  {transition && (
                    <p className="machines-vm-transition" role="status">
                      {transition}
                    </p>
                  )}
                  {notice &&
                  feedbackInSelection &&
                  notice.id === operationId ? (
                    actionFeedback
                  ) : notice ? (
                    <VMOperationNotice
                      operation={notice}
                      onShowActivity={showActivity}
                    />
                  ) : null}
                  {guidance && <p className="machines-form-help">{guidance}</p>}
                </div>
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
                      <span>
                        {(selected?.id === machine.id && transition) ||
                          machineTransition(
                            machine,
                            activeVMOperation(machine, data.operations),
                          ) ||
                          humanize(machine.status)}
                      </span>
                    </button>
                  ))}
                </div>
              ) : data.error && !data.machines.length ? null : (
                <div className="machines-empty">
                  <h2>
                    {data.machines.length
                      ? 'No VMs match your filters'
                      : data.loading
                        ? 'Loading virtual machines…'
                        : firecrackerImageRequired
                          ? 'Firecracker guest image required'
                          : !availableProviders.length
                            ? 'VM provider unavailable'
                            : !hasReadyImage
                              ? 'VM image required'
                              : 'No VMs yet'}
                  </h2>
                  {firecrackerImageRequired && !imageSetupCommand && (
                    <p>
                      Ask the deployment host administrator to register a
                      compatible Firecracker guest image. See the{' '}
                      <a
                        href={linuxVMSetupGuide}
                        target="_blank"
                        rel="noreferrer"
                      >
                        Linux VM setup guide
                      </a>
                      .
                    </p>
                  )}
                  {data.machines.length ? (
                    <Button
                      onClick={() => {
                        setSearch('');
                        setStateFilter('all');
                      }}
                    >
                      Clear filters
                    </Button>
                  ) : (
                    !canCreate &&
                    !imageSetupCommand &&
                    !data.loading &&
                    (!data.error && canImportImages ? (
                      <Button onClick={() => setTab('images')}>
                        Open Images
                      </Button>
                    ) : (
                      <Button onClick={() => setShowHost(true)}>
                        View environment
                      </Button>
                    ))
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
                windowId={window.id}
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
                defaultSetupCommand={imageSetupCommand}
              />
            </div>
            <div
              hidden={showHost || tab !== 'operations'}
              inert={showHost || tab !== 'operations'}
            >
              {selectedActivity &&
                !activityFound &&
                !data.loading &&
                !data.refreshing &&
                !data.error && (
                  <p className="machines-message">
                    This operation is unavailable in recent Activity.
                  </p>
                )}
              <table className="machines-table machines-inventory-table machines-activity-table">
                <colgroup>
                  <col span={2} className="machines-activity-label-column" />
                  <col />
                </colgroup>
                <thead>
                  <tr>
                    <th scope="col">Operation</th>
                    <th scope="col">Target</th>
                    <th scope="col">State</th>
                  </tr>
                </thead>
                <tbody ref={activityBody}>
                  {data.operations.map((op) => {
                    const createdAt = new Date(op.createdAt);
                    const phase = operationPhase(op);
                    return (
                      <tr
                        key={op.id}
                        data-operation-id={op.id}
                        data-selected={selectedActivity === op.id}
                        aria-current={
                          selectedActivity === op.id ? 'true' : undefined
                        }
                      >
                        <td>
                          <strong>{operationLabel(op, data.volumes)}</strong>
                          <small>
                            <time
                              dateTime={op.createdAt}
                              title={createdAt.toLocaleString()}
                            >
                              {createdAt.toLocaleDateString(undefined, {
                                dateStyle: 'short',
                              })}{' '}
                              <span>
                                {createdAt.toLocaleTimeString(undefined, {
                                  timeStyle: 'short',
                                })}
                              </span>
                            </time>
                          </small>
                        </td>
                        <td>
                          {operationTarget(op, data.machines, data.volumes)}
                        </td>
                        <td>
                          {humanize(op.status)}
                          {phase && <small>{phase}</small>}
                          {op.error && (
                            <small className="machines-failure">
                              {op.error}
                            </small>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                  {!data.operations.length && (
                    <tr>
                      <td colSpan={3}>No activity yet.</td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </>
      )}
      {!feedbackInSelection && actionFeedback}
      {!showHost && tab === 'machines' && data.machines.length > 0 && (
        <StatusBar>
          {`${data.machines.length} ${data.machines.length === 1 ? 'VM' : 'VMs'} · ${data.machines.filter((machine) => machine.status === 'running').length} running`}
        </StatusBar>
      )}
    </div>
  );
}
