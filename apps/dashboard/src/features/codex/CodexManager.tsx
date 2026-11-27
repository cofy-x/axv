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
  IconButton,
  PixelIcon,
  StatusBar,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import {
  codexAPI,
  isVMSession,
  projectName,
  sessionKey,
  sessionTitle,
  sessionIsLive,
  sessionCanEnd,
} from './api';
import type { CodexProject, CodexSessionRecord } from './api';
import { vmCodexAPI } from './vm-api';
import { CodexFolderPicker } from './CodexFolderPicker';
import { CodexSessionRow } from './CodexSessionRow';
import type { SessionContinuationRecovery } from './CodexSessionRow';
import { VMNewSessionPanel } from './VMNewSessionPanel';
import { machinesApi } from '../machines/api';
import type { Machine } from '../machines/types';
import { useCodexAvailability } from './CodexContext';
import { openCodexEnvironment, openCodexSession } from './definition';
import {
  confirmEndSession,
  renameSession,
  setSessionArchived,
} from './actions';
import {
  mergeSessionMetadata,
  publishSessionMetadata,
  subscribeSessionMetadata,
} from './metadata';
import {
  CodexResumeRequests,
  continueConversation,
  launchRejectedBeforeStart,
} from './continuation';
import { useHostFolderPicker } from './use-host-folder-picker';
import { useDraftNamespace } from '../../shared/drafts/context';
import { useWindowCommands } from '../../shared/desktop/WindowCommandsContext';
import {
  canReviewActivity,
  hasActivityAction,
  mergeSessionActivity,
  mergeSessionSnapshot,
  publishSessionActivity,
  reviewSessionActivity,
  subscribeSessionActivity,
} from './activity';

