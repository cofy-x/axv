import { APIError } from '../../shared/api/http';
import { RequestIdentity } from '../../shared/api/request-identity';
import { sameProjectBinding } from './vm-init-api';
import { validVMTaskSubmission, vmTasksAPI } from './vm-task-api';
import type {
  VMTaskAttempt,
  VMTaskResult,
  VMTaskSubmission,
} from './vm-task-api';

type TaskIntent = Omit<VMTaskSubmission, 'requestId'>;

type Confirm = () => Promise<boolean>;

type PendingSubmission = {
  kind: 'pending';
  raw: string;
  submission: VMTaskSubmission;
};

type RecoveryRecord =
  | { kind: 'empty'; raw: null }
  | PendingSubmission
  | { kind: 'invalid'; raw: string; error: string }
  | { kind: 'unavailable'; error: string };

type ActiveCommand =
  | { phase: 'confirming'; kind: 'retry' | 'cancel' | 'dismiss' }
  | { phase: 'sending'; kind: 'submit' | 'recover' | 'cancel' };

type Command = { phase: 'idle' } | ActiveCommand;

interface Options {
  hostId: string;
  projectId: string;
  recoveryKey: string;
  isOpen: () => boolean;
  onSend: () => void;
  onError: (message: string) => void;
}

const describe = (error: unknown) =>
  error instanceof Error ? error.message : String(error);

function checkReceipt(result: VMTaskResult, submission: VMTaskSubmission) {
  const { task, attempt } = result;
  const { binding, requestId, spec, taskId } = submission;
  if (
    !task?.id ||
    !attempt?.id ||
    task.hostId !== binding.hostId ||
    task.projectId !== binding.projectId ||
    attempt.taskId !== task.id ||
    attempt.requestId !== requestId ||
    !sameProjectBinding(attempt.binding, binding) ||
    (taskId ? task.id !== taskId : task.requestId !== requestId) ||
    (spec &&
      (task.spec?.initialization ||
        task.spec?.directory !== spec.directory ||
        task.spec?.argv?.length !== spec.argv.length ||
        spec.argv.some((arg, index) => task.spec.argv[index] !== arg) ||
        Object.entries(spec.limits).some(
          ([key, limit]) =>
            task.spec.limits?.[key as keyof typeof spec.limits] !== limit,
        )))
  )
    throw new Error(
      'The host returned a receipt for a different task request.',
    );
}

