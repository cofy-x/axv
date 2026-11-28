import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Button, Select } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { machinesApi } from '../machines/api';
import type { Machine } from '../machines/types';
import { openGuestTerminal } from '../machines/open-terminal';
import { vmProjectsAPI } from '../project-files/vm-api';
import type { VMProject } from '../project-files/vm-api';
import type { CodexSessionRecord } from './api';
import { vmCodexAPI } from './vm-api';
import type { GuestCodexInfo, VMTarget } from './vm-api';

interface VMNewSessionPanelProps {
  desktop: DesktopAPI;
  machines: Machine[];
  initialMachineId?: string;
  initialProjectId?: string;
  initialProjectPath?: string;
  onClose: () => void;
  onStarted: (session: CodexSessionRecord) => void;
}

function vmTargetFor(machine: Machine): VMTarget {
  return {
    hostId: machine.hostId,
    machineId: machine.id,
    machineName: machine.spec.name || machine.id,
  };
}

function vmSelectionKey(machine: Machine | null, hasCodexCap: boolean): string {
  if (!machine) return '';
  return `${machine.hostId}:${machine.id}:${machine.spec.volumeId || ''}:${hasCodexCap ? '1' : '0'}`;
}

export function VMNewSessionPanel({
  desktop,
  machines,
  initialMachineId,
  initialProjectId,
  initialProjectPath,
  onClose,
  onStarted,
}: VMNewSessionPanelProps) {
  const runningMachines = useMemo(
    () =>
      machines.filter(
        (machine) => machine.status === 'running' && machine.ready,
      ),
    [machines],
  );
  const [selectedId, setSelectedId] = useState(
    () =>
      runningMachines.find((m) => m.id === initialMachineId)?.id ||
      runningMachines[0]?.id ||
      '',
  );
  const selected =
    runningMachines.find((m) => m.id === selectedId) ||
    runningMachines[0] ||
    null;
  const hasCodexCap = !!selected?.guestCapabilities?.includes('codex');
  const selectedKey = vmSelectionKey(selected, hasCodexCap);
  const selectedRef = useRef(selected);
  selectedRef.current = selected;

  const [loadedKey, setLoadedKey] = useState('');
  const [vmInfo, setVmInfo] = useState<GuestCodexInfo | null>(null);
  const [loadingInfo, setLoadingInfo] = useState(false);
  const [infoError, setInfoError] = useState('');
  const [projects, setProjects] = useState<VMProject[]>([]);
  const [projectPath, setProjectPath] = useState(initialProjectPath || '');
  const [installing, setInstalling] = useState(false);
  const [starting, setStarting] = useState(false);
  const [openingTerminal, setOpeningTerminal] = useState(false);
  const [panelError, setPanelError] = useState('');
  const requestController = useRef<AbortController | null>(null);

  useEffect(() => {
    if (
      initialMachineId &&
      runningMachines.some((m) => m.id === initialMachineId)
    ) {
      setSelectedId(initialMachineId);
    }
  }, [initialMachineId, runningMachines]);

  useEffect(() => {
    if (initialProjectPath !== undefined) setProjectPath(initialProjectPath);
  }, [initialProjectPath]);

  useEffect(() => {
    if (!initialProjectId || initialProjectPath) return;
    const matched = projects.find((project) => project.id === initialProjectId);
    if (matched) setProjectPath(matched.root);
  }, [initialProjectId, initialProjectPath, projects]);

  const loadVMDetails = useCallback(
    async (
      machine: Machine,
      key: string,
      signal?: AbortSignal,
      preserveCurrent = false,
    ) => {
      if (!preserveCurrent) {
        setVmInfo(null);
        setProjects([]);
      }
      setLoadingInfo(true);
      setInfoError('');
      setPanelError('');
      const target = vmTargetFor(machine);
      try {
        const [infoResult, projectsResult] = await Promise.all([
          machine.guestCapabilities?.includes('codex')
            ? vmCodexAPI.info(target, signal)
            : Promise.resolve(null),
          vmProjectsAPI
            .list(machine.hostId, signal)
            .catch(() => ({ projects: [] })),
        ]);
        if (signal?.aborted) return;
        setVmInfo(infoResult);
        const bound = (projectsResult.projects || []).filter(
          (project) =>
            !machine.spec.volumeId ||
            project.volumeId === machine.spec.volumeId,
        );
        setProjects(bound);
        setLoadedKey(key);
        setInstalling(infoResult?.install?.state === 'running');
      } catch (error) {
        if (signal?.aborted) return;
        if (!preserveCurrent) setVmInfo(null);
        setInfoError(
          error instanceof Error
            ? error.message
            : 'Could not check Codex inside this VM.',
        );
      } finally {
        if (!signal?.aborted) setLoadingInfo(false);
      }
    },
    [],
  );

  useEffect(() => {
    requestController.current?.abort();
    const current = selectedRef.current;
    if (!selectedKey || !current) {
      setLoadedKey('');
      setVmInfo(null);
      setProjects([]);
      setLoadingInfo(false);
      return;
    }
    const controller = new AbortController();
    requestController.current = controller;
    void loadVMDetails(current, selectedKey, controller.signal, false);
    return () => {
      controller.abort();
      if (requestController.current === controller) {
        requestController.current = null;
      }
    };
  }, [selectedKey, loadVMDetails]);

  useEffect(() => {
    const current = selectedRef.current;
    if (!installing || !selectedKey || !current || !hasCodexCap) return;
    const target = vmTargetFor(current);
    const timer = setInterval(() => {
      void vmCodexAPI
        .installStatus(target)
        .then((status) => {
          setVmInfo((prev) => (prev ? { ...prev, install: status } : prev));
          if (status.state !== 'running') {
            setInstalling(false);
            if (status.state === 'succeeded' && selectedRef.current) {
              void loadVMDetails(
                selectedRef.current,
                selectedKey,
                undefined,
                true,
              );
            }
            if (status.state === 'failed' && status.message)
              setPanelError(status.message);
          }
        })
        .catch(() => {});
    }, 2000);
    return () => clearInterval(timer);
  }, [installing, selectedKey, hasCodexCap, loadVMDetails]);

  const recheckVM = () => {
    if (!selected || !selectedKey || loadingInfo || starting) return;
    requestController.current?.abort();
    const controller = new AbortController();
    requestController.current = controller;
    void loadVMDetails(
      selected,
      selectedKey,
      controller.signal,
      loadedKey === selectedKey,
    );
  };

  const startInstall = async () => {
    if (!selected || installing) return;
    setInstalling(true);
    setPanelError('');
    try {
      const status = await vmCodexAPI.install(vmTargetFor(selected));
      setVmInfo((prev) => (prev ? { ...prev, install: status } : prev));
    } catch (error) {
      setInstalling(false);
      setPanelError(
        error instanceof Error
          ? error.message
          : 'Could not start Codex CLI installation in VM.',
      );
    }
  };

  const openVMTerminalForSignIn = async () => {
    if (!selected || openingTerminal) return;
    setOpeningTerminal(true);
    setPanelError('');
    try {
      const terminal = await machinesApi.createTerminal(
        selected.id,
        crypto.randomUUID(),
        selected.hostId,
      );
      openGuestTerminal(desktop, selected, terminal);
    } catch (error) {
      setPanelError(
        error instanceof Error ? error.message : 'Could not open VM terminal.',
      );
    } finally {
      setOpeningTerminal(false);
    }
  };

  const startVMSession = async () => {
    if (!selected || !vmInfo?.ready || starting) return;
    setStarting(true);
    setPanelError('');
    try {
      const target = vmTargetFor(selected);
      const resolvedPath = projectPath
        ? projectPath.startsWith('/')
          ? projectPath
          : `/projects/${projectPath}`
        : undefined;
      const session = await vmCodexAPI.create(
        target,
        crypto.randomUUID(),
        resolvedPath,
      );
      onStarted(session);
    } catch (error) {
      setPanelError(
        error instanceof Error
          ? error.message
          : 'Could not start Codex in this VM.',
      );
    } finally {
      setStarting(false);
    }
  };

  const authBadge =
    vmInfo?.authMode === 'proxy'
      ? 'Zero-credential Host API Proxy (no keys in VM)'
      : vmInfo?.authMode === 'oauth'
        ? 'VM OAuth signed in (~/.codex/auth.json)'
        : vmInfo?.authMode === 'api_key'
          ? 'VM API key configured'
          : 'Sign in required — run codex login in VM Terminal or set AXV_VM_CODEX_API_KEY on host';
  const initialLoading = loadingInfo && loadedKey !== selectedKey;

  return (
    <section
      id="codex-vm-picker"
      className="codex-section codex-vm-picker"
      data-testid="codex-vm-picker"
      aria-label="New Codex session in VM"
    >
      <div className="codex-vm-picker-header">
        <strong>Start Codex inside a VM</strong>
        <span className="codex-target-badge codex-target-badge--fullauto">
          VM Full-Auto Sandbox
        </span>
      </div>
      {runningMachines.length === 0 ? (
        <div className="codex-vm-picker-empty">
          <p className="codex-note">
            No running VMs are ready. Start a VM in Virtual Machines first.
          </p>
          <div className="codex-project-choice">
            <Button onClick={onClose}>Cancel</Button>
            <Button primary onClick={() => desktop.openApp('machines')}>
              Open Virtual Machines
            </Button>
          </div>
        </div>
      ) : (
        <>
          <div className="codex-vm-picker-fields">
            <Select
              label="Target VM"
              value={selected?.id || ''}
              onChange={(event) => {
                setSelectedId(event.target.value);
                setProjectPath('');
              }}
              disabled={starting}
            >
              {runningMachines.map((machine) => (
                <option key={machine.id} value={machine.id}>
                  {machine.spec.name || machine.id}
                </option>
              ))}
            </Select>
            <Select
              label="VM Project"
              value={projectPath}
              onChange={(event) => setProjectPath(event.target.value)}
              disabled={starting || initialLoading}
            >
              <option value="">/projects (workspace root)</option>
              {projects.map((project) => (
                <option key={project.id} value={project.root}>
                  {project.name} (/projects/{project.root})
                </option>
              ))}
            </Select>
          </div>

          {!hasCodexCap && selected && (
            <p className="app-error" role="alert">
              This VM image does not declare the <code>codex</code> guest
              capability. Recreate or upgrade the VM with the current AXV guest
              image.
            </p>
          )}

          {hasCodexCap && initialLoading && !vmInfo && (
            <p className="codex-note" role="status">
              Checking Codex CLI and authentication in VM…
            </p>
          )}

          {infoError && (
            <p className="app-error" role="alert">
              {infoError}
            </p>
          )}

          {hasCodexCap && vmInfo && (
            <div className="codex-vm-status-card" data-testid="codex-vm-status">
              <div className="codex-vm-status-row">
                <span>Codex CLI</span>
                <strong>
                  {vmInfo.ready
                    ? vmInfo.codexVersion || 'Ready'
                    : vmInfo.installed
                      ? `Update needed (${vmInfo.codexVersion || 'installed'})`
                      : 'Not installed in VM'}
                </strong>
                {(!vmInfo.ready || installing) && (
                  <Button
                    disabled={installing}
                    onClick={() => void startInstall()}
                  >
                    {installing
                      ? 'Installing…'
                      : vmInfo.installed
                        ? 'Update Codex CLI'
                        : 'Install Codex CLI in VM'}
                  </Button>
                )}
              </div>
              {vmInfo.install.state !== 'idle' && vmInfo.install.message && (
                <p className="codex-note" role="status">
                  {vmInfo.install.message}
                </p>
              )}
              <div className="codex-vm-status-row">
                <span>Auth</span>
                <span>{authBadge}</span>
                {vmInfo.authMode === 'none' && (
                  <Button
                    disabled={openingTerminal}
                    onClick={() => void openVMTerminalForSignIn()}
                  >
                    {openingTerminal
                      ? 'Opening…'
                      : 'Open VM Terminal to Sign In'}
                  </Button>
                )}
              </div>
              {vmInfo.reason && !vmInfo.ready && (
                <p className="codex-note">{vmInfo.reason}</p>
              )}
            </div>
          )}

          {panelError && (
            <p className="app-error" role="alert">
              {panelError}
            </p>
          )}

          <div className="codex-project-choice">
            <Button disabled={starting} onClick={onClose}>
              Cancel
            </Button>
            {hasCodexCap && selected && (
              <Button disabled={loadingInfo || starting} onClick={recheckVM}>
                {loadingInfo && loadedKey === selectedKey
                  ? 'Checking…'
                  : 'Re-check VM'}
              </Button>
            )}
            <Button
              primary
              disabled={!hasCodexCap || !vmInfo?.ready || starting}
              onClick={() => void startVMSession()}
            >
              {starting ? 'Starting in VM…' : 'Start in VM'}
            </Button>
          </div>
        </>
      )}
    </section>
  );
}
