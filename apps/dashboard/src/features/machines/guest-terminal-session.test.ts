import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { APIError } from '../../shared/api/http';
import { machinesApi } from './api';
import type { GuestTerminal } from './api';
import { GuestTerminalSession } from './guest-terminal-session';

class FakeSocket {
  static OPEN = 1;
  static instances: FakeSocket[] = [];
  readyState = 0;
  bufferedAmount = 0;
  binaryType: BinaryType = 'blob';
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  onclose: ((event: CloseEvent) => void) | null = null;
  send = vi.fn<(data: string | Uint8Array) => void>();
  close = vi.fn(() => {
    this.readyState = 3;
  });

  constructor(readonly url: URL) {
    FakeSocket.instances.push(this);
  }

  message(data: unknown) {
    this.onmessage?.({ data } as MessageEvent);
  }

  ready() {
    this.readyState = FakeSocket.OPEN;
    this.message(JSON.stringify({ ready: true, bootId: 'boot-original' }));
  }

  interrupt() {
    this.readyState = 3;
    this.onclose?.({ code: 1006 } as CloseEvent);
  }
}

const terminal: GuestTerminal = {
  id: 'terminal-original',
  bootId: 'boot-original',
  status: 'running',
};
const sessions: GuestTerminalSession[] = [];

function harness(hostId = 'host-original') {
  const lookup = vi.spyOn(machinesApi, 'terminal').mockResolvedValue(terminal);
  const create = vi.spyOn(machinesApi, 'createTerminal');
  const options = {
    machineId: 'vm-original',
    hostId,
    terminalId: terminal.id,
    bootId: terminal.bootId,
    reset: vi.fn(async () => {}),
    write: vi.fn((_data: Uint8Array, done: () => void) => done()),
    onState: vi.fn(),
    onInputRejected: vi.fn(),
  };
  const session = new GuestTerminalSession(options);
  sessions.push(session);
  return { session, lookup, create, options };
}

beforeEach(() => {
  vi.useFakeTimers();
  FakeSocket.instances = [];
  vi.stubGlobal('WebSocket', FakeSocket);
  vi.stubGlobal('location', { href: 'https://desktop.example/workspace' });
});
afterEach(() => {
  for (const session of sessions.splice(0)) session.dispose();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('guest terminal attachment identity', () => {
  it('starts once, waits for readiness, and reconnects only to the original terminal', async () => {
    const { session, lookup, create, options } = harness();
    session.resize(120, 40);
    await Promise.all([session.start(), session.start()]);
    expect(lookup).toHaveBeenCalledOnce();
    expect(FakeSocket.instances).toHaveLength(1);
    const first = FakeSocket.instances[0];
    expect(first.url.href).toBe(
      'wss://desktop.example/api/compute/machines/vm-original/guest/terminals/terminal-original/attach?hostId=host-original',
    );
    expect(session.sendText('before ready')).toBe(false);
    first.ready();
    expect(session.getSnapshot().phase).toBe('connected');
    expect(first.send).toHaveBeenCalledExactlyOnceWith('{"resize":[120,40]}');
    first.interrupt();
    expect(session.getSnapshot().phase).toBe('connecting');
    await vi.advanceTimersByTimeAsync(1000);
    expect(FakeSocket.instances).toHaveLength(2);
    expect(lookup.mock.calls.map((call) => call.slice(0, 3))).toEqual([
      ['vm-original', 'terminal-original', 'host-original'],
      ['vm-original', 'terminal-original', 'host-original'],
    ]);
    expect(options.reset).toHaveBeenCalledTimes(2);
    expect(create).not.toHaveBeenCalled();
  });

  it('refuses a restored window without a verified host before sending any request', async () => {
    const { session, lookup, create } = harness('');
    await session.start();
    expect(session.getSnapshot().phase).toBe('error');
    expect(session.getSnapshot().message).toContain('no verified VM host');
    expect(lookup).not.toHaveBeenCalled();
    expect(create).not.toHaveBeenCalled();
    expect(FakeSocket.instances).toHaveLength(0);
  });

  it('does not reconnect or create another terminal when the host binding changed', async () => {
    const { session, lookup, create } = harness();
    lookup.mockRejectedValueOnce(
      new APIError(
        'This view belongs to a different VM host.',
        409,
        'binding_changed',
      ),
    );
    await session.start();
    await vi.advanceTimersByTimeAsync(30000);
    expect(session.getSnapshot().phase).toBe('error');
    expect(lookup).toHaveBeenCalledOnce();
    expect(create).not.toHaveBeenCalled();
    expect(FakeSocket.instances).toHaveLength(0);
  });

  it('fails a missing terminal without creating a replacement or retrying a definite error', async () => {
    const { session, lookup, create } = harness();
    lookup.mockRejectedValueOnce(
      new APIError('Terminal is gone', 404, 'terminal_lost'),
    );
    await session.start();
    await vi.advanceTimersByTimeAsync(30000);
    expect(session.getSnapshot()).toEqual({
      phase: 'error',
      message: 'Terminal is gone',
    });
    expect(lookup).toHaveBeenCalledOnce();
    expect(FakeSocket.instances).toHaveLength(0);
    expect(create).not.toHaveBeenCalled();
  });

  it('rejects a changed boot before opening an attachment', async () => {
    const { session, lookup, create } = harness();
    lookup.mockResolvedValueOnce({ ...terminal, bootId: 'new-boot' });
    await session.start();
    expect(session.getSnapshot().phase).toBe('error');
    expect(session.getSnapshot().message).toContain('guest has restarted');
    expect(FakeSocket.instances).toHaveLength(0);
    expect(create).not.toHaveBeenCalled();
  });

  it('aborts a pending lookup and ignores its late result after the window closes', async () => {
    const { session, lookup, options } = harness();
    let resolve!: (terminal: GuestTerminal) => void;
    lookup.mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    const starting = session.start();
    session.dispose();
    expect(lookup.mock.calls[0][3]?.aborted).toBe(true);
    resolve(terminal);
    await starting;
    expect(FakeSocket.instances).toHaveLength(0);
    expect(options.reset).not.toHaveBeenCalled();
    expect(options.onState).not.toHaveBeenCalled();
  });

  it('stops reconnecting after terminal exit and never accepts input while detached', async () => {
    const { session, lookup } = harness();
    await session.start();
    const socket = FakeSocket.instances[0];
    socket.ready();
    socket.message('{"ended":true}');
    expect(session.getSnapshot().phase).toBe('ended');
    expect(session.sendText('after exit')).toBe(false);
    await vi.advanceTimersByTimeAsync(60000);
    expect(lookup).toHaveBeenCalledOnce();
    expect(socket.close).toHaveBeenCalledOnce();
  });
});
