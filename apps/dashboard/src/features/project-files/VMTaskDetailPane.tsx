import type { RefObject } from 'react';
import { Button, Select } from '@cofy-x/axv-nextstep';
import type { VMProjectBinding } from './vm-api';
import {
  currentTaskAttempt,
  taskFinished,
  vmTaskLogPageBytes,
} from './vm-task-api';
import type { readVMTaskLog, VMTaskAttempt, VMTaskResult } from './vm-task-api';

const dateLabel = (value: string) => new Date(value).toLocaleString();

export interface VMTaskDetailPaneProps {
  resultSurfaceRef: RefObject<HTMLElement | null>;
  outputRef: RefObject<HTMLPreElement | null>;
  composing: boolean;
  interactive: boolean;
  busy: boolean;
  hasPending: boolean;
  hasRecoveryError: boolean;
  binding: VMProjectBinding | null;
  hostId: string;
  projectId: string;
  selected?: VMTaskResult;
  attempt?: VMTaskAttempt;
  commandLabel: (result: VMTaskResult) => string;
  log?: Awaited<ReturnType<typeof readVMTaskLog>>;
  logKey: string;
  logOffset: number;
  followLatest: boolean;
  pagePending: boolean;
  paginated: boolean;
  showLatest: boolean;
  pollError: string;
  onSelectAttempt: (attemptId: string) => void;
  onCancel: (attempt: VMTaskAttempt, label: string) => void;
  onRetry: (result: VMTaskResult) => void;
  onLogViewChange: (next: {
    key: string;
    offset: number;
    follow: boolean;
  }) => void;
}

