import { afterEach, describe, expect, it, vi } from 'vitest';
import { APIError } from '../../shared/api/http';
import { machinesApi } from './api';
import type { HostInfo, Machine, Operation, Volume } from './api';
import { machineActionReason, MachineActionRequests } from './lifecycle';

const machine = { id: 'vm-test', hostId: 'host-test', revision: 1 } as Machine;

afterEach(() => vi.restoreAllMocks());

describe('VM lifecycle request ownership', () => {
  it('keeps identical VM IDs on different hosts and their uncertain retries separate', async () => {
    const other = { ...machine, hostId: 'other-host' };
    const action = vi
      .spyOn(machinesApi, 'action')
      .mockRejectedValueOnce(new TypeError('Response lost'))
      .mockResolvedValue({
        machine: other,
        operation: { id: 'other-action' },
      } as Awaited<ReturnType<typeof machinesApi.action>>);
    const requests = new MachineActionRequests();
    await expect(requests.execute(machine, 'restart')).rejects.toThrow(
      'Response lost',
    );
    const original = requests.getPending(machine.hostId, machine.id);
    expect(requests.getPending(other.hostId, other.id)).toBeUndefined();
    await requests.execute(other, 'stop');
    expect(action.mock.calls[1][0]).toBe(other);
    expect(requests.getPending(machine.hostId, machine.id)).toBe(original);
    await requests.execute({ ...machine, revision: 2 }, 'restart');
    expect(action.mock.calls[2]).toEqual(action.mock.calls[0]);
  });

  it('resolves a lost restart response with its original revision and request ID', async () => {
    const result = {
      machine: { ...machine, revision: 2 },
      operation: { id: 'original-restart', status: 'succeeded' },
    } as Awaited<ReturnType<typeof machinesApi.action>>;
    const action = vi
      .spyOn(machinesApi, 'action')
      .mockRejectedValueOnce(new TypeError('Response lost'))
      .mockResolvedValueOnce(result)
      .mockResolvedValueOnce(result);
    const requests = new MachineActionRequests();
    await expect(requests.execute(machine, 'restart')).rejects.toThrow(
      'Response lost',
    );
    expect(await requests.execute(result.machine, 'restart')).toBe(result);
    expect(action.mock.calls[1]).toEqual(action.mock.calls[0]);
    await requests.execute(result.machine, 'restart');
    expect(action.mock.calls[2][0].revision).toBe(2);
    expect(action.mock.calls[2][2]).not.toBe(action.mock.calls[0][2]);
  });

  it('allows a corrected action after a definite rejection', async () => {
    const action = vi
      .spyOn(machinesApi, 'action')
      .mockRejectedValue(new APIError('VM changed', 409, 'revision_conflict'));
    const requests = new MachineActionRequests();
    await expect(requests.execute(machine, 'stop')).rejects.toThrow(
      'VM changed',
    );
    await expect(
      requests.execute({ ...machine, revision: 2 }, 'stop'),
    ).rejects.toThrow('VM changed');
    expect(action.mock.calls[1][0].revision).toBe(2);
    expect(action.mock.calls[1][2]).not.toBe(action.mock.calls[0][2]);
  });

  it('retains the original request when a changed host binding rejects its retry', async () => {
    const action = vi
      .spyOn(machinesApi, 'action')
      .mockRejectedValueOnce(new TypeError('Response lost'))
      .mockRejectedValueOnce(
        new APIError('Different host', 409, 'binding_changed'),
      )
      .mockResolvedValue({
        machine,
        operation: { id: 'original-operation' },
      } as Awaited<ReturnType<typeof machinesApi.action>>);
    const requests = new MachineActionRequests();
    await expect(requests.execute(machine, 'stop')).rejects.toThrow(
      'Response lost',
    );
    await expect(requests.execute(machine, 'stop')).rejects.toThrow(
      'Different host',
    );
    expect(requests.getPending(machine.hostId, machine.id)?.uncertain).toBe(
      true,
    );
    await requests.execute(machine, 'stop');
    expect(action.mock.calls[2]).toEqual(action.mock.calls[0]);
  });

  it('requires resolving an uncertain original action before another action on that VM', async () => {
    const result = {
      machine: { ...machine, revision: 2 },
      operation: { id: 'original-start', status: 'succeeded' },
    } as Awaited<ReturnType<typeof machinesApi.action>>;
    const action = vi
      .spyOn(machinesApi, 'action')
      .mockRejectedValueOnce(
        new DOMException('The request timed out.', 'TimeoutError'),
      )
      .mockResolvedValue(result);
    const requests = new MachineActionRequests();
    await expect(requests.execute(machine, 'start')).rejects.toMatchObject({
      name: 'TimeoutError',
    });
    const pending = requests.getPending(machine.hostId, machine.id);
    expect(pending).toEqual({
      machine,
      action: 'start',
      requestId: action.mock.calls[0][2],
      uncertain: true,
    });
    await expect(requests.execute(result.machine, 'stop')).rejects.toThrow(
      'Resolve the original start request',
    );
    expect(action).toHaveBeenCalledOnce();
    expect(requests.getPending(machine.hostId, machine.id)).toBe(pending);
    await requests.execute(pending!.machine, pending!.action);
    expect(action.mock.calls[1]).toEqual(action.mock.calls[0]);
    expect(requests.getPending(machine.hostId, machine.id)).toBeUndefined();
    await requests.execute(result.machine, 'stop');
    expect(action.mock.calls[2][1]).toBe('stop');
    expect(action.mock.calls[2][0].revision).toBe(2);
  });

  it('shares an in-flight request and rejects a competing action before sending it', async () => {
    let resolve!: (
      result: Awaited<ReturnType<typeof machinesApi.action>>,
    ) => void;
    const action = vi.spyOn(machinesApi, 'action').mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    const requests = new MachineActionRequests();
    const first = requests.execute(machine, 'restart');
    expect(requests.execute(machine, 'restart')).toBe(first);
    await expect(requests.execute(machine, 'delete')).rejects.toThrow(
      'original restart request',
    );
    expect(action).toHaveBeenCalledOnce();
    resolve({ machine, operation: { id: 'original-restart' } } as Awaited<
      ReturnType<typeof machinesApi.action>
    >);
    await first;
    expect(requests.getPending(machine.hostId, machine.id)).toBeUndefined();
  });
});

