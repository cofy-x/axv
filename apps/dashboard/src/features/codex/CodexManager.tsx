import { desktopDatabase } from '../../shared/api/desktop';
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
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
import type { CodexProject, CodexSessionRecord } from './api';
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
import { launchRejectedBeforeStart } from './vm-project-requests';
import { useHostFolderPicker } from './use-host-folder-picker';
import { useDraftNamespace } from '../../shared/drafts/context';
import { CodexActivity } from './CodexActivity';
import {
  canReviewActivity,
  hasActivityAction,
  mergeSessionActivity,
  mergeSessionSnapshot,
  publishSessionActivity,
  reviewSessionActivity,
  subscribeSessionActivity,
} from './activity';

interface ContinuationRecovery {
  requestId?: string;
  error?: string;
}

export function CodexManager({
  desktop,
  window,
  visible = true,
  interactive = true,
}: AppWindowProps) {
  const vmProjectId = window.params.vmProjectId || '';
  const vmHostId = window.params.hostId || '';
  const vm = useVMProject(vmHostId, vmProjectId);
  const {
    open: choosingFolder,
    path,
    directories,
    error: directoryError,
    browsing,
    changePath,
    browse: readDirectory,
    show: showFolderPicker,
    close: closeFolderPicker,
  } = useHostFolderPicker();
  const namespace = useDraftNamespace();
  const { info, loading, error, status } = useCodexAvailability();
  const canStart = info?.ready === true && !loading && !error;
  const defaultProjectPath = info?.defaultProjectPath;
  const environmentIssue =
    error ||
    (info && !info.ready
      ? info.reason || 'Set up the host environment before starting a session.'
      : '');
  const setTitle = desktop.setTitle;
  const [sessions, setSessions] = useState<CodexSessionRecord[]>([]);
  const [sessionsLoaded, setSessionsLoaded] = useState(false);
  const [projects, setProjects] = useState<CodexProject[]>([]);
  const [projectError, setProjectError] = useState('');
  const [baseFilter, setBaseFilter] = useState<'unarchived' | 'archived'>(
    'unarchived',
  );
  const [showAttention, setShowAttention] = useState(false);
  const managerElement = useRef<HTMLDivElement>(null);
  const actionFocus = useRef<HTMLElement | null>(null);
  const actionFocusRow = useRef(0);
  const browseFocus = useRef<HTMLElement | null>(null);
  const returnFolderFocus = useRef(false);
  const launchIntent = useRef<object | null>(null);
  const activeManager = useRef(false);
  const [reviewing, setReviewing] = useState<string | null>(null);
  const reviewingRequest = useRef(false);
  const [changingMetadata, setChangingMetadata] = useState<string | null>(null);
  const [continuing, setContinuing] = useState<string | null>(null);
  const [continuationRecovery, setContinuationRecovery] = useState(
    new Map<string, ContinuationRecovery>(),
  );
  const [sessionError, setSessionError] = useState('');
  const [creating, setCreating] = useState<'default' | 'folder' | null>(null);
  const [ending, setEnding] = useState<string | null>(null);
  const [actionError, setActionError] = useState('');
  const [startNotice, setStartNotice] = useState('');
  const mounted = useRef(false);
  const listing = useRef<AbortController | null>(null);
  const listingProjects = useRef<AbortController | null>(null);
  const creatingRequest = useRef(false);
  const requestKey = useRef<{ path?: string; id: string } | null>(null);
  const resumeRequests = useRef(
    new CodexResumeRequests(JSON.stringify(namespace), namespace.databaseId),
  );
  const continuingRequest = useRef(false);

  useLayoutEffect(() => {
    activeManager.current =
      visible && interactive && desktop.activeWindowId === window.id;
    if (!activeManager.current) {
      launchIntent.current = null;
      browseFocus.current = null;
    }
  }, [visible, interactive, desktop.activeWindowId, window.id]);
  const mayFocus = useCallback(
    () =>
      activeManager.current &&
      document.visibilityState === 'visible' &&
      document.hasFocus(),
    [],
  );

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
      desktopDatabase.assertUnchanged();
      if (!controller.signal.aborted && mounted.current) {
        const snapshots = result.sessions.map((session) =>
          publishSessionActivity(session),
        );
        setSessions((current) =>
          snapshots.map((session) => {
            const previous = current.find((item) => item.id === session.id);
            return previous ? mergeSessionSnapshot(previous, session) : session;
          }),
        );
        for (const session of result.sessions) {
          publishSessionMetadata(session);
        }
        setSessionsLoaded(true);
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
      desktopDatabase.assertUnchanged();
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

  const browse = (next?: string) => {
    const target = document.activeElement;
    browseFocus.current =
      target instanceof HTMLElement &&
      target.closest('#codex-folder-picker') &&
      managerElement.current?.contains(target)
        ? target
        : null;
    void readDirectory(next);
  };
  const closePicker = (restoreFocus = false) => {
    browseFocus.current = null;
    returnFolderFocus.current = restoreFocus;
    closeFolderPicker();
  };
  useLayoutEffect(() => {
    if (!choosingFolder && returnFolderFocus.current) {
      returnFolderFocus.current = false;
      if (mayFocus() && document.activeElement === document.body) {
        const manager = managerElement.current;
        (
          manager?.querySelector<HTMLButtonElement>(
            '[aria-controls="codex-folder-picker"]:not(:disabled)',
          ) ||
          manager?.querySelector<HTMLButtonElement>(
            '.codex-manager-tools [aria-label="Refresh"]',
          )
        )?.focus();
      }
    }
    const target = browseFocus.current;
    if (!browsing && target) {
      browseFocus.current = null;
      if (
        choosingFolder &&
        mayFocus() &&
        document.activeElement === document.body
      ) {
        if (target.isConnected && !target.matches(':disabled')) target.focus();
        else
          managerElement.current
            ?.querySelector<HTMLInputElement>('#codex-folder-picker input')
            ?.focus();
      }
    }
  }, [browsing, choosingFolder, mayFocus]);

  useEffect(() => {
    mounted.current = true;
    const unsubscribe = subscribeSessionMetadata((metadata) => {
      setSessions((current) =>
        current.map((session) => mergeSessionMetadata(session, metadata)),
      );
    });
    const unsubscribeActivity = subscribeSessionActivity((update) => {
      setSessions((current) =>
        current.map((session) =>
          mergeSessionMetadata(mergeSessionActivity(session, update), update),
        ),
      );
    });
    return () => {
      mounted.current = false;
      unsubscribe();
      unsubscribeActivity();
      listing.current?.abort();
      listingProjects.current?.abort();
      launchIntent.current = null;
    };
  }, []);

  useEffect(() => {
    const poll = () => {
      if (
        desktopDatabase.getSnapshot() ||
        !visible ||
        document.visibilityState !== 'visible'
      )
        return;
      void refresh();
      if (!listingProjects.current) void refreshProjects();
    };
    poll();
    const timer = setInterval(() => {
      poll();
    }, 4000);
    const visibility = () => {
      if (document.visibilityState !== 'visible') {
        launchIntent.current = null;
        browseFocus.current = null;
        listing.current?.abort();
        listingProjects.current?.abort();
      } else poll();
    };
    globalThis.addEventListener('focus', poll);
    document.addEventListener('visibilitychange', visibility);
    return () => {
      clearInterval(timer);
      globalThis.removeEventListener('focus', poll);
      document.removeEventListener('visibilitychange', visibility);
      listing.current?.abort();
      listing.current = null;
      listingProjects.current?.abort();
      listingProjects.current = null;
    };
  }, [refresh, refreshProjects, visible]);

  const create = async (project?: string) => {
    if (
      creatingRequest.current ||
      (vmProjectId
        ? (!vm.pending && (!canStart || !vm.data)) || !!vm.recoveryError
        : !canStart)
    )
      return;
    creatingRequest.current = true;
    const intent = {};
    launchIntent.current = intent;
    setCreating(project ? 'folder' : 'default');
    setActionError('');
    setStartNotice('');
    try {
      desktopDatabase.assertUnchanged();
      if (!requestKey.current || requestKey.current.path !== project)
        requestKey.current = { path: project, id: crypto.randomUUID() };
      const result = vmProjectId
        ? await vm.create()
        : {
            session: await codexAPI.create(requestKey.current.id, project),
            recovered: false,
          };
      desktopDatabase.assertUnchanged();
      const { session } = result;
      requestKey.current = null;
      if (mounted.current) {
        if (sessionIsLive(session) || result.recovered) {
          closePicker();
          if (launchIntent.current === intent && mayFocus()) {
            setBaseFilter('unarchived');
            setShowAttention(false);
            openCodexSession(desktop, session);
          } else
            setStartNotice(
              result.recovered
                ? 'Start resolved. Open the original run from Sessions.'
                : 'Session started. Open it from Sessions.',
            );
        } else
          setActionError(
            session.reason ||
              `Codex could not start (${session.status}). Choose New Session to try again.`,
          );
        void refresh();
        void refreshProjects();
      }
    } catch (error) {
      if (launchRejectedBeforeStart(error)) requestKey.current = null;
      if (mounted.current)
        setActionError(
          error instanceof Error ? error.message : 'Could not start Codex.',
        );
    } finally {
      creatingRequest.current = false;
      if (launchIntent.current === intent) launchIntent.current = null;
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
        const snapshot = publishSessionActivity(result);
        setSessions((current) =>
          current.map((item) =>
            item.id === result.id && item.runId === result.runId
              ? mergeSessionSnapshot(item, snapshot)
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
    const intent = {};
    launchIntent.current = intent;
    setContinuing(session.id);
    setActionError('');
    setStartNotice('');
    try {
      const result = await continueConversation(
        {
          dialog: async (options) => {
            if (
              !mounted.current ||
              launchIntent.current !== intent ||
              !mayFocus()
            )
              return null;
            const answer = await desktop.dialog(options);
            if (answer?.button === 'continue') launchIntent.current = intent;
            return answer;
          },
        },
        session,
        resumeRequests.current,
        expectedRequestId,
      );
      if (result && mounted.current) {
        if (launchIntent.current === intent && mayFocus())
          openCodexSession(desktop, result);
        else setStartNotice('Continuation ready. Open it from Sessions.');
      }
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
      if (launchIntent.current === intent) launchIntent.current = null;
      if (mounted.current) {
        setContinuing(null);
        void refresh();
        void refreshProjects();
      }
    }
  };
  const review = async (session: CodexSessionRecord) => {
    if (reviewingRequest.current || !canReviewActivity(session.activity))
      return;
    reviewingRequest.current = true;
    setReviewing(session.id);
    setActionError('');
    try {
      await reviewSessionActivity(session);
    } catch (error) {
      if (mounted.current) {
        setActionError(
          error instanceof Error
            ? error.message
            : 'Could not dismiss this alert.',
        );
        void refresh();
      }
    } finally {
      reviewingRequest.current = false;
      if (mounted.current) setReviewing(null);
    }
  };
  const scopedSessions = sessions.filter(
    (session) =>
      !vmProjectId ||
      (session.vmProject?.project.id === vmProjectId &&
        session.vmProject.project.hostId === vmHostId),
  );
  const attentionCount = scopedSessions.filter((session) =>
    hasActivityAction(session.activity),
  ).length;
  const filter = showAttention && attentionCount > 0 ? 'attention' : baseFilter;
  useEffect(() => {
    if (showAttention && attentionCount === 0) setShowAttention(false);
  }, [showAttention, attentionCount]);
  useEffect(() => {
    const focus = (event: FocusEvent) => {
      if (
        event.target instanceof HTMLElement &&
        event.target !== document.body &&
        !managerElement.current?.contains(event.target)
      ) {
        actionFocus.current = null;
        browseFocus.current = null;
        launchIntent.current = null;
      }
    };
    const pointer = (event: PointerEvent) => {
      if (
        event.target instanceof Node &&
        !managerElement.current?.contains(event.target)
      ) {
        actionFocus.current = null;
        browseFocus.current = null;
        launchIntent.current = null;
      }
    };
    document.addEventListener('focusin', focus);
    document.addEventListener('pointerdown', pointer, true);
    return () => {
      document.removeEventListener('focusin', focus);
      document.removeEventListener('pointerdown', pointer, true);
    };
  }, []);
  useLayoutEffect(() => {
    const focused = actionFocus.current;
    if (
      focused &&
      !focused.isConnected &&
      document.activeElement === document.body &&
      mayFocus()
    ) {
      const manager = managerElement.current;
      const actions = Array.from(
        manager?.querySelectorAll<HTMLButtonElement>(
          '[data-action-needed="true"] [data-session-primary-action]:not(:disabled)',
        ) || [],
      );
      const target =
        actions[Math.min(actionFocusRow.current, actions.length - 1)] ||
        manager?.querySelector<HTMLButtonElement>(
          '[data-testid="codex-action-needed"]',
        ) ||
        manager?.querySelector<HTMLButtonElement>(
          `[data-session-filter="${baseFilter}"]`,
        );
      target?.focus();
    }
  }, [
    sessions,
    filter,
    baseFilter,
    desktop.activeWindowId,
    interactive,
    visible,
    window.id,
    mayFocus,
  ]);
  const workingCount = scopedSessions.filter(
    (session) => session.activity?.state === 'working',
  ).length;
  const sessionNameCounts = new Map<string, number>();
  for (const session of scopedSessions) {
    const title = sessionTitle(session);
    sessionNameCounts.set(title, (sessionNameCounts.get(title) || 0) + 1);
  }
  const visibleSessions = scopedSessions.filter((session) =>
    filter === 'attention'
      ? hasActivityAction(session.activity)
      : !!session.archivedAt === (filter === 'archived'),
  );
  const recentProjects = projects.filter(
    (project) => project.path !== defaultProjectPath,
  );

  return (
    <div
      className="codex-manager"
      data-testid="codex-manager"
      ref={managerElement}
      onFocusCapture={(event) => {
        const target = event.target;
        if (browseFocus.current && target !== browseFocus.current)
          browseFocus.current = null;
        actionFocus.current =
          target instanceof HTMLElement &&
          (target.closest('[data-testid="codex-action-needed"]') ||
            (filter === 'attention' && target.closest('[data-session-id]')))
            ? target
            : null;
        if (actionFocus.current) {
          const row = target.closest('[data-session-id]');
          const rows = Array.from(
            event.currentTarget.querySelectorAll('[data-session-id]'),
          );
          actionFocusRow.current = Math.max(0, row ? rows.indexOf(row) : 0);
        }
      }}
      onPointerDownCapture={() => {
        launchIntent.current = null;
        browseFocus.current = null;
      }}
      onKeyDownCapture={() => {
        launchIntent.current = null;
      }}
    >
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
              if (choosingFolder) closePicker(true);
              else showFolderPicker();
            }}
          >
            New in Folder
            <span aria-hidden="true">▾</span>
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
          <details className="codex-default-project">
            <summary title={defaultProjectPath}>
              Default:{' '}
              <span data-testid="codex-default-project-name">
                {defaultProjectPath
                  ? projectName(defaultProjectPath)
                  : loading
                    ? 'Loading…'
                    : 'Unavailable'}
              </span>
            </summary>
            {defaultProjectPath ? (
              <code data-testid="codex-default-project">
                {defaultProjectPath}
              </code>
            ) : (
              <p>Check Environment for the default host folder.</p>
            )}
          </details>
        )}
        {environmentIssue && (
          <div className="app-error codex-environment-notice" role="alert">
            <p>{environmentIssue}</p>
            <Button onClick={() => openCodexEnvironment(desktop)}>
              Environment
            </Button>
          </div>
        )}
        {!vmProjectId && projectError && !choosingFolder && (
          <p className="app-error" role="alert">
            {projectError}
          </p>
        )}
        {choosingFolder && (
          <section
            id="codex-folder-picker"
            className="codex-section codex-folder-picker"
            aria-label="New Codex session"
            onKeyDown={(event) => {
              if (
                event.key === 'Escape' &&
                !event.nativeEvent.isComposing &&
                creating === null
              ) {
                event.preventDefault();
                event.stopPropagation();
                closePicker(true);
              }
            }}
          >
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
                disabled={creating !== null}
                onChange={(event) => changePath(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
                    event.preventDefault();
                    if (!browsing && creating === null) browse(path);
                  }
                }}
              />
              <div className="codex-directory-actions">
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
                    browsing ||
                    creating !== null ||
                    !directories?.parentPath ||
                    !!directoryError
                  }
                  onClick={() =>
                    void browse(directories?.parentPath || undefined)
                  }
                />
                {(recentProjects.length > 0 || projectError) && (
                  <section
                    className="codex-recent"
                    aria-label="Recent projects"
                  >
                    <Button
                      aria-haspopup="menu"
                      disabled={creating !== null || !recentProjects.length}
                      onKeyDown={(event) => {
                        if (
                          event.key === 'ArrowDown' &&
                          !event.altKey &&
                          !event.ctrlKey &&
                          !event.metaKey &&
                          !event.nativeEvent.isComposing
                        ) {
                          event.preventDefault();
                          event.currentTarget.click();
                        }
                      }}
                      onClick={(event) => {
                        const rect =
                          event.currentTarget.getBoundingClientRect();
                        desktop.contextMenu(
                          recentProjects.map((project) => {
                            // Host projects use canonical POSIX paths; a
                            // backslash can be part of a directory name.
                            const separator = project.path.lastIndexOf('/');
                            return {
                              label:
                                project.path.slice(separator + 1) ||
                                project.path,
                              description:
                                project.path === '/'
                                  ? undefined
                                  : project.path.slice(0, separator) || '/',
                              action: () => {
                                changePath(project.path);
                                browse(project.path);
                              },
                            };
                          }),
                          { x: rect.right, y: rect.bottom },
                          {
                            title: 'Recent Projects',
                            ownerWindowId: window.id,
                            align: 'end',
                            width: 'wide',
                          },
                        );
                      }}
                    >
                      Recent Projects{projectError && ' · Outdated'}
                      <span aria-hidden="true">▾</span>
                    </Button>
                  </section>
                )}
              </div>
            </form>
            {projectError && (
              <p className="app-error" role="alert">
                {projectError}
              </p>
            )}
            {directoryError && (
              <p className="app-error" role="alert">
                {directoryError}
              </p>
            )}
            {!browsing && directories && path !== directories.path && (
              <p
                className="codex-note codex-directory-context"
                data-testid="codex-directory-context"
              >
                Showing: <code>{directories.path}</code>
              </p>
            )}
            <div
              className="codex-directory-list"
              aria-label="Host folders"
              aria-busy={browsing}
            >
              {!browsing && directoryError && directories && (
                <p className="codex-note">
                  Previous folder contents · Outdated
                </p>
              )}
              {browsing ? (
                <p role="status">Loading folders…</p>
              ) : (
                directories?.directories.map((directory) => (
                  <button
                    type="button"
                    key={directory.path}
                    disabled={creating !== null || !!directoryError}
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
              <Button
                disabled={creating !== null}
                onClick={() => {
                  closePicker(true);
                }}
              >
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
          {startNotice && (
            <p
              className="codex-note"
              role="status"
              data-testid="codex-start-notice"
            >
              {startNotice}
            </p>
          )}
          <div className="codex-session-heading">
            <div className="codex-session-title">
              <h2>Sessions</h2>
              {attentionCount > 0 && (
                <Button
                  className="codex-action-link"
                  data-testid="codex-action-needed"
                  aria-label={`Action needed (${attentionCount})`}
                  title={`Action needed (${attentionCount})`}
                  aria-pressed={filter === 'attention'}
                  onClick={() => setShowAttention(true)}
                >
                  <span aria-hidden="true">Action needed ·</span>
                  <span aria-hidden="true">{attentionCount}</span>
                </Button>
              )}
            </div>
            <ButtonGroup label="Session list">
              <Button
                aria-pressed={filter === 'unarchived'}
                data-session-filter="unarchived"
                onClick={() => {
                  setBaseFilter('unarchived');
                  setShowAttention(false);
                }}
              >
                Unarchived
              </Button>
              <Button
                aria-pressed={filter === 'archived'}
                data-session-filter="archived"
                onClick={() => {
                  setBaseFilter('archived');
                  setShowAttention(false);
                }}
              >
                Archived
              </Button>
            </ButtonGroup>
          </div>
          {(!sessionsLoaded || workingCount > 0) && (
            <p
              className="codex-activity-summary"
              role="status"
              data-testid="codex-activity-summary"
            >
              {sessionsLoaded
                ? `${workingCount} working${sessionError ? ' · Outdated' : ''}`
                : sessionError
                  ? 'Session activity unavailable'
                  : 'Loading session activity…'}
            </p>
          )}
          {filter === 'attention' && (
            <p className="codex-note">Includes archived.</p>
          )}
          {sessionError && (
            <p className="app-error" role="alert">
              Session activity may be outdated. {sessionError}
            </p>
          )}
          {sessionsLoaded && !visibleSessions.length && !sessionError && (
            <div className="app-empty-state codex-empty">
              <PixelIcon name="companion" size={32} />
              <div>
                <strong>
                  {filter === 'attention'
                    ? 'No action needed.'
                    : filter === 'archived'
                      ? 'No archived sessions.'
                      : 'No unarchived sessions.'}
                </strong>
                <p>
                  {filter === 'attention'
                    ? 'Sessions needing a response or review appear here.'
                    : filter === 'archived'
                      ? 'Archive a session from its More menu to keep it here.'
                      : vmProjectId
                        ? 'Conversations and activity for this VM project appear here.'
                        : 'New Session uses the default folder. New in Folder starts in another project.'}
                </p>
              </div>
            </div>
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
                  data-action-needed={hasActivityAction(session.activity)}
                >
                  <div className="codex-session-summary">
                    <div className="codex-session-header">
                      <strong>{sessionTitle(session)}</strong>
                      {(sessionNameCounts.get(sessionTitle(session)) || 0) >
                        1 && (
                        <code
                          className="codex-session-reference"
                          data-testid="codex-run-id"
                          title={session.runId}
                        >
                          Run {session.runId.slice(0, 8)}
                        </code>
                      )}
                      <CodexActivity
                        activity={session.activity}
                        stale={!!sessionError}
                      />
                    </div>
                    {session.activity?.reason && (
                      <span>{session.activity.reason}</span>
                    )}
                    <span
                      data-testid="codex-session-project"
                      title={
                        session.vmProject
                          ? session.vmProject.project.root
                          : session.projectPath
                      }
                    >
                      {session.vmProject
                        ? `Host → VM · ${session.vmProject.project.name}`
                        : projectName(session.projectPath)}
                      {session.archivedAt && ' · Archived'}
                    </span>
                    {(ending === session.id ||
                      session.status !== 'running') && (
                      <span className="codex-session-process-state">
                        {ending === session.id
                          ? 'Ending…'
                          : `Run ${session.status}`}
                      </span>
                    )}
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
                      data-session-primary-action
                      disabled={
                        !hasActivityAction(session.activity) &&
                        (continuing === session.id ||
                          (shouldContinue && !canContinue))
                      }
                      onClick={() => {
                        setStartNotice('');
                        if (
                          shouldContinue &&
                          !hasActivityAction(session.activity)
                        )
                          void continueSession(
                            session,
                            recovery?.requestId || null,
                          );
                        else openCodexSession(desktop, session);
                      }}
                    >
                      {hasActivityAction(session.activity)
                        ? 'Open'
                        : continuing === session.id
                          ? 'Continuing…'
                          : recovery?.requestId
                            ? 'Resolve Continue'
                            : sessionIsLive(session)
                              ? 'Open'
                              : sessionCanContinue(session)
                                ? 'Continue'
                                : 'View'}
                    </Button>
                    {filter === 'attention' &&
                      canReviewActivity(session.activity) && (
                        <Button
                          disabled={reviewing !== null}
                          onClick={() => void review(session)}
                        >
                          {reviewing === session.id
                            ? 'Dismissing…'
                            : 'Dismiss alert'}
                        </Button>
                      )}
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
                              label: 'Details',
                              action: () =>
                                void showSessionDetails(desktop, session),
                            },
                            ...(canReviewActivity(session.activity)
                              ? [
                                  {
                                    label:
                                      reviewing === session.id
                                        ? 'Dismissing…'
                                        : 'Dismiss alert',
                                    disabled: reviewing !== null,
                                    action: () => void review(session),
                                  },
                                ]
                              : []),
                            { separator: true },
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
