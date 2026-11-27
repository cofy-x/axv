import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import { Button, ButtonGroup, StatusBar } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useWindowCommands } from '../../shared/desktop/WindowCommandsContext';
import { machineMenuCommand } from './menus';
import { useCompute } from './useCompute';
import { RequestDetails } from './OperationFeedback';
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
import { creationImages } from './form-state';
import { openMachineInspector } from './desktop-icons';
import { VolumeManager } from './VolumeManager';
import { ImageManager } from './ImageManager';
import { MachineEnvironment } from './MachineEnvironment';
import { MachinesGridView } from './MachinesGridView';
import { MachinesActivityTable } from './MachinesActivityTable';
import { preparationRunning } from './preparation-store';
import {
  canCopyLocalLimaShell,
  guestTerminalReason,
  humanize,
  latestVMFailure,
  machineGuidance,
  machineTransition,
  operationPhase,
} from './presentation';

export function Machines({
  window,
  desktop,
  visible: windowVisible,
  interactive,
}: AppWindowProps) {
  const { setTitle, updateWindowState } = desktop;
  const commands = useWindowCommands();
  const data = useCompute(windowVisible);
  const refreshData = data.refresh;
  const tab =
    (window.viewState.vmTab as
      'machines' | 'volumes' | 'images' | 'operations') || 'machines';
  const setTab = (value: string) =>
    desktop.updateWindowState(window.id, { vmTab: value });
  const [busy, setBusy] = useState(false);
  const [openingTerminal, setOpeningTerminal] = useState<string>();
  const [copyingLimaShell, setCopyingLimaShell] = useState(false);
  const [limaShellNotice, setLimaShellNotice] = useState<{
    key: string;
    copied: boolean;
    command: string;
  }>();
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
  const emptyInventory = canCreate && !data.loading && !data.machines.length;
  const canImportImages = availableProviders.some((item) => item.images);
  const firecrackerImageRequired =
    !data.machines.length &&
    !data.loading &&
    !data.error &&
    !hasReadyImage &&
    !canImportImages &&
    availableProviders.some((item) => item.provider === 'firecracker');
  const operation = data.operations.find((item) => item.id === operationId);
  const preparing = preparationRunning(data.preparation.job);
  const inventoryError = !preparing ? data.error : undefined;
  const offlineActions = configured && !!inventoryError;

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

  const newVM = useCallback(() => {
    desktop.openApp('machine-create');
  }, [desktop]);

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

  const selectedKey = selected ? `${selected.hostId}:${selected.id}` : '';
  const localOwner = Boolean(data.preparation.check);
  const canCopyLimaShell =
    !transition && !data.error && canCopyLocalLimaShell(selected, localOwner);
  const copyLimaShell = useCallback(async () => {
    if (!selected || !canCopyLimaShell) return;
    const key = `${selected.hostId}:${selected.id}`;
    setCopyingLimaShell(true);
    setError('');
    try {
      const info = await machinesApi.limaShell(selected.id);
      let copied = false;
      try {
        if (navigator.clipboard?.writeText) {
          await navigator.clipboard.writeText(info.command);
          copied = true;
        }
      } catch {
        copied = false;
      }
      setLimaShellNotice({ key, copied, command: info.command });
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      setCopyingLimaShell(false);
    }
  }, [selected, canCopyLimaShell]);

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
        canCreate && !busy,
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

  const viewSwitcher = (
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
  );

  return (
    <div className="machines-app">
      {configured && !offlineActions && (
        <div className="app-toolbar machines-toolbar">
          {!data.error && (
            <Button
              primary={emptyInventory && tab === 'machines' && !showHost}
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
          )}
          {viewSwitcher}
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
          {configured && inventoryError && (
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
          {offlineActions && viewSwitcher}
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
              <MachinesGridView
                desktop={desktop}
                windowId={window.id}
                machines={data.machines}
                visible={visible}
                volumes={data.volumes}
                operations={data.operations}
                selected={selected}
                search={search}
                stateFilter={stateFilter}
                loading={data.loading}
                error={data.error}
                transition={transition}
                notice={notice}
                operationId={operationId}
                feedbackInSelection={!!feedbackInSelection}
                actionFeedback={actionFeedback}
                guidance={guidance}
                limaShellNotice={limaShellNotice}
                selectedKey={selectedKey}
                emptyInventory={emptyInventory}
                firecrackerImageRequired={firecrackerImageRequired}
                availableProvidersCount={availableProviders.length}
                hasReadyImage={hasReadyImage}
                canCreate={canCreate}
                canImportImages={canImportImages}
                actionReason={actionReason}
                terminalReason={terminalReason}
                terminalPending={
                  openingTerminal === terminalIntent && !data.error
                }
                canCopyLimaShell={canCopyLimaShell}
                copyingLimaShell={copyingLimaShell}
                onSearchChange={setSearch}
                onStateFilterChange={setStateFilter}
                onLifecycle={(action) => void lifecycle(action)}
                onTerminal={() => void terminal()}
                onCopyLimaShell={() => void copyLimaShell()}
                onInspect={inspect}
                onShowActivity={showActivity}
                onOpenImages={() => setTab('images')}
                onShowHost={() => setShowHost(true)}
              />
            </div>
            <div
              hidden={showHost || tab !== 'volumes'}
              inert={showHost || tab !== 'volumes'}
            >
              <VolumeManager
                data={data}
                desktop={desktop}
                windowId={window.id}
                visible={
                  windowVisible !== false && !showHost && tab === 'volumes'
                }
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
              <MachinesActivityTable
                operations={data.operations}
                machines={data.machines}
                volumes={data.volumes}
                selectedActivity={selectedActivity}
                activityFound={activityFound}
                loading={data.loading}
                refreshing={data.refreshing}
                error={data.error}
                activityBodyRef={activityBody}
              />
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
