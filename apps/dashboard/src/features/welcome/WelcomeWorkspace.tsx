import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, IconButton } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { codexAPI, sessionWindowTitle } from '../codex/api';
import type { CodexSessionRecord } from '../codex/api';
import { openCodexSession } from '../codex/definition';
import { useCompute } from '../machines/useCompute';
import { openMachineInspector } from '../machines/desktop-icons';
import {
  currentGuestTerminalTarget,
  guestTerminalRequests,
  openGuestTerminal,
} from '../machines/open-terminal';
import { machinesApi } from '../machines/api';
import type { Machine } from '../machines/api';
import { guestTerminalReason, humanize } from '../machines/presentation';
import { projectsAPI } from '../project-files/api';
import type { Project } from '../project-files/api';
import { projectLabel } from '../project-files/presentation';
import { vmProjectsAPI } from '../project-files/vm-api';
import type { VMProject } from '../project-files/vm-api';
import { canOpenVMProjectVolume } from '../project-files/vm-project-volume';

export function WelcomeWorkspace({
  desktop,
  visible,
}: {
  desktop: DesktopAPI;
  visible?: boolean;
}) {
  const compute = useCompute(visible !== false);
  const [sessions, setSessions] = useState<CodexSessionRecord[]>([]);
  const [hostProjects, setHostProjects] = useState<Project[]>([]);
  const [vmProjects, setVMProjects] = useState<VMProject[]>([]);
  const [refreshing, setRefreshing] = useState(false);
  const [openingTerminalId, setOpeningTerminalId] = useState('');
  const [actionError, setActionError] = useState('');
  const mounted = useRef(true);
  const hostId = compute.info?.configured ? compute.info.hostId || '' : '';

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const loadWorkspace = useCallback(
    async (signal?: AbortSignal) => {
      setRefreshing(true);
      try {
        const [codexResult, hostResult, vmResult] = await Promise.allSettled([
          codexAPI.list(signal),
          projectsAPI.list(),
          hostId
            ? vmProjectsAPI.list(hostId, signal)
            : Promise.resolve({ projects: [] as VMProject[] }),
        ]);
        if (!mounted.current || signal?.aborted) return;
        if (codexResult.status === 'fulfilled')
          setSessions(codexResult.value.sessions);
        if (hostResult.status === 'fulfilled')
          setHostProjects(hostResult.value.projects);
        if (vmResult.status === 'fulfilled')
          setVMProjects(vmResult.value.projects);
      } finally {
        if (mounted.current && !signal?.aborted) setRefreshing(false);
      }
    },
    [hostId],
  );

  useEffect(() => {
    if (visible === false) return;
    const controller = new AbortController();
    void loadWorkspace(controller.signal);
    return () => controller.abort();
  }, [visible, loadWorkspace]);

  const attentionSessions = sessions.filter(
    (session) => !session.archivedAt && session.activity?.needsAttention,
  );
  const activeSessions = sessions.filter(
    (session) =>
      !session.archivedAt &&
      !session.activity?.needsAttention &&
      (session.status === 'running' || session.status === 'starting'),
  );
  const sortedMachines = [...compute.machines].sort((a, b) => {
    const rank = (status: string) =>
      status === 'running' ? 0 : status === 'starting' ? 1 : 2;
    return rank(a.status) - rank(b.status);
  });
  const visibleMachines = sortedMachines.slice(0, 4);
  const runningMachinesCount = compute.machines.filter(
    (machine) => machine.status === 'running',
  ).length;
  const totalProjects = vmProjects.length + hostProjects.length;
  const hasContent =
    attentionSessions.length > 0 ||
    activeSessions.length > 0 ||
    visibleMachines.length > 0 ||
    totalProjects > 0;

  if (!hasContent) return null;

  async function openTerminal(machine: Machine) {
    if (openingTerminalId) return;
    const target = currentGuestTerminalTarget(machine);
    if (!target) return;
    setOpeningTerminalId(machine.id);
    setActionError('');
    try {
      const { terminals } = await machinesApi.terminals(
        target.id,
        target.hostId,
        AbortSignal.timeout(15000),
      );
      if (!mounted.current) return;
      const running = terminals.filter((item) => item.status === 'running');
      const session =
        running[0] ??
        (await guestTerminalRequests.execute(target.id, target.hostId));
      if (!mounted.current) return;
      const current = currentGuestTerminalTarget(target);
      if (current) openGuestTerminal(desktop, current, session);
    } catch (failure) {
      if (mounted.current)
        setActionError(
          failure instanceof Error ? failure.message : String(failure),
        );
    } finally {
      if (mounted.current) setOpeningTerminalId('');
    }
  }

  const summaryParts = [
    runningMachinesCount > 0
      ? `${runningMachinesCount} VM${runningMachinesCount === 1 ? '' : 's'} running`
      : visibleMachines.length > 0
        ? `${compute.machines.length} VM${compute.machines.length === 1 ? '' : 's'}`
        : '',
    totalProjects > 0
      ? `${totalProjects} project${totalProjects === 1 ? '' : 's'}`
      : '',
    activeSessions.length + attentionSessions.length > 0
      ? `${activeSessions.length + attentionSessions.length} active session${activeSessions.length + attentionSessions.length === 1 ? '' : 's'}`
      : '',
  ].filter(Boolean);

  return (
    <section className="welcome-workspace" aria-label="Workspace overview">
      <div className="welcome-workspace-header">
        <div>
          <h2>Workspace</h2>
          {summaryParts.length > 0 && (
            <span className="welcome-workspace-summary">
              {summaryParts.join(' · ')}
            </span>
          )}
        </div>
        <IconButton
          icon="refresh"
          label="Refresh workspace"
          disabled={refreshing}
          onClick={() => {
            compute.refresh();
            void loadWorkspace();
          }}
        />
      </div>

      {actionError && (
        <p className="welcome-workspace-error" role="alert">
          {actionError}
        </p>
      )}

      {attentionSessions.length > 0 && (
        <div
          className="welcome-section"
          role="region"
          aria-label="Action needed"
        >
          <h3>Action needed</h3>
          <ul className="welcome-list">
            {attentionSessions.slice(0, 3).map((session) => (
              <li key={session.id} className="welcome-item is-attention">
                <div className="welcome-item-main">
                  <strong>{sessionWindowTitle(session)}</strong>
                  <small>
                    {humanize(session.activity?.state || session.status)} ·{' '}
                    {session.projectPath}
                  </small>
                </div>
                <div className="welcome-item-actions">
                  <Button
                    primary
                    onClick={() => openCodexSession(desktop, session)}
                  >
                    Open
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        </div>
      )}

      {visibleMachines.length > 0 && (
        <div
          className="welcome-section"
          role="region"
          aria-label="Recent virtual machines"
        >
          <div className="welcome-section-heading">
            <h3>Virtual Machines</h3>
            <button
              type="button"
              className="welcome-section-link"
              onClick={() => desktop.openApp('machines')}
            >
              View all
            </button>
          </div>
          <ul className="welcome-list">
            {visibleMachines.map((machine) => {
              const volume = compute.volumes.find(
                (item) => item.id === machine.spec.volumeId,
              );
              const canOpenFiles =
                !!volume && canOpenVMProjectVolume(machine, volume);
              const canTerminal = !guestTerminalReason(machine);
              return (
                <li
                  key={`${machine.hostId}:${machine.id}`}
                  className="welcome-item"
                >
                  <div className="welcome-item-main">
                    <strong>
                      <span
                        className="welcome-status-dot"
                        data-status={machine.status}
                        aria-hidden="true"
                      />
                      {machine.spec.name}
                    </strong>
                    <small>
                      {humanize(machine.status)} · {machine.spec.resources.cpus}{' '}
                      CPU · {machine.spec.resources.memoryMiB} MiB
                    </small>
                  </div>
                  <div className="welcome-item-actions">
                    {canTerminal && (
                      <Button
                        disabled={openingTerminalId === machine.id}
                        onClick={() => void openTerminal(machine)}
                      >
                        {openingTerminalId === machine.id
                          ? 'Opening…'
                          : 'Terminal'}
                      </Button>
                    )}
                    {canOpenFiles && volume && (
                      <Button
                        onClick={() =>
                          desktop.openApp(
                            'project-files',
                            {
                              hostId: machine.hostId,
                              machineId: machine.id,
                              vmProjectVolumeId: volume.id,
                            },
                            { title: `${machine.spec.name} — Project Files` },
                          )
                        }
                      >
                        Projects
                      </Button>
                    )}
                    <Button
                      onClick={() => openMachineInspector(desktop, machine)}
                    >
                      Inspect
                    </Button>
                  </div>
                </li>
              );
            })}
          </ul>
        </div>
      )}

      {totalProjects > 0 && (
        <div
          className="welcome-section"
          role="region"
          aria-label="Recent projects"
        >
          <div className="welcome-section-heading">
            <h3>Projects</h3>
            <button
              type="button"
              className="welcome-section-link"
              onClick={() =>
                desktop.openApp('project-files', {
                  ...(vmProjects.length > 0 ? { projectLocation: 'vm' } : {}),
                })
              }
            >
              Manage
            </button>
          </div>
          <ul className="welcome-list">
            {vmProjects.slice(0, 4).map((project) => (
              <li key={`vm:${project.id}`} className="welcome-item">
                <div className="welcome-item-main">
                  <strong>
                    <span className="welcome-badge">VM</span>
                    {project.name}
                  </strong>
                  <small>{project.root || 'Volume root'}</small>
                </div>
                <div className="welcome-item-actions">
                  <Button
                    onClick={() =>
                      desktop.openApp(
                        'project-files',
                        { vmProjectId: project.id, hostId: project.hostId },
                        { title: `VM · ${project.name}` },
                      )
                    }
                  >
                    Open
                  </Button>
                </div>
              </li>
            ))}
            {hostProjects
              .slice(0, Math.max(0, 6 - Math.min(vmProjects.length, 4)))
              .map((project) => {
                const label = projectLabel(project, hostProjects);
                return (
                  <li key={`host:${project.id}`} className="welcome-item">
                    <div className="welcome-item-main">
                      <strong>
                        <span className="welcome-badge">Host</span>
                        {label}
                      </strong>
                      <small>{project.rootPath}</small>
                    </div>
                    <div className="welcome-item-actions">
                      <Button
                        onClick={() =>
                          desktop.openApp(
                            'project-files',
                            { projectId: project.id },
                            { title: label },
                          )
                        }
                      >
                        Open
                      </Button>
                    </div>
                  </li>
                );
              })}
          </ul>
        </div>
      )}

      {activeSessions.length > 0 && (
        <div
          className="welcome-section"
          role="region"
          aria-label="Active Codex sessions"
        >
          <div className="welcome-section-heading">
            <h3>Active Codex Sessions</h3>
            <button
              type="button"
              className="welcome-section-link"
              onClick={() => desktop.openApp('codex')}
            >
              All sessions
            </button>
          </div>
          <ul className="welcome-list">
            {activeSessions.slice(0, 3).map((session) => (
              <li key={session.id} className="welcome-item">
                <div className="welcome-item-main">
                  <strong>{sessionWindowTitle(session)}</strong>
                  <small>{session.projectPath}</small>
                </div>
                <div className="welcome-item-actions">
                  <Button onClick={() => openCodexSession(desktop, session)}>
                    Open
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}
