import { useCallback } from 'react';
import { Button, StatusBar } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { codexAPI } from '../codex/api';
import { readVMTaskLog, vmTasksAPI } from '../project-files/vm-task-api';
import { notificationsAPI } from './api';
import { TargetIdentity } from './NotificationDetails';
import { kindLabel } from './presentation';
import { useNotificationResource } from './useNotificationResource';

export function NotificationWork({
  window,
  interactive = true,
}: AppWindowProps) {
  const storeId = window.params.storeId || '';
  const notificationId = window.params.notificationId || '';
  const read = useCallback(
    async (signal: AbortSignal) => {
      const detail = await notificationsAPI.detail(
        { storeId, notificationId },
        signal,
      );
      const target = detail.event.target;
      if (!detail.resolution.available) return { detail, unavailable: true };
      if (
        Object.keys({ ...target, ...detail.resolution.target }).some(
          (key) =>
            target[key as keyof typeof target] !==
            detail.resolution.target[key as keyof typeof target],
        )
      )
        throw new Error('Original work identity changed.');
      if (target.kind === 'codex' && target.sessionId && target.runId) {
        const session = await codexAPI.getRun(
          target.sessionId,
          target.runId,
          signal,
        );
        if (session.id !== target.sessionId || session.runId !== target.runId)
          throw new Error('Original run identity changed.');
        return { detail, session };
      }
      if (
        target.kind === 'task' &&
        target.projectId &&
        target.hostId &&
        target.taskId &&
        target.attemptId
      ) {
        const result = await vmTasksAPI.get(
          target.projectId,
          target.hostId,
          target.taskId,
          signal,
        );
        const attempt = result.attempts.find(
          (item) => item.id === target.attemptId,
        );
        if (
          result.task.id !== target.taskId ||
          result.task.hostId !== target.hostId ||
          result.task.projectId !== target.projectId ||
          !attempt
        )
          return { detail, unavailable: true };
        const log = await readVMTaskLog(
          target.projectId,
          target.hostId,
          target.taskId,
          target.attemptId,
          signal,
        );
        return { detail, attempt, log };
      }
      return { detail, unavailable: true };
    },
    [storeId, notificationId],
  );
  const resource = useNotificationResource(read, interactive);
  const value = resource.value;
  return (
    <div className="notifications-app">
      <div className="app-pad notifications-work-content">
        <div className="notification-heading">
          <h2>Original work</h2>
          <Button
            disabled={!interactive || resource.loading}
            onClick={resource.refresh}
          >
            Refresh
          </Button>
        </div>
        <p className="fine-print">
          Read-only inspection of the recorded run or task attempt.
        </p>
        {resource.error && (
          <p className="notification-error" role="alert">
            {resource.error} Retained information may be stale.
          </p>
        )}
        {!value && !resource.error && (
          <p role="status">Resolving original work…</p>
        )}
        {value && (
          <>
            <p>
              <strong>{kindLabel(value.detail.event.kind)}</strong>
            </p>
            {!value.detail.resolution.current && (
              <p className="notification-error">
                This notification is stale. Its recorded identity is preserved.
              </p>
            )}
            {value.unavailable && (
              <p role="alert">
                The original work is unavailable. No newer run or attempt will
                be opened in its place.
              </p>
            )}
            {value.session && (
              <dl className="info-list">
                <dt>Session</dt>
                <dd>{value.session.title}</dd>
                <dt>Process</dt>
                <dd>{value.session.status}</dd>
                <dt>Last inspected activity</dt>
                <dd>{value.session.activity?.state || 'Unknown'}</dd>
              </dl>
            )}
            {value.attempt && (
              <dl className="info-list">
                <dt>Attempt state</dt>
                <dd>{value.attempt.status}</dd>
                <dt>Attempt number</dt>
                <dd>{value.attempt.number}</dd>
                {value.attempt.exitCode !== undefined && (
                  <>
                    <dt>Exit code</dt>
                    <dd>{value.attempt.exitCode}</dd>
                  </>
                )}
              </dl>
            )}
            <details open>
              <summary>Recorded identity</summary>
              <TargetIdentity target={value.detail.event.target} />
            </details>
            {value.log && (
              <>
                <h3>Original attempt output</h3>
                <pre
                  className="notification-task-output"
                  aria-label="Original attempt output"
                  tabIndex={interactive ? 0 : -1}
                >
                  {value.log.text || 'No output recorded.'}
                </pre>
                {value.log.nextOffset < value.log.logBytes && (
                  <p className="fine-print">
                    Showing the first page of recorded output.
                  </p>
                )}
              </>
            )}
          </>
        )}
      </div>
      <StatusBar>Read-only · Original work identity</StatusBar>
    </div>
  );
}