describe('VM lifecycle subscriptions', () => {
  it('publishes stable pending snapshots to every window until request state changes', async () => {
    const result = { machine, operation: { id: 'original-action' } } as Awaited<
      ReturnType<typeof machinesApi.action>
    >;
    vi.spyOn(machinesApi, 'action')
      .mockRejectedValueOnce(new TypeError('Connection lost'))
      .mockResolvedValueOnce(result);
    const requests = new MachineActionRequests();
    const snapshots: ReturnType<typeof requests.getPending>[] = [];
    const unsubscribe = requests.subscribe(() =>
      snapshots.push(requests.getPending(machine.hostId, machine.id)),
    );
    const first = requests.execute(machine, 'start');
    const sending = requests.getPending(machine.hostId, machine.id);
    expect(sending?.uncertain).toBe(false);
    expect(requests.getPending(machine.hostId, machine.id)).toBe(sending);
    await expect(first).rejects.toThrow('Connection lost');
    const uncertain = requests.getPending(machine.hostId, machine.id);
    expect(uncertain?.uncertain).toBe(true);
    expect(uncertain).not.toBe(sending);
    expect(requests.getPending(machine.hostId, machine.id)).toBe(uncertain);
    await requests.execute(machine, 'start');
    expect(snapshots.map((snapshot) => snapshot?.uncertain)).toEqual([
      false,
      true,
      false,
      undefined,
    ]);
    expect(
      snapshots
        .filter(Boolean)
        .every((snapshot) => snapshot?.requestId === sending?.requestId),
    ).toBe(true);
    unsubscribe();
  });
});

