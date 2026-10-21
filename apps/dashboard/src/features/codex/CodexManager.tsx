import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Button,
  ButtonGroup,
  Field,
  IconButton,
  PixelIcon,
  StatusBar,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import {
  codexAPI,
  projectName,
  sessionTitle,
  sessionIsLive,
  sessionCanEnd,
  sessionCanContinue,
} from './api';
import { APIError } from '../../shared/api/http';
import type { CodexProject, CodexSessionRecord, HostDirectories } from './api';
import { useCodexAvailability } from './CodexContext';
import { openCodexEnvironment, openCodexSession } from './definition';
import {
  confirmEndSession,
  renameSession,
  setSessionArchived,
} from './actions';
import { showSessionDetails } from './details';
import {
  mergeSessionMetadata,
  publishSessionMetadata,
  subscribeSessionMetadata,
} from './metadata';
import { openTerminalWindow } from '../terminal/definition';
import { CodexResumeRequests, continueConversation } from './continuation';
import { useVMProject } from './use-vm-project';
import { useDraftNamespace } from '../../shared/drafts/context';

interface ContinuationRecovery {
  requestId?: string;
  error?: string;
}

export function CodexManager({ desktop, window }: AppWindowProps) {
  const vmProjectId = window.params.vmProjectId || '';
  const vmHostId = window.params.hostId || '';
  const vm = useVMProject(vmHostId, vmProjectId);
  const namespace = useDraftNamespace();
  const { info, loading, error, status } = useCodexAvailability();
  const canStart = info?.ready === true && !loading && !error;
  const environmentIssue =
    error ||
    (info && !info.ready
      ? info.reason || 'Set up the host environment before starting a session.'
      : '');
  const setTitle = desktop.setTitle;
  const [sessions, setSessions] = useState<CodexSessionRecord[]>([]);
  const [projects, setProjects] = useState<CodexProject[]>([]);
  const [projectError, setProjectError] = useState('');
  const [showArchived, setShowArchived] = useState(false);
  const [changingMetadata, setChangingMetadata] = useState<string | null>(null);
  const [continuing, setContinuing] = useState<string | null>(null);
  const [continuationRecovery, setContinuationRecovery] = useState(
    new Map<string, ContinuationRecovery>(),
  );
  const [sessionError, setSessionError] = useState('');
  const [directories, setDirectories] = useState<HostDirectories | null>(null);
  const [path, setPath] = useState('');
  const [choosingFolder, setChoosingFolder] = useState(false);
  const [directoryError, setDirectoryError] = useState('');
  const [browsing, setBrowsing] = useState(false);
  const [creating, setCreating] = useState<'default' | 'folder' | null>(null);
  const [ending, setEnding] = useState<string | null>(null);
  const [actionError, setActionError] = useState('');
  const mounted = useRef(false);
  const listing = useRef<AbortController | null>(null);
  const listingProjects = useRef<AbortController | null>(null);
  const browsingRequest = useRef<AbortController | null>(null);
  const creatingRequest = useRef(false);
  const requestKey = useRef<{ path?: string; id: string } | null>(null);
  const resumeRequests = useRef(
    new CodexResumeRequests(JSON.stringify(namespace), namespace.databaseId),
  );
  const continuingRequest = useRef(false);

  useEffect(() => {
    const recovery = new Map<string, ContinuationRecovery>();
    for (const session of sessions) {
      try {
        recovery.set(session.id, {
          requestId: resumeRequests.current.restore(session)?.requestId,
        });
      } catch (error) {
        recovery.set(session.id, {
          error: error instanceof Error ? error.message : String(error),
        });
      }
    }
    setContinuationRecovery(recovery);
  }, [sessions, continuing]);

  const vmName = vm.data?.project.name || 'VM project';
  const resolvedVMName = vm.data?.project.name;
  useEffect(() => {
    if (!vmProjectId) setTitle(window.id, 'Codex');
    else if (resolvedVMName)
      setTitle(window.id, `Codex Sessions · ${resolvedVMName}`);
  }, [setTitle, window.id, vmProjectId, resolvedVMName]);

  const refresh = useCallback(async () => {
    if (listing.current) return;
    const controller = new AbortController();
    listing.current = controller;
    try {
      const result = await codexAPI.list(controller.signal);
      if (!controller.signal.aborted && mounted.current) {
        setSessions((current) =>
          result.sessions.map((session) => {
            const previous = current.find((item) => item.id === session.id);
            return previous ? mergeSessionMetadata(session, previous) : session;
          }),
        );
        for (const session of result.sessions) publishSessionMetadata(session);
        setSessionError('');
      }
    } catch (error) {
      if (!controller.signal.aborted && mounted.current)
        setSessionError(
          error instanceof Error
            ? error.message
            : 'Could not load Codex sessions.',
        );
    } finally {
      if (listing.current === controller) listing.current = null;
    }
  }, []);

  const refreshProjects = useCallback(async () => {
    if (vmProjectId) return;
    listingProjects.current?.abort();
    const controller = new AbortController();
    listingProjects.current = controller;
    try {
      const result = await codexAPI.projects(controller.signal);
      if (!controller.signal.aborted && mounted.current) {
        setProjects(result.projects);
        setProjectError('');
      }
    } catch (error) {
      if (!controller.signal.aborted && mounted.current)
        setProjectError(
          error instanceof Error
            ? error.message
            : 'Could not load recent projects.',
        );
    } finally {
      if (listingProjects.current === controller)
        listingProjects.current = null;
    }
  }, [vmProjectId]);

  const browse = useCallback(async (next?: string) => {
    browsingRequest.current?.abort();
    const controller = new AbortController();
    browsingRequest.current = controller;
    setBrowsing(true);
    setDirectoryError('');
    try {
      const result = await codexAPI.directories(next, controller.signal);
      if (!controller.signal.aborted && mounted.current) {
        setDirectories(result);
        setPath(result.path);
      }
    } catch (error) {
      if (!controller.signal.aborted && mounted.current)
        setDirectoryError(
          error instanceof Error
            ? error.message
            : 'Could not read this directory.',
        );
    } finally {
      if (!controller.signal.aborted && mounted.current) setBrowsing(false);
    }
  }, []);

  useEffect(() => {
    mounted.current = true;
    const unsubscribe = subscribeSessionMetadata((metadata) => {
      setSessions((current) =>
        current.map((session) => mergeSessionMetadata(session, metadata)),
      );
    });
    void refresh();
    void refreshProjects();
    const timer = setInterval(() => {
      if (document.visibilityState === 'visible') {
        void refresh();
        if (!listingProjects.current) void refreshProjects();
      }
    }, 4000);
    const focus = () => {
      void refresh();
      void refreshProjects();
    };
    globalThis.addEventListener('focus', focus);
    return () => {
      mounted.current = false;
      unsubscribe();
      clearInterval(timer);
      globalThis.removeEventListener('focus', focus);
      listing.current?.abort();
      listing.current = null;
      listingProjects.current?.abort();
      listingProjects.current = null;
      browsingRequest.current?.abort();
    };
  }, [refresh, refreshProjects]);

  const closeFolderPicker = () => {
    browsingRequest.current?.abort();
    setBrowsing(false);
    setChoosingFolder(false);
    setDirectoryError('');
  };

  const create = async (project?: string) => {
    if (
      creatingRequest.current ||
      (vmProjectId
        ? (!vm.pending && (!canStart || !vm.data)) || !!vm.recoveryError
        : !canStart)
    )
      return;
    creatingRequest.current = true;
    setCreating(project ? 'folder' : 'default');
    setActionError('');
    if (!requestKey.current || requestKey.current.path !== project)
      requestKey.current = { path: project, id: crypto.randomUUID() };
    try {
      const result = vmProjectId
        ? await vm.create()
        : {
            session: await codexAPI.create(requestKey.current.id, project),
            recovered: false,
          };
      const { session } = result;
      requestKey.current = null;
      if (mounted.current) {
        if (sessionIsLive(session) || result.recovered) {
          closeFolderPicker();
          setShowArchived(false);
          openCodexSession(desktop, session);
        } else
          setActionError(
            session.reason ||
              `Codex could not start (${session.status}). Choose New Session to try again.`,
          );
        void refresh();
        void refreshProjects();
      }
    } catch (error) {
      if (error instanceof APIError && error.status < 500)
        requestKey.current = null;
      if (mounted.current)
        setActionError(
          error instanceof Error ? error.message : 'Could not start Codex.',
        );
    } finally {
      creatingRequest.current = false;
      if (mounted.current) setCreating(null);
    }
  };

  const end = async (session: CodexSessionRecord) => {
    if (ending) return;
    setEnding(session.id);
    setActionError('');
    try {
      const result = await confirmEndSession(desktop, session);
      if (result && mounted.current) {
        setSessions((current) =>
          current.map((item) =>
            item.id === result.id && item.runId === result.runId
              ? mergeSessionMetadata(result, item)
              : mergeSessionMetadata(item, result),
          ),
        );
        void refresh();
      }
    } catch (error) {
      if (mounted.current)
        setActionError(
          error instanceof Error
            ? error.message
            : 'Could not end this session.',
        );
    } finally {
      if (mounted.current) setEnding(null);
    }
  };

  const changeMetadata = async (
    session: CodexSessionRecord,
    action: 'rename' | 'archive',
  ) => {
    if (changingMetadata) return;
    setChangingMetadata(session.id);
    setActionError('');
    try {
      if (action === 'rename') await renameSession(desktop, session);
      else await setSessionArchived(session);
    } catch (error) {
      if (mounted.current)
        setActionError(
          error instanceof Error
            ? error.message
            : 'Could not update the session.',
        );
    } finally {
      if (mounted.current) setChangingMetadata(null);
    }
  };
  const continueSession = async (
    session: CodexSessionRecord,
    expectedRequestId: string | null,
  ) => {
    if (continuingRequest.current) return;
    continuingRequest.current = true;
    setContinuing(session.id);
    setActionError('');
    try {
      const result = await continueConversation(
        desktop,
        session,
        resumeRequests.current,
        expectedRequestId,
      );
      if (result && mounted.current) openCodexSession(desktop, result);
    } catch (error) {
      if (mounted.current) {
        const reason =
          error instanceof Error
            ? error.message
            : 'Could not continue the conversation.';
        setActionError(
          resumeRequests.current.hasPending(session.id)
            ? `The continuation result is unknown. Choose Continue again to check the same request, or open the running session if it appears. ${reason}`
            : reason,
        );
      }
    } finally {
      continuingRequest.current = false;
      if (mounted.current) {
        setContinuing(null);
        void refresh();
        void refreshProjects();
      }
    }
  };
  const visibleSessions = sessions.filter(
    (session) =>
      !!session.archivedAt === showArchived &&
      (!vmProjectId ||
        (session.vmProject?.project.id === vmProjectId &&
          session.vmProject.project.hostId === vmHostId)),
  );

  return (
    <div className="codex-manager" data-testid="codex-manager">
      <div className="app-toolbar codex-manager-toolbar">
        <PixelIcon name="companion" size={16} />
        <Button
          primary
          disabled={
            creating !== null ||
            (vmProjectId
              ? !!vm.recoveryError || (!vm.pending && (!canStart || !vm.data))
              : !canStart)
          }
          onClick={() => void create()}
        >
          {creating === 'default'
            ? 'Starting…'
            : vmProjectId && vm.pending
              ? 'Resolve Start'
              : 'New Session'}
        </Button>
        {!vmProjectId && (
          <Button
            disabled={creating !== null || (!choosingFolder && !canStart)}
            aria-expanded={choosingFolder}
            aria-controls="codex-folder-picker"
            onClick={() => {
              if (choosingFolder) closeFolderPicker();
              else {
                setChoosingFolder(true);
                void browse(path || undefined);
              }
            }}
          >
            New in Folder
          </Button>
        )}
        {vmProjectId && (
          <>
            <Button
              disabled={creating !== null || !!vm.pending || !!vm.recoveryError}
              onClick={vm.reconnect}
            >
              Reconnect Project
            </Button>
            <Button
              onClick={() =>
                desktop.openApp(
                  'project-files',
                  { hostId: vmHostId, vmProjectId },
                  { title: `VM · ${vmName}` },
                )
              }
            >
              Project Files
            </Button>
            <Button
              onClick={() =>
                desktop.openApp(
                  'project-files',
                  { hostId: vmHostId, vmTasksProjectId: vmProjectId },
                  { title: `Tasks · ${vmName}` },
                )
              }
            >
              Tasks
            </Button>
          </>
        )}
        <div className="codex-manager-tools">
          <IconButton
            icon="refresh"
            label="Refresh"
            onClick={() => {
              void refresh();
              void refreshProjects();
            }}
          />
          <IconButton
            icon="more"
            label="More Codex manager actions"
            aria-haspopup="menu"
            onClick={(event) => {
              const rect = event.currentTarget.getBoundingClientRect();
              desktop.contextMenu(
                [
                  {
                    label: 'Environment',
                    action: () => openCodexEnvironment(desktop),
                  },
                ],
                { x: rect.left, y: rect.bottom },
                { title: 'Codex' },
              );
            }}
          />
        </div>
      </div>
      <div className="codex-manager-content">
        {vmProjectId ? (
          <section className="codex-vm-project" aria-label="VM project context">
            <strong>VM · {vmName}</strong>
            <p>
              Codex runs on the host. The AXV Skill guides it to read and edit
              this VM project and run persistent tasks through AXV. Host-agent
              permissions remain unchanged.
            </p>
            {vm.pending ? (
              <p role="status">
                The previous start is unconfirmed. Resolve Start checks the same
                request for VM <code>{vm.pending.binding.machineId}</code>; it
                does not select another environment.
              </p>
            ) : vm.data ? (
              <p>
                New session environment:{' '}
                <code>{vm.data.binding.machineId}</code> ·{' '}
                {vm.data.project.root || 'Volume root'}
              </p>
            ) : (
              !vm.error && <p role="status">Connecting to VM project…</p>
            )}
            {(vm.error || vm.recoveryError) && (
              <p className="app-error" role="alert">
                {vm.recoveryError || vm.error}
              </p>
            )}
            <details>
              <summary>Details</summary>
              <dl className="info-list">
                <dt>Host</dt>
                <dd>
                  <code>{vmHostId}</code>
                </dd>
                <dt>Project</dt>
                <dd>
                  <code>{vmProjectId}</code>
                </dd>
                {vm.data && (
                  <>
                    <dt>Volume</dt>
                    <dd>
                      <code>{vm.data.binding.volumeId}</code>
                    </dd>
                    <dt>Boot / instance</dt>
                    <dd>
                      <code>
                        {vm.data.binding.bootId} / {vm.data.binding.instanceId}
                      </code>
                    </dd>
                  </>
                )}
              </dl>
            </details>
          </section>
        ) : (
          <p className="codex-default-project">
            <span>Default host folder</span>{' '}
            <code data-testid="codex-default-project">
              {info?.defaultProjectPath || '~/.axv/workspace'}
            </code>
          </p>
        )}
        {environmentIssue && (
          <div className="app-error codex-environment-notice" role="alert">
            <p>{environmentIssue}</p>
            <Button onClick={() => openCodexEnvironment(desktop)}>
              Environment
            </Button>
          </div>
        )}
        {!vmProjectId && (projects.length > 0 || projectError) && (
          <section
            className="codex-section codex-recent"
            aria-label="Recent projects"
          >
            <h2>Recent Projects</h2>
            {projectError && (
              <p className="app-error" role="alert">
                {projectError}
              </p>
            )}
            <ul className="codex-recent-list">
              {projects.map((project) => (
                <li key={project.path}>
                  <span>
                    <strong>{projectName(project.path)}</strong>
                    <code title={project.path}>{project.path}</code>
                  </span>
                  <Button
                    disabled={!canStart || creating !== null}
                    aria-label={`New session in ${project.path}`}
                    onClick={() => void create(project.path)}
                  >
                    New Session
                  </Button>
                </li>
              ))}
            </ul>
          </section>
        )}
        {choosingFolder && (
          <section
            id="codex-folder-picker"
            className="codex-section codex-folder-picker"
            aria-label="New Codex session"
          >
            <h2>Choose a Host Folder</h2>
            <form
              className="codex-path-form"
              onSubmit={(event) => {
                event.preventDefault();
                void browse(path);
              }}
            >
              <Field
                label="Project directory"
                value={path}
                disabled={browsing || creating !== null}
                onChange={(event) => setPath(event.target.value)}
              />
              <Button type="submit" disabled={browsing || creating !== null}>
                Go
              </Button>
              <Button
                disabled={browsing || creating !== null}
                onClick={() => void browse()}
              >
                Home
              </Button>
              <IconButton
                icon="up"
                label="Up"
                disabled={
                  browsing || creating !== null || !directories?.parentPath
                }
                onClick={() =>
                  void browse(directories?.parentPath || undefined)
                }
              />
            </form>
            {directoryError && (
              <p className="app-error" role="alert">
                {directoryError}
              </p>
            )}
            <div
              className="codex-directory-list"
              aria-label="Host folders"
              aria-busy={browsing}
            >
              {browsing ? (
                <p role="status">Loading folders…</p>
              ) : (
                directories?.directories.map((directory) => (
                  <button
                    type="button"
                    key={directory.path}
                    disabled={creating !== null}
                    aria-label={`Open folder ${directory.name}`}
                    onClick={() => void browse(directory.path)}
                  >
                    <PixelIcon name="folder" size={16} />
                    <span>{directory.name}</span>
                  </button>
                ))
              )}
              {!browsing && directories?.directories.length === 0 && (
                <p>No subfolders.</p>
              )}
            </div>
            {directories?.truncated && (
              <p className="codex-note">
                Some folders are not shown. Enter a full path to browse.
              </p>
            )}
            <div className="codex-project-choice">
              <code title={directories?.path}>
                {directories?.path || 'Choose an accessible directory.'}
              </code>
              <Button disabled={creating !== null} onClick={closeFolderPicker}>
                Cancel
              </Button>
              <Button
                primary
                disabled={
                  !canStart ||
                  !directories ||
                  browsing ||
                  !!directoryError ||
                  creating !== null ||
                  path !== directories.path
                }
                onClick={() => {
                  if (directories) void create(directories.path);
                }}
              >
                {creating === 'folder' ? 'Starting…' : 'Start Here'}
              </Button>
            </div>
          </section>
        )}
        {actionError && (
          <p className="app-error" role="alert">
            {actionError}
          </p>
        )}
        <section className="codex-section" aria-label="Codex sessions">
          <div className="codex-session-heading">
            <h2>Sessions</h2>
            <ButtonGroup label="Session list">
              <Button
                aria-pressed={!showArchived}
                onClick={() => setShowArchived(false)}
              >
                Unarchived
              </Button>
              <Button
                aria-pressed={showArchived}
                onClick={() => setShowArchived(true)}
              >
                Archived
              </Button>
            </ButtonGroup>
          </div>
          <p className="codex-note">
            Archiving does not stop runs or free capacity.
          </p>
          {sessionError && (
            <p className="app-error" role="alert">
              {sessionError}
            </p>
          )}
          {!visibleSessions.length && !sessionError && (
            <p className="codex-empty">
              {showArchived
                ? 'No archived sessions.'
                : 'No unarchived sessions.'}
            </p>
          )}
          <ul className="codex-session-list">
            {visibleSessions.map((session) => {
              const recovery = continuationRecovery.get(session.id);
              const shouldContinue =
                !!recovery?.requestId || sessionCanContinue(session);
              const canContinue =
                !recovery?.error &&
                (!!recovery?.requestId ||
                  (sessionCanContinue(session) && canStart));
              return (
                <li
                  key={session.id}
                  data-session-id={session.id}
                  data-run-id={session.runId}
                >
                  <div className="codex-session-summary">
                    <strong>{sessionTitle(session)}</strong>
                    <code
                      title={
                        session.vmProject
                          ? session.vmProject.project.root
                          : session.projectPath
                      }
                    >
                      {session.vmProject
                        ? `Host → VM · ${session.vmProject.project.name} · ${session.vmProject.binding.machineId}`
                        : session.projectPath}
                    </code>
                    <code data-testid="codex-run-id">Run {session.runId}</code>
                    <span>
                      {ending === session.id ? 'Ending…' : session.status} ·{' '}
                      {new Date(session.createdAt).toLocaleString()}
                    </span>
                    {session.reason && <span>{session.reason}</span>}
                    {recovery?.error && (
                      <span role="alert">{recovery.error}</span>
                    )}
                    {!sessionCanEnd(session) &&
                      session.continuation.state === 'unavailable' && (
                        <span>
                          {session.continuation.reason ||
                            'Conversation history is unavailable.'}
                        </span>
                      )}
                  </div>
                  <div className="codex-session-actions">
                    <Button
                      disabled={
                        continuing === session.id ||
                        (shouldContinue && !canContinue)
                      }
                      onClick={() => {
                        if (shouldContinue)
                          void continueSession(
                            session,
                            recovery?.requestId || null,
                          );
                        else openCodexSession(desktop, session);
                      }}
                    >
                      {continuing === session.id
                        ? 'Continuing…'
                        : recovery?.requestId
                          ? 'Resolve Continue'
                          : sessionIsLive(session)
                            ? 'Open'
                            : sessionCanContinue(session)
                              ? 'Continue'
                              : 'View'}
                    </Button>
                    <Button
                      onClick={() => void showSessionDetails(desktop, session)}
                    >
                      Details
                    </Button>
                    <IconButton
                      icon="more"
                      label={`More session actions for ${sessionTitle(session)}, run ${session.runId.slice(0, 8)}`}
                      aria-haspopup="menu"
                      disabled={
                        changingMetadata === session.id || ending === session.id
                      }
                      onClick={(event) => {
                        const rect =
                          event.currentTarget.getBoundingClientRect();
                        desktop.contextMenu(
                          [
                            {
                              label: recovery?.requestId
                                ? 'Resolve Continue'
                                : 'Continue',
                              disabled: !canContinue || continuing !== null,
                              action: () =>
                                void continueSession(
                                  session,
                                  recovery?.requestId || null,
                                ),
                            },
                            { separator: true },
                            {
                              label: 'Rename',
                              disabled: changingMetadata !== null,
                              action: () =>
                                void changeMetadata(session, 'rename'),
                            },
                            {
                              label: session.archivedAt
                                ? 'Unarchive'
                                : 'Archive',
                              disabled: changingMetadata !== null,
                              action: () =>
                                void changeMetadata(session, 'archive'),
                            },
                            {
                              label: 'Host Terminal',
                              action: () => {
                                openTerminalWindow(
                                  desktop,
                                  session.projectPath,
                                );
                              },
                            },
                            { separator: true },
                            {
                              label: 'End Session',
                              disabled:
                                !sessionCanEnd(session) || ending !== null,
                              action: () => void end(session),
                            },
                          ],
                          { x: rect.left, y: rect.bottom },
                          {
                            title: `Run ${session.runId.slice(0, 8)} · ${sessionTitle(session)}`,
                          },
                        );
                      }}
                    />
                  </div>
                </li>
              );
            })}
          </ul>
        </section>
      </div>
      <StatusBar>
        <span role="status" data-testid="codex-environment-status">
          Host · {status}
        </span>
        <span>{sessions.filter(sessionCanEnd).length} active / 4 maximum</span>
      </StatusBar>
    </div>
  );
}
