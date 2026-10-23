import { useState } from 'react';
import { IconButton } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { CodexSessionRecord } from './api';
import { sessionTitle } from './api';
import { activityLabel } from './activity';
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
      setFeedback('Clipboard access denied. Select and copy the command.');
    }
  };
  return (
    <div className="codex-session-details">
      <dl className="info-list">
        <dt>Name</dt>
        <dd>{sessionTitle(session)}</dd>
        <dt>List</dt>
        <dd>{session.archivedAt ? 'Archived' : 'Unarchived'}</dd>
        <dt>Activity</dt>
        <dd>{activityLabel(session.activity)}</dd>
        {session.activity?.updatedAt && (
          <>
            <dt>Last activity</dt>
            <dd>{new Date(session.activity.updatedAt).toLocaleString()}</dd>
          </>
        )}
        <dt>Target</dt>
        <dd>Host</dd>
        <dt>{session.vmProject ? 'Host working directory' : 'Project'}</dt>
        <dd>
          <code>{session.projectPath}</code>
        </dd>
        {session.vmProject && (
          <>
            <dt>VM project</dt>
            <dd>{session.vmProject.project.name}</dd>
            <dt>Project ID</dt>
            <dd>
              <code>{session.vmProject.project.id}</code>
            </dd>
            <dt>Host ID</dt>
            <dd>
              <code>{session.vmProject.project.hostId}</code>
            </dd>
            <dt>Volume / directory</dt>
            <dd>
              <code>
                {session.vmProject.binding.volumeId} /{' '}
                {session.vmProject.project.root || 'Volume root'}
              </code>
            </dd>
            <dt>VM</dt>
            <dd>
              <code>{session.vmProject.binding.machineId}</code>
            </dd>
            <dt>Boot / instance</dt>
            <dd>
              <code>
                {session.vmProject.binding.bootId} /{' '}
                {session.vmProject.binding.instanceId}
              </code>
            </dd>
          </>
        )}
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
              available: 'History available for a new run.',
              active: 'This conversation has an active run.',
              unavailable: 'Saved history is unavailable.',
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
        <dt>Run status</dt>
        <dd>{session.status}</dd>
        <dt>Run started</dt>
        <dd>{new Date(session.createdAt).toLocaleString()}</dd>
        {session.endedAt && (
          <>
            <dt>Run ended</dt>
            <dd>{new Date(session.endedAt).toLocaleString()}</dd>
          </>
        )}
      </dl>
      {session.attachCommand && (
        <>
          <p className="codex-attach-command">
            <strong>Attach command</strong>
            <code>{session.attachCommand}</code>
          </p>
          <IconButton
            icon="copy"
            label="Copy Command"
            onClick={() => void copy()}
          />
          {feedback && <p role={copyFailed ? 'alert' : 'status'}>{feedback}</p>}
        </>
      )}
      {session.reason && <p>{session.reason}</p>}
      {session.activity?.reason && <p>{session.activity.reason}</p>}
      <p>
        Closing a Codex run window only detaches. End Session stops its run.
        Continue starts a new run with saved history. Archiving does not stop
        runs.
      </p>
      <p>Choose New Session for a different conversation.</p>
      {session.vmProject && (
        <p>
          The agent runs on the host. Its AXV Skill uses this captured VM
          environment; project files and persistent tasks remain compute-owned.
        </p>
      )}
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
