import { useEffect, useRef, useState } from 'react';
import {
  Button,
  Field,
  Select,
  StatusBar,
  TextArea,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { APIError } from '../../shared/api/http';
import { RequestIdentity } from '../../shared/api/request-identity';
import { useDraftNamespace } from '../../shared/drafts/context';
import { vmProjectsAPI } from './vm-api';
import type { VMProject, VMProjectBinding } from './vm-api';
import {
  currentTaskAttempt,
  readVMTaskLog,
  taskFinished,
  validVMTaskSubmission,
  vmTasksAPI,
} from './vm-task-api';
import type {
  VMTaskAttempt,
  VMTaskResult,
  VMTaskSubmission,
} from './vm-task-api';

const describe = (error: unknown) =>
  error instanceof Error ? error.message : String(error);
const commandLabel = (result: VMTaskResult) =>
  result.task.spec.argv.slice(0, 2).join(' ') === '/bin/sh -lc'
    ? result.task.spec.argv[2]
    : result.task.spec.argv.join(' ');

export function VMProjectTasks({
  window,
  desktop,
  interactive,
}: AppWindowProps) {
  const projectId = window.params.vmTasksProjectId!;
  const hostId = window.params.hostId || '';
  const namespace = useDraftNamespace();
  const recoveryKey = JSON.stringify([
    'axv-vm-task-submission',
    namespace.databaseId,
    namespace.clientId,
    hostId,
    projectId,
  ]);
  const requests = useRef(new RequestIdentity(recoveryKey));
  const [recovery] = useState(() => {
    try {
      const raw = sessionStorage.getItem(recoveryKey);
      const value = raw === null ? null : (JSON.parse(raw) as VMTaskSubmission);
      if (raw !== null && !validVMTaskSubmission(value, hostId, projectId))
        throw new Error(
          'The saved task submission could not be read. Keep its browser record before discarding recovery.',
        );
      return { value, error: '' };
    } catch (error) {
      return { value: null, error: describe(error) };
    }
  });
  const [pending, setPending] = useState(recovery.value);
  const [recoveryError, setRecoveryError] = useState(recovery.error);
  const [command, setCommand] = useState(recovery.value?.spec?.argv[2] || '');
  const [directory, setDirectory] = useState(
    recovery.value?.spec?.directory || '',
  );
  const [timeout, setTimeoutSeconds] = useState(
    recovery.value?.spec?.limits.timeoutSeconds || 900,
  );
  const [cpus, setCPUs] = useState(recovery.value?.spec?.limits.cpus || 1);
  const [memoryMiB, setMemoryMiB] = useState(
    recovery.value?.spec?.limits.memoryMiB || 512,
  );
  const [maxProcesses, setMaxProcesses] = useState(
    recovery.value?.spec?.limits.maxProcesses || 64,
  );
  const [logKiB, setLogKiB] = useState(
    (recovery.value?.spec?.limits.maxLogBytes || 1 << 20) / 1024,
  );
  const [project, setProject] = useState<VMProject | null>(null);
  const [binding, setBinding] = useState<VMProjectBinding | null>(null);
  const [connectionError, setConnectionError] = useState('');
  const [results, setResults] = useState<VMTaskResult[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [stale, setStale] = useState(false);
  const [pollError, setPollError] = useState('');
  const [error, setError] = useState('');
  const [feedback, setFeedback] = useState('');
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [focusRequest, setFocusRequest] = useState(0);
  const [logs, setLogs] = useState<
    Record<string, Awaited<ReturnType<typeof readVMTaskLog>>>
  >({});
  const mounted = useRef(false);
  const mutation = useRef(false);
  const connectionGeneration = useRef(0);
  const resultSurface = useRef<HTMLElement>(null);
  const focusedTask = useRef('');
  const selectedId =
    typeof window.viewState.taskId === 'string' ? window.viewState.taskId : '';
  const selected = results.find((result) => result.task.id === selectedId);
  const hasSelectedResult = !!selected;
  const attemptId =
    typeof window.viewState.attemptId === 'string'
      ? window.viewState.attemptId
      : '';
  const attempt = selected
    ? attemptId
      ? selected.attempts.find((item) => item.id === attemptId)
      : currentTaskAttempt(selected)
    : undefined;
  const log = attempt && logs[`${selectedId}:${attempt.id}`];

  useEffect(() => {
    const key = `${selectedId}:${focusRequest}`;
    if (
      interactive &&
      focusRequest > 0 &&
      selectedId &&
      hasSelectedResult &&
      focusedTask.current !== key
    ) {
      focusedTask.current = key;
      resultSurface.current?.focus({ preventScroll: true });
      resultSurface.current?.scrollIntoView({ block: 'start' });
    }
  }, [interactive, selectedId, focusRequest, hasSelectedResult]);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  async function connect() {
    const generation = ++connectionGeneration.current;
    setConnectionError('');
    setBinding(null);
    try {
      if (!hostId)
        throw new Error(
          'This project has no recorded host. Reopen it from Projects.',
        );
      const value = await vmProjectsAPI.binding(projectId, hostId);
      if (mounted.current && generation === connectionGeneration.current) {
        setProject(value.project);
        setBinding(value.binding);
        setFeedback(
          'VM connection updated. Review Execution environment before running another task.',
        );
      }
    } catch (error) {
      if (mounted.current && generation === connectionGeneration.current) {
        setBinding(null);
        setConnectionError(describe(error));
      }
    }
  }

  useEffect(() => {
    let current = true;
    const generation = ++connectionGeneration.current;
    void (async () => {
      try {
        if (!hostId)
          throw new Error(
            'This project has no recorded host. Reopen it from Projects.',
          );
        const value = await vmProjectsAPI.binding(projectId, hostId);
        if (current && generation === connectionGeneration.current) {
          setProject(value.project);
          setBinding(value.binding);
        }
      } catch (error) {
        if (current && generation === connectionGeneration.current)
          setConnectionError(describe(error));
      }
    })();
    return () => {
      current = false;
    };
  }, [projectId, hostId]);

  useEffect(() => {
    const prevent = (event: BeforeUnloadEvent) => {
      if (pending || recoveryError) {
        event.preventDefault();
        event.returnValue = '';
      }
    };
    globalThis.addEventListener('beforeunload', prevent);
    return () => globalThis.removeEventListener('beforeunload', prevent);
  }, [pending, recoveryError]);

  useEffect(() => {
    if (!interactive || !hostId) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof globalThis.setTimeout>;

    async function poll() {
      try {
        const value = await vmTasksAPI.list(
          projectId,
          hostId,
          controller.signal,
        );
        let items = value.tasks;
        let detail: VMTaskResult | undefined;
        if (selectedId) {
          detail = await vmTasksAPI.get(
            projectId,
            hostId,
            selectedId,
            controller.signal,
          );
          items = [
            ...items.filter((item) => item.task.id !== selectedId),
            detail,
          ];
        }
        if (controller.signal.aborted) return;
        setResults(items);
        setLoaded(true);
        if (detail) {
          const target = attemptId
            ? detail.attempts.find((item) => item.id === attemptId)
            : currentTaskAttempt(detail);
          if (target) {
            const value = await readVMTaskLog(
              projectId,
              hostId,
              selectedId,
              target.id,
              controller.signal,
            );
            if (controller.signal.aborted) return;
            setLogs((current) => ({
              ...current,
              [`${selectedId}:${target.id}`]: value,
            }));
          }
        }
        setPollError('');
        setStale(false);
      } catch (error) {
        if (!controller.signal.aborted) {
          setPollError(describe(error));
          setStale(true);
        }
      } finally {
        if (!controller.signal.aborted)
          timer = globalThis.setTimeout(() => void poll(), 2000);
      }
    }

    void poll();
    return () => {
      controller.abort();
      globalThis.clearTimeout(timer);
    };
  }, [projectId, hostId, selectedId, attemptId, interactive, refresh]);

  function show(result: VMTaskResult) {
    setFocusRequest((value) => value + 1);
    setResults((current) => [
      ...current.filter((item) => item.task.id !== result.task.id),
      result,
    ]);
    desktop.updateWindowState(window.id, {
      taskId: result.task.id,
      attemptId: result.attempt.id,
    });
    setRefresh((value) => value + 1);
  }

  async function send(submission: VMTaskSubmission, recover = false) {
    if (mutation.current || !interactive) return;
    mutation.current = true;
    setBusy(true);
    setError('');
    setFeedback('');
    let stored = false;
    try {
      sessionStorage.setItem(recoveryKey, JSON.stringify(submission));
      stored = true;
      setPending(submission);
      let result: VMTaskResult;
      if (recover) {
        try {
          result = await vmTasksAPI.recover(
            projectId,
            hostId,
            submission.requestId,
          );
        } catch (error) {
          if (!(error instanceof APIError) || error.status !== 404) throw error;
          result = await vmTasksAPI.submit(projectId, submission);
        }
      } else result = await vmTasksAPI.submit(projectId, submission);
      if (!mounted.current) return;
      show(result);
      sessionStorage.removeItem(recoveryKey);
      setPending(null);
      if (!requests.current.complete(submission.requestId))
        setError(
          'Task accepted, but its local retry identity could not be cleared. Review task history before submitting the same command again.',
        );
      setFeedback('Task accepted in VM. Closing this window does not stop it.');
    } catch (error) {
      if (mounted.current)
        setError(
          stored
            ? `${describe(error)} Recover the original submission before starting another task.`
            : `The task was not submitted because its recovery record could not be saved. ${describe(error)}`,
        );
    } finally {
      mutation.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  function submit() {
    if (
      !binding ||
      !command.trim() ||
      pending ||
      recoveryError ||
      mutation.current ||
      !interactive
    )
      return;
    if (
      !Number.isInteger(timeout) ||
      timeout < 1 ||
      timeout > 86400 ||
      !Number.isInteger(cpus) ||
      cpus < 1 ||
      cpus > 128 ||
      !Number.isInteger(memoryMiB) ||
      memoryMiB < 64 ||
      memoryMiB > 1048576 ||
      !Number.isInteger(maxProcesses) ||
      maxProcesses < 2 ||
      maxProcesses > 256 ||
      !Number.isInteger(logKiB) ||
      logKiB < 1 ||
      logKiB > 8192
    ) {
      setError(
        'Check task limits: timeout 1–86400 seconds, CPU 1–128, memory 64–1048576 MiB, processes 2–256, and logs 1–8192 KiB.',
      );
      return;
    }
    try {
      const spec = {
        argv: ['/bin/sh', '-lc', command],
        directory,
        limits: {
          timeoutSeconds: timeout,
          cpus,
          memoryMiB,
          maxProcesses,
          maxLogBytes: logKiB * 1024,
        },
      };
      const values = { binding, spec };
      void send({ ...values, requestId: requests.current.for(values) });
    } catch (error) {
      setError(describe(error));
    }
  }

  async function retry(result: VMTaskResult) {
    const target = binding;
    if (
      !target ||
      pending ||
      busy ||
      !interactive ||
      !taskFinished(currentTaskAttempt(result)?.status || '')
    )
      return;
    const answer = await desktop.dialog({
      title: 'Retry task?',
      message: `Run “${commandLabel(result)}” again in the connected VM? This creates a new attempt and preserves previous results.`,
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Retry', value: 'retry', default: true },
      ],
    });
    if (answer?.button !== 'retry' || !mounted.current) return;
    try {
      const values = { taskId: result.task.id, binding: target };
      await send({ ...values, requestId: requests.current.for(values) });
    } catch (error) {
      setError(describe(error));
    }
  }

  async function cancel(target: VMTaskAttempt) {
    if (mutation.current || !interactive) return;
    const answer = await desktop.dialog({
      title: `Cancel attempt ${target.number}?`,
      message:
        'Stop only this execution and its child processes in its original VM. Other tasks continue.',
      buttons: [
        { label: 'Keep Running', value: 'keep', cancel: true },
        { label: 'Cancel Attempt', value: 'cancel', default: true },
      ],
    });
    if (answer?.button !== 'cancel' || !mounted.current) return;
    mutation.current = true;
    setBusy(true);
    setError('');
    try {
      await vmTasksAPI.cancel(projectId, target);
      if (mounted.current) {
        setFeedback(`Cancellation requested for attempt ${target.number}.`);
        setRefresh((value) => value + 1);
      }
    } catch (error) {
      if (mounted.current) setError(describe(error));
    } finally {
      mutation.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  return (
    <div className="vm-project-tasks">
      <div className="app-toolbar">
        <Button
          disabled={!interactive}
          onClick={() => setRefresh((value) => value + 1)}
        >
          Refresh
        </Button>
        <Button
          disabled={!interactive || busy || !!pending}
          onClick={() => void connect()}
        >
          Reconnect VM
        </Button>
        <Button
          disabled={!interactive}
          onClick={() =>
            desktop.openApp(
              'project-files',
              { hostId, vmProjectId: projectId },
              { title: `VM · ${project?.name || 'Project'}` },
            )
          }
        >
          Project Files
        </Button>
      </div>
      <div className="vm-task-scroll">
        <h2>{project?.name || 'VM project'} · Tasks</h2>
        <p>
          Commands run inside the connected VM. Accepted tasks continue after
          this window or browser closes.
        </p>
        {(error || pollError || connectionError || recoveryError) && (
          <p className="project-error" role="alert">
            {[error, pollError, connectionError, recoveryError]
              .filter(Boolean)
              .join(' ')}
          </p>
        )}
        {stale && (
          <p role="status">
            Last known task data. The current state could not be checked.
          </p>
        )}
        {feedback && <p role="status">{feedback}</p>}
        {recoveryError && (
          <Button
            disabled={!interactive}
            onClick={() =>
              void (async () => {
                const answer = await desktop.dialog({
                  title: 'Discard submission recovery?',
                  message:
                    'Preserve the browser record first. A task may already have been accepted; review task history before starting it again.',
                  buttons: [
                    { label: 'Cancel', value: 'cancel', cancel: true },
                    { label: 'Discard', value: 'discard' },
                  ],
                });
                if (answer?.button === 'discard')
                  try {
                    sessionStorage.removeItem(recoveryKey);
                    setRecoveryError('');
                  } catch (error) {
                    setError(describe(error));
                  }
              })()
            }
          >
            Discard Recovery
          </Button>
        )}
        {pending && (
          <section aria-label="Unconfirmed task submission">
            <p>
              A submission has no confirmed receipt in this tab. Recover it with
              its original request and VM binding.
            </p>
            <Button
              disabled={!interactive || busy}
              onClick={() => void send(pending, true)}
            >
              {busy ? 'Recovering…' : 'Recover Submission'}
            </Button>
            <Button
              disabled={!interactive || busy}
              onClick={() =>
                void (async () => {
                  const answer = await desktop.dialog({
                    title: 'Dismiss submission recovery?',
                    message:
                      'A task may already have been accepted. This only dismisses this tab’s recovery prompt; it does not cancel work. Review task history before changing the command or VM.',
                    buttons: [
                      { label: 'Keep Recovery', value: 'keep', cancel: true },
                      { label: 'Dismiss', value: 'dismiss' },
                    ],
                  });
                  if (answer?.button === 'dismiss')
                    try {
                      sessionStorage.removeItem(recoveryKey);
                      setPending(null);
                      setError('');
                    } catch (error) {
                      setError(describe(error));
                    }
                })()
              }
            >
              Dismiss Recovery
            </Button>
          </section>
        )}
        <details>
          <summary>Execution environment</summary>
          <dl className="info-list">
            <dt>Host / project</dt>
            <dd>
              <code>
                {hostId} / {projectId}
              </code>
            </dd>
            <dt>VM / volume</dt>
            <dd>
              <code>
                {binding
                  ? `${binding.machineId} / ${binding.volumeId}`
                  : 'Unavailable'}
              </code>
            </dd>
            <dt>Boot / guest</dt>
            <dd>
              <code>
                {binding
                  ? `${binding.bootId} / ${binding.instanceId}`
                  : 'Unavailable'}
              </code>
            </dd>
            <dt>New task limits</dt>
            <dd>
              Unsupported guest enforcement rejects the task. Reaching the log
              limit stops that attempt.
            </dd>
            <dt>Additional controls</dt>
            <dd>Per-task disk, I/O and network controls are unavailable.</dd>
          </dl>
          <div className="vm-task-limit-fields">
            <Field
              label="CPU limit"
              type="number"
              min={1}
              max={128}
              value={cpus}
              disabled={!interactive || busy || !!pending || !!recoveryError}
              onChange={(event) => setCPUs(Number(event.target.value))}
            />
            <Field
              label="Memory limit (MiB)"
              type="number"
              min={64}
              max={1048576}
              value={memoryMiB}
              disabled={!interactive || busy || !!pending || !!recoveryError}
              onChange={(event) => setMemoryMiB(Number(event.target.value))}
            />
            <Field
              label="Process limit"
              type="number"
              min={2}
              max={256}
              value={maxProcesses}
              disabled={!interactive || busy || !!pending || !!recoveryError}
              onChange={(event) => setMaxProcesses(Number(event.target.value))}
            />
            <Field
              label="Log limit (KiB)"
              type="number"
              min={1}
              max={8192}
              value={logKiB}
              disabled={!interactive || busy || !!pending || !!recoveryError}
              onChange={(event) => setLogKiB(Number(event.target.value))}
            />
          </div>
        </details>
        <form
          className="vm-task-form"
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <TextArea
            label="Shell command"
            value={command}
            rows={2}
            spellCheck={false}
            disabled={!interactive || busy || !!pending || !!recoveryError}
            onChange={(event) => setCommand(event.target.value)}
          />
          <p className="fine-print">
            Uses /bin/sh -lc in the project directory. Save file edits before
            running a command.
          </p>
          <div className="vm-task-fields">
            <Field
              label="Directory in project"
              value={directory}
              disabled={!interactive || busy || !!pending || !!recoveryError}
              onChange={(event) => setDirectory(event.target.value)}
              placeholder="Project root"
            />
            <Field
              label="Timeout (seconds)"
              type="number"
              min={1}
              max={86400}
              value={timeout}
              disabled={!interactive || busy || !!pending || !!recoveryError}
              onChange={(event) =>
                setTimeoutSeconds(Number(event.target.value))
              }
            />
            <Button
              type="submit"
              primary
              disabled={
                !interactive ||
                busy ||
                !binding ||
                !command.trim() ||
                !!pending ||
                !!recoveryError
              }
            >
              {busy ? 'Submitting…' : 'Run in VM'}
            </Button>
          </div>
        </form>
        <div className="vm-task-workspace">
          <nav aria-label="Project tasks" className="vm-task-list">
            {!loaded && !pollError && <p role="status">Loading tasks…</p>}
            {loaded && !results.length && <p>No project tasks yet.</p>}
            {results.map((result) => {
              const current = currentTaskAttempt(result);
              return (
                <button
                  key={result.task.id}
                  disabled={!interactive}
                  aria-current={
                    selectedId === result.task.id ? 'true' : undefined
                  }
                  onClick={() => {
                    setFocusRequest((value) => value + 1);
                    desktop.updateWindowState(window.id, {
                      taskId: result.task.id,
                      attemptId: result.task.currentAttemptId,
                    });
                  }}
                >
                  <strong>{commandLabel(result)}</strong>
                  <span>
                    {current
                      ? `${current.status} · Attempt ${current.number}`
                      : 'Current attempt unavailable'}
                  </span>
                </button>
              );
            })}
          </nav>
          <section
            aria-label="Task results"
            className="vm-task-result"
            ref={resultSurface}
            tabIndex={-1}
          >
            {selected && attempt ? (
              <>
                <h3>{commandLabel(selected)}</h3>
                <p>
                  <strong>{attempt.status}</strong> · Attempt {attempt.number}
                  {attempt.exitCode !== undefined
                    ? ` · Exit ${attempt.exitCode}`
                    : ''}
                </p>
                {attempt.reason && <p role="status">{attempt.reason}</p>}
                {attempt.syncError && (
                  <p role="alert">
                    {attempt.syncError} Retained results and cached output
                    remain available.
                  </p>
                )}
                {attempt.status === 'unknown' && (
                  <p>
                    The original execution could not be verified. Reconnect to
                    its VM to reconcile; no replacement execution starts
                    automatically.
                  </p>
                )}
                <Select
                  label="Attempt"
                  aria-label="Attempt"
                  value={attempt.id}
                  disabled={!interactive}
                  onChange={(event) =>
                    desktop.updateWindowState(window.id, {
                      attemptId: event.target.value,
                    })
                  }
                >
                  {selected.attempts.map((item) => (
                    <option key={item.id} value={item.id}>
                      Attempt {item.number} · {item.status}
                    </option>
                  ))}
                </Select>
                <div className="app-toolbar">
                  <Button
                    disabled={
                      !interactive ||
                      busy ||
                      taskFinished(attempt.status) ||
                      attempt.cancelRequested
                    }
                    onClick={() => void cancel(attempt)}
                  >
                    {attempt.cancelRequested
                      ? 'Cancellation requested'
                      : 'Cancel Attempt'}
                  </Button>
                  <Button
                    disabled={
                      !interactive ||
                      busy ||
                      !!pending ||
                      !!recoveryError ||
                      !binding ||
                      !taskFinished(currentTaskAttempt(selected)?.status || '')
                    }
                    onClick={() => void retry(selected)}
                  >
                    Retry Task
                  </Button>
                </div>
                <pre aria-label="Task output" tabIndex={interactive ? 0 : -1}>
                  {log?.text ||
                    (pollError
                      ? 'Task output could not be refreshed.'
                      : 'No output yet.')}
                </pre>
                {log && log.displayedBytes < log.logBytes && (
                  <p>
                    Showing {log.displayedBytes} of {log.logBytes} log bytes.
                    Cached output may still be arriving; the viewer displays at
                    most the first 128 KiB.
                  </p>
                )}
                {attempt.logLimited && (
                  <p>
                    Task stopped after reaching the log limit. Retained output
                    is incomplete.
                  </p>
                )}
                <details>
                  <summary>Execution details</summary>
                  <dl className="info-list">
                    <dt>Task / attempt</dt>
                    <dd>
                      <code>
                        {selected.task.id} / {attempt.id}
                      </code>
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
                      Per-task disk, I/O and network limits are unavailable.
                      VM-wide limits remain separate.
                    </dd>
                    <dt>Directory</dt>
                    <dd>
                      <code>
                        {selected.task.spec.directory || 'Project root'}
                      </code>
                    </dd>
                  </dl>
                </details>
              </>
            ) : selected ? (
              <>
                <p role="alert">
                  The selected attempt is unavailable in the recorded history.
                  Choose a recorded attempt to inspect it; no other execution
                  was selected automatically.
                </p>
                <Select
                  label="Attempt"
                  value=""
                  disabled={!interactive}
                  onChange={(event) =>
                    desktop.updateWindowState(window.id, {
                      attemptId: event.target.value,
                    })
                  }
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
        </div>
      </div>
      <StatusBar>
        {stale ? 'Task state unavailable' : 'VM project tasks'}
        {binding ? ' · VM connected' : ' · VM unavailable'}
      </StatusBar>
    </div>
  );
}