/** Owns one view's confirmations and requests, independently of its saved receipt. */
export function createVMTaskRequests(
  options: Options,
  api: Pick<typeof vmTasksAPI, 'submit' | 'recover' | 'cancel'> = vmTasksAPI,
  storage: () => Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> = () =>
    globalThis.sessionStorage,
) {
  const { hostId, projectId, recoveryKey, isOpen, onSend, onError } = options;
  const empty = { kind: 'empty', raw: null } as const;
  let state: { command: Command; recovery: RecoveryRecord } = {
    command: { phase: 'idle' },
    recovery: readRecovery(),
  };
  const subscribers = new Set<() => void>();

  function readRecovery(): RecoveryRecord {
    let raw: string | null;
    try {
      raw = storage().getItem(recoveryKey);
    } catch (error) {
      return { kind: 'unavailable', error: describe(error) };
    }
    if (raw === null) return empty;
    try {
      const submission: unknown = JSON.parse(raw);
      if (validVMTaskSubmission(submission, hostId, projectId))
        return { kind: 'pending', raw, submission };
    } catch {
      // Keep the original bytes for an explicit discard.
    }
    return {
      kind: 'invalid',
      raw,
      error:
        'The saved task submission could not be read. Keep its browser record before discarding recovery.',
    };
  }

  function publish(patch: Partial<typeof state>) {
    state = { ...state, ...patch };
    for (const subscriber of subscribers) subscriber();
  }

  function requireRecovery(expected: string | null) {
    const current = readRecovery();
    if (current.kind === 'unavailable') throw new Error(current.error);
    if (current.raw === expected) return;
    publish({ recovery: current });
    throw new Error(
      'The task recovery record changed. Review the current record before continuing.',
    );
  }

  function replaceRecovery(
    expected: string | null,
    next: PendingSubmission | typeof empty,
  ) {
    requireRecovery(expected);
    if (next.raw === null) storage().removeItem(recoveryKey);
    else storage().setItem(recoveryKey, next.raw);
    publish({ recovery: next });
  }

  async function run(
    initial: ActiveCommand,
    work: (send: (kind: 'submit' | 'cancel') => boolean) => Promise<void>,
  ) {
    if (!isOpen() || state.command.phase !== 'idle') return;
    let owner = initial;
    const current = () => state.command === owner;
    publish({ command: owner });
    try {
      if (!isOpen()) return;
      if (initial.phase === 'sending') onSend();
      if (!isOpen()) return;
      await work((kind) => {
        if (!isOpen()) return false;
        owner = { phase: 'sending', kind };
        publish({ command: owner });
        if (isOpen()) onSend();
        return isOpen();
      });
    } catch (error) {
      if (isOpen()) onError(describe(error));
    } finally {
      if (current()) publish({ command: { phase: 'idle' } });
    }
  }

  async function sendStored(
    record: PendingSubmission,
    recover: boolean,
    accepted: (result: VMTaskResult) => void,
    identities: RequestIdentity,
  ) {
    const { submission, raw } = record;
    requireRecovery(raw);
    let result: VMTaskResult;
    if (recover) {
      try {
        result = await api.recover(projectId, hostId, submission.requestId);
      } catch (error) {
        if (
          !(error instanceof APIError) ||
          error.status !== 404 ||
          error.code !== 'not_found'
        )
          throw error;
        if (!isOpen()) return;
        requireRecovery(raw);
        result = await api.submit(projectId, submission);
      }
    } else result = await api.submit(projectId, submission);
    checkReceipt(result, submission);
    if (!isOpen()) return;
    let warning = '';
    try {
      replaceRecovery(raw, empty);
      if (!identities.complete(submission.requestId))
        warning =
          'Task accepted, but its local retry identity could not be cleared. Review task history before submitting the same command again.';
    } catch (error) {
      warning = `Task accepted. Its browser recovery record could not be cleared. ${describe(error)}`;
    }
    accepted(result);
    if (warning) onError(warning);
  }

  function identities() {
    return new RequestIdentity(recoveryKey, storage());
  }

  async function submit(
    values: TaskIntent,
    accepted: (result: VMTaskResult) => void,
  ) {
    let record: PendingSubmission;
    let requests: RequestIdentity;
    try {
      requireRecovery(null);
      requests = identities();
      const submission = { ...values, requestId: requests.for(values) };
      record = { kind: 'pending', raw: JSON.stringify(submission), submission };
      replaceRecovery(null, record);
    } catch (error) {
      throw new Error(
        `The task was not submitted because its recovery record could not be saved. ${describe(error)}`,
        { cause: error },
      );
    }
    if (isOpen()) await sendStored(record, false, accepted, requests);
  }

  return {
    getSnapshot: () => state,
    subscribe(subscriber: () => void) {
      subscribers.add(subscriber);
      return () => subscribers.delete(subscriber);
    },
    readRecovery() {
      if (isOpen() && state.command.phase === 'idle')
        publish({ recovery: readRecovery() });
    },
    submit(values: TaskIntent, accepted: (result: VMTaskResult) => void) {
      return run({ phase: 'sending', kind: 'submit' }, () =>
        submit(values, accepted),
      );
    },
    retry(
      values: TaskIntent,
      confirm: Confirm,
      accepted: (result: VMTaskResult) => void,
    ) {
      return run({ phase: 'confirming', kind: 'retry' }, async (send) => {
        requireRecovery(null);
        if (!(await confirm()) || !send('submit')) return;
        await submit(values, accepted);
      });
    },
    recover(
      record: PendingSubmission,
      accepted: (result: VMTaskResult) => void,
    ) {
      return run({ phase: 'sending', kind: 'recover' }, () =>
        sendStored(record, true, accepted, identities()),
      );
    },
    cancel(
      target: VMTaskAttempt,
      confirm: Confirm,
      accepted: (result: VMTaskResult) => void,
    ) {
      return run({ phase: 'confirming', kind: 'cancel' }, async (send) => {
        if (!(await confirm()) || !send('cancel')) return;
        const result = await api.cancel(projectId, target);
        const { task, attempt } = result;
        if (
          task?.id !== target.taskId ||
          task.hostId !== hostId ||
          task.projectId !== projectId ||
          attempt?.id !== target.id ||
          attempt.taskId !== target.taskId ||
          attempt.requestId !== target.requestId ||
          !sameProjectBinding(attempt.binding, target.binding)
        )
          throw new Error(
            'The host returned a cancellation receipt for a different attempt.',
          );
        if (isOpen()) accepted(result);
      });
    },
    dismiss(raw: string, confirm: Confirm, dismissed: () => void) {
      return run({ phase: 'confirming', kind: 'dismiss' }, async () => {
        if (!(await confirm()) || !isOpen()) return;
        replaceRecovery(raw, empty);
        dismissed();
      });
    },
  };
}
