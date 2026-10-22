import type { VMProjectBinding, VMProjectContent } from './vm-api';

export interface VMFileObservation {
  projectId: string;
  binding: VMProjectBinding;
  path: string;
  navigation: number;
  input: number;
  save: number;
  sha256: string;
  dirty: boolean;
  acceptBlocked: boolean;
}

export function vmFileBindingKey(binding: VMProjectBinding) {
  return JSON.stringify([
    binding.hostId,
    binding.projectId,
    binding.projectRevision,
    binding.volumeId,
    binding.volumeRevision,
    binding.machineId,
    binding.bootId,
    binding.instanceId,
  ]);
}

export function vmFileTargetKey(
  projectId: string,
  binding: VMProjectBinding,
  path: string,
) {
  return JSON.stringify([projectId, vmFileBindingKey(binding), path]);
}

export function sameVMFileObservation(
  captured: VMFileObservation,
  current: VMFileObservation | null,
) {
  return (
    !!current &&
    vmFileTargetKey(captured.projectId, captured.binding, captured.path) ===
      vmFileTargetKey(current.projectId, current.binding, current.path) &&
    captured.navigation === current.navigation &&
    captured.input === current.input &&
    captured.save === current.save &&
    captured.sha256 === current.sha256
  );
}

export function vmFileObservationDecision(
  captured: VMFileObservation,
  current: VMFileObservation | null,
  value: VMProjectContent,
): 'ignore' | 'same' | 'changed' | 'accept' {
  if (!sameVMFileObservation(captured, current)) return 'ignore';
  if (
    value.path !== captured.path ||
    value.project.id !== captured.projectId ||
    vmFileBindingKey(value.binding) !== vmFileBindingKey(captured.binding)
  )
    throw new Error('The file check returned another file or VM environment.');
  if (value.sha256 === current!.sha256) return 'same';
  return current!.dirty || current!.acceptBlocked ? 'changed' : 'accept';
}

interface ObserverOptions {
  snapshot: () => VMFileObservation | null;
  read: (
    captured: VMFileObservation,
    signal: AbortSignal,
  ) => Promise<VMProjectContent>;
  receive: (
    value: VMProjectContent,
    decision: 'same' | 'changed' | 'accept',
    captured: VMFileObservation,
  ) => void;
  fail: (error: unknown, captured: VMFileObservation) => void;
}

// One in-flight read per mounted observer. Visibility/input transitions can
// abort a read; epochs still reject replies from transports ignoring abort.
export function createVMFileObserver(options: ObserverOptions) {
  let closed = false;
  let visible = true;
  let pending = false;
  let controller: AbortController | null = null;
  let inFlight: VMFileObservation | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;

  function schedule() {
    clearTimeout(timer);
    if (!closed && visible) timer = setTimeout(() => void run(), 2000);
  }

  async function run() {
    if (closed || !visible) return;
    const captured = options.snapshot();
    if (!captured) {
      // A public editor/input gate can change without a React notification.
      // Keep a bounded local retry while visible; permanent stops dispose us.
      schedule();
      return;
    }
    const request = new AbortController();
    controller = request;
    inFlight = captured;
    try {
      const value = await options.read(captured, request.signal);
      if (closed || request.signal.aborted) return;
      const decision = vmFileObservationDecision(
        captured,
        options.snapshot(),
        value,
      );
      if (decision !== 'ignore') options.receive(value, decision, captured);
    } catch (error) {
      if (
        !closed &&
        !request.signal.aborted &&
        sameVMFileObservation(captured, options.snapshot())
      )
        options.fail(error, captured);
    } finally {
      controller = null;
      inFlight = null;
      if (!closed && visible) {
        if (pending) {
          pending = false;
          void run();
        } else schedule();
      }
    }
  }

  function check() {
    if (closed || !visible) return;
    clearTimeout(timer);
    if (controller) {
      const next = options.snapshot();
      if (!next) {
        pending = false;
        controller.abort();
      } else if (
        controller.signal.aborted ||
        !inFlight ||
        !sameVMFileObservation(inFlight, next)
      )
        pending = true;
    } else void run();
  }

  return {
    check,
    setVisible(next: boolean) {
      if (closed || visible === next) return;
      visible = next;
      if (visible) check();
      else {
        clearTimeout(timer);
        pending = false;
        controller?.abort();
      }
    },
    stop() {
      closed = true;
      pending = false;
      clearTimeout(timer);
      controller?.abort();
    },
  };
}

export function vmFileBindingError(code?: string) {
  return (
    !!code &&
    [
      'binding_changed',
      'boot_changed',
      'instance_changed',
      'guest_identity_mismatch',
      'project_changed',
    ].includes(code)
  );
}
