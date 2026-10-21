import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { Button, IconButton, PixelIcon, StatusBar } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { request } from '../../shared/api/http';
import { useWindowCommands } from '../../shared/desktop/WindowCommandsContext';
import { useTerminalPaste } from '../../shared/terminal/use-terminal-paste';
import { TerminalSession } from './terminal-session';
import type { TerminalInfo, TerminalState } from './terminal-session';
import { isDesktopTerminalKey } from './terminal-keys';
import { terminalSessionMenu } from './definition';

function mayFocus() {
  return !document.querySelector('.ns-dialog-layer, .ns-menu-layer');
}

export function TerminalApp({
  window,
  desktop,
  interactive = true,
}: AppWindowProps) {
  const projectPath = window.params.projectPath;
  const screen = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const session = useRef<TerminalSession | null>(null);
  const active =
    desktop.activeWindowId === window.id &&
    !window.shaded &&
    !window.minimized &&
    (window.sticky || desktop.currentWorkspaceId === window.workspaceId);
  const activeRef = useRef(active);
  const interactiveRef = useRef(interactive && active);
  const commands = useWindowCommands();
  const [generation, setGeneration] = useState(0);
  const [state, setState] = useState<TerminalState>({ phase: 'connecting' });
  const [size, setSize] = useState({ cols: 80, rows: 24 });
  const [hasSelection, setHasSelection] = useState(false);
  const [feedback, setFeedback] = useState('');
  const { pasteClipboard, pasteText, invalidatePaste, recordPasteInput } =
    useTerminalPaste({
      desktop,
      title: window.title,
      terminal,
      connection: session,
      interactive: interactiveRef,
      mayFocus,
      setFeedback,
    });
  const live = state.phase === 'connecting' || state.phase === 'connected';
  const projectName =
    projectPath?.split(/[\\/]/).filter(Boolean).at(-1) ||
    projectPath ||
    'No project directory selected.';

  useLayoutEffect(() => {
    interactiveRef.current = interactive && active;
    if (terminal.current) {
      terminal.current.options.disableStdin =
        !interactiveRef.current ||
        session.current?.getSnapshot().phase !== 'connected';
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
    const host = screen.current;
    if (!host) return;
    let disposed = false;
    let opened = false;
    let frame = 0;
    setState({ phase: 'connecting' });
    setFeedback('');
    setHasSelection(false);
    const term = new Terminal({
      fontFamily:
        getComputedStyle(host).getPropertyValue('--ns-mono').trim() ||
        '"SFMono-Regular", Consolas, "Liberation Mono", monospace',
      fontSize: 13,
      lineHeight: 1.15,
      cursorBlink: true,
      scrollback: 5000,
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
    const connection = new TerminalSession({
      baseURL: globalThis.location.href,
      projectPath,
      getInfo: (signal) => request<TerminalInfo>('/terminal/info', { signal }),
      createSocket: (url) => new WebSocket(url),
      write: (data, done) => term.write(data, done),
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
    session.current = connection;
    const subscriptions = [
      term.onData((data) => {
        recordPasteInput(connection.sendText(data));
      }),
      term.onBinary((data) => {
        connection.sendBinary(data);
      }),
      term.onResize(({ cols, rows }) => {
        setSize({ cols, rows });
        connection.resize(cols, rows);
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
        void Promise.resolve()
          .then(() => navigator.clipboard.writeText(term.getSelection()))
          .catch(() => {
            if (!disposed)
              setFeedback(
                'Clipboard access was denied. Use the browser’s Copy command for the selected text.',
              );
          });
        return false;
      }
      return true;
    });

    function fitScreen() {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        if (disposed || host!.clientWidth <= 0 || host!.clientHeight <= 0)
          return;
        const firstOpen = !opened;
        if (firstOpen) {
          term.open(host!);
          opened = true;
          term.textarea?.setAttribute('aria-label', 'Host terminal input');
        }
        const dimensions = fit.proposeDimensions();
        if (dimensions)
          term.resize(
            Math.max(2, Math.min(500, dimensions.cols)),
            Math.max(1, Math.min(200, dimensions.rows)),
          );
        connection.resize(term.cols, term.rows);
        void connection.start();
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
      cancelAnimationFrame(frame);
      observer.disconnect();
      connection.dispose();
      for (const subscription of subscriptions) subscription.dispose();
      term.dispose();
      terminal.current = null;
      session.current = null;
    };
  }, [generation, projectPath, invalidatePaste, recordPasteInput]);

  useEffect(
    () =>
      desktop.beforeClose(window.id, async () => {
        const phase = session.current?.getSnapshot().phase;
        if (phase !== 'connecting' && phase !== 'connected') return true;
        const answer = await desktop.dialog({
          title: 'End host shell?',
          message:
            'Closing this window ends its shell and running foreground jobs.',
          buttons: [
            { label: 'Cancel', value: 'cancel', default: true, cancel: true },
            { label: 'End Session', value: 'end' },
          ],
        });
        return answer?.button === 'end';
      }),
    [desktop, window.id],
  );

  useEffect(() => {
    const prevent = (event: BeforeUnloadEvent) => {
      const phase = session.current?.getSnapshot().phase;
      if (phase === 'connecting' || phase === 'connected') {
        event.preventDefault();
        event.returnValue = '';
      }
    };
    globalThis.addEventListener('beforeunload', prevent);
    return () => globalThis.removeEventListener('beforeunload', prevent);
  }, []);

  const copySelection = useCallback(async () => {
    const target = terminal.current;
    const text = target?.getSelection();
    if (!text) return false;
    try {
      await navigator.clipboard.writeText(text);
      if (terminal.current !== target) return false;
      setFeedback('');
      return true;
    } catch {
      if (terminal.current !== target) return false;
      setFeedback(
        'Clipboard access was denied. Use the browser’s Copy command for the selected text.',
      );
      return false;
    }
  }, []);

  const newSession = useCallback(async () => {
    const phase = session.current?.getSnapshot().phase;
    if (phase === 'connecting' || phase === 'connected') return false;
    setGeneration((value) => value + 1);
    return true;
  }, []);

  const clear = useCallback(async () => {
    terminal.current?.clear();
    return !!terminal.current;
  }, []);

  const interrupt = useCallback(async () => {
    const sent = session.current?.sendText('\u0003') ?? false;
    if (interactiveRef.current && mayFocus()) terminal.current?.focus();
    return sent;
  }, []);

  const details = useCallback(async () => {
    await desktop.dialog({
      title: 'Host Terminal Details',
      message: (
        <div className="terminal-details">
          <dl className="info-list">
            <dt>Execution target</dt>
            <dd>Host filesystem</dd>
            <dt>Initial directory</dt>
            <dd>
              {projectPath === undefined ? (
                "Service user's home directory"
              ) : (
                <code data-testid="terminal-project-path">
                  {projectPath || 'No project directory selected.'}
                </code>
              )}
            </dd>
            <dt>Shell</dt>
            <dd>
              <code>{state.shell || 'Unavailable'}</code>
            </dd>
          </dl>
          <p>
            AXV documents are separate from the host filesystem. Closing this
            window ends its shell and foreground jobs. New Session starts in the
            initial directory, not a later shell directory.
          </p>
        </div>
      ),
      buttons: [
        { label: 'Close', value: 'close', default: true, cancel: true },
      ],
    });
    return true;
  }, [desktop, projectPath, state.shell]);

  useEffect(() => {
    const unregister = [
      commands.register(window.id, 'terminal:new-session', newSession, !live),
      commands.register(
        window.id,
        'terminal:copy',
        copySelection,
        hasSelection,
      ),
      commands.register(
        window.id,
        'terminal:paste',
        pasteClipboard,
        state.phase === 'connected',
      ),
      commands.register(
        window.id,
        'terminal:paste-text',
        pasteText,
        state.phase === 'connected',
      ),
      commands.register(window.id, 'terminal:clear', clear),
      commands.register(window.id, 'terminal:details', details),
      commands.register(
        window.id,
        'terminal:interrupt',
        interrupt,
        state.phase === 'connected',
      ),
    ];
    return () => {
      for (const cleanup of unregister) cleanup();
    };
  }, [
    commands,
    window.id,
    newSession,
    copySelection,
    pasteClipboard,
    pasteText,
    clear,
    details,
    interrupt,
    live,
    hasSelection,
    state.phase,
  ]);

  return (
    <div
      className="terminal-app"
      data-state={state.phase}
      data-session-id={window.id}
      data-project-path={projectPath}
    >
      <div className="app-toolbar terminal-toolbar">
        <PixelIcon name="terminal" size={16} />
        <span className="toolbar-caption">Host shell</span>
        {!live && (
          <Button onClick={() => void newSession()}>New Session</Button>
        )}
        <IconButton
          icon="copy"
          label="Copy"
          disabled={!hasSelection}
          onClick={() => void copySelection()}
        />
        <IconButton
          icon="paste"
          label="Paste"
          disabled={state.phase !== 'connected'}
          onClick={() => void pasteClipboard()}
        />
        <Button
          disabled={state.phase !== 'connected'}
          onClick={() => void interrupt()}
        >
          Interrupt
        </Button>
        <IconButton
          icon="more"
          label="More terminal actions"
          aria-haspopup="menu"
          onClick={(event) => {
            const rect = event.currentTarget.getBoundingClientRect();
            desktop.contextMenu(
              terminalSessionMenu(commands, window.id),
              { x: rect.left, y: rect.bottom },
              {
                title: 'Session',
                pin: {
                  kind: 'application',
                  groupId: 'session',
                  ownerWindowId: window.id,
                  title: 'Session',
                },
              },
            );
          }}
        />
      </div>
      <div className="terminal-host-info terminal-project-info">
        <span>Initial directory</span>
        <strong
          className="terminal-project-name"
          data-testid="terminal-project-name"
        >
          {projectPath === undefined ? 'Home' : projectName}
        </strong>
        <Button onClick={() => void details()}>Details</Button>
      </div>
      {(state.message || feedback) && (
        <div
          className="terminal-message"
          role={state.phase === 'ended' && !feedback ? 'status' : 'alert'}
        >
          {state.message && <div>{state.message}</div>}
          {feedback && feedback !== state.message && <div>{feedback}</div>}
        </div>
      )}
      <div className="terminal-screen" data-testid="terminal-screen">
        <div className="terminal-emulator" ref={screen} />
      </div>
      <StatusBar>
        <span data-testid="terminal-status" data-state={state.phase}>
          {state.phase === 'connecting'
            ? 'Starting host shell…'
            : state.phase === 'connected'
              ? 'Connected'
              : state.phase === 'ended'
                ? 'Session ended'
                : state.phase === 'unsupported'
                  ? 'Unavailable'
                  : 'Disconnected'}
        </span>
        <span>
          {size.cols} × {size.rows}
        </span>
      </StatusBar>
    </div>
  );
}
