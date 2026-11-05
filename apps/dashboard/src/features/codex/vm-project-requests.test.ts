import { describe, expect, it } from 'vitest';
import {
  launchRejectedBeforeStart,
  VMProjectRequests,
} from './vm-project-requests';
import { APIError } from '../../shared/api/http';

const binding = {
  hostId: 'host',
  projectId: 'project',
  projectRevision: 1,
  volumeId: 'volume',
  volumeRevision: 3,
  machineId: 'machine',
  bootId: 'boot',
  instanceId: 'instance',
};

function storage() {
  const values = new Map<string, string>();
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => void values.set(key, value),
    removeItem: (key: string) => void values.delete(key),
  };
}

describe('VM Codex launch recovery', () => {
  it('preserves original request receipts on desktop-store or request identity conflicts', () => {
    expect(
      launchRejectedBeforeStart(
        new APIError('Store changed', 409, 'database_changed'),
      ),
    ).toBe(false);
    expect(
      launchRejectedBeforeStart(
        new APIError('Payload changed', 409, 'request_conflict'),
      ),
    ).toBe(false);
    expect(
      launchRejectedBeforeStart(
        new APIError('VM unavailable', 503, 'vm_project_unavailable'),
      ),
    ).toBe(true);
  });
  it.each([
    [409, 'database_changed'],
    [428, 'database_identity_required'],
    [401, 'authentication_required'],
    [503, 'server_stopping'],
    [409, 'history_identity_conflict'],
    [409, 'invalid_run_state'],
    [400, 'invalid_session'],
    [403, 'host_forbidden'],
    [404, 'not_found'],
    [409, 'unknown_conflict'],
  ])('preserves uncertain receipts for HTTP %s / %s', (status, code) => {
    expect(
      launchRejectedBeforeStart(
        new APIError('Request outcome is unknown', status, code),
      ),
    ).toBe(false);
  });

  it.each([
    [409, 'vm_project_changed'],
    [409, 'conversation_active'],
    [409, 'run_changed'],
    [503, 'codex_limit'],
  ])(
    'permits another explicit attempt after a proven HTTP %s / %s refusal',
    (status, code) => {
      expect(
        launchRejectedBeforeStart(
          new APIError('No run was reserved', status, code),
        ),
      ).toBe(true);
    },
  );
  it('retains the original request and complete environment across reload and VM replacement', () => {
    const saved = storage();
    const first = new VMProjectRequests('key', 'host', 'project', saved);
    const original = first.capture(binding);
    const reloaded = new VMProjectRequests('key', 'host', 'project', saved);
    expect(
      reloaded.capture({
        ...binding,
        machineId: 'replacement',
        bootId: 'new-boot',
      }),
    ).toEqual(original);
    reloaded.complete(original.requestId);
    expect(
      reloaded.capture({ ...binding, machineId: 'replacement' }).requestId,
    ).not.toBe(original.requestId);
    first.complete(original.requestId);
    expect(reloaded.read()?.binding.machineId).toBe('replacement');
  });

  it('preserves corrupt or differently scoped recovery data without replacing it', () => {
    const saved = storage();
    const pending = new VMProjectRequests('key', 'host', 'project', saved);
    saved.setItem('key', '{invalid');
    expect(() => pending.capture(binding)).toThrow('could not be read');
    expect(saved.getItem('key')).toBe('{invalid');
    saved.setItem(
      'key',
      JSON.stringify({
        requestId: 'request-id',
        binding: { ...binding, hostId: 'other-host' },
      }),
    );
    const original = saved.getItem('key');
    expect(() => pending.capture(binding)).toThrow('could not be read');
    expect(saved.getItem('key')).toBe(original);
  });

  it('does not submit an unrecorded request when recovery persistence fails', () => {
    const saved = storage();
    const pending = new VMProjectRequests('key', 'host', 'project', {
      ...saved,
      setItem: () => {
        throw new Error('Storage full');
      },
    });
    expect(() => pending.capture(binding)).toThrow('Storage full');
    expect(saved.getItem('key')).toBeNull();
  });
});
