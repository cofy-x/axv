import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { GuestTerminal, Machine } from './api';
import { machinesApi } from './api';
import { isDefiniteOperationRejection } from './operation-requests';

/** Preserve a creation request while its outcome is uncertain. */
export class GuestTerminalRequests {
  private readonly pending = new Map<string, string>();
  private readonly inFlight = new Map<string, Promise<GuestTerminal>>();

  execute(machineId: string, hostId: string) {
    const key = JSON.stringify([hostId, machineId]);
    const active = this.inFlight.get(key);
    if (active) return active;
    const requestId = this.pending.get(key) || crypto.randomUUID();
    this.pending.set(key, requestId);
    const request = machinesApi
      .createTerminal(machineId, requestId, hostId)
      .then((result) => {
        this.pending.delete(key);
        return result;
      })
      .catch((error: unknown) => {
        if (isDefiniteOperationRejection(error)) this.pending.delete(key);
        throw error;
      })
      .finally(() => this.inFlight.delete(key));
    this.inFlight.set(key, request);
    return request;
  }
}

export const guestTerminalRequests = new GuestTerminalRequests();

export function openGuestTerminal(
  desktop: DesktopAPI,
  machine: Machine,
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
