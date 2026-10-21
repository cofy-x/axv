import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { Button, IconButton, StatusBar } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { useWindowCommands } from '../../shared/desktop/WindowCommandsContext';
import { useTerminalPaste } from '../../shared/terminal/use-terminal-paste';
import { isDesktopTerminalKey } from '../terminal/terminal-keys';
import {
  codexAPI,
  sessionWindowTitle,
  sessionCanEnd,
  sessionCanContinue,
  otherActiveRun,
  projectName,
} from './api';
import { CodexConnection } from './session';
import type { CodexConnectionState } from './session';
import {
  confirmEndSession,
  renameSession,
  setSessionArchived,
} from './actions';
import { showSessionDetails } from './details';
import { codexMoreMenu, openCodexSession } from './definition';
import { publishSessionMetadata, subscribeSessionMetadata } from './metadata';
import { openTerminalWindow } from '../terminal/definition';
import { CodexResumeRequests, continueConversation } from './continuation';
import { useCodexAvailability } from './CodexContext';
import { projectsAPI } from '../project-files/api';
import { runCommand } from '../../shared/desktop/dialogs';
import { useDraftNamespace } from '../../shared/drafts/context';

function mayFocus() {
  return !document.querySelector('.ns-dialog-layer, .ns-menu-layer');
}

