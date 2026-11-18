import { desktopDatabase } from '../../shared/api/desktop';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import {
  codexAPI,
  sessionCanContinue,
  sessionCanEnd,
  sessionTitle,
} from './api';
import type { CodexSessionRecord } from './api';
import { vmProjectsAPI } from '../project-files/vm-api';
import type { VMProjectBinding } from '../project-files/vm-api';
import {
  launchRejectedBeforeStart,
  VMProjectRequests,
} from './vm-project-requests';

interface ResumeRequest {
  requestId: string;
  expectedRunId: string;
  vmProject?: { binding: VMProjectBinding };
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
  private readonly persisted = new Map<string, VMProjectRequests>();

  constructor(
    private readonly scope?: string,
    private readonly databaseId?: string,
  ) {}

  restore(session: CodexSessionRecord) {
    if (!session.vmProject || !this.scope)
      return this.states.get(session.id)?.pending;
    let store = this.persisted.get(session.id);
    if (!store) {
      store = new VMProjectRequests(
        JSON.stringify(['axv-codex-vm-resume', this.scope, session.id]),
        session.vmProject.project.hostId,
        session.vmProject.project.id,
      );
      this.persisted.set(session.id, store);
    }
    const saved = store.read();
    if (saved) {
      if (!saved.expectedRunId)
        throw new Error(
          'The saved Codex continuation request has no original run. Preserve its recovery data before clearing it.',
        );
      const pending: ResumeRequest = {
        requestId: saved.requestId,
        expectedRunId: saved.expectedRunId,
        vmProject: { binding: saved.binding },
      };
      const state = this.states.get(session.id);
      if (state?.phase === 'sending') {
        // A newer shared receipt changes recovery, never the request in flight.
        state.pending = pending;
      } else this.states.set(session.id, { phase: 'uncertain', pending });
    } else if (this.states.get(session.id)?.phase !== 'sending') {
      // Other mounted views can resolve and remove the shared receipt. Keep
      // only this instance's active submission when persistence is now empty.
      this.states.delete(session.id);
    }
    return this.states.get(session.id)?.pending;
  }

  hasPending(sessionId: string) {
    return this.states.has(sessionId);
  }

  pendingRequestId(sessionId: string) {
    return this.states.get(sessionId)?.pending.requestId;
  }

  private settle(sessionId: string, sending: SendingResume, confirmed = false) {
    let released = false;
    try {
      if (confirmed) {
        this.persisted.get(sessionId)?.complete(sending.attempt.requestId);
        released = true;
      }
    } finally {
      if (this.states.get(sessionId) === sending) {
        if (released && sending.pending.requestId === sending.attempt.requestId)
          this.states.delete(sessionId);
        else
          this.states.set(sessionId, {
            phase: 'uncertain',
            pending: sending.pending,
          });
      }
    }
  }

  resume(session: CodexSessionRecord, binding?: VMProjectBinding) {
    const state = this.states.get(session.id);
    if (state?.phase === 'sending') return state.promise;
    const pending = this.restore(session);
    const saved =
      !pending && session.vmProject && binding
        ? this.persisted.get(session.id)?.capture(binding, session.runId)
        : undefined;
    if (session.vmProject && !pending?.vmProject && !binding)
      throw new Error('Review the VM project environment before continuing.');
    const attempt: ResumeRequest = pending
      ? structuredClone(pending)
      : {
          requestId: saved?.requestId || crypto.randomUUID(),
          expectedRunId: saved?.expectedRunId || session.runId,
          ...(session.vmProject && binding
            ? { vmProject: { binding: { ...(saved?.binding || binding) } } }
            : {}),
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
      const result = await (attempt.vmProject
        ? this.databaseId
          ? codexAPI.resume(
              sessionId,
              attempt.requestId,
              attempt.expectedRunId,
              attempt.vmProject,
              this.databaseId,
            )
          : codexAPI.resume(
              sessionId,
              attempt.requestId,
              attempt.expectedRunId,
              attempt.vmProject,
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
  const environment = latest.vmProject
    ? pending?.vmProject
      ? {
          project: latest.vmProject.project,
          binding: pending.vmProject.binding,
        }
      : await vmProjectsAPI.binding(
          latest.vmProject.project.id,
          latest.vmProject.project.hostId,
        )
    : undefined;
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
            <code>
              {latest.vmProject
                ? `VM · ${latest.vmProject.project.name}`
                : latest.projectPath}
            </code>
          </dd>
          {environment && (
            <>
              <dt>
                {retrying ? 'Original requested VM' : 'VM for the new run'}
              </dt>
              <dd>
                <code>{environment.binding.machineId}</code>
              </dd>
              <dt>{retrying ? 'Latest observed VM' : 'Previous run VM'}</dt>
              <dd>
                <code>{latest.vmProject!.binding.machineId}</code>
              </dd>
              <dt>Volume / directory</dt>
              <dd>
                <code>
                  {environment.binding.volumeId} /{' '}
                  {environment.project.root || 'Volume root'}
                </code>
              </dd>
              <dt>Boot / instance</dt>
              <dd>
                <code>
                  {environment.binding.bootId} /{' '}
                  {environment.binding.instanceId}
                </code>
              </dd>
            </>
          )}
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
        {environment && (
          <p>
            Codex runs on the host and uses the AXV Skill for this reviewed VM
            environment. Existing runs and pending tasks keep their original
            targets.
          </p>
        )}
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
  const result = await requests.resume(latest, environment?.binding);
  if (!retrying && !sessionCanEnd(result))
    throw new Error(
      result.reason ||
        'The new run could not start. The previous run is unchanged; choose Continue to try again.',
    );
  return result;
}
