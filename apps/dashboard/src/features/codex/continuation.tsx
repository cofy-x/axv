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

/** Unknown outcomes retain their request identity until a definite response. */
export class CodexResumeRequests {
  private pending = new Map<string, ResumeRequest>();
  private inFlight = new Map<string, Promise<CodexSessionRecord>>();
  private persisted = new Map<string, VMProjectRequests>();

  constructor(
    private readonly scope?: string,
    private readonly databaseId?: string,
  ) {}

  restore(session: CodexSessionRecord) {
    if (!session.vmProject || !this.scope) return this.pending.get(session.id);
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
      this.pending.set(session.id, {
        requestId: saved.requestId,
        expectedRunId: saved.expectedRunId,
        vmProject: { binding: saved.binding },
      });
    } else if (!this.inFlight.has(session.id)) {
      // Other mounted views can resolve and remove the shared receipt. Keep
      // only this instance's active submission when persistence is now empty.
      this.pending.delete(session.id);
    }
    return this.pending.get(session.id);
  }

  hasPending(sessionId: string) {
    return this.pending.has(sessionId);
  }

  pendingRequestId(sessionId: string) {
    return this.pending.get(sessionId)?.requestId;
  }

  private complete(sessionId: string, requestId: string) {
    this.persisted.get(sessionId)?.complete(requestId);
    if (this.pending.get(sessionId)?.requestId === requestId)
      this.pending.delete(sessionId);
  }

  resume(session: CodexSessionRecord, binding?: VMProjectBinding) {
    const active = this.inFlight.get(session.id);
    if (active) return active;
    const pending = this.restore(session);
    const saved =
      !pending && session.vmProject && binding
        ? this.persisted.get(session.id)?.capture(binding, session.runId)
        : undefined;
    if (session.vmProject && !pending?.vmProject && !binding)
      throw new Error('Review the VM project environment before continuing.');
    const attempt = pending || {
      requestId: saved?.requestId || crypto.randomUUID(),
      expectedRunId: saved?.expectedRunId || session.runId,
      ...(session.vmProject && binding
        ? { vmProject: { binding: saved?.binding || binding } }
        : {}),
    };
    this.pending.set(session.id, attempt);
    const submission = attempt.vmProject
      ? this.databaseId
        ? codexAPI.resume(
            session.id,
            attempt.requestId,
            attempt.expectedRunId,
            attempt.vmProject,
            this.databaseId,
          )
        : codexAPI.resume(
            session.id,
            attempt.requestId,
            attempt.expectedRunId,
            attempt.vmProject,
          )
      : codexAPI.resume(session.id, attempt.requestId, attempt.expectedRunId);
    const request = submission
      .then((result) => {
        desktopDatabase.assertUnchanged();
        if (
          result.id !== session.id ||
          !result.runId ||
          result.runId === attempt.expectedRunId
        )
          throw new Error(
            'The host did not confirm a new run for this conversation. Retry the same continuation request.',
          );
        this.complete(session.id, attempt.requestId);
        return result;
      })
      .catch((error: unknown) => {
        if (launchRejectedBeforeStart(error)) {
          this.complete(session.id, attempt.requestId);
        }
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
