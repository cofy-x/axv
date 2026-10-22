import { describe, expect, it } from 'vitest';
import type { Operation, Volume } from './api';
import {
  isProjectBackup,
  volumeActionAllowed,
  volumeAvailableForMachine,
} from './volume-policy';

const volume: Volume = {
  id: 'volume-original',
  name: 'Project',
  revision: 1,
  provider: 'lima',
  sizeGiB: 4,
  retain: true,
  status: 'ready',
};
const backup: Volume = {
  ...volume,
  backup: {
    hostId: 'host-original',
    sourceVolumeId: 'source-volume',
    sourceVolumeRevision: 2,
    sourceMachineId: 'source-vm',
    sourceMachineRevision: 3,
    projects: [],
    createdAt: '2026-10-22T00:00:00Z',
  },
};
const restored: Volume = { ...backup, restoredFrom: 'backup-original' };

describe('project volume provenance policy', () => {
  it('preserves explicit backup deletion while excluding modification and attachment', () => {
    expect(isProjectBackup(backup)).toBe(true);
    expect(volumeActionAllowed(backup, 'delete')).toBe(true);
    for (const action of [
      'attach',
      'detach',
      'rename',
      'resize',
      'retain',
      'copy',
    ] as const)
      expect(volumeActionAllowed(backup, action)).toBe(false);
    expect(volumeAvailableForMachine(backup, 'lima', 'development', [])).toBe(
      false,
    );
  });

  it('keeps restored working volumes mutable and attachable while forbidding generic copying', () => {
    expect(isProjectBackup(restored)).toBe(false);
    expect(volumeActionAllowed(restored, 'copy')).toBe(false);
    for (const action of [
      'attach',
      'detach',
      'rename',
      'resize',
      'retain',
      'delete',
    ] as const)
      expect(volumeActionAllowed(restored, action)).toBe(true);
    expect(volumeAvailableForMachine(restored, 'lima', 'development', [])).toBe(
      true,
    );
    expect(volumeActionAllowed(volume, 'copy')).toBe(true);
  });

  it('still rejects changed lifecycle, provider, retention and operation ownership', () => {
    const pending: Operation = {
      id: 'copy-operation',
      machineId: '',
      volumeId: 'another-volume',
      action: 'copy-volume',
      status: 'running',
      locks: [restored.id],
      createdAt: '2026-10-22T00:00:00Z',
    };
    for (const changed of [
      { ...restored, machineId: 'attached-vm' },
      { ...restored, pendingMachineId: 'pending-vm' },
      { ...restored, pendingSizeGiB: 8 },
      { ...restored, status: 'copying' },
      { ...restored, retain: false },
    ])
      expect(
        volumeAvailableForMachine(changed, 'lima', 'development', []),
      ).toBe(false);
    expect(
      volumeAvailableForMachine(restored, 'firecracker', 'development', []),
    ).toBe(false);
    expect(
      volumeAvailableForMachine(restored, 'lima', 'development', [pending]),
    ).toBe(false);
    expect(
      volumeAvailableForMachine(
        { ...restored, retain: false },
        'lima',
        'task',
        [],
      ),
    ).toBe(true);
  });
});
