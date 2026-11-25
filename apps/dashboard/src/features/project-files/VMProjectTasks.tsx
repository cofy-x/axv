import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react';
import {
  Button,
  Field,
  IconButton,
  Select,
  StatusBar,
  TextArea,
} from '@cofy-x/axv-nextstep';
import type { VMProjectTaskProps } from './VMProjectWindows';
import { vmProjectsAPI } from './vm-api';
import type { VMProject, VMProjectBinding } from './vm-api';
import {
  currentTaskAttempt,
  readVMTaskLog,
  taskFinished,
  vmTaskLogPageBytes,
  vmTasksAPI,
} from './vm-task-api';
import type { VMTaskAttempt, VMTaskResult } from './vm-task-api';
import { createVMTaskRequests } from './vm-task-requests';

const describe = (error: unknown) =>
  error instanceof Error ? error.message : String(error);
const shellLabel = (argv: string[]) =>
  argv[0] === '/bin/sh' && argv[1] === '-lc' && argv[2] !== undefined
    ? argv[2]
    : argv.join(' ');
const commandLabel = (result: VMTaskResult) =>
  result.task.spec.initialization
    ? `${result.task.spec.initialization.repository ? 'Clone Repository' : 'Create Project'} · ${result.task.spec.initialization.directory}`
    : shellLabel(result.task.spec.argv);
const dateLabel = (value: string) => new Date(value).toLocaleString();
const timeLabel = (value: string) =>
  new Date(value).toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });

function replaceTask(items: VMTaskResult[], result: VMTaskResult) {
  return items.some((item) => item.task.id === result.task.id)
    ? items.map((item) => (item.task.id === result.task.id ? result : item))
    : [...items, result];
}

