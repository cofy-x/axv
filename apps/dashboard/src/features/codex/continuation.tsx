import { desktopDatabase } from '../../shared/api/desktop';
import { APIError } from '../../shared/api/http';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import {
  codexAPI,
  sessionCanContinue,
  sessionCanEnd,
  sessionTitle,
} from './api';
import type { CodexSessionRecord } from './api';

// These refusals follow request replay lookup and precede run reservation.
// Shutdown, transport and reconciliation failures can describe an existing run.
const preLaunchRefusals = new Map<string, readonly number[]>([
  ['codex_limit', [503]],
  ['codex_unavailable', [503]],
  ['conversation_active', [409]],
  ['run_changed', [409]],
  ['project_changed', [409]],
  ['workspace_changed', [409]],
  ['history_unavailable', [409, 503]],
]);

export function launchRejectedBeforeStart(error: unknown) {
  return (
    error instanceof APIError &&
    preLaunchRefusals.get(error.code)?.includes(error.status) === true
  );
}

interface ResumeRequest {
  requestId: string;
  expectedRunId: string;
}

interface SendingResume {
  phase: 'sending';
  attempt: ResumeRequest;
  pending: ResumeRequest;
  promise: Promise<CodexSessionRecord>;
}

type ResumeState =
  SendingResume | { phase: 'uncertain'; pending: ResumeRequest };

/** Unknown outcomes retain their request identity until a definite response. */
export class CodexResumeRequests {
  private readonly states = new Map<string, ResumeState>();

  constructor(
    _scope?: string,
    private readonly databaseId?: string,
  ) {}

  restore(session: CodexSessionRecord) {
    return this.states.get(session.id)?.pending;
  }

  hasPending(sessionId: string) {
    return this.states.has(sessionId);
  }

  pendingRequestId(sessionId: string) {
    return this.states.get(sessionId)?.pending.requestId;
  }

  private settle(sessionId: string, sending: SendingResume, confirmed = false) {
    if (this.states.get(sessionId) === sending) {
      if (confirmed && sending.pending.requestId === sending.attempt.requestId)
        this.states.delete(sessionId);
      else
        this.states.set(sessionId, {
          phase: 'uncertain',
          pending: sending.pending,
        });
    }
  }

  resume(session: CodexSessionRecord) {
    const state = this.states.get(session.id);
    if (state?.phase === 'sending') return state.promise;
    const pending = this.restore(session);
    const attempt: ResumeRequest = pending
      ? structuredClone(pending)
      : {
          requestId: crypto.randomUUID(),
          expectedRunId: session.runId,
        };
    let complete!: (
      result: CodexSessionRecord | PromiseLike<CodexSessionRecord>,
    ) => void;
    const promise = new Promise<CodexSessionRecord>((resolve) => {
      complete = resolve;
    });
    const sending: SendingResume = {
      phase: 'sending',
      attempt,
      pending: attempt,
      promise,
    };
    this.states.set(session.id, sending);
    complete(this.send(session.id, sending));
    return promise;
  }

  private async send(sessionId: string, sending: SendingResume) {
    const { attempt } = sending;
    try {
      const result = await (this.databaseId
        ? codexAPI.resume(
            sessionId,
            attempt.requestId,
            attempt.expectedRunId,
            this.databaseId,
          )
        : codexAPI.resume(sessionId, attempt.requestId, attempt.expectedRunId));
      desktopDatabase.assertUnchanged();
      if (
        result.id !== sessionId ||
        !result.runId ||
        result.runId === attempt.expectedRunId
      )
        throw new Error(
          'The host did not confirm a new run for this conversation. Retry the same continuation request.',
        );
      this.settle(sessionId, sending, true);
      return result;
    } catch (error) {
      if (this.states.get(sessionId) === sending)
        this.settle(sessionId, sending, launchRejectedBeforeStart(error));
      throw error;
    }
  }
}

export async function continueConversation(
  desktop: Pick<DesktopAPI, 'dialog'>,
  source: CodexSessionRecord,
  requests: CodexResumeRequests,
  expectedRequestId: string | null = requests.pendingRequestId(source.id) ??
    null,
) {
  // This explicit action checks the latest run; the old window stays bound to
  // its own run for reading, reconnecting, input and termination.
  const latest = await codexAPI.get(source.id);
  const pending = requests.restore(latest);
  const reviewedRequestId = pending?.requestId || null;
  const changedRequest = () =>
    new Error(
      'The Codex continuation request changed in another window. Review the current session and request before continuing or resolving it.',
    );
  if (reviewedRequestId !== expectedRequestId) throw changedRequest();
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
          <dt>Previous run</dt>
          <dd>
            <code>{pending?.expectedRunId || latest.runId}</code>
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
  if ((requests.restore(latest)?.requestId || null) !== reviewedRequestId)
    throw changedRequest();
  const result = await requests.resume(latest);
  if (!retrying && !sessionCanEnd(result))
    throw new Error(
      result.reason ||
        'The new run could not start. The previous run is unchanged; choose Continue to try again.',
    );
  return result;
}
