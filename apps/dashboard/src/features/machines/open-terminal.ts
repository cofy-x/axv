import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { GuestTerminal, Machine } from './api';
import { machinesApi } from './api';
import { isDefiniteOperationRejection } from './operation-requests';

interface TerminalAttempt {
  machineId: string;
  hostId: string;
  requestId: string;
}

type SendingTerminal = {
  phase: 'sending';
  attempt: TerminalAttempt;
  promise: Promise<GuestTerminal>;
};

type TerminalRequestState =
  SendingTerminal | { phase: 'uncertain'; attempt: TerminalAttempt };

/** Preserve a creation request while its outcome is uncertain. */
export class GuestTerminalRequests {
  private readonly states = new Map<string, TerminalRequestState>();

  execute(machineId: string, hostId: string) {
    const key = JSON.stringify([hostId, machineId]);
    const state = this.states.get(key);
    if (state?.phase === 'sending') return state.promise;
    const attempt = state?.attempt || {
      machineId,
      hostId,
      requestId: crypto.randomUUID(),
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
    const { machineId, hostId, requestId } = sending.attempt;
    try {
      const result = await machinesApi.createTerminal(
        machineId,
        requestId,
        hostId,
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
    { title: `${machine.spec.name} — Terminal` },
  );
}