export function VMTaskDetailPane({
  resultSurfaceRef,
  outputRef,
  composing,
  interactive,
  busy,
  hasPending,
  hasRecoveryError,
  binding,
  hostId,
  projectId,
  selected,
  attempt,
  commandLabel,
  log,
  logKey,
  logOffset,
  followLatest,
  pagePending,
  paginated,
  showLatest,
  pollError,
  onSelectAttempt,
  onCancel,
  onRetry,
  onLogViewChange,
}: VMTaskDetailPaneProps) {
  return (
    <section
      hidden={composing}
      aria-label="Task results"
      className="vm-task-result"
      ref={resultSurfaceRef}
      tabIndex={-1}
    >
      {selected && attempt ? (
        <>
          <h3>{commandLabel(selected)}</h3>
          <div className="vm-task-attempt-actions">
            {selected.attempts.length > 1 ? (
              <Select
                className="vm-task-attempt-select"
                aria-label="Attempt"
                value={attempt.id}
                disabled={!interactive}
                onChange={(event) => onSelectAttempt(event.target.value)}
              >
                {selected.attempts.map((item) => (
                  <option key={item.id} value={item.id}>
                    Attempt {item.number} · {item.status}
                  </option>
                ))}
              </Select>
            ) : (
              <span
                role="status"
                aria-label="Attempt status"
                className="vm-task-state"
              >
                {attempt.status} · Attempt {attempt.number}
                {attempt.exitCode !== undefined
                  ? ` · Exit ${attempt.exitCode}`
                  : ''}
              </span>
            )}

            <div className="vm-task-actions">
              {!taskFinished(attempt.status) && (
                <Button
                  disabled={
                    !interactive ||
                    busy ||
                    taskFinished(attempt.status) ||
                    attempt.cancelRequested
                  }
                  aria-label={
                    attempt.cancelRequested
                      ? 'Cancellation requested'
                      : 'Cancel Attempt'
                  }
                  onClick={() => onCancel(attempt, commandLabel(selected))}
                >
                  {attempt.cancelRequested ? 'Cancelling…' : 'Cancel'}
                </Button>
              )}
              {!selected.task.spec.initialization &&
                (taskFinished(currentTaskAttempt(selected)?.status || '') ||
                  attempt.status === 'unknown') && (
                  <Button
                    disabled={
                      !interactive ||
                      busy ||
                      hasPending ||
                      hasRecoveryError ||
                      !binding ||
                      !taskFinished(currentTaskAttempt(selected)?.status || '')
                    }
                    aria-label="Retry Task"
                    onClick={() => onRetry(selected)}
                  >
                    Retry
                  </Button>
                )}
            </div>
          </div>
          <p className="vm-task-origin" title={attempt.binding.machineId}>
            VM · {attempt.binding.machineId}
            {selected.task.spec.directory
              ? ` · ${selected.task.spec.directory}`
              : ''}
            {selected.attempts.length > 1 && attempt.exitCode !== undefined
              ? ` · Exit ${attempt.exitCode}`
              : ''}
          </p>
          {attempt.reason && <p role="status">{attempt.reason}</p>}
          {attempt.syncError && (
            <p role="alert">
              {attempt.syncError} Retained results and cached output remain
              available.
            </p>
          )}
          {attempt.status === 'unknown' && (
            <p>
              The original execution could not be verified. Reconnect to its VM
              to reconcile; no replacement execution starts automatically.
            </p>
          )}
          <pre
            ref={outputRef}
            aria-label="Task output"
            tabIndex={interactive ? 0 : -1}
          >
            {log?.text ||
              (pollError
                ? 'Task output could not be refreshed.'
                : !log
                  ? 'Loading output…'
                  : log.logBytes > log.nextOffset
                    ? 'Output is not cached yet.'
                    : taskFinished(attempt.status)
                      ? 'This attempt produced no output.'
                      : 'No output yet.')}
          </pre>
          {pagePending && (
            <p className="vm-task-output-notice" role="status">
              {pollError
                ? 'Requested page unavailable. The displayed output is retained.'
                : 'Loading requested page…'}
            </p>
          )}
          {log &&
            log.nextOffset < log.logBytes &&
            log.nextOffset >= attempt.cachedBytes && (
              <p>Cached output is still arriving. Refresh checks for more.</p>
            )}
          {attempt.logLimited && (
            <p>
              Task stopped after reaching the log limit. Retained output is
              incomplete.
            </p>
          )}
          <div className="vm-task-output-footer">
            {(paginated || showLatest) && (
              <div className="vm-task-log-navigation" aria-label="Output pages">
                <div className="vm-task-actions">
                  {paginated && (
                    <>
                      <Button
                        disabled={
                          !interactive ||
                          !log ||
                          (log.offset === 0 && !followLatest && logOffset === 0)
                        }
                        onClick={() =>
                          onLogViewChange({
                            key: logKey,
                            offset: 0,
                            follow: false,
                          })
                        }
                      >
                        First
                      </Button>
                      <Button
                        disabled={!interactive || !log || log.offset === 0}
                        onClick={() =>
                          onLogViewChange({
                            key: logKey,
                            offset: Math.max(
                              0,
                              (log?.offset || 0) - vmTaskLogPageBytes,
                            ),
                            follow: false,
                          })
                        }
                      >
                        Previous
                      </Button>
                      <Button
                        disabled={
                          !interactive ||
                          !log ||
                          log.nextOffset >= attempt.cachedBytes
                        }
                        onClick={() =>
                          onLogViewChange({
                            key: logKey,
                            offset: log?.nextOffset || 0,
                            follow: false,
                          })
                        }
                      >
                        Next
                      </Button>
                    </>
                  )}
                  {showLatest && (
                    <Button
                      disabled={!interactive}
                      aria-pressed={followLatest}
                      onClick={() =>
                        onLogViewChange({
                          key: logKey,
                          offset: log?.offset || 0,
                          follow: !followLatest,
                        })
                      }
                    >
                      Latest
                    </Button>
                  )}
                </div>
                {followLatest && (
                  <span className="visually-hidden" role="status">
                    Following latest
                  </span>
                )}
              </div>
            )}
            <details>
              <summary>Execution details</summary>
              <dl className="info-list">
                <dt>Output range</dt>
                <dd>
                  {log
                    ? `Bytes ${log.offset}–${log.nextOffset} of ${log.logBytes}`
                    : 'Loading output…'}
                </dd>
                <dt>Task / attempt</dt>
                <dd>
                  <code>
                    {selected.task.id} / {attempt.id}
                  </code>
                </dd>
                <dt>Submitted</dt>
                <dd>
                  <time dateTime={selected.task.createdAt}>
                    {dateLabel(selected.task.createdAt)}
                  </time>
                </dd>
                <dt>Started / finished</dt>
                <dd>
                  {attempt.startedAt
                    ? dateLabel(attempt.startedAt)
                    : 'Not recorded'}{' '}
                  /{' '}
                  {attempt.finishedAt
                    ? dateLabel(attempt.finishedAt)
                    : 'Not recorded'}
                </dd>
                <dt>Host / project</dt>
                <dd>
                  <code>
                    {hostId} / {projectId}
                  </code>
                </dd>
                <dt>VM / boot / guest</dt>
                <dd>
                  <code>
                    {attempt.binding.machineId} / {attempt.binding.bootId} /{' '}
                    {attempt.binding.instanceId}
                  </code>
                </dd>
                <dt>Requested limits</dt>
                <dd>
                  {selected.task.spec.limits.cpus} CPU;{' '}
                  {selected.task.spec.limits.memoryMiB} MiB memory;{' '}
                  {selected.task.spec.limits.maxProcesses} processes;{' '}
                  {selected.task.spec.limits.timeoutSeconds} seconds;{' '}
                  {selected.task.spec.limits.maxLogBytes} log bytes
                </dd>
                <dt>Additional controls</dt>
                <dd>
                  Per-task disk, I/O and network limits are unavailable. VM-wide
                  limits remain separate.
                </dd>
                <dt>Directory</dt>
                <dd>
                  <code>{selected.task.spec.directory || 'Project root'}</code>
                </dd>
              </dl>
            </details>
          </div>
        </>
      ) : selected ? (
        <>
          <p role="alert">
            The selected attempt is unavailable in the recorded history. Choose
            a recorded attempt to inspect it; no other execution was selected
            automatically.
          </p>
          <Select
            label="Attempt"
            value=""
            disabled={!interactive}
            onChange={(event) => onSelectAttempt(event.target.value)}
          >
            <option value="" disabled>
              Choose a recorded attempt
            </option>
            {selected.attempts.map((item) => (
              <option key={item.id} value={item.id}>
                Attempt {item.number} · {item.status}
              </option>
            ))}
          </Select>
        </>
      ) : (
        <p>Select a task to inspect its attempts and output.</p>
      )}
    </section>
  );
}