export function VMProjectTasks({
  window,
  desktop,
  interactive,
  recoveryKey,
}: VMProjectTaskProps) {
  const projectId = window.params.vmTasksProjectId!;
  const hostId = window.params.hostId || '';
  const mounted = useRef(false);
  const [error, setError] = useState('');
  const [feedback, setFeedback] = useState('');
  const [requests] = useState(() =>
    createVMTaskRequests({
      hostId,
      projectId,
      recoveryKey,
      isOpen: () => mounted.current,
      onSend: () => {
        setError('');
        setFeedback('');
      },
      onError: setError,
    }),
  );
  const { command: taskCommand, recovery } = useSyncExternalStore(
    requests.subscribe,
    requests.getSnapshot,
  );
  const pending = recovery.kind === 'pending' ? recovery.submission : null;
  const recoveryError = 'error' in recovery ? recovery.error : '';
  const action = taskCommand.phase === 'sending' ? taskCommand.kind : '';
  const busy = taskCommand.phase !== 'idle';
  const [command, setCommand] = useState(pending?.spec?.argv[2] || '');
  const [directory, setDirectory] = useState(pending?.spec?.directory || '');
  const [timeout, setTimeoutSeconds] = useState(
    pending?.spec?.limits.timeoutSeconds || 900,
  );
  const [cpus, setCPUs] = useState(pending?.spec?.limits.cpus || 1);
  const [memoryMiB, setMemoryMiB] = useState(
    pending?.spec?.limits.memoryMiB || 512,
  );
  const [maxProcesses, setMaxProcesses] = useState(
    pending?.spec?.limits.maxProcesses || 64,
  );
  const [logKiB, setLogKiB] = useState(
    (pending?.spec?.limits.maxLogBytes || 1 << 20) / 1024,
  );
  const [project, setProject] = useState<VMProject | null>(null);
  const [binding, setBinding] = useState<VMProjectBinding | null>(null);
  const [connectionError, setConnectionError] = useState('');
  const [results, setResults] = useState<VMTaskResult[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [stale, setStale] = useState(false);
  const [pollError, setPollError] = useState('');
  const [refresh, setRefresh] = useState(0);
  const [focusRequest, setFocusRequest] = useState(0);
  const [logs, setLogs] = useState<
    Record<string, Awaited<ReturnType<typeof readVMTaskLog>>>
  >({});
  const activePoll = useRef<AbortController | null>(null);
  const connectionGeneration = useRef(0);
  const resultSurface = useRef<HTMLElement>(null);
  const focusedTask = useRef('');
  const selectedId =
    typeof window.viewState.taskId === 'string' ? window.viewState.taskId : '';
  const [composing, setComposing] = useState(!selectedId);
  const [browsing, setBrowsing] = useState(false);
  const focusState = useRef({ interactive, composing, browsing });
  focusState.current = { interactive, composing, browsing };
  const [logView, setLogView] = useState({ key: '', offset: 0, follow: false });
  const composer = useRef<HTMLFormElement>(null);
  const executionOptions = useRef<HTMLDetailsElement>(null);
  const taskList = useRef<HTMLElement>(null);
  const output = useRef<HTMLPreElement>(null);
  const displayedPage = useRef('');
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
  const logKey = `${selectedId}:${attempt?.id || ''}`;
  const logOffset = logView.key === logKey ? logView.offset : 0;
  const followLatest = logView.key === logKey && logView.follow;
  const log = logs[logKey];
  const pagePending =
    !!log && !followLatest && log.requestedOffset !== logOffset;
  const paginated =
    !!log &&
    (log.logBytes > vmTaskLogPageBytes || log.offset > 0 || logOffset > 0);
  const showLatest =
    !!attempt && (!taskFinished(attempt.status) || paginated || followLatest);
  const customOptions =
    !!directory ||
    timeout !== 900 ||
    cpus !== 1 ||
    memoryMiB !== 512 ||
    maxProcesses !== 64 ||
    logKiB !== 1024;

  function browse() {
    setComposing(false);
    setBrowsing(true);
    setFocusRequest(0);
    requestAnimationFrame(() => {
      if (
        mounted.current &&
        focusState.current.interactive &&
        focusState.current.browsing &&
        !focusState.current.composing
      )
        taskList.current?.focus({ preventScroll: true });
    });
  }

  function openFiles() {
    if (mounted.current)
      desktop.openApp(
        'project-files',
        { hostId, vmProjectId: projectId },
        { title: `VM · ${project?.name || 'Project'}` },
      );
  }

  useEffect(() => {
    if (!log) return;
    const page = `${logKey}:${log.offset}:${followLatest}`;
    if (followLatest) output.current?.scrollTo(0, output.current.scrollHeight);
    else if (displayedPage.current !== page) output.current?.scrollTo(0, 0);
    displayedPage.current = page;
  }, [followLatest, log, logKey]);

  useEffect(() => {
    const key = `${selectedId}:${focusRequest}`;
    if (
      interactive &&
      focusRequest > 0 &&
      selectedId &&
      hasSelectedResult &&
      !composing &&
      !browsing &&
      focusedTask.current !== key
    ) {
      focusedTask.current = key;
      resultSurface.current?.focus({ preventScroll: true });
    }
  }, [
    interactive,
    selectedId,
    focusRequest,
    hasSelectedResult,
    composing,
    browsing,
  ]);

  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  async function connect() {
    const current = requests.getSnapshot();
    if (
      !mounted.current ||
      current.command.phase !== 'idle' ||
      current.recovery.kind !== 'empty'
    )
      return;
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
        setFeedback('VM binding updated. Review the target before running.');
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
    if (!interactive || !hostId) return;
    const controller = new AbortController();
    activePoll.current = controller;
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
          items = replaceTask(items, detail);
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
              followLatest
                ? Math.max(0, target.cachedBytes - vmTaskLogPageBytes)
                : logOffset,
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
      if (activePoll.current === controller) activePoll.current = null;
      controller.abort();
      globalThis.clearTimeout(timer);
    };
  }, [
    projectId,
    hostId,
    selectedId,
    attemptId,
    interactive,
    refresh,
    logOffset,
    followLatest,
  ]);

  function recordResult(result: VMTaskResult) {
    activePoll.current?.abort();
    setResults((current) => replaceTask(current, result));
    setRefresh((value) => value + 1);
  }

  function show(result: VMTaskResult) {
    setFocusRequest((value) => value + 1);
    setComposing(false);
    setBrowsing(false);
    recordResult(result);
    desktop.updateWindowState(window.id, {
      taskId: result.task.id,
      attemptId: result.attempt.id,
    });
  }

  function accepted(result: VMTaskResult) {
    show(result);
    setFeedback(
      taskFinished(result.attempt.status)
        ? `Attempt ${result.attempt.number} · ${result.attempt.status}`
        : 'Task accepted.',
    );
  }

  function submit() {
    if (
      !binding ||
      !command.trim() ||
      pending ||
      recoveryError ||
      busy ||
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
      if (executionOptions.current) executionOptions.current.open = true;
      setError(
        'Check task limits: timeout 1–86400 seconds, CPU 1–128, memory 64–1048576 MiB, processes 2–256, and logs 1–8192 KiB.',
      );
      return;
    }
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
    void requests.submit({ binding: { ...binding }, spec }, accepted);
  }

  function retry(result: VMTaskResult) {
    const target = binding;
    if (
      !target ||
      pending ||
      recoveryError ||
      busy ||
      !interactive ||
      !taskFinished(currentTaskAttempt(result)?.status || '')
    )
      return;
    const label = commandLabel(result);
    void requests.retry(
      { taskId: result.task.id, binding: { ...target } },
      async () => {
        const answer = await desktop.dialog({
          title: 'Retry task?',
          message: `Run “${label}” again in VM “${target.machineId}”? This creates a new attempt and preserves previous results.`,
          buttons: [
            { label: 'Cancel', value: 'cancel', cancel: true },
            { label: 'Retry', value: 'retry', default: true },
          ],
        });
        return answer?.button === 'retry';
      },
      accepted,
    );
  }

  function cancel(target: VMTaskAttempt, label: string) {
    if (!interactive || target.cancelRequested || taskFinished(target.status))
      return;
    void requests.cancel(
      { ...target, binding: { ...target.binding } },
      async () => {
        const answer = await desktop.dialog({
          title: `Cancel attempt ${target.number}?`,
          message: `Stop “${label}” (attempt ${target.number}) and its child processes in its original VM (${target.binding.machineId})? Other tasks continue.`,
          buttons: [
            { label: 'Keep Running', value: 'keep', cancel: true },
            { label: 'Cancel Attempt', value: 'cancel', default: true },
          ],
        });
        return answer?.button === 'cancel';
      },
      (result) => {
        recordResult(result);
        setFeedback(
          taskFinished(result.attempt.status)
            ? `Attempt ${target.number} · ${result.attempt.status}`
            : `Cancellation requested for “${label}” (attempt ${target.number}).`,
        );
      },
    );
  }

  function dismissRecovery(raw: string, corrupt: boolean) {
    if (!interactive) return;
    void requests.dismiss(
      raw,
      async () => {
        const answer = await desktop.dialog({
          title: corrupt
            ? 'Discard submission recovery?'
            : 'Dismiss submission recovery?',
          message: corrupt
            ? 'Preserve the browser record first. A task may already have been accepted. Review task history before starting it again.'
            : 'A task may already have been accepted. Dismissing this prompt keeps any running work active. Review task history before changing the command or VM.',
          buttons: [
            {
              label: corrupt ? 'Cancel' : 'Keep Recovery',
              value: 'keep',
              cancel: true,
            },
            { label: corrupt ? 'Discard' : 'Dismiss', value: 'dismiss' },
          ],
        });
        return answer?.button === 'dismiss';
      },
      () => setError(''),
    );
  }

  return (
    <div className="vm-project-tasks">
      <div className="app-toolbar">
        {(composing || (selectedId && !browsing)) && (
          <IconButton
            className="vm-task-back"
            icon="back"
            label="Back to Tasks"
            disabled={!interactive}
            onClick={browse}
          />
        )}
        <Button
          primary={composing}
          aria-expanded={composing}
          disabled={!interactive}
          onClick={() => {
            setFocusRequest(0);
            setComposing((value) => !value);
            if (!composing)
              requestAnimationFrame(() => {
                if (
                  mounted.current &&
                  focusState.current.interactive &&
                  focusState.current.composing
                )
                  composer.current?.querySelector('textarea')?.focus();
              });
          }}
        >
          New Task
        </Button>
        <IconButton
          icon="refresh"
          label="Refresh"
          disabled={!interactive}
          onClick={() => setRefresh((value) => value + 1)}
        />
        <IconButton
          icon="more"
          label="More task actions"
          aria-haspopup="menu"
          className="vm-task-more"
          disabled={!interactive}
          onClick={(event) => {
            const rect = event.currentTarget.getBoundingClientRect();
            desktop.contextMenu(
              [
                { label: 'Project Files', action: openFiles },
                {
                  label: 'Reconnect VM',
                  disabled: busy || !!pending || !!recoveryError,
                  action: () => void connect(),
                },
                { separator: true },
                {
                  label: 'Task Help',
                  action: () => {
                    if (mounted.current)
                      void desktop.dialog({
                        title: 'Task Help',
                        message: (
                          <>
                            <p>
                              Commands use /bin/sh -lc inside the selected VM
                              and project directory. Save file edits before
                              running.
                            </p>
                            <p>
                              Accepted tasks continue after this window or
                              browser closes. Cancel stops only the displayed
                              attempt in its original VM. Retry creates a new
                              attempt after a final outcome.
                            </p>
                            <p>
                              Execution options control timeout, CPU, memory,
                              processes and log capacity. Unsupported
                              enforcement rejects a task; reaching the log limit
                              stops it. Per-task disk, I/O and network controls
                              are unavailable.
                            </p>
                          </>
                        ),
                        buttons: [
                          {
                            label: 'Close',
                            value: 'close',
                            cancel: true,
                            default: true,
                          },
                        ],
                      });
                  },
                },
              ],
              { x: rect.left, y: rect.bottom },
              { title: 'Task actions' },
            );
          }}
        />
      </div>
      <div className="vm-task-notices">
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
        {connectionError && (
          <Button
            disabled={!interactive || busy || !!pending || !!recoveryError}
            onClick={() => void connect()}
          >
            Reconnect VM
          </Button>
        )}
        {recovery.kind === 'unavailable' && (
          <Button
            disabled={!interactive || busy}
            onClick={requests.readRecovery}
          >
            Check Recovery
          </Button>
        )}
        {recovery.kind === 'invalid' && (
          <Button
            disabled={!interactive || busy}
            onClick={() => dismissRecovery(recovery.raw, true)}
          >
            Discard Recovery
          </Button>
        )}
        {recovery.kind === 'pending' && action !== 'submit' && (
          <section aria-label="Unconfirmed task submission">
            <p>Recover the original submission and its VM binding.</p>
            <div className="app-actions">
              <Button
                disabled={!interactive || busy}
                onClick={() => {
                  if (!interactive) return;
                  void requests.recover(recovery, (result) => {
                    show(result);
                    setFeedback(
                      `Recovered attempt ${result.attempt.number} · ${result.attempt.status}`,
                    );
                  });
                }}
              >
                {action === 'recover' ? 'Recovering…' : 'Recover Submission'}
              </Button>
              <Button
                disabled={!interactive || busy}
                onClick={() => dismissRecovery(recovery.raw, false)}
              >
                Dismiss Recovery
              </Button>
            </div>
            <details>
              <summary>Submission details</summary>
              <dl className="info-list">
                <dt>VM</dt>
                <dd>
                  <code>{recovery.submission.binding.machineId}</code>
                </dd>
                <dt>Request ID</dt>
                <dd>
                  <code>{recovery.submission.requestId}</code>
                </dd>
                {recovery.submission.taskId ? (
                  <>
                    <dt>Task</dt>
                    <dd>
                      <code>{recovery.submission.taskId}</code>
                    </dd>
                  </>
                ) : (
                  <>
                    <dt>Command</dt>
                    <dd>
                      <code>
                        {shellLabel(recovery.submission.spec?.argv ?? [])}
                      </code>
                    </dd>
                  </>
                )}
              </dl>
            </details>
          </section>
        )}
      </div>
      <div
        className={`vm-task-workspace${composing ? ' is-composing' : ''}${selectedId && !browsing ? ' is-reading' : ''}`}
      >
        <nav
          aria-label="Project tasks"
          className="vm-task-list"
          ref={taskList}
          tabIndex={-1}
        >
          <h2>Task history</h2>
          {!loaded && !pollError && <p role="status">Loading tasks…</p>}
          {loaded && !results.length && <p>No project tasks yet.</p>}
          {results.map((result) => {
            const current = currentTaskAttempt(result);
            return (
              <button
                key={result.task.id}
                title={commandLabel(result)}
                disabled={!interactive}
                aria-current={
                  selectedId === result.task.id ? 'true' : undefined
                }
                onClick={() => {
                  setFocusRequest((value) => value + 1);
                  setComposing(false);
                  setBrowsing(false);
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
                <small>
                  <time dateTime={result.task.createdAt}>
                    {timeLabel(result.task.createdAt)}
                  </time>{' '}
                  · {result.task.id.slice(-8)}
                </small>
              </button>
            );
          })}
        </nav>
        <form
          className="vm-task-form"
          aria-label="New task"
          noValidate
          hidden={!composing}
          ref={composer}
          onFocusCapture={() => setFocusRequest(0)}
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <p className="vm-task-target" title={binding?.machineId}>
            <strong>
              {binding ? `VM · ${binding.machineId}` : 'No VM available'}
            </strong>
          </p>
          <TextArea
            label="Shell command"
            value={command}
            rows={2}
            spellCheck={false}
            disabled={!interactive || busy || !!pending || !!recoveryError}
            onChange={(event) => setCommand(event.target.value)}
          />
          <details className="vm-task-options" ref={executionOptions}>
            <summary>
              <span>Execution options</span>
              {customOptions ? ' · Customized' : ''}
            </summary>
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
            </div>
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
                onChange={(event) =>
                  setMaxProcesses(Number(event.target.value))
                }
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
                  Unsupported guest enforcement rejects the task. Reaching the
                  log limit stops that attempt.
                </dd>
                <dt>Additional controls</dt>
                <dd>
                  Per-task disk, I/O and network controls are unavailable.
                </dd>
              </dl>
            </details>
          </details>
          <div className="vm-task-compose-actions">
            <Button
              type="submit"
              primary
              aria-label={action === 'submit' ? 'Submitting…' : 'Run in VM'}
              disabled={
                !interactive ||
                busy ||
                !binding ||
                !command.trim() ||
                !!pending ||
                !!recoveryError
              }
            >
              {action === 'submit' ? 'Submitting…' : 'Run'}
            </Button>
          </div>
        </form>
        <section
          hidden={composing}
          aria-label="Task results"
          className="vm-task-result"
          ref={resultSurface}
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
                      onClick={() =>
                        void cancel(attempt, commandLabel(selected))
                      }
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
                          !!pending ||
                          !!recoveryError ||
                          !binding ||
                          !taskFinished(
                            currentTaskAttempt(selected)?.status || '',
                          )
                        }
                        aria-label="Retry Task"
                        onClick={() => void retry(selected)}
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
                  The original execution could not be verified. Reconnect to its
                  VM to reconcile; no replacement execution starts
                  automatically.
                </p>
              )}
              <pre
                ref={output}
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
                  <p>
                    Cached output is still arriving. Refresh checks for more.
                  </p>
                )}
              {attempt.logLimited && (
                <p>
                  Task stopped after reaching the log limit. Retained output is
                  incomplete.
                </p>
              )}
              <div className="vm-task-output-footer">
                {(paginated || showLatest) && (
                  <div
                    className="vm-task-log-navigation"
                    aria-label="Output pages"
                  >
                    <div className="vm-task-actions">
                      {paginated && (
                        <>
                          <Button
                            disabled={
                              !interactive ||
                              !log ||
                              (log.offset === 0 &&
                                !followLatest &&
                                logOffset === 0)
                            }
                            onClick={() =>
                              setLogView({
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
                              setLogView({
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
                              setLogView({
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
                            setLogView({
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
              </div>
            </>
          ) : selected ? (
            <>
              <p role="alert">
                The selected attempt is unavailable in the recorded history.
                Choose a recorded attempt to inspect it; no other execution was
                selected automatically.
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
      <StatusBar>
        <span title={stale ? pollError : feedback}>
          {stale
            ? 'Task state unavailable'
            : feedback ||
              (loaded
                ? `${results.length} project ${results.length === 1 ? 'task' : 'tasks'}`
                : 'Loading tasks…')}
        </span>
      </StatusBar>
    </div>
  );
}
