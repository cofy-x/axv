import { afterEach, describe, expect, it, vi } from 'vitest';
import { APIError } from '../../shared/api/http';
import { machinesApi } from './api';
import type { GuestTerminal } from './api';
import { GuestTerminalRequests, openGuestTerminal } from './open-terminal';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { Machine } from './api';

const terminal: GuestTerminal = {
  id: 'terminal-original',
  bootId: 'boot-original',
  status: 'running',
};

afterEach(() => vi.restoreAllMocks());

describe('guest terminal creation ownership', () => {
  it('coalesces a repeated click and preserves the request after an uncertain response', async () => {
    let reject!: (error: Error) => void;
    const create = vi
      .spyOn(machinesApi, 'createTerminal')
      .mockImplementationOnce(
        () =>
          new Promise((_resolve, fail) => {
            reject = fail;
          }),
      )
      .mockResolvedValue(terminal);
    const requests = new GuestTerminalRequests();
    const first = requests.execute('vm-one', 'host-original');
    expect(requests.execute('vm-one', 'host-original')).toBe(first);
    const failure = expect(first).rejects.toThrow('Response lost');
    reject(new TypeError('Response lost'));
    await failure;
    expect(await requests.execute('vm-one', 'host-original')).toBe(terminal);
    expect(create.mock.calls[1]).toEqual(create.mock.calls[0]);
    await requests.execute('vm-one', 'host-original');
    expect(create.mock.calls[2][1]).not.toBe(create.mock.calls[0][1]);
  });

  it('clears a definite rejection while isolating requests for different VMs', async () => {
    const create = vi
      .spyOn(machinesApi, 'createTerminal')
      .mockRejectedValueOnce(new APIError('VM not ready', 409, 'unavailable'))
      .mockResolvedValue(terminal);
    const requests = new GuestTerminalRequests();
    await expect(requests.execute('vm-one', 'host-original')).rejects.toThrow(
      'VM not ready',
    );
    await Promise.all([
      requests.execute('vm-one', 'host-original'),
      requests.execute('vm-two', 'host-original'),
    ]);
    expect(create.mock.calls[1][1]).not.toBe(create.mock.calls[0][1]);
    expect(create.mock.calls[2][0]).toBe('vm-two');
    expect(create.mock.calls[2][1]).not.toBe(create.mock.calls[1][1]);
  });

  it('does not reuse a creation request for another host with the same machine ID', async () => {
    const create = vi
      .spyOn(machinesApi, 'createTerminal')
      .mockRejectedValueOnce(new TypeError('Response lost'))
      .mockResolvedValue(terminal);
    const requests = new GuestTerminalRequests();
    await expect(requests.execute('vm-one', 'host-one')).rejects.toThrow(
      'Response lost',
    );
    await requests.execute('vm-one', 'host-two');
    expect(create.mock.calls[1][1]).not.toBe(create.mock.calls[0][1]);
    expect(create.mock.calls[1][2]).toBe('host-two');
  });

  it('persists the exact host alongside the guest terminal and boot identity', () => {
    const openApp = vi.fn();
    openGuestTerminal(
      { openApp } as unknown as DesktopAPI,
      {
        id: 'vm-one',
        hostId: 'host-one',
        spec: { name: 'Project' },
      } as Machine,
      terminal,
    );
    expect(openApp).toHaveBeenCalledWith(
      'guest-terminal',
      {
        hostId: 'host-one',
        machineId: 'vm-one',
        terminalId: terminal.id,
        bootId: terminal.bootId,
      },
      { title: 'Project — Terminal' },
    );
  });

  it('retains the original creation request after a gateway failure', async () => {
    const create = vi
      .spyOn(machinesApi, 'createTerminal')
      .mockRejectedValueOnce(
        new APIError('Guest unavailable', 503, 'guest_unavailable'),
      )
      .mockResolvedValue(terminal);
    const requests = new GuestTerminalRequests();
    await expect(requests.execute('vm-one', 'host-original')).rejects.toThrow(
      'Guest unavailable',
    );
    await requests.execute('vm-one', 'host-original');
    expect(create.mock.calls[1]).toEqual(create.mock.calls[0]);
  });

  it('preserves the original request when a different host refuses its retry', async () => {
    const create = vi
      .spyOn(machinesApi, 'createTerminal')
      .mockRejectedValueOnce(new TypeError('Response lost'))
      .mockRejectedValueOnce(
        new APIError('Host changed', 409, 'binding_changed'),
      )
      .mockResolvedValue(terminal);
    const requests = new GuestTerminalRequests();
    await expect(requests.execute('vm-one', 'host-original')).rejects.toThrow();
    await expect(requests.execute('vm-one', 'host-original')).rejects.toThrow(
      'Host changed',
    );
    await requests.execute('vm-one', 'host-original');
    expect(create.mock.calls[2]).toEqual(create.mock.calls[0]);
  });
});
