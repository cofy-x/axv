import { useCallback, useEffect, useState, useSyncExternalStore } from 'react';
import type { RefObject } from 'react';
import { Button, TextArea } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { Terminal } from '@xterm/xterm';
import { TerminalPasteRequests } from './terminal-paste';
import type {
  TerminalPasteRequest,
  TerminalPasteResult,
} from './terminal-paste';

interface Options {
  desktop: DesktopAPI;
  title: string;
  terminal: RefObject<Terminal | null>;
  connection: RefObject<{ getSnapshot: () => { phase: string } } | null>;
  interactive: RefObject<boolean>;
  mayFocus: () => boolean;
  setFeedback: (message: string) => void;
}

const UNAVAILABLE =
  'The terminal connection changed. Start a new paste for the current connection.';
const REJECTED =
  'Text was not queued. Check the connection, wait, or try a smaller paste.';
const FAILED = 'Pasting failed. Check the terminal before trying again.';

function sendError(result: TerminalPasteResult) {
  switch (result) {
    case 'unavailable':
      return UNAVAILABLE;
    case 'rejected':
      return REJECTED;
    case 'failed':
      return FAILED;
    case 'empty':
      return 'Enter text to paste.';
    default:
      return '';
  }
}

function PasteTextEditor({
  requests,
  request,
  title,
  reason,
  initialText,
  setFeedback,
}: {
  requests: TerminalPasteRequests;
  request: TerminalPasteRequest;
  title: string;
  reason: string;
  initialText: string;
  setFeedback: Options['setFeedback'];
}) {
  useSyncExternalStore(requests.subscribe, requests.getSnapshot);
  const [text, setText] = useState(initialText);
  const [error, setError] = useState(reason);
  const current = requests.isCurrent(request);
  return (
    <div className="terminal-paste-editor">
      <p>
        Target: <strong>{title}</strong>
      </p>
      <p>
        Paste into the field using your device’s menu, review it, then choose
        Send. Line breaks may run commands. AXV does not add an Enter key.
      </p>
      <TextArea
        label="Text to paste"
        rows={6}
        value={text}
        readOnly={request.sent}
        spellCheck={false}
        autoCorrect="off"
        autoCapitalize="none"
        onChange={(event) => {
          setText(event.target.value);
          setError('');
        }}
      />
      {request.sent ? (
        <p role="status">Text queued for the terminal.</p>
      ) : !current ? (
        <p role="alert">{UNAVAILABLE}</p>
      ) : error ? (
        <p role="alert">{error}</p>
      ) : null}
      <div className="app-actions">
        <Button
          disabled={!current || request.sent || !text}
          onClick={() => {
            const result = requests.send(request, text);
            const message = sendError(result);
            setError(message);
            setFeedback(message);
          }}
        >
          Send
        </Button>
      </div>
    </div>
  );
}

export function useTerminalPaste({
  desktop,
  title,
  terminal,
  connection,
  interactive,
  mayFocus,
  setFeedback,
}: Options) {
  const [requests] = useState(() => new TerminalPasteRequests());
  useEffect(() => () => requests.invalidate(), [requests]);

  const begin = useCallback(() => {
    const target = terminal.current;
    const session = connection.current;
    return requests.begin(
      target && session?.getSnapshot().phase === 'connected'
        ? {
            terminal: target,
            isCurrent: () =>
              terminal.current === target &&
              connection.current === session &&
              session.getSnapshot().phase === 'connected',
          }
        : null,
    );
  }, [requests, terminal, connection]);

  const edit = useCallback(
    async (request: TerminalPasteRequest, reason = '', initialText = '') => {
      if (!requests.isCurrent(request)) return false;
      await desktop.dialog({
        title: 'Paste text',
        message: (
          <PasteTextEditor
            requests={requests}
            request={request}
            title={title}
            reason={reason}
            initialText={initialText}
            setFeedback={setFeedback}
          />
        ),
        buttons: [{ label: 'Close', value: 'close', cancel: true }],
      });
      const sent = request.sent;
      // readText cannot be aborted; closing a manual paste invalidates late work.
      if (requests.isCurrent(request)) requests.invalidate();
      return sent;
    },
    [requests, desktop, title, setFeedback],
  );

  const pasteText = useCallback(async () => {
    const request = begin();
    return request ? edit(request) : false;
  }, [begin, edit]);

  const pasteClipboard = useCallback(async () => {
    const request = begin();
    if (!request) return false;
    let text: string;
    try {
      // Keep the read in the initiating click, before any other awaited work.
      text = await navigator.clipboard.readText();
    } catch {
      if (!requests.isCurrent(request)) return false;
      const message = 'Clipboard text could not be read.';
      setFeedback(message);
      return edit(request, message);
    }
    if (!requests.isCurrent(request)) return false;
    const result = requests.send(request, text);
    if (result === 'empty') {
      setFeedback('The clipboard has no plain text to paste.');
      return false;
    }
    const message = sendError(result);
    setFeedback(message);
    if (result !== 'sent') return edit(request, message, text);
    if (interactive.current && mayFocus()) terminal.current?.focus();
    return true;
  }, [begin, requests, edit, setFeedback, interactive, mayFocus, terminal]);

  return {
    pasteClipboard,
    pasteText,
    invalidatePaste: requests.invalidate,
    recordPasteInput: requests.recordInput,
  };
}
