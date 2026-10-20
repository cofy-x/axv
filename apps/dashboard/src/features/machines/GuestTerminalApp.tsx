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
import { useWindowCommands } from '../../shared/desktop/WindowCommandsContext';
import { useTerminalPaste } from '../../shared/terminal/use-terminal-paste';
import { GuestTerminalSession } from './guest-terminal-session';
import { machinesApi } from './api';
import type { TerminalState } from '../terminal/terminal-session';
import { isDesktopTerminalKey } from '../terminal/terminal-keys';
import { guestTerminalSessionMenu } from './definition';

function mayFocus() {
  return !document.querySelector('.ns-dialog-layer, .ns-menu-layer');
}

export function GuestTerminalApp({
  window,
  desktop,
  interactive = true,
}: AppWindowProps) {
  const machineId = window.params.machineId || '';
  const hostId = window.params.hostId || '';
  const terminalId = window.params.terminalId || '';
  const bootId = window.params.bootId || '';
  const screen = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const session = useRef<GuestTerminalSession | null>(null);
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
    const connection = new GuestTerminalSession({
      hostId,
      machineId,
      terminalId,
      bootId,
      reset: () =>
        new Promise<void>((resolve) =>
          term.write('', () => {
            term.reset();
            resolve();
          }),
        ),
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
          term.textarea?.setAttribute('aria-label', 'Guest terminal input');
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
  }, [
    generation,
    hostId,
    machineId,
    terminalId,
    bootId,
    invalidatePaste,
    recordPasteInput,
  ]);

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

  const reconnect = useCallback(async () => {
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
      title: 'Guest Terminal Details',
      message: (
        <div className="terminal-details">
          <dl className="info-list">
            <dt>Execution target</dt>
            <dd>{window.title}</dd>
            <dt>Initial directory</dt>
            <dd>VM project volume</dd>
            <dt>VM</dt>
            <dd>
              <code>{machineId || 'Unavailable'}</code>
            </dd>
            <dt>Host</dt>
            <dd>
              <code>{hostId || 'Unavailable'}</code>
            </dd>
            <dt>Guest boot</dt>
            <dd>
              <code>{bootId || 'Unavailable'}</code>
            </dd>
            <dt>Terminal</dt>
            <dd>
              <code>{terminalId || 'Unavailable'}</code>
            </dd>
            <dt>Shell</dt>
            <dd>
              <code>{state.shell || 'Unavailable'}</code>
            </dd>
          </dl>
          <p>
            Runs in this VM on its project volume. Closing this window detaches
            the shell. End Session stops the shell and its jobs; stopping the VM
            also ends the shell. Reconnect returns to this same guest terminal.
          </p>
        </div>
      ),
      buttons: [
        { label: 'Close', value: 'close', default: true, cancel: true },
      ],
    });
    return true;
  }, [
    desktop,
    window.title,
    machineId,
    hostId,
    bootId,
    terminalId,
    state.shell,
  ]);

  const endSession = useCallback(async () => {
    const answer = await desktop.dialog({
      title: 'End guest terminal?',
      message: `End “${window.title}” and its jobs? Terminal: ${terminalId}. Closing the window only detaches it.`,
      buttons: [
        { label: 'Cancel', value: 'cancel', default: true, cancel: true },
        { label: 'End Session', value: 'end' },
      ],
    });
    if (answer?.button !== 'end') return false;
    try {
      await machinesApi.endTerminal(machineId, terminalId, hostId);
      invalidatePaste();
      session.current?.dispose();
      setState({ phase: 'ended', message: 'Guest terminal ended.' });
      if (terminal.current) terminal.current.options.disableStdin = true;
      return true;
    } catch (error) {
      setFeedback(error instanceof Error ? error.message : String(error));
      return false;
    }
  }, [desktop, window.title, machineId, terminalId, hostId, invalidatePaste]);

  useEffect(() => {
    const unregister = [
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
      commands.register(
        window.id,
        'guest-terminal:reconnect',
        reconnect,
        !live,
      ),
      commands.register(
        window.id,
        'guest-terminal:end',
        endSession,
        !!hostId && state.phase !== 'ended',
      ),
    ];
    return () => {
      for (const cleanup of unregister) cleanup();
    };
  }, [
    commands,
    window.id,
    reconnect,
    copySelection,
    pasteClipboard,
    pasteText,
    clear,
    details,
    interrupt,
    endSession,
    live,
    hostId,
    hasSelection,
    state.phase,
  ]);

  return (
    <div
      className="terminal-app"
      data-state={state.phase}
      data-session-id={window.id}
    >
      <div className="app-toolbar terminal-toolbar">
        <PixelIcon name="terminal" size={16} />
        <span className="toolbar-caption">Guest shell</span>
        {!live && <Button onClick={() => void reconnect()}>Reconnect</Button>}
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
          label="More guest terminal actions"
          aria-haspopup="menu"
          onClick={(event) => {
            const rect = event.currentTarget.getBoundingClientRect();
            desktop.contextMenu(
              guestTerminalSessionMenu(commands, window.id),
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
        <span>VM</span>
        <strong className="terminal-project-name">Project volume</strong>
        <Button onClick={() => void details()}>Details</Button>
      </div>
      {(state.message || feedback) && (
        <div
          className="terminal-message"
          role={state.phase === 'ended' ? 'status' : 'alert'}
        >
          {feedback || state.message}
        </div>
      )}
      <div className="terminal-screen" data-testid="terminal-screen">
        <div className="terminal-emulator" ref={screen} />
      </div>
      <StatusBar>
        <span data-testid="terminal-status" data-state={state.phase}>
          {state.phase === 'connecting'
            ? 'Attaching to guest shell…'
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
