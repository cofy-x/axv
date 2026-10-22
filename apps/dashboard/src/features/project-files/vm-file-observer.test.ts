import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  createVMFileObserver,
  vmFileBindingError,
  vmFileObservationDecision,
} from './vm-file-observer';
import type { VMFileObservation } from './vm-file-observer';
import type { VMProjectContent } from './vm-api';

const binding = {
  hostId: 'host-one',
  projectId: 'project-one',
  projectRevision: 1,
  volumeId: 'volume-one',
  volumeRevision: 3,
  machineId: 'machine-one',
  bootId: 'boot-one',
  instanceId: 'instance-one',
};

function state(): VMFileObservation {
  return {
    projectId: binding.projectId,
    binding: { ...binding },
    path: 'src/main.ts',
    navigation: 1,
    input: 0,
    save: 0,
    sha256: 'original-hash',
    dirty: false,
    acceptBlocked: false,
  };
}

function content(sha256 = 'external-hash'): VMProjectContent {
  return {
    project: {
      id: binding.projectId,
      hostId: binding.hostId,
      kind: 'vm',
      name: 'Project',
      volumeId: binding.volumeId,
      root: 'repo',
      rootIdentity: 'root-one',
      revision: binding.projectRevision,
      createdAt: '',
    },
    binding: { ...binding },
    path: 'src/main.ts',
    content: 'const answer = 42;\r\n',
    sha256,
    size: 20,
    modifiedAt: '',
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

afterEach(() => vi.useRealTimers());

describe('VM file observation acceptance', () => {
  it('accepts only a new clean snapshot and leaves unchanged text alone', () => {
    const current = state();
    expect(vmFileObservationDecision(current, current, content())).toBe(
      'accept',
    );
    expect(
      vmFileObservationDecision(current, current, content(current.sha256)),
    ).toBe('same');
    // The decision has no manual-edit flag: clean Edit can accept a snapshot.
    expect(current.sha256).toBe('original-hash');
  });

  it('reports changes without advancing a dirty or failed-write baseline', () => {
    const captured = state();
    for (const current of [
      { ...captured, dirty: true },
      { ...captured, acceptBlocked: true },
      { ...captured, dirty: true, acceptBlocked: true },
    ]) {
      expect(vmFileObservationDecision(captured, current, content())).toBe(
        'changed',
      );
      expect(current.sha256).toBe('original-hash');
    }
  });

  it.each([
    ['navigation', { navigation: 2 }],
    ['typing or composition', { input: 1 }],
    ['save', { save: 1 }],
    ['acknowledged snapshot', { sha256: 'saved-hash' }],
    ['path', { path: 'src/other.ts' }],
    ['project', { projectId: 'project-two' }],
    ['VM boot', { binding: { ...binding, bootId: 'boot-two' } }],
    ['guest instance', { binding: { ...binding, instanceId: 'instance-two' } }],
  ])('rejects a reply after the %s identity changes', (_label, change) => {
    const captured = state();
    expect(
      vmFileObservationDecision(
        captured,
        { ...captured, ...change },
        content(),
      ),
    ).toBe('ignore');
  });

  it('rejects replies after observation is suspended or its owner disappears', () => {
    expect(vmFileObservationDecision(state(), null, content())).toBe('ignore');
  });

  it.each([
    { path: 'src/other.ts' },
    { binding: { ...binding, machineId: 'machine-two' } },
    { project: { ...content().project, id: 'project-two' } },
  ])('rejects a response belonging to another target: %o', (change) => {
    const captured = state();
    expect(() =>
      vmFileObservationDecision(captured, captured, {
        ...content(),
        ...change,
      }),
    ).toThrow('another file or VM environment');
  });

  it('recognizes binding failures that require explicit reconnection', () => {
    for (const code of [
      'binding_changed',
      'boot_changed',
      'instance_changed',
      'project_changed',
      'guest_identity_mismatch',
    ])
      expect(vmFileBindingError(code)).toBe(true);
    for (const code of ['not_found', 'not_text', 'file_too_large', undefined])
      expect(vmFileBindingError(code)).toBe(false);
  });
});

describe('bounded VM file observation', () => {
  it('keeps its polling deadline through repeated visible resize notifications', async () => {
    vi.useFakeTimers();
    const read = vi.fn().mockResolvedValue(content());
    const observer = createVMFileObserver({
      snapshot: state,
      read,
      receive: vi.fn(),
      fail: vi.fn(),
    });
    observer.check();
    await vi.advanceTimersByTimeAsync(0);
    for (let frame = 0; frame < 100; frame++) {
      observer.setVisible(true);
      await vi.advanceTimersByTimeAsync(19);
    }
    expect(read).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(100);
    expect(read).toHaveBeenCalledTimes(2);
    // Explicit retry remains immediate, independently of resize notifications.
    observer.check();
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(3);
    observer.stop();
  });

  it('rechecks transient local blockers without issuing reads or spinning', async () => {
    vi.useFakeTimers();
    let current: VMFileObservation | null = null;
    const snapshot = vi.fn(() => current);
    const read = vi.fn().mockResolvedValue(content());
    const observer = createVMFileObserver({
      snapshot,
      read,
      receive: vi.fn(),
      fail: vi.fn(),
    });
    observer.check();
    await vi.advanceTimersByTimeAsync(10000);
    expect(snapshot).toHaveBeenCalledTimes(6);
    expect(read).not.toHaveBeenCalled();
    current = state(); // No React/input event accompanies this gate change.
    await vi.advanceTimersByTimeAsync(1999);
    expect(read).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(read).toHaveBeenCalledOnce();
    observer.stop();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('fully suspends hidden views and immediately checks on becoming visible', async () => {
    vi.useFakeTimers();
    const first = deferred<VMProjectContent>();
    const read = vi
      .fn()
      .mockReturnValueOnce(first.promise)
      .mockResolvedValue(content());
    const receive = vi.fn();
    const observer = createVMFileObserver({
      snapshot: state,
      read,
      receive,
      fail: vi.fn(),
    });
    observer.check();
    const signal = read.mock.calls[0][1] as AbortSignal;
    observer.setVisible(false);
    expect(signal.aborted).toBe(true);
    first.resolve(content());
    await vi.advanceTimersByTimeAsync(10000);
    observer.check();
    expect(read).toHaveBeenCalledOnce();
    expect(receive).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
    observer.setVisible(true);
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(2);
    expect(receive).toHaveBeenCalledOnce();
    observer.stop();
  });

  it('coalesces visibility flips while an aborted transport is still settling', async () => {
    vi.useFakeTimers();
    const first = deferred<VMProjectContent>();
    const read = vi
      .fn()
      .mockReturnValueOnce(first.promise)
      .mockResolvedValue(content());
    const receive = vi.fn();
    const observer = createVMFileObserver({
      snapshot: state,
      read,
      receive,
      fail: vi.fn(),
    });
    observer.check();
    for (let flip = 0; flip < 3; flip++) {
      observer.setVisible(false);
      observer.setVisible(true);
    }
    expect(read).toHaveBeenCalledOnce();
    first.resolve(content());
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(2);
    expect(receive).toHaveBeenCalledOnce();
    observer.stop();
  });

  it('disposes a permanent stop even when its sample is temporarily unavailable', async () => {
    vi.useFakeTimers();
    const snapshot = vi.fn(() => null);
    const read = vi.fn();
    const observer = createVMFileObserver({
      snapshot,
      read,
      receive: vi.fn(),
      fail: vi.fn(),
    });
    observer.check();
    observer.stop();
    observer.setVisible(true);
    await vi.advanceTimersByTimeAsync(10000);
    expect(snapshot).toHaveBeenCalledOnce();
    expect(read).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('serializes reads and waits two seconds after completion before polling', async () => {
    vi.useFakeTimers();
    const first = deferred<VMProjectContent>();
    const read = vi
      .fn()
      .mockReturnValueOnce(first.promise)
      .mockResolvedValue(content());
    const receive = vi.fn();
    const observer = createVMFileObserver({
      snapshot: state,
      read,
      receive,
      fail: vi.fn(),
    });
    observer.check();
    observer.check();
    await vi.advanceTimersByTimeAsync(10000);
    expect(read).toHaveBeenCalledTimes(1);
    first.resolve(content());
    await vi.advanceTimersByTimeAsync(0);
    expect(receive).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(1999);
    expect(read).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(read).toHaveBeenCalledTimes(2);
    observer.stop();
  });

  it('aborts on suspension and ignores a transport that still returns old text', async () => {
    vi.useFakeTimers();
    let current: VMFileObservation | null = state();
    const first = deferred<VMProjectContent>();
    const read = vi
      .fn()
      .mockReturnValueOnce(first.promise)
      .mockResolvedValue(content());
    const receive = vi.fn();
    const fail = vi.fn();
    const observer = createVMFileObserver({
      snapshot: () => current,
      read,
      receive,
      fail,
    });
    observer.check();
    const signal = read.mock.calls[0][1] as AbortSignal;
    current = null;
    observer.check();
    expect(signal.aborted).toBe(true);
    first.resolve(content());
    await vi.advanceTimersByTimeAsync(10000);
    expect(receive).not.toHaveBeenCalled();
    expect(fail).not.toHaveBeenCalled();
    expect(read).toHaveBeenCalledOnce();
    current = state();
    observer.check();
    await vi.advanceTimersByTimeAsync(0);
    expect(receive).toHaveBeenCalledOnce();
    expect(read).toHaveBeenCalledTimes(2);
    observer.stop();
  });

  it('checks again after a save while rejecting the read begun before it', async () => {
    vi.useFakeTimers();
    let current: VMFileObservation | null = state();
    const oldRead = deferred<VMProjectContent>();
    const read = vi
      .fn()
      .mockReturnValueOnce(oldRead.promise)
      .mockResolvedValue(content());
    const receive = vi.fn();
    const observer = createVMFileObserver({
      snapshot: () => current,
      read,
      receive,
      fail: vi.fn(),
    });
    observer.check();
    current = null; // Saving, composition or unresolved recovery blocks reads.
    observer.check();
    current = { ...state(), save: 2, sha256: 'saved-hash', dirty: true };
    observer.check();
    oldRead.resolve(content('original-hash'));
    await vi.advanceTimersByTimeAsync(0);
    expect(read).toHaveBeenCalledTimes(2);
    expect(receive).toHaveBeenCalledOnce();
    expect(receive.mock.calls[0][1]).toBe('changed');
    expect(receive.mock.calls[0][2].sha256).toBe('saved-hash');
    observer.stop();
  });

  it('retains a failed check for retry and suppresses failures from an old target', async () => {
    vi.useFakeTimers();
    let current = state();
    const oldRead = deferred<VMProjectContent>();
    const failure = new Error('File was removed');
    const read = vi
      .fn()
      .mockReturnValueOnce(oldRead.promise)
      .mockRejectedValueOnce(failure)
      .mockResolvedValue(content());
    const fail = vi.fn();
    const receive = vi.fn();
    const observer = createVMFileObserver({
      snapshot: () => current,
      read,
      fail,
      receive,
    });
    observer.check();
    current = { ...current, input: 1 };
    oldRead.reject(new Error('Late failure'));
    await vi.advanceTimersByTimeAsync(0);
    expect(fail).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(2000);
    expect(fail).toHaveBeenCalledWith(failure, current);
    observer.check();
    await vi.advanceTimersByTimeAsync(0);
    expect(receive).toHaveBeenCalledOnce();
    observer.stop();
  });

  it('never delivers or reschedules after closing the observer', async () => {
    vi.useFakeTimers();
    const pending = deferred<VMProjectContent>();
    const read = vi.fn(() => pending.promise);
    const receive = vi.fn();
    const observer = createVMFileObserver({
      snapshot: state,
      read,
      receive,
      fail: vi.fn(),
    });
    observer.check();
    observer.stop();
    pending.resolve(content());
    observer.check();
    await vi.advanceTimersByTimeAsync(10000);
    expect(read).toHaveBeenCalledOnce();
    expect(receive).not.toHaveBeenCalled();
  });
});
