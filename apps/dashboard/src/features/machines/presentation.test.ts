import { describe, expect, it } from 'vitest';
import type { Machine, Operation } from './api';
import { activeVMOperation } from './lifecycle';
import {
  canCopyLocalLimaShell,
  guestTerminalReason,
  imageTerminalSummary,
  latestVMFailure,
  machineGuestStatus,
  machineGuidance,
  machineTransition,
} from './presentation';

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
    expect(machineGuidance(stopped)).toBeUndefined();
    expect(machineGuidance(ready)).toBeUndefined();
    expect(machineGuidance({ ...stopped, status: 'unknown' })).toContain(
      'Stop the VM before trying to start it again.',
    );
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
      expect(
        machineTransition(stopped, activeVMOperation(stopped, [operation])),
      ).toBe('Updating VM storage…');
      expect(
        activeVMOperation(stopped, [{ ...operation, locks: ['another-vm'] }]),
      ).toBeUndefined();
      expect(
        activeVMOperation(stopped, [{ ...operation, status: 'succeeded' }]),
      ).toBeUndefined();
    },
  );

  it('shows acknowledged startup on a stopped snapshot and clears a superseded failure', () => {
    const start: Operation = {
      id: 'new-start',
      machineId: stopped.id,
      action: 'start',
      status: 'queued',
      createdAt: '',
    };
    const failed = {
      ...start,
      id: 'previous-start',
      status: 'failed',
      error: 'Startup failed.',
    };
    expect(machineTransition(stopped, start)).toBe('Starting…');
    expect(latestVMFailure(stopped, [failed])).toEqual(failed);
    expect(
      latestVMFailure(stopped, [{ ...start, status: 'succeeded' }, failed]),
    ).toBeUndefined();
  });

  it('retains configuration, storage and guest capability blockers', () => {
    expect(
      machineGuidance({ ...stopped, pendingSpec: stopped.spec }),
    ).toContain('pending');
    expect(
      machineGuidance({
        ...stopped,
        spec: { ...stopped.spec, volumeId: undefined },
      }),
    ).toContain('project volume');
    expect(
      machineGuidance({ ...ready, guestState: 'not-installed' }),
    ).toContain('no guest enhancements');
    expect(machineGuidance({ ...ready, guestState: 'incompatible' })).toContain(
      'incompatible',
    );
    expect(
      machineGuidance({ ...ready, ready: false, guestState: 'unavailable' }),
    ).toBe('Waiting for the guest service.');
    expect(machineGuidance(running)).toContain('terminals');
  });

  it.each([stopped, ready])(
    'keeps a reported reason when the VM is $status',
    (machine) => {
      const reason = 'The provider could not verify the project volume.';
      expect(machineGuidance({ ...machine, reason })).toBe(reason);
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

  it('summarizes image terminal capabilities before creation', () => {
    expect(
      imageTerminalSummary({
        id: 'axv-lima-arm64',
        name: 'AXV Lima arm64',
        provider: 'lima',
        arch: 'arm64',
        guest: {
          imageVersion: 1,
          protocolVersion: 3,
          capabilities: ['exec', 'terminals'],
        },
      }),
    ).toContain('Browser terminal included');
    expect(
      imageTerminalSummary({
        id: 'ubuntu-base',
        name: 'Ubuntu 24.04 base',
        provider: 'lima',
        arch: 'arm64',
      }),
    ).toContain('No browser terminal');
  });

  it('reports independent guest readiness and restricts local Lima shell fallback', () => {
    const basicRunning: Machine = {
      ...running,
      ready: false,
      guestState: 'not-installed',
      guestCapabilities: undefined,
    };
    const guestReady: Machine = {
      ...running,
      ready: true,
      guestState: 'ready',
      guestCapabilities: ['exec', 'terminals'],
    };
    expect(machineGuestStatus(basicRunning)).toBe('Basic VM (no guest)');
    expect(machineGuestStatus(guestReady)).toBeUndefined();
    expect(machineGuestStatus(running)).toBe('No browser terminal');
    expect(
      machineGuestStatus({
        ...guestReady,
        ready: false,
        guestState: 'starting',
      }),
    ).toBe('Guest starting…');
    expect(canCopyLocalLimaShell(basicRunning, true)).toBe(true);
    expect(canCopyLocalLimaShell(basicRunning, false)).toBe(false);
    expect(canCopyLocalLimaShell(guestReady, true)).toBe(false);
    expect(
      canCopyLocalLimaShell(
        {
          ...basicRunning,
          spec: { ...basicRunning.spec, provider: 'firecracker' },
        },
        true,
      ),
    ).toBe(false);
  });
});