describe('VM lifecycle availability', () => {
  const stopped = {
    ...machine,
    spec: { volumeId: 'vol-test', provider: 'lima' },
    status: 'stopped',
    desired: 'stopped',
    ready: false,
  } as Machine;
  const volume = {
    id: 'vol-test',
    machineId: machine.id,
    status: 'ready',
  } as Volume;
  const provider = {
    available: true,
    forceStop: true,
  } as NonNullable<HostInfo['providers']>[number];

  it('does not start a running VM when only guest readiness is missing', () => {
    const running = { ...stopped, status: 'running', desired: 'running' };
    expect(
      machineActionReason(running, 'start', [], [volume], provider),
    ).toContain('already running');
    expect(
      machineActionReason(running, 'restart', [], [volume], provider),
    ).toBeUndefined();
    expect(
      machineActionReason(stopped, 'restart', [], [volume], provider),
    ).toContain('Start this VM');
    expect(
      machineActionReason(stopped, 'stop', [], [volume], provider),
    ).toContain('already stopped');
    expect(
      machineActionReason(
        { ...stopped, desired: 'running' },
        'stop',
        [],
        [volume],
        provider,
      ),
    ).toBeUndefined();
  });

  it('blocks both machine and attached-volume locks but ignores completed and unrelated operations', () => {
    for (const reference of [
      { locks: [machine.id] },
      { locks: [volume.id] },
      { machineId: machine.id },
      { volumeId: volume.id },
      { resourceId: volume.id },
    ]) {
      const operation = { ...reference, status: 'running' } as Operation;
      expect(
        machineActionReason(stopped, 'start', [operation], [volume]),
      ).toContain('active VM or project-volume operation');
      expect(
        machineActionReason(
          stopped,
          'start',
          [{ ...operation, status: 'failed' }],
          [volume],
        ),
      ).toBeUndefined();
    }
    expect(
      machineActionReason(
        stopped,
        'start',
        [{ status: 'running', locks: ['other'] } as Operation],
        [volume],
      ),
    ).toBeUndefined();
  });

  it('requires a ready attached project volume, resolved configuration, and an active task lease', () => {
    expect(machineActionReason(stopped, 'start', [], [volume])).toBeUndefined();
    for (const volumes of [
      [],
      [{ ...volume, status: 'failed' }],
      [{ ...volume, machineId: 'different' }],
    ])
      expect(machineActionReason(stopped, 'start', [], volumes)).toContain(
        'Attach a ready project volume',
      );
    expect(
      machineActionReason(
        { ...stopped, pendingSpec: stopped.spec },
        'start',
        [],
        [volume],
      ),
    ).toContain('pending configuration');
    expect(
      machineActionReason(
        { ...stopped, expiresAt: '2000-01-01T00:00:00Z' },
        'start',
        [],
        [volume],
      ),
    ).toContain('expired task lease');
  });

  it('respects provider capabilities and leaves recovery actions available after failures', () => {
    const failed = { ...stopped, status: 'failed' };
    expect(
      machineActionReason(failed, 'force-stop', [], [volume], {
        ...provider,
        forceStop: false,
      }),
    ).toContain('does not support force stop');
    expect(
      machineActionReason(failed, 'force-stop', [], [volume], provider),
    ).toBeUndefined();
    expect(
      machineActionReason(failed, 'delete', [], [volume], provider),
    ).toBeUndefined();
    expect(
      machineActionReason(stopped, 'start', [], [volume], {
        ...provider,
        available: false,
        reason: 'Install Lima',
      }),
    ).toBe('Install Lima');
    expect(
      machineActionReason(
        stopped,
        'delete',
        [],
        [{ ...volume, pendingMachineId: stopped.id }],
        provider,
      ),
    ).toContain('pending project-volume attachment');
    expect(
      machineActionReason(
        { ...stopped, status: 'starting' },
        'stop',
        [],
        [volume],
        provider,
      ),
    ).toContain('current VM transition');
  });
});
