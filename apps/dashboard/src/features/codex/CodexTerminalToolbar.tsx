import { Button, IconButton } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { WindowCommands } from '../../shared/desktop/window-commands';
import { runCommand } from '../../shared/desktop/dialogs';
import { sessionCanContinue, sessionCanEnd } from './api';
import type { CodexConnectionState } from './session';
import { CodexActivity } from './CodexActivity';
import { canReviewActivity } from './activity';
import { codexMoreMenu } from './definition';

export interface CodexTerminalToolbarProps {
  desktop: DesktopAPI;
  commands: WindowCommands;
  windowId: string;
  isVM: boolean;
  vmMachineName: string;
  state: CodexConnectionState;
  canReconnect: boolean;
  canContinue: boolean;
  continuing: boolean;
  resumePending?: string;
  activeRun?: string;
  metadataError: string;
  feedback: string;
  resumeRecoveryError: string;
  reviewing: boolean;
  onInterrupt: () => void;
  onReconnect: () => void;
  onContinue: () => void;
  onOpenRunning: () => void;
  onReview: (button: HTMLButtonElement) => void;
  onProjectFiles: () => Promise<boolean>;
}

export function CodexTerminalToolbar({
  desktop,
  commands,
  windowId,
  isVM,
  vmMachineName,
  state,
  canReconnect,
  canContinue,
  continuing,
  resumePending,
  activeRun,
  metadataError,
  feedback,
  resumeRecoveryError,
  reviewing,
  onInterrupt,
  onReconnect,
  onContinue,
  onOpenRunning,
  onReview,
  onProjectFiles,
}: CodexTerminalToolbarProps) {
  return (
    <>
      <div className="app-toolbar terminal-toolbar codex-primary-toolbar">
        <div className="codex-toolbar-actions">
          <Button onClick={() => desktop.openApp('codex')}>Sessions</Button>
          <Button disabled={state.phase !== 'connected'} onClick={onInterrupt}>
            Interrupt
          </Button>
          {canReconnect && <Button onClick={onReconnect}>Reconnect</Button>}
          {resumePending ? (
            <Button disabled={!canContinue} onClick={onContinue}>
              {continuing ? 'Continuing…' : 'Resolve Continue'}
            </Button>
          ) : activeRun ? (
            <Button onClick={onOpenRunning}>Open Running Session</Button>
          ) : state.session && sessionCanContinue(state.session) ? (
            <Button disabled={!canContinue} onClick={onContinue}>
              {continuing ? 'Continuing…' : 'Continue'}
            </Button>
          ) : null}
        </div>
        <div className="codex-toolbar-status">
          {isVM && (
            <span
              className="codex-target-badge codex-target-badge--vm"
              data-testid="codex-target-badge"
            >
              VM · {vmMachineName}
            </span>
          )}
          {state.session?.archivedAt && (
            <span className="codex-archived-label">Archived</span>
          )}
          <div className="codex-terminal-activity">
            <CodexActivity
              activity={state.session?.activity}
              stale={!!metadataError}
            />
            {canReviewActivity(state.session?.activity) && (
              <Button
                disabled={reviewing}
                onClick={(event) => onReview(event.currentTarget)}
              >
                {reviewing ? 'Dismissing…' : 'Dismiss alert'}
              </Button>
            )}
          </div>
          <IconButton
            icon="more"
            label="More Codex actions"
            className="codex-more-actions"
            aria-haspopup="menu"
            onClick={(event) => {
              const rect = event.currentTarget.getBoundingClientRect();
              desktop.contextMenu(
                codexMoreMenu(commands, windowId, {
                  projectFiles: () =>
                    void runCommand(desktop, async () => {
                      await onProjectFiles();
                    }),
                  projectFilesAvailable: !!state.session,
                }),
                { x: rect.left, y: rect.bottom },
                { title: 'Codex' },
              );
            }}
          />
        </div>
      </div>
      {state.session?.activity?.reason && (
        <div className="terminal-message" role="status">
          {state.session.activity.reason}
        </div>
      )}
      {(state.message || feedback || metadataError || resumeRecoveryError) && (
        <div
          className="terminal-message"
          role={
            !feedback &&
            !metadataError &&
            !resumeRecoveryError &&
            (state.phase === 'ended' || state.phase === 'reconnecting')
              ? 'status'
              : 'alert'
          }
        >
          {[
            ...new Set(
              [
                resumeRecoveryError,
                state.message,
                feedback,
                metadataError,
              ].filter(Boolean),
            ),
          ].map((message) => (
            <div key={message}>{message}</div>
          ))}
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
    </>
  );
}
