import { Button, IconButton } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import {
  isVMSession,
  projectName,
  sessionCanContinue,
  sessionCanEnd,
  sessionIsLive,
  sessionKey,
  sessionTitle,
} from './api';
import type { CodexSessionRecord } from './api';
import { CodexActivity } from './CodexActivity';
import { canReviewActivity, hasActivityAction } from './activity';
import { openCodexSession } from './definition';
import { showSessionDetails } from './details';
import { openTerminalWindow } from '../terminal/definition';
import { machinesApi } from '../machines/api';
import { openGuestTerminal } from '../machines/open-terminal';

export interface SessionContinuationRecovery {
  requestId?: string;
  error?: string;
}

export interface CodexSessionRowProps {
  desktop: DesktopAPI;
  session: CodexSessionRecord;
  hasVMSessions: boolean;
  duplicateTitle: boolean;
  filter: 'unarchived' | 'archived' | 'attention';
  sessionError: string;
  canStart: boolean;
  recovery?: SessionContinuationRecovery;
  continuing: string | null;
  reviewing: string | null;
  changingMetadata: string | null;
  ending: string | null;
  onClearNotice: () => void;
  onContinue: (session: CodexSessionRecord, requestId: string | null) => void;
  onReview: (session: CodexSessionRecord) => void;
  onChangeMetadata: (
    session: CodexSessionRecord,
    action: 'rename' | 'archive',
  ) => void;
  onEnd: (session: CodexSessionRecord) => void;
  onError: (message: string) => void;
}

export function CodexSessionRow({
  desktop,
  session,
  hasVMSessions,
  duplicateTitle,
  filter,
  sessionError,
  canStart,
  recovery,
  continuing,
  reviewing,
  changingMetadata,
  ending,
  onClearNotice,
  onContinue,
  onReview,
  onChangeMetadata,
  onEnd,
  onError,
}: CodexSessionRowProps) {
  const key = sessionKey(session);
  const vmSession = isVMSession(session);
  const ended = session.status === 'ended';
  const showActivity =
    !ended || (session.activity && session.activity.state !== 'unknown');
  const shouldContinue = !!recovery?.requestId || sessionCanContinue(session);
  const canContinue =
    !recovery?.error &&
    (!!recovery?.requestId ||
      (sessionCanContinue(session) && (vmSession || canStart)));

  return (
    <li
      data-session-id={session.id}
      data-session-target={vmSession ? 'vm' : 'host'}
      data-run-id={session.runId}
      data-action-needed={hasActivityAction(session.activity)}
    >
      <div className="codex-session-summary">
        <div className="codex-session-header">
          <strong>{sessionTitle(session)}</strong>
          {vmSession ? (
            <span
              className="codex-target-badge codex-target-badge--vm"
              data-testid="codex-session-target"
            >
              VM · {session.machineName || session.machineId}
            </span>
          ) : hasVMSessions ? (
            <span
              className="codex-target-badge"
              data-testid="codex-session-target"
            >
              Host
            </span>
          ) : null}
          {duplicateTitle && (
            <code
              className="codex-session-reference"
              data-testid="codex-run-id"
              title={session.runId}
            >
              Run {session.runId.slice(0, 8)}
            </code>
          )}
          {showActivity && (
            <CodexActivity activity={session.activity} stale={!!sessionError} />
          )}
        </div>
        <span>
          <span data-testid="codex-session-project" title={session.projectPath}>
            {projectName(session.projectPath)}
            {session.archivedAt && filter === 'attention' && ' · Archived'}
          </span>
          {ending === key
            ? ' · Ending…'
            : session.status !== 'running' && ` · Run ${session.status}`}
        </span>
        {showActivity && session.activity?.reason && (
          <span>{session.activity.reason}</span>
        )}
        {!ended && session.reason && <span>{session.reason}</span>}
        {recovery?.error && <span role="alert">{recovery.error}</span>}
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
            (continuing === key || (shouldContinue && !canContinue))
          }
          onClick={() => {
            onClearNotice();
            if (shouldContinue && !hasActivityAction(session.activity))
              onContinue(session, recovery?.requestId || null);
            else openCodexSession(desktop, session);
          }}
        >
          {hasActivityAction(session.activity)
            ? 'Open'
            : continuing === key
              ? 'Continuing…'
              : recovery?.requestId
                ? 'Resolve Continue'
                : sessionIsLive(session)
                  ? 'Open'
                  : sessionCanContinue(session)
                    ? 'Continue'
                    : 'View'}
        </Button>
        {filter === 'attention' && canReviewActivity(session.activity) && (
          <Button
            disabled={reviewing !== null}
            onClick={() => onReview(session)}
          >
            {reviewing === key ? 'Dismissing…' : 'Dismiss alert'}
          </Button>
        )}
        <IconButton
          icon="more"
          label={`More session actions for ${sessionTitle(session)}, run ${session.runId.slice(0, 8)}`}
          aria-haspopup="menu"
          disabled={changingMetadata === key || ending === key}
          onClick={(event) => {
            const rect = event.currentTarget.getBoundingClientRect();
            desktop.contextMenu(
              [
                {
                  label: 'Details',
                  action: () => void showSessionDetails(desktop, session),
                },
                ...(canReviewActivity(session.activity)
                  ? [
                      {
                        label:
                          reviewing === key ? 'Dismissing…' : 'Dismiss alert',
                        disabled: reviewing !== null,
                        action: () => onReview(session),
                      },
                    ]
                  : []),
                { separator: true },
                {
                  label: recovery?.requestId ? 'Resolve Continue' : 'Continue',
                  disabled: !canContinue || continuing !== null,
                  action: () =>
                    onContinue(session, recovery?.requestId || null),
                },
                { separator: true },
                {
                  label: 'Rename',
                  disabled: changingMetadata !== null,
                  action: () => onChangeMetadata(session, 'rename'),
                },
                {
                  label: session.archivedAt ? 'Unarchive' : 'Archive',
                  disabled: changingMetadata !== null,
                  action: () => onChangeMetadata(session, 'archive'),
                },
                vmSession
                  ? {
                      label: 'VM Terminal',
                      action: () => {
                        const dir = session.projectPath.replace(
                          /^\/projects\/?/,
                          '',
                        );
                        void machinesApi
                          .createTerminal(
                            session.machineId!,
                            crypto.randomUUID(),
                            session.hostId!,
                            dir || undefined,
                          )
                          .then((terminal) =>
                            openGuestTerminal(
                              desktop,
                              {
                                id: session.machineId!,
                                hostId: session.hostId!,
                                spec: {
                                  name:
                                    session.machineName || session.machineId!,
                                },
                              },
                              terminal,
                            ),
                          )
                          .catch((err: unknown) => {
                            onError(
                              err instanceof Error
                                ? err.message
                                : 'Could not open VM terminal.',
                            );
                          });
                      },
                    }
                  : {
                      label: 'Host Terminal',
                      action: () => {
                        openTerminalWindow(desktop, session.projectPath);
                      },
                    },
                { separator: true },
                {
                  label: 'End Session',
                  disabled: !sessionCanEnd(session) || ending !== null,
                  action: () => onEnd(session),
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
}