export function CodexTerminal({
  window,
  desktop,
  interactive = true,
}: AppWindowProps) {
  const id = window.params.sessionId;
  const runId = window.params.runId;
  const { info, loading, error: environmentError } = useCodexAvailability();
  const setTitle = desktop.setTitle;
  const screen = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const connection = useRef<CodexConnection | null>(null);
  const active =
    desktop.activeWindowId === window.id &&
    !window.shaded &&
    !window.minimized &&
    (window.sticky || desktop.currentWorkspaceId === window.workspaceId);
  const activeRef = useRef(active);
  const interactiveRef = useRef(interactive && active);
  const mounted = useRef(false);
  const namespace = useDraftNamespace();
  const scrolling = useRef(false);
  const endingRef = useRef(false);
  const changingMetadataRef = useRef(false);
  const continuingRef = useRef(false);
  const resumeRequests = useRef(
    new CodexResumeRequests(JSON.stringify(namespace), namespace.databaseId),
  );
  const commands = useWindowCommands();
  const [state, setState] = useState<CodexConnectionState>({
    phase: 'connecting',
  });
  const [size, setSize] = useState({ cols: 80, rows: 24 });
  const [hasSelection, setHasSelection] = useState(false);
  const [feedback, setFeedback] = useState('');
  const {
    pasteClipboard: paste,
    pasteText,
    invalidatePaste,
    recordPasteInput,
  } = useTerminalPaste({
    desktop,
    title: window.title,
    terminal,
    connection,
    interactive: interactiveRef,
    mayFocus,
    setFeedback,
  });
  const [ending, setEnding] = useState(false);
  const [changingMetadata, setChangingMetadata] = useState(false);
  const [metadataError, setMetadataError] = useState('');
  const [continuing, setContinuing] = useState(false);
  const [resumePending, setResumePending] = useState<string>();
  const [resumeRecoveryError, setResumeRecoveryError] = useState('');
  const live =
    state.phase !== 'ended' && !!state.session && sessionCanEnd(state.session);

  useEffect(() => {
    if (!state.session) return;
    try {
      setResumePending(
        resumeRequests.current.restore(state.session)?.requestId,
      );
      setResumeRecoveryError('');
    } catch (error) {
      setResumeRecoveryError(
        error instanceof Error ? error.message : String(error),
      );
    }
  }, [state.session]);

  useLayoutEffect(() => {
    interactiveRef.current = interactive && active;
    if (terminal.current) {
      terminal.current.options.disableStdin =
        !interactiveRef.current ||
        connection.current?.getSnapshot().phase !== 'connected';
      if (!interactiveRef.current) terminal.current.blur();
    }
  }, [interactive, active]);

  useEffect(() => {
    const newlyActive = active && !activeRef.current;
    activeRef.current = active;
    if (
      active &&
      interactiveRef.current &&
      mayFocus() &&
      (newlyActive || document.activeElement === document.body)
    )
      terminal.current?.focus();
    else if (!active) terminal.current?.blur();
  }, [active, interactive]);

  useEffect(() => {
    if (state.session) setTitle(window.id, sessionWindowTitle(state.session));
  }, [state.session, setTitle, window.id]);

  useEffect(() => {
    const host = screen.current;
    if (!host) return;
    mounted.current = true;
    let disposed = false;
    let opened = false;
    let frame = 0;
    let wheelRemainder = 0;
    let touchY: number | null = null;
    const term = new Terminal({
      fontFamily:
        getComputedStyle(host).getPropertyValue('--ns-mono').trim() ||
        '"SFMono-Regular", Consolas, monospace',
      fontSize: 13,
      lineHeight: 1.15,
      cursorBlink: true,
      scrollback: 0,
      screenReaderMode: true,
      disableStdin: true,
      theme: {
        background: '#151918',
        foreground: '#e2e7dc',
        cursor: '#e2e7dc',
        cursorAccent: '#151918',
        selectionBackground: '#647b6d',
        black: '#1e2421',
        red: '#e78c88',
        green: '#a4c486',
        yellow: '#e0c78a',
        blue: '#91b7dc',
        magenta: '#c7a4d6',
        cyan: '#8cc8c6',
        white: '#e2e7dc',
        brightBlack: '#79847b',
        brightRed: '#ffaaa1',
        brightGreen: '#c0dd9e',
        brightYellow: '#f4dda2',
        brightBlue: '#b3d1f2',
        brightMagenta: '#dfbbe9',
        brightCyan: '#a9e1dc',
        brightWhite: '#ffffff',
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    terminal.current = term;
    const session = new CodexConnection({
      sessionId: id,
      runId,
      baseURL: globalThis.location.href,
      getSession: (signal) => codexAPI.getRun(id, runId, signal),
      createSocket: (url) => new WebSocket(url),
      write: (data, done) => term.write(data, done),
      reset: () => {
        // Serialize reset behind bytes already queued inside xterm, before the
        // new tmux attachment can enqueue its restored screen.
        term.write('', () => {
          if (!disposed) term.reset();
        });
        scrolling.current = false;
      },
      onState: (next) => {
        if (disposed) return;
        if (next.phase !== 'connected') invalidatePaste();
        term.options.disableStdin =
          next.phase !== 'connected' || !interactiveRef.current;
        setState(next);
      },
      onInputRejected: () =>
        setFeedback(
          'Input not sent: connection busy. Wait, then try a smaller paste.',
        ),
    });
    connection.current = session;
    const returnToLive = () => {
      if (scrolling.current) {
        session.scroll(0);
        scrolling.current = false;
      }
    };
    const subscriptions = [
      term.onData((data) => {
        returnToLive();
        recordPasteInput(session.sendText(data));
      }),
      term.onBinary((data) => {
        returnToLive();
        session.sendBinary(data);
      }),
      term.onResize(({ cols, rows }) => {
        setSize({ cols, rows });
        session.resize(cols, rows);
      }),
      term.onSelectionChange(() => setHasSelection(term.hasSelection())),
    ];
    term.attachCustomKeyEventHandler((event) => {
      if (isDesktopTerminalKey(event)) return false;
      event.stopPropagation();
      if (!interactiveRef.current) return false;
      if (
        event.type === 'keydown' &&
        term.hasSelection() &&
        event.key.toLowerCase() === 'c' &&
        (event.metaKey || (event.ctrlKey && event.shiftKey))
      ) {
        event.preventDefault();
        void navigator.clipboard.writeText(term.getSelection()).catch(() => {
          if (!disposed)
            setFeedback(
              'Clipboard access was denied. Use the browser’s Copy command for selected text.',
            );
        });
        return false;
      }
      return true;
    });
    const wheel = (event: WheelEvent) => {
      if (event.ctrlKey || !event.deltaY) return;
      event.preventDefault();
      event.stopPropagation();
      if (!interactiveRef.current) return;
      const pixels =
        event.deltaMode === 1
          ? event.deltaY * 15
          : event.deltaMode === 2
            ? event.deltaY * host.clientHeight
            : event.deltaY;
      wheelRemainder -= pixels / 15;
      const lines = Math.trunc(wheelRemainder);
      if (lines) {
        scrolling.current = session.scroll(lines);
        wheelRemainder -= lines;
      }
    };
    // xterm's inner viewport consumes wheel events before its public bubbling
    // handler. Capture first so tmux remains the sole scrollback authority.
    host.addEventListener('wheel', wheel, { capture: true, passive: false });
    const touchStart = (event: TouchEvent) => {
      touchY = event.touches.length === 1 ? event.touches[0].clientY : null;
    };
    const touchMove = (event: TouchEvent) => {
      if (
        touchY === null ||
        event.touches.length !== 1 ||
        !interactiveRef.current ||
        term.hasSelection()
      )
        return;
      const next = event.touches[0].clientY;
      const lines = Math.trunc((next - touchY) / 15);
      if (!lines) return;
      event.preventDefault();
      scrolling.current = session.scroll(lines);
      touchY += lines * 15;
    };
    const touchEnd = () => {
      touchY = null;
    };
    host.addEventListener('touchstart', touchStart, { passive: true });
    host.addEventListener('touchmove', touchMove, { passive: false });
    host.addEventListener('touchend', touchEnd);
    host.addEventListener('touchcancel', touchEnd);

    function fitScreen() {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        if (disposed || host!.clientWidth <= 0 || host!.clientHeight <= 0)
          return;
        const firstOpen = !opened;
        if (firstOpen) {
          term.open(host!);
          opened = true;
          term.textarea?.setAttribute('aria-label', 'Codex terminal input');
        }
        const dimensions = fit.proposeDimensions();
        if (dimensions)
          term.resize(
            Math.max(2, Math.min(500, dimensions.cols)),
            Math.max(1, Math.min(200, dimensions.rows)),
          );
        session.resize(term.cols, term.rows);
        void session.start();
        if (
          firstOpen &&
          activeRef.current &&
          interactiveRef.current &&
          mayFocus() &&
          !host!.contains(document.activeElement)
        )
          term.focus();
      });
    }

    const observer = new ResizeObserver(fitScreen);
    observer.observe(host);
    fitScreen();
    void document.fonts.ready.then(() => {
      if (!disposed) fitScreen();
    });
    return () => {
      disposed = true;
      invalidatePaste();
      mounted.current = false;
      cancelAnimationFrame(frame);
      observer.disconnect();
      host.removeEventListener('wheel', wheel, true);
      host.removeEventListener('touchstart', touchStart);
      host.removeEventListener('touchmove', touchMove);
      host.removeEventListener('touchend', touchEnd);
      host.removeEventListener('touchcancel', touchEnd);
      session.dispose();
      for (const subscription of subscriptions) subscription.dispose();
      term.dispose();
      terminal.current = null;
      connection.current = null;
    };
  }, [id, runId, invalidatePaste, recordPasteInput]);

  useEffect(() => {
    let disposed = false;
    let pending: AbortController | null = null;
    const unsubscribe = subscribeSessionMetadata((session) => {
      if (session.id === id) connection.current?.updateMetadata(session);
    });
    const refresh = async () => {
      if (pending || disposed) return;
      const controller = new AbortController();
      pending = controller;
      try {
        const session = await codexAPI.getRun(id, runId, controller.signal);
        if (!disposed && !controller.signal.aborted) {
          connection.current?.updateRunInfo(session);
          publishSessionMetadata(session);
          setMetadataError('');
        }
      } catch (error) {
        if (!disposed && !controller.signal.aborted)
          setMetadataError(
            `Could not refresh session details. ${error instanceof Error ? error.message : 'Choose Refresh in the manager.'}`,
          );
      } finally {
        if (pending === controller) pending = null;
      }
    };
    const focus = () => void refresh();
    const timer = setInterval(() => {
      if (document.visibilityState === 'visible') void refresh();
    }, 4000);
    globalThis.addEventListener('focus', focus);
    return () => {
      disposed = true;
      pending?.abort();
      clearInterval(timer);
      globalThis.removeEventListener('focus', focus);
      unsubscribe();
    };
  }, [id, runId]);

  const copy = useCallback(async () => {
    const target = terminal.current;
    const text = target?.getSelection();
    if (!text) return false;
    try {
      await navigator.clipboard.writeText(text);
      if (terminal.current !== target) return false;
      setFeedback('');
      return true;
    } catch {
      if (terminal.current === target)
        setFeedback(
          'Clipboard access was denied. Use the browser’s Copy command for selected text.',
        );
      return false;
    }
  }, []);
  const clear = useCallback(async () => {
    terminal.current?.clear();
    return !!terminal.current;
  }, []);
  const reconnect = useCallback(async () => {
    if (!connection.current) return false;
    setFeedback('');
    connection.current.reconnect();
    return true;
  }, []);
  const interrupt = useCallback(async () => {
    const session = connection.current;
    if (session?.getSnapshot().phase !== 'connected') return false;
    if (scrolling.current) {
      session.scroll(0);
      scrolling.current = false;
    }
    const sent = session.sendText('\u0003');
    if (interactiveRef.current && mayFocus()) terminal.current?.focus();
    return sent;
  }, []);
  const end = useCallback(async () => {
    const session = connection.current?.getSnapshot().session;
    if (!session || endingRef.current) return false;
    endingRef.current = true;
    setEnding(true);
    setFeedback('');
    try {
      const result = await confirmEndSession(desktop, session);
      if (result) connection.current?.ended(result);
      return !!result;
    } catch (error) {
      if (mounted.current)
        setFeedback(
          error instanceof Error ? error.message : 'Could not end the session.',
        );
      return false;
    } finally {
      endingRef.current = false;
      if (mounted.current) setEnding(false);
    }
  }, [desktop]);
  const canReconnect =
    state.phase !== 'connected' &&
    state.phase !== 'connecting' &&
    state.phase !== 'ended' &&
    state.retryable !== false;
  const canContinue =
    !!state.session &&
    (!!resumePending ||
      (sessionCanContinue(state.session) &&
        info?.ready === true &&
        !loading &&
        !environmentError)) &&
    !resumeRecoveryError &&
    !continuing;
  const activeRun = state.session ? otherActiveRun(state.session) : undefined;
  const continueSession = useCallback(async () => {
    const source = connection.current?.getSnapshot().session;
    if (!source || continuingRef.current) return false;
    continuingRef.current = true;
    setContinuing(true);
    setFeedback('');
    try {
      const result = await continueConversation(
        desktop,
        source,
        resumeRequests.current,
        resumePending || null,
      );
      if (!result || !mounted.current) return false;
      connection.current?.updateRunInfo({
        ...source,
        conversationId: result.conversationId,
        continuation: result.continuation,
      });
      publishSessionMetadata(result);
      openCodexSession(desktop, result);
      return true;
    } catch (error) {
      if (mounted.current) {
        const reason =
          error instanceof Error
            ? error.message
            : 'Could not continue the conversation.';
        setFeedback(
          resumeRequests.current.hasPending(source.id)
            ? `The continuation result is unknown. Choose Continue again to check the same request, or open the running session if it appears. ${reason}`
            : reason,
        );
      }
      return false;
    } finally {
      continuingRef.current = false;
      if (mounted.current) {
        setContinuing(false);
        setResumePending(resumeRequests.current.pendingRequestId(source.id));
      }
    }
  }, [desktop, resumePending]);
  const openRunning = useCallback(async () => {
    const source = connection.current?.getSnapshot().session;
    const active = source ? otherActiveRun(source) : undefined;
    if (!source || !active) return false;
    setFeedback('');
    try {
      const session = await codexAPI.getRun(source.id, active);
      if (!mounted.current) return false;
      openCodexSession(desktop, session);
      return true;
    } catch (error) {
      if (mounted.current)
        setFeedback(
          error instanceof Error
            ? error.message
            : 'Could not open the running session.',
        );
      return false;
    }
  }, [desktop]);
  const details = useCallback(async () => {
    const session = connection.current?.getSnapshot().session;
    if (!session) return false;
    await showSessionDetails(desktop, session);
    return true;
  }, [desktop]);
  const changeMetadata = useCallback(
    async (action: 'rename' | 'archive') => {
      const session = connection.current?.getSnapshot().session;
      if (!session || changingMetadataRef.current) return false;
      changingMetadataRef.current = true;
      setChangingMetadata(true);
      setFeedback('');
      try {
        const result =
          action === 'rename'
            ? await renameSession(desktop, session)
            : await setSessionArchived(session);
        if (result && mounted.current && action === 'archive')
          setFeedback(
            result.archivedAt
              ? 'Archived. This does not stop runs or free capacity.'
              : 'Session unarchived.',
          );
        return !!result;
      } catch (error) {
        if (mounted.current)
          setFeedback(
            error instanceof Error
              ? error.message
              : 'Could not update the session.',
          );
        return false;
      } finally {
        changingMetadataRef.current = false;
        if (mounted.current) setChangingMetadata(false);
      }
    },
    [desktop],
  );
  const rename = useCallback(() => changeMetadata('rename'), [changeMetadata]);
  const archive = useCallback(
    () => changeMetadata('archive'),
    [changeMetadata],
  );
  const projectTerminal = useCallback(async () => {
    const session = connection.current?.getSnapshot().session;
    if (!session) return false;
    openTerminalWindow(desktop, session.projectPath);
    return true;
  }, [desktop]);
  const projectFiles = useCallback(async () => {
    const session = connection.current?.getSnapshot().session;
    if (!session) return;
    if (session.vmProject) {
      const project = session.vmProject.project;
      desktop.openApp(
        'project-files',
        { vmProjectId: project.id, hostId: project.hostId },
        { title: `VM · ${project.name}` },
      );
      return;
    }
    const { project } = await projectsAPI.register(session.projectPath);
    desktop.openApp(
      'project-files',
      { projectId: project.id },
      { title: project.name },
    );
  }, [desktop]);
  useEffect(() => {
    const unregister = [
      commands.register(window.id, 'codex:copy', copy, hasSelection),
      commands.register(
        window.id,
        'codex:paste',
        paste,
        state.phase === 'connected',
      ),
      commands.register(
        window.id,
        'codex:paste-text',
        pasteText,
        state.phase === 'connected',
      ),
      commands.register(window.id, 'codex:clear', clear),
      commands.register(
        window.id,
        'codex:interrupt',
        interrupt,
        state.phase === 'connected',
      ),
      commands.register(window.id, 'codex:reconnect', reconnect, canReconnect),
      commands.register(
        window.id,
        'codex:continue',
        continueSession,
        canContinue,
      ),
      commands.register(
        window.id,
        'codex:open-running',
        openRunning,
        !!activeRun,
      ),
      commands.register(window.id, 'codex:details', details, !!state.session),
      commands.register(
        window.id,
        'codex:rename',
        rename,
        !!state.session && !changingMetadata,
      ),
      commands.register(
        window.id,
        'codex:archive',
        archive,
        !!state.session && !state.session.archivedAt && !changingMetadata,
      ),
      commands.register(
        window.id,
        'codex:unarchive',
        archive,
        !!state.session?.archivedAt && !changingMetadata,
      ),
      commands.register(
        window.id,
        'codex:terminal',
        projectTerminal,
        !!state.session,
      ),
      commands.register(window.id, 'codex:end', end, live && !ending),
    ];
    return () => {
      for (const cleanup of unregister) cleanup();
    };
  }, [
    commands,
    window.id,
    copy,
    paste,
    pasteText,
    clear,
    interrupt,
    reconnect,
    continueSession,
    openRunning,
    canContinue,
    activeRun,
    details,
    rename,
    archive,
    projectTerminal,
    changingMetadata,
    end,
    hasSelection,
    state.phase,
    state.session,
    canReconnect,
    live,
    ending,
  ]);
  const scroll = (lines: number) => {
    if (connection.current?.scroll(lines)) scrolling.current = lines !== 0;
  };
  return (
    <div
      className="terminal-app codex-terminal"
      data-testid="codex-session"
      data-session-id={id}
      data-run-id={runId}
    >
      <div className="app-toolbar terminal-toolbar codex-primary-toolbar">
        <Button onClick={() => desktop.openApp('codex')}>Sessions</Button>
        <IconButton
          icon="attachment"
          label="Attachments"
          onClick={() =>
            desktop.openApp('conversation-context', { sessionId: id, runId })
          }
        />
        {state.session?.vmProject && (
          <>
            <Button onClick={() => void runCommand(desktop, projectFiles)}>
              Project Files
            </Button>
            <Button
              onClick={() => {
                const project = state.session?.vmProject?.project;
                if (project)
                  desktop.openApp(
                    'project-files',
                    { hostId: project.hostId, vmTasksProjectId: project.id },
                    { title: `Tasks · ${project.name}` },
                  );
              }}
            >
              Tasks
            </Button>
          </>
        )}
        <Button
          disabled={state.phase !== 'connected'}
          onClick={() => void interrupt()}
        >
          Interrupt
        </Button>
        {canReconnect && (
          <Button onClick={() => void reconnect()}>Reconnect</Button>
        )}
        {resumePending ? (
          <Button
            disabled={!canContinue}
            onClick={() => void continueSession()}
          >
            {continuing ? 'Continuing…' : 'Resolve Continue'}
          </Button>
        ) : activeRun ? (
          <Button onClick={() => void openRunning()}>
            Open Running Session
          </Button>
        ) : state.session && sessionCanContinue(state.session) ? (
          <Button
            disabled={!canContinue}
            onClick={() => void continueSession()}
          >
            {continuing ? 'Continuing…' : 'Continue'}
          </Button>
        ) : null}
        <IconButton
          icon="more"
          label="More Codex actions"
          className="codex-more-actions"
          aria-haspopup="menu"
          onClick={(event) => {
            const rect = event.currentTarget.getBoundingClientRect();
            desktop.contextMenu(
              codexMoreMenu(commands, window.id, {
                attachments: () => {
                  desktop.openApp('conversation-context', {
                    sessionId: id,
                    runId,
                  });
                },
                projectFiles: () => void runCommand(desktop, projectFiles),
                projectFilesAvailable: !!state.session,
              }),
              { x: rect.left, y: rect.bottom },
              { title: 'Codex' },
            );
          }}
        />
      </div>
      <div
        className={`terminal-host-info codex-session-context${state.session?.vmProject ? ' has-vm-project' : ''}`}
      >
        <span>Host</span>
        {state.session && (
          <>
            <code
              className="codex-terminal-project"
              data-testid="codex-project-name"
            >
              {state.session.vmProject
                ? `VM · ${state.session.vmProject.project.name}`
                : projectName(state.session.projectPath)}
            </code>
            {state.session.vmProject && (
              <span className="codex-vm-target">
                via AXV · {state.session.vmProject.binding.machineId}
              </span>
            )}
            {state.session.archivedAt && (
              <span className="codex-archived-label">Archived</span>
            )}
          </>
        )}
        <code className="codex-run-reference" data-testid="codex-run-id">
          Run {runId.slice(0, 8)}
        </code>
        <Button
          className="codex-context-details"
          disabled={!state.session}
          onClick={() => void details()}
        >
          Details
        </Button>
      </div>
      {(state.message || feedback || metadataError || resumeRecoveryError) && (
        <div
          className="terminal-message"
          role={
            !feedback &&
            !metadataError &&
            (state.phase === 'ended' || state.phase === 'reconnecting')
              ? 'status'
              : 'alert'
          }
        >
          {resumeRecoveryError || feedback || state.message || metadataError}
        </div>
      )}
      {state.session &&
        !sessionCanEnd(state.session) &&
        state.session.continuation.state === 'unavailable' && (
          <div className="terminal-message" role="status">
            {state.session.continuation.reason ||
              'Conversation history is unavailable. This run cannot be continued.'}
          </div>
        )}
      <div
        className="terminal-screen codex-terminal-screen"
        data-testid="codex-screen"
      >
        <div className="terminal-emulator" ref={screen} />
      </div>
      <div
        className="app-toolbar bottom-toolbar codex-scroll-controls"
        aria-label="Codex scrollback"
      >
        <IconButton
          icon="page-up"
          label="Page Up"
          disabled={state.phase !== 'connected'}
          onClick={() => scroll(size.rows - 1)}
        />
        <IconButton
          icon="page-down"
          label="Page Down"
          disabled={state.phase !== 'connected'}
          onClick={() => scroll(1 - size.rows)}
        />
        <Button
          disabled={state.phase !== 'connected'}
          onClick={() => scroll(0)}
        >
          Live
        </Button>
      </div>
      <StatusBar>
        <span data-testid="codex-status" data-state={state.phase}>
          {state.phase === 'connected'
            ? 'Terminal connected'
            : state.phase === 'connecting'
              ? 'Connecting…'
              : state.phase === 'reconnecting'
                ? 'Reconnecting…'
                : state.phase === 'ended'
                  ? 'Session ended'
                  : state.phase === 'stopping'
                    ? 'Stopping session'
                    : 'Disconnected'}
        </span>
        <span>
          {size.cols} × {size.rows}
        </span>
      </StatusBar>
    </div>
  );
}
