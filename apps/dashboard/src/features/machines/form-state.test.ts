import { describe, expect, it } from 'vitest';
import type { HostInfo, Machine, Operation } from './api';
import {
  configurationDraft,
  creationDefaults,
  integer,
  machineNameError,
  nameError,
  normalizeMachineName,
  reconcileConfiguration,
  resourceBusy,
} from './form-state';

const machine: Machine = {
  id: 'vm-one',
  hostId: 'host-one',
  revision: 1,
  spec: {
    name: 'Development',
    provider: 'lima',
    imageId: 'image-one',
    kind: 'development',
    resources: { cpus: 2, memoryMiB: 1024, diskGiB: 8 },
    projectGiB: 4,
    retainVolume: true,
  },
  desired: 'stopped',
  status: 'stopped',
  ready: false,
  guestState: 'unavailable',
  guestCapabilities: [],
  reserved: false,
  createdAt: '',
  updatedAt: '',
};
const operation: Operation = {
  id: 'configure-one',
  machineId: machine.id,
  action: 'configure',
  status: 'succeeded',
  createdAt: '',
};

function configured() {
  const spec = { ...machine.spec, name: 'Renamed' };
  const draft = configurationDraft(spec);
  const submission = { revision: 1, spec, draft, operationId: operation.id };
  const queued = { ...machine, pendingSpec: spec, revision: 2 };
  const finished = { ...machine, spec, revision: 3 };
  return { draft, submission, queued, finished };
}

describe('configuration draft reconciliation', () => {
  it('advances past its own completed save so the next save uses the final revision', () => {
    const { draft, submission, queued, finished } = configured();
    expect(
      reconcileConfiguration(queued, finished, draft, submission, [operation]),
    ).toEqual({ base: finished, draft: configurationDraft(finished.spec) });
  });

  it('preserves typing after submission while advancing the successful base', () => {
    const { draft, submission, queued, finished } = configured();
    const later = { ...draft, name: 'Typed while configuring', memoryMiB: '' };
    const result = reconcileConfiguration(queued, finished, later, submission, [
      operation,
    ]);
    expect(result).toEqual({ base: finished, draft: later });
  });

  it('does not rebase dirty edits over an unrelated revision', () => {
    const { draft, submission, queued, finished } = configured();
    expect(
      reconcileConfiguration(
        queued,
        { ...finished, revision: 4 },
        { ...draft, name: 'My draft' },
        submission,
        [operation],
      ),
    ).toBeUndefined();
    expect(
      reconcileConfiguration(
        machine,
        { ...machine, revision: 2 },
        { ...configurationDraft(machine.spec), name: 'My draft' },
        undefined,
        [],
      ),
    ).toBeUndefined();
  });

  it('refreshes a clean form when another writer changes the machine', () => {
    const current = {
      ...machine,
      revision: 4,
      spec: { ...machine.spec, name: 'External update' },
    };
    expect(
      reconcileConfiguration(
        machine,
        current,
        configurationDraft(machine.spec),
        undefined,
        [],
      ),
    ).toEqual({ base: current, draft: configurationDraft(current.spec) });
  });
});

describe('compute form validation', () => {
  it('keeps blank numeric edits invalid and applies whole-number limits', () => {
    expect(integer('', 1)).toBeUndefined();
    expect(integer(' ', 1)).toBeUndefined();
    expect(integer('1.5', 1)).toBeUndefined();
    expect(integer('129', 128, 256)).toBe(129);
    expect(integer('257', 128, 256)).toBeUndefined();
  });

  it('checks the server UTF-8 name limit', () => {
    expect(nameError('界'.repeat(26))).toBe('');
    expect(nameError('界'.repeat(27))).not.toBe('');
    expect(nameError('invalid\nname')).not.toBe('');
  });

  it('reserves immutable IDs and both applied and pending machine names', () => {
    const pending = {
      ...machine,
      pendingSpec: { ...machine.spec, name: 'Next name' },
    };
    expect(machineNameError(`vm-${'a'.repeat(32)}`)).not.toBe('');
    expect(machineNameError('vm-short')).toBe('');
    expect(machineNameError('Development', [pending])).not.toBe('');
    expect(machineNameError(' Next name ', [pending])).not.toBe('');
    expect(machineNameError('development', [pending])).toBe('');
    expect(machineNameError('Development', [pending], machine.id)).toBe('');
    expect(
      machineNameError('Next name', [{ ...pending, status: 'deleted' }]),
    ).toBe('');
  });

  it('normalizes machine whitespace with the same Unicode rules as the host', () => {
    expect(normalizeMachineName('\u0085\u2003工作 VM\u00a0')).toBe('工作 VM');
    expect(normalizeMachineName('\ufefflabel\ufeff')).toBe('\ufefflabel\ufeff');
    expect(machineNameError('\u0085\u2003\u00a0')).not.toBe('');
  });

  it('uses host defaults and selected-image minimums without mutating the defaults', () => {
    const info: HostInfo = {
      configured: true,
      defaults: {
        spec: machine.spec,
        start: true,
        taskTtlSeconds: 3600,
        taskRetainVolume: false,
      },
      images: [
        {
          id: machine.spec.imageId,
          name: 'Large image',
          provider: 'lima',
          arch: 'arm64',
          minRootGiB: 20,
        },
      ],
      capacity: {
        cpus: 1,
        memoryMiB: 512,
        diskGiB: 10,
        machines: 1,
        workers: 1,
      },
    };
    const defaults = creationDefaults(info);
    expect(defaults?.spec.resources).toEqual({
      cpus: 2,
      memoryMiB: 1024,
      diskGiB: 20,
    });
    expect(info.defaults?.spec.resources.diskGiB).toBe(8);
    expect(creationDefaults({ configured: true })).toBeUndefined();
  });

  it('finds related locks across resource identifiers', () => {
    expect(
      resourceBusy(
        [
          {
            ...operation,
            status: 'running',
            machineId: '',
            locks: ['volume-one'],
          },
        ],
        'volume-one',
      ),
    ).toBe(true);
    expect(resourceBusy([operation], machine.id)).toBe(false);
  });
});