export function CodexManager({
  desktop,
  window,
  visible = true,
  interactive = true,
}: AppWindowProps) {
  const commands = useWindowCommands();
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
  const [vmMachines, setVmMachines] = useState<Machine[]>([]);
  const [choosingVM, setChoosingVM] = useState(
    () => !!window.params.newVmMachineId,
  );
  const [targetFilter, setTargetFilter] = useState<'all' | 'host' | 'vm'>(
    'all',
  );
  const [sessionsLoaded, setSessionsLoaded] = useState(false);
  const [projects, setProjects] = useState<CodexProject[]>([]);
  const [projectError, setProjectError] = useState('');
  const [baseFilter, setBaseFilter] = useState<'unarchived' | 'archived'>(
    'unarchived',
  );
  const [showAttention, setShowAttention] = useState(false);
  const managerElement = useRef<HTMLDivElement>(null);
  const defaultFolder = useRef<HTMLDetailsElement>(null);
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
    new Map<string, SessionContinuationRecovery>(),
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
    if (window.params.newVmMachineId) {
      setChoosingVM(true);
      closeFolderPicker();
    }
  }, [window.params.newVmMachineId, closeFolderPicker]);

  useEffect(() => {
    const recovery = new Map<string, SessionContinuationRecovery>();
    for (const session of sessions) {
      const key = sessionKey(session);
      try {
        recovery.set(key, {
          requestId: resumeRequests.current.restore(session)?.requestId,
        });
      } catch (error) {
        recovery.set(key, {
          error: error instanceof Error ? error.message : String(error),
        });
      }
    }
    setContinuationRecovery(recovery);
  }, [sessions, continuing]);

  useEffect(() => {
    setTitle(window.id, 'Codex');
  }, [setTitle, window.id]);

  const refresh = useCallback(async () => {
    if (listing.current) return;
    const controller = new AbortController();
    listing.current = controller;
    try {
      const [hostResult, machinesResult] = await Promise.all([
        codexAPI.list(controller.signal),
        machinesApi
          .machines(controller.signal)
          .catch(() => ({ machines: [] as Machine[] })),
      ]);
      desktopDatabase.assertUnchanged();
      const machines = Array.isArray(machinesResult.machines)
        ? machinesResult.machines
        : [];
      const codexRunning = machines.filter(
        (machine) =>
          machine.status === 'running' &&
          machine.ready &&
          machine.guestCapabilities?.includes('codex'),
      );
      const vmSessionBatches = await Promise.all(
        codexRunning.map((machine) =>
          vmCodexAPI
            .list(
              {
                hostId: machine.hostId,
                machineId: machine.id,
                machineName: machine.spec.name || machine.id,
              },
              controller.signal,
            )
            .then((result) => result.sessions)
            .catch(() => [] as CodexSessionRecord[]),
        ),
      );
      desktopDatabase.assertUnchanged();
      if (!controller.signal.aborted && mounted.current) {
        setVmMachines(machines);
        const combined = [
          ...hostResult.sessions,
          ...vmSessionBatches.flat(),
        ].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
        const snapshots = combined.map((session) =>
          publishSessionActivity(session),
        );
        setSessions((current) =>
          snapshots.map((session) => {
            const key = sessionKey(session);
            const previous = current.find((item) => sessionKey(item) === key);
            return previous ? mergeSessionSnapshot(previous, session) : session;
          }),
        );
        for (const session of combined) {
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
  }, []);

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
  const toggleFolderPicker = () => {
    if (choosingFolder) {
      closePicker(true);
      return;
    }
    if (defaultFolder.current) {
      defaultFolder.current.open = false;
      defaultFolder.current.scrollIntoView({ block: 'nearest' });
    }
    showFolderPicker();
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
    if (creatingRequest.current || !canStart) return;
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
      const session = await codexAPI.create(requestKey.current.id, project);
      desktopDatabase.assertUnchanged();
      requestKey.current = null;
      if (mounted.current) {
        if (sessionIsLive(session)) {
          closePicker();
          if (launchIntent.current === intent && mayFocus()) {
            setBaseFilter('unarchived');
            setShowAttention(false);
            openCodexSession(desktop, session);
          } else setStartNotice('Session started. Open it from Sessions.');
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
    const key = sessionKey(session);
    setEnding(key);
    setActionError('');
    try {
      const result = await confirmEndSession(desktop, session);
      if (result && mounted.current) {
        const snapshot = publishSessionActivity(result);
        setSessions((current) =>
          current.map((item) =>
            sessionKey(item) === sessionKey(result) &&
            item.runId === result.runId
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
    setChangingMetadata(sessionKey(session));
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
    const key = sessionKey(session);
    setContinuing(key);
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
          resumeRequests.current.hasPending(key)
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
    setReviewing(sessionKey(session));
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
  const hasVMSessions = sessions.some(isVMSession);
  const showVMControls = vmMachines.length > 0 || hasVMSessions || choosingVM;
  const scopedSessions =
    targetFilter === 'all'
      ? sessions
      : targetFilter === 'vm'
        ? sessions.filter(isVMSession)
        : sessions.filter((session) => !isVMSession(session));
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
  const canCreateSession = creating === null && canStart;
  const canChooseFolder = creating === null && (choosingFolder || canStart);
  const [menuAction, setMenuAction] = useState<'create' | 'folder' | null>(
    null,
  );
  const menuActions = useRef({
    create: async () => {},
    folder: () => {},
    refresh: async () => {},
  });
  useLayoutEffect(() => {
    menuActions.current = {
      create: () => create(),
      folder: toggleFolderPicker,
      refresh: async () => {
        await Promise.all([refresh(), refreshProjects()]);
      },
    };
  });
  useEffect(() => {
    if (
      !menuAction ||
      (visible && desktop.activeWindowId === window.id && !interactive)
    )
      return;
    setMenuAction(null);
    // Run after the menu closes so its focus suspension cannot cancel this intent.
    if (menuAction === 'create') void menuActions.current.create();
    else menuActions.current.folder();
  }, [menuAction, visible, interactive, desktop.activeWindowId, window.id]);
  useEffect(() => {
    const unregister = [
      commands.register(
        window.id,
        'codex:new-session',
        async () => {
          setMenuAction('create');
          return true;
        },
        canCreateSession,
      ),
      commands.register(
        window.id,
        'codex:new-in-folder',
        async () => {
          setMenuAction('folder');
          return true;
        },
        canChooseFolder,
      ),
      commands.register(window.id, 'codex:refresh', async () => {
        await menuActions.current.refresh();
        return true;
      }),
    ];
    return () => {
      for (const cleanup of unregister) cleanup();
    };
  }, [commands, window.id, canCreateSession, canChooseFolder]);

  return (
    <div
      className="codex-manager codex-host-manager"
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
        <Button
          primary
          disabled={!canCreateSession}
          onClick={() => void create()}
        >
          {creating === 'default' ? 'Starting…' : 'New Session'}
        </Button>
        <Button
          disabled={!canChooseFolder}
          aria-expanded={choosingFolder}
          aria-controls="codex-folder-picker"
          onClick={() => {
            setChoosingVM(false);
            toggleFolderPicker();
          }}
        >
          New in Folder
          <span className="codex-folder-chevron" aria-hidden="true" />
        </Button>
        {showVMControls && (
          <Button
            aria-expanded={choosingVM}
            aria-controls="codex-vm-picker"
            onClick={() => {
              if (choosingVM) {
                setChoosingVM(false);
              } else {
                closePicker();
                setChoosingVM(true);
              }
            }}
          >
            New in VM
            <span className="codex-folder-chevron" aria-hidden="true" />
          </Button>
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
        </div>
      </div>
      <div className="codex-manager-content">
        <div className="codex-manager-launch-context">
          <details className="codex-default-project" ref={defaultFolder}>
            <summary className="ns-disclosure" title={defaultProjectPath}>
              <span className="codex-default-project-name">
                <span>Default folder</span>
                <strong data-testid="codex-default-project-name">
                  {defaultProjectPath
                    ? projectName(defaultProjectPath)
                    : loading
                      ? 'Loading…'
                      : 'Unavailable'}
                </strong>
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
          {environmentIssue && (
            <div className="app-error codex-environment-notice" role="alert">
              <p>{environmentIssue}</p>
              <Button onClick={() => openCodexEnvironment(desktop)}>
                Environment
              </Button>
            </div>
          )}
          {projectError && !choosingFolder && (
            <p className="app-error" role="alert">
              {projectError}
            </p>
          )}
          {choosingFolder && (
            <CodexFolderPicker
              desktop={desktop}
              windowId={window.id}
              path={path}
              directories={directories}
              directoryError={directoryError}
              projectError={projectError}
              recentProjects={recentProjects}
              browsing={browsing}
              creating={creating}
              canStart={canStart}
              onChangePath={changePath}
              onBrowse={browse}
              onClose={closePicker}
              onStart={(directory) => void create(directory)}
            />
          )}
          {choosingVM && (
            <VMNewSessionPanel
              desktop={desktop}
              machines={vmMachines}
              initialMachineId={window.params.newVmMachineId}
              initialProjectPath={window.params.newVmProjectPath}
              onClose={() => setChoosingVM(false)}
              onStarted={(session) => {
                setChoosingVM(false);
                setBaseFilter('unarchived');
                setShowAttention(false);
                openCodexSession(desktop, session);
                void refresh();
              }}
            />
          )}
          {actionError && (
            <p className="app-error" role="alert">
              {actionError}
            </p>
          )}
        </div>
        <section
          className="codex-section codex-sessions"
          aria-label="Codex sessions"
        >
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
            <div className="codex-session-filters">
              {showVMControls && (
                <ButtonGroup label="Target filter">
                  {(['all', 'host', 'vm'] as const).map((target) => (
                    <Button
                      key={target}
                      aria-pressed={targetFilter === target}
                      data-target-filter={target}
                      onClick={() => setTargetFilter(target)}
                    >
                      {target === 'all'
                        ? 'All'
                        : target === 'host'
                          ? 'Host'
                          : 'VM'}
                    </Button>
                  ))}
                </ButtonGroup>
              )}
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
                      : 'New Session uses the default folder. New in Folder starts in another project.'}
                </p>
              </div>
            </div>
          )}
          <ul className="codex-session-list">
            {visibleSessions.map((session) => {
              const key = sessionKey(session);
              return (
                <CodexSessionRow
                  key={key}
                  desktop={desktop}
                  session={session}
                  hasVMSessions={hasVMSessions}
                  duplicateTitle={
                    (sessionNameCounts.get(sessionTitle(session)) || 0) > 1
                  }
                  filter={filter}
                  sessionError={sessionError}
                  canStart={canStart}
                  recovery={continuationRecovery.get(key)}
                  continuing={continuing}
                  reviewing={reviewing}
                  changingMetadata={changingMetadata}
                  ending={ending}
                  onClearNotice={() => setStartNotice('')}
                  onContinue={(target, requestId) =>
                    void continueSession(target, requestId)
                  }
                  onReview={(target) => void review(target)}
                  onChangeMetadata={(target, action) =>
                    void changeMetadata(target, action)
                  }
                  onEnd={(target) => void end(target)}
                  onError={(message) => {
                    if (mounted.current) setActionError(message);
                  }}
                />
              );
            })}
          </ul>
        </section>
      </div>
      <StatusBar>
        <span role="status" data-testid="codex-environment-status">
          Host · {status}
        </span>
        <span>
          {sessions.filter((s) => !isVMSession(s) && sessionCanEnd(s)).length}{' '}
          active / 4 maximum
          {hasVMSessions &&
            ` · ${sessions.filter((s) => isVMSession(s) && sessionCanEnd(s)).length} VM active`}
        </span>
      </StatusBar>
    </div>
  );
}
