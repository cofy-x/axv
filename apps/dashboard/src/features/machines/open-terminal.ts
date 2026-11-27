import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { GuestTerminal, Machine } from './api';
import { machinesApi } from './api';
import { computeStore } from './compute-store';
import { activeVMOperation, machineActionRequests } from './lifecycle';
import { isDefiniteOperationRejection } from './operation-requests';
import { guestTerminalReason } from './presentation';

interface TerminalAttempt {
  machineId: string;
  hostId: string;
  requestId: string;
  directory?: string;
}

type SendingTerminal = {
  phase: 'sending';
  attempt: TerminalAttempt;
  promise: Promise<GuestTerminal>;
};

type TerminalRequestState =
  SendingTerminal | { phase: 'uncertain'; attempt: TerminalAttempt };

export function currentGuestTerminalTarget({
  id,
  hostId,
}: Pick<Machine, 'id' | 'hostId'>) {
  const latest = computeStore.getSnapshot();
  if (latest.error || latest.info?.hostId !== hostId) return;
  const target = latest.machines.find(
    (item) => item.id === id && item.hostId === hostId,
  );
  if (
    !target ||
    guestTerminalReason(target) ||
    activeVMOperation(target, latest.operations) ||
    machineActionRequests.getPending(hostId, id)
  )
    return;
  return target;
}

/** Preserve a creation request while its outcome is uncertain. */
export class GuestTerminalRequests {
  private readonly states = new Map<string, TerminalRequestState>();

  retryPending(machineId: string, hostId: string, directory?: string) {
    if (this.states.has(JSON.stringify([hostId, machineId, directory || ''])))
      return this.execute(machineId, hostId, directory);
  }

  execute(machineId: string, hostId: string, directory?: string) {
    const key = JSON.stringify([hostId, machineId, directory || '']);
    const state = this.states.get(key);
    if (state?.phase === 'sending') return state.promise;
    const attempt = state?.attempt || {
      machineId,
      hostId,
      requestId: crypto.randomUUID(),
      ...(directory ? { directory } : {}),
    };
    let complete!: (result: GuestTerminal | PromiseLike<GuestTerminal>) => void;
    const promise = new Promise<GuestTerminal>((resolve) => {
      complete = resolve;
    });
    const sending: SendingTerminal = { phase: 'sending', attempt, promise };
    this.states.set(key, sending);
    complete(this.send(key, sending));
    return promise;
  }

  private async send(key: string, sending: SendingTerminal) {
    const { machineId, hostId, requestId, directory } = sending.attempt;
    try {
      const result = await machinesApi.createTerminal(
        machineId,
        requestId,
        hostId,
        directory,
      );
      if (this.states.get(key) === sending) this.states.delete(key);
      return result;
    } catch (error) {
      if (this.states.get(key) === sending) {
        if (isDefiniteOperationRejection(error)) this.states.delete(key);
        else
          this.states.set(key, {
            phase: 'uncertain',
            attempt: sending.attempt,
          });
      }
      throw error;
    }
  }
}

export const guestTerminalRequests = new GuestTerminalRequests();

export function guestTerminalLabel(session: GuestTerminal) {
  const { id, sequence } = session;
  const reference =
    sequence && sequence > 0 && Number.isSafeInteger(sequence)
      ? sequence
      : id.slice(-12);
  return `Terminal ${reference}`;
}

export function openGuestTerminal(
  desktop: DesktopAPI,
  machine: Pick<Machine, 'id' | 'hostId'> & {
    spec: Pick<Machine['spec'], 'name'>;
  },
  session: GuestTerminal,
) {
  return desktop.openApp(
    'guest-terminal',
    {
      hostId: machine.hostId,
      machineId: machine.id,
      terminalId: session.id,
      bootId: session.bootId,
    },
    { title: `${machine.spec.name} — ${guestTerminalLabel(session)}` },
  );
}
