import { describe, expect, it } from 'vitest';
import type { Machine, Operation } from './api';
import { guestTerminalReason, machineGuidance } from './presentation';

const running: Machine = {
  id: 'vm-presentation',
  hostId: 'host-presentation',
  revision: 1,
  spec: {
    name: 'Presentation VM',
    provider: 'lima',
    imageId: 'linux',
    kind: 'development',
    resources: { cpus: 1, memoryMiB: 512, diskGiB: 8 },
    projectGiB: 1,
    retainVolume: true,
    volumeId: 'volume-presentation',
  },
  desired: 'running',
  status: 'running',
  ready: true,
  guestState: 'ready',
  guestCapabilities: ['exec'],
  reserved: true,
  createdAt: '',
  updatedAt: '',
};

describe('machine guidance', () => {
  const ready: Machine = {
    ...running,
    guestCapabilities: ['exec', 'terminals'],
  };
  const stopped: Machine = {
    ...ready,
    desired: 'stopped',
    status: 'stopped',
    ready: false,
    guestState: 'unavailable',
  };

  it('leaves normal states and states without diagnostics free of generic instructions', () => {
    expect(machineGuidance(stopped, [])).toBeUndefined();
    expect(machineGuidance(ready, [])).toBeUndefined();
    expect(
      machineGuidance({ ...stopped, status: 'unknown' }, []),
    ).toBeUndefined();
  });

  it.each(['queued', 'running'])(
    'keeps %s progress for operations that lock the VM',
    (status) => {
      const operation: Operation = {
        id: 'operation-presentation',
        machineId: '',
        locks: [stopped.id],
        action: 'copy-volume',
        status,
        phase: 'copying-data',
        createdAt: '',
      };
      expect(machineGuidance(stopped, [operation])).toContain(status);
      expect(machineGuidance(stopped, [operation])).toContain('Copying data');
      expect(
        machineGuidance(stopped, [{ ...operation, locks: ['another-vm'] }]),
      ).toBeUndefined();
      expect(
        machineGuidance(stopped, [{ ...operation, status: 'succeeded' }]),
      ).toBeUndefined();
    },
  );

  it('retains configuration, storage and guest capability blockers', () => {
    expect(
      machineGuidance({ ...stopped, pendingSpec: stopped.spec }, []),
    ).toContain('pending');
    expect(
      machineGuidance(
        { ...stopped, spec: { ...stopped.spec, volumeId: undefined } },
        [],
      ),
    ).toContain('project volume');
    expect(
      machineGuidance({ ...ready, guestState: 'not-installed' }, []),
    ).toContain('no guest enhancements');
    expect(
      machineGuidance({ ...ready, guestState: 'incompatible' }, []),
    ).toContain('incompatible');
    expect(
      machineGuidance(
        { ...ready, ready: false, guestState: 'unavailable' },
        [],
      ),
    ).toContain('not ready');
    expect(machineGuidance(running, [])).toContain('terminals');
  });

  it.each([stopped, ready])(
    'keeps a reported reason when the VM is $status',
    (machine) => {
      const reason = 'The provider could not verify the project volume.';
      expect(machineGuidance({ ...machine, reason }, [])).toBe(reason);
    },
  );
});

describe('guest terminal availability', () => {
  it('explains a missing terminal capability while the guest is ready', () => {
    expect(guestTerminalReason(running)).toBe(
      'This image does not provide guest terminals.',
    );
    expect(
      guestTerminalReason({
        ...running,
        guestCapabilities: ['exec', 'terminals'],
      }),
    ).toBeUndefined();
  });
  it('keeps basic lifecycle, protocol and current run state distinct', () => {
    expect(
      guestTerminalReason({
        ...running,
        ready: false,
        guestState: 'not-installed',
      }),
    ).toContain('does not include');
    expect(
      guestTerminalReason({
        ...running,
        ready: false,
        guestState: 'incompatible',
      }),
    ).toContain('incompatible');
    expect(
      guestTerminalReason({
        ...running,
        desired: 'stopped',
        guestCapabilities: ['terminals'],
      }),
    ).toContain('Start this VM');
    expect(
      guestTerminalReason({
        ...running,
        ready: false,
        guestState: 'unavailable',
      }),
    ).toContain('not ready');
  });
});
