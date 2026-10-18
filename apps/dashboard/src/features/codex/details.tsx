import { useState } from 'react';
import { Button } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { CodexSessionRecord } from './api';
import { sessionTitle } from './api';
import { openTerminalWindow } from '../terminal/definition';

function SessionDetails({ session }: { session: CodexSessionRecord }) {
  const [feedback, setFeedback] = useState('');
  const [copyFailed, setCopyFailed] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(session.attachCommand);
      setCopyFailed(false);
      setFeedback('Attach command copied.');
    } catch {
      setCopyFailed(true);
      setFeedback(
        'Clipboard access was denied. Select and copy the command above.',
      );
    }
  };
  return (
    <div className="codex-session-details">
      <dl className="info-list">
        <dt>Name</dt>
        <dd>{sessionTitle(session)}</dd>
        <dt>List</dt>
        <dd>{session.archivedAt ? 'Archived' : 'Unarchived'}</dd>
        <dt>Execution target</dt>
        <dd>Host</dd>
        <dt>Project</dt>
        <dd>
          <code>{session.projectPath}</code>
        </dd>
        <dt>Run</dt>
        <dd>
          <code>{session.runId}</code>
        </dd>
        <dt>Conversation</dt>
        <dd>
          <code>{session.conversationId || 'Not identified'}</code>
        </dd>
        <dt>Continuation</dt>
        <dd>
          {session.continuation.reason ||
            {
              available:
                'Saved conversation is available to continue in a new run.',
              active: 'This conversation already has an active run.',
              unavailable: 'Saved conversation history is unavailable.',
            }[session.continuation.state]}
        </dd>
        <dt>tmux session</dt>
        <dd>
          <code>{session.tmuxSession}</code>
        </dd>
        <dt>tmux socket</dt>
        <dd>
          {session.tmuxSocket ? (
            <code>{session.tmuxSocket}</code>
          ) : (
            'Default server'
          )}
        </dd>
        <dt>Status</dt>
        <dd>{session.status}</dd>
        <dt>Run started</dt>
        <dd>{new Date(session.createdAt).toLocaleString()}</dd>
      </dl>
      {session.attachCommand && (
        <>
          <p className="codex-attach-command">
            <strong>Attach command</strong>
            <code>{session.attachCommand}</code>
          </p>
          <Button onClick={() => void copy()}>Copy Command</Button>
          {feedback && <p role={copyFailed ? 'alert' : 'status'}>{feedback}</p>}
        </>
      )}
      {session.reason && <p>{session.reason}</p>}
      <p>
        Closing a window detaches its connection. End Session stops this run.
        Continuing a saved conversation starts a separate run. Archiving does
        not stop it.
      </p>
      <p>
        Each AXV session uses one Codex conversation. Start a new session in AXV
        for a different conversation.
      </p>
    </div>
  );
}

export async function showSessionDetails(
  desktop: DesktopAPI,
  session: CodexSessionRecord,
) {
  const answer = await desktop.dialog({
    title: 'Codex Session Details',
    message: <SessionDetails session={session} />,
    buttons: [
      { label: 'Host Terminal', value: 'terminal' },
      { label: 'Close', value: 'close', default: true, cancel: true },
    ],
  });
  if (answer?.button === 'terminal')
    openTerminalWindow(desktop, session.projectPath);
  return answer;
}
