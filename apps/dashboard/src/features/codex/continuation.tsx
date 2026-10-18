import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { APIError } from '../../shared/api/http';
import {
  codexAPI,
  sessionCanContinue,
  sessionCanEnd,
  sessionTitle,
} from './api';
import type { CodexSessionRecord } from './api';

interface ResumeRequest {
  requestId: string;
  expectedRunId: string;
}

const rejectedBeforeStart = new Set([
  'codex_limit',
  'codex_unavailable',
  'project_unavailable',
  'server_stopping',
]);

/** Unknown outcomes retain their request identity until a definite response. */
export class CodexResumeRequests {
  private pending = new Map<string, ResumeRequest>();
  private inFlight = new Map<string, Promise<CodexSessionRecord>>();

  hasPending(sessionId: string) {
    return this.pending.has(sessionId);
  }

  resume(session: CodexSessionRecord) {
    const active = this.inFlight.get(session.id);
    if (active) return active;
    const attempt = this.pending.get(session.id) || {
      requestId: crypto.randomUUID(),
      expectedRunId: session.runId,
    };
    this.pending.set(session.id, attempt);
    const request = codexAPI
      .resume(session.id, attempt.requestId, attempt.expectedRunId)
      .then((result) => {
        if (
          result.id !== session.id ||
          !result.runId ||
          result.runId === attempt.expectedRunId
        )
          throw new Error(
            'The host did not confirm a new run for this conversation. Retry the same continuation request.',
          );
        this.pending.delete(session.id);
        return result;
      })
      .catch((error: unknown) => {
        if (
          error instanceof APIError &&
          (error.status < 500 || rejectedBeforeStart.has(error.code))
        )
          this.pending.delete(session.id);
        throw error;
      })
      .finally(() => this.inFlight.delete(session.id));
    this.inFlight.set(session.id, request);
    return request;
  }
}

export async function continueConversation(
  desktop: Pick<DesktopAPI, 'dialog'>,
  source: CodexSessionRecord,
  requests: CodexResumeRequests,
) {
  // This explicit action checks the latest run; the old window stays bound to
  // its own run for reading, reconnecting, input and termination.
  const latest = await codexAPI.get(source.id);
  if (sessionCanEnd(latest) && !requests.hasPending(source.id)) return latest;
  if (!sessionCanContinue(latest) && !requests.hasPending(source.id))
    throw new Error(
      latest.continuation.reason || 'This conversation cannot be continued.',
    );
  const retrying = requests.hasPending(source.id);
  const answer = await desktop.dialog({
    title: 'Continue Conversation?',
    message: (
      <div className="codex-session-details">
        <p>
          {retrying
            ? 'Retry the previous continuation request. AXV will resolve that request before starting another run.'
            : 'Start a new Codex process with the same saved history. The previous run is not restarted.'}
        </p>
        <dl className="info-list">
          <dt>Name</dt>
          <dd>{sessionTitle(latest)}</dd>
          <dt>Project</dt>
          <dd>
            <code>{latest.projectPath}</code>
          </dd>
          <dt>{retrying ? 'Latest run started' : 'Previous run started'}</dt>
          <dd>{new Date(latest.createdAt).toLocaleString()}</dd>
          <dt>Conversation</dt>
          <dd>
            <code>{latest.conversationId || 'Not identified'}</code>
          </dd>
        </dl>
        {latest.archivedAt && <p>This conversation will remain in Archived.</p>}
      </div>
    ),
    buttons: [
      { label: 'Cancel', value: 'cancel', cancel: true },
      { label: 'Continue', value: 'continue', default: true },
    ],
  });
  if (answer?.button !== 'continue') return null;
  const result = await requests.resume(latest);
  if (!sessionCanEnd(result))
    throw new Error(
      result.reason ||
        'The new run could not start. The previous run is unchanged; choose Continue to try again.',
    );
  return result;
}
