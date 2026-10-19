import { afterEach, describe, expect, it, vi } from 'vitest';
import { TerminalSession } from './terminal-session';
import type { TerminalInfo, TerminalSocket } from './terminal-session';

class FakeSocket implements TerminalSocket {
  readyState: TerminalSocket['readyState'] = 0;
  bufferedAmount = 0;
  binaryType: BinaryType = 'blob';
  onopen: TerminalSocket['onopen'] = null;
  onmessage: TerminalSocket['onmessage'] = null;
  onerror: TerminalSocket['onerror'] = null;
  onclose: TerminalSocket['onclose'] = null;
  send = vi.fn<TerminalSocket['send']>();
  close = vi.fn((_code?: number, _reason?: string) => {
    this.readyState = 3;
  });

  message(data: unknown) {
    this.onmessage?.call(
      this as unknown as WebSocket,
      { data } as MessageEvent,
    );
  }

  ready() {
    this.readyState = 1;
    this.message('{"ready":true}');
  }

  end(code = 1000, reason = '') {
    this.readyState = 3;
    this.onclose?.call(
      this as unknown as WebSocket,
      { code, reason } as CloseEvent,
    );
  }
}

const sessions: TerminalSession[] = [];

function harness(
  overrides: Partial<ConstructorParameters<typeof TerminalSession>[0]> = {},
) {
  const socket = new FakeSocket();
  const writes: { data: Uint8Array; done: () => void }[] = [];
  const options = {
    baseURL: 'https://desktop.example/workspace',
    getInfo: vi.fn(async (_signal: AbortSignal): Promise<TerminalInfo> => ({
      supported: true,
      shell: '/bin/sh',
    })),
    createSocket: vi.fn((_url: string) => socket),
    write: vi.fn((data: Uint8Array, done: () => void) => {
      writes.push({ data, done });
    }),
    onState: vi.fn(),
    onInputRejected: vi.fn(),
    ...overrides,
  };
  const session = new TerminalSession(options);
  sessions.push(session);
  return { session, socket, writes, options };
}

afterEach(() => {
  for (const session of sessions.splice(0)) session.dispose();
  vi.useRealTimers();
});

describe('host terminal sessions', () => {
  it('waits for server readiness and starts only once with the latest clamped dimensions', async () => {
    const { session, socket, options } = harness();
    session.resize(800, -4);
    await Promise.all([session.start(), session.start()]);
    expect(options.createSocket).toHaveBeenCalledOnce();
    expect(options.createSocket).toHaveBeenCalledWith(
      'wss://desktop.example/api/terminal?cols=500&rows=1',
    );
    expect(socket.binaryType).toBe('arraybuffer');
    expect(session.getSnapshot().phase).toBe('connecting');
    expect(session.sendText('before ready')).toBe(false);
    session.resize(91.7, 30.8);
    socket.ready();
    expect(session.getSnapshot()).toEqual({
      phase: 'connected',
      shell: '/bin/sh',
      message: undefined,
    });
    expect(socket.send).toHaveBeenCalledExactlyOnceWith('{"resize":[91,30]}');
  });

  it('reports an unsupported host without opening a websocket', async () => {
    const { session, options } = harness({
      getInfo: async () => ({
        supported: false,
        reason: 'No PTY on this host.',
      }),
    });
    await session.start();
    expect(options.createSocket).not.toHaveBeenCalled();
    expect(session.getSnapshot()).toEqual({
      phase: 'unsupported',
      message: 'No PTY on this host.',
    });
  });

  it('encodes the selected project as metadata without sending a shell command', async () => {
    const projectPath = "/tmp/project's 中文; $(touch bad)?value=#fragment";
    const { session, socket, options } = harness({ projectPath });
    await session.start();
    const url = new URL(vi.mocked(options.createSocket).mock.calls[0][0]);
    expect(url.searchParams.get('projectPath')).toBe(projectPath);
    expect([...url.searchParams.keys()]).toEqual([
      'cols',
      'rows',
      'projectPath',
    ]);
    expect(url.hash).toBe('');
    socket.ready();
    expect(socket.send).not.toHaveBeenCalled();
  });

  it('retains a rejected project error without falling back to a home shell', async () => {
    const { session, socket, options } = harness({
      projectPath: '/missing/project',
    });
    await session.start();
    socket.message(
      JSON.stringify({
        error: 'The project directory does not exist or cannot be accessed.',
        code: 'terminal_project_unavailable',
      }),
    );
    await session.start();
    expect(session.getSnapshot().phase).toBe('error');
    expect(session.getSnapshot().message).toBe(
      'The project directory does not exist or cannot be accessed.',
    );
    expect(options.createSocket).toHaveBeenCalledOnce();
    expect(socket.send).not.toHaveBeenCalled();
    expect(socket.close).toHaveBeenCalledOnce();
  });

  it('preserves a failed capability request and never reconnects automatically', async () => {
    const { session, options } = harness({
      getInfo: async () => {
        throw new Error('Backend unavailable');
      },
    });
    await session.start();
    await session.start();
    expect(options.createSocket).not.toHaveBeenCalled();
    expect(session.getSnapshot()).toEqual({
      phase: 'error',
      message: 'Backend unavailable',
    });
  });

  it('aborts a pending lookup and ignores its late result after window cleanup', async () => {
    let resolveInfo!: (info: TerminalInfo) => void;
    let signal!: AbortSignal;
    const { session, options } = harness({
      getInfo: (nextSignal) => {
        signal = nextSignal;
        return new Promise((resolve) => {
          resolveInfo = resolve;
        });
      },
    });
    const starting = session.start();
    session.dispose();
    expect(signal.aborted).toBe(true);
    resolveInfo({ supported: true });
    await starting;
    expect(options.createSocket).not.toHaveBeenCalled();
    expect(options.onState).not.toHaveBeenCalled();
  });

  it('times out a shell which never acknowledges readiness', async () => {
    vi.useFakeTimers();
    const { session, socket } = harness();
    await session.start();
    vi.advanceTimersByTime(15_000);
    expect(session.getSnapshot().phase).toBe('error');
    expect(session.getSnapshot().message).toContain('did not respond');
    expect(socket.close).toHaveBeenCalledOnce();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('streams UTF-8 and raw mouse/control bytes in bounded binary frames', async () => {
    const { session, socket } = harness();
    await session.start();
    socket.ready();
    const text = `${'文🙂'.repeat(6000)}\r\u0003\u0017`;
    expect(session.sendText(text)).toBe(true);
    const frames = socket.send.mock.calls.map(([data]) => data as Uint8Array);
    expect(frames.length).toBeGreaterThan(1);
    expect(frames.every((data) => data.byteLength <= 16 * 1024)).toBe(true);
    expect(
      new TextDecoder().decode(
        Uint8Array.from(frames.flatMap((data) => Array.from(data))),
      ),
    ).toBe(text);
    session.sendBinary('\u001b[M\u00ff');
    expect(socket.send).toHaveBeenLastCalledWith(
      new Uint8Array([27, 91, 77, 255]),
    );
  });

  it('pauses input under network backpressure and sends the latest resize before queued input', async () => {
    vi.useFakeTimers();
    const { session, socket } = harness();
    await session.start();
    socket.ready();
    socket.bufferedAmount = 70_000;
    session.sendText('queued command\r');
    session.resize(70, 20);
    session.resize(120, 40);
    vi.advanceTimersByTime(32);
    expect(socket.send).not.toHaveBeenCalled();
    socket.bufferedAmount = 0;
    vi.advanceTimersByTime(16);
    expect(socket.send.mock.calls[0][0]).toBe('{"resize":[120,40]}');
    expect(
      new TextDecoder().decode(socket.send.mock.calls[1][0] as Uint8Array),
    ).toBe('queued command\r');
    expect(vi.getTimerCount()).toBe(0);
  });

  it('rejects an oversized paste atomically and clears queued input on disposal', async () => {
    vi.useFakeTimers();
    const { session, socket, options } = harness();
    await session.start();
    socket.ready();
    socket.bufferedAmount = 70_000;
    expect(session.sendText('a'.repeat(1024 * 1024))).toBe(false);
    expect(options.onInputRejected).toHaveBeenCalledOnce();
    expect(socket.send).not.toHaveBeenCalled();
    expect(session.sendText('queued')).toBe(true);
    session.dispose();
    socket.bufferedAmount = 0;
    vi.runAllTimers();
    expect(socket.send).not.toHaveBeenCalled();
    expect(socket.close).toHaveBeenCalledOnce();
    expect(session.sendText('after close')).toBe(false);
  });

  it('serializes rendering and drains final output after natural shell exit', async () => {
    const { session, socket, writes } = harness();
    await session.start();
    socket.ready();
    socket.message(new TextEncoder().encode('one').buffer);
    socket.message(new TextEncoder().encode('two').buffer);
    expect(writes).toHaveLength(1);
    socket.end();
    expect(session.getSnapshot().phase).toBe('ended');
    writes[0].done();
    expect(writes).toHaveLength(2);
    expect(new TextDecoder().decode(writes[1].data)).toBe('two');
    writes[1].done();
    expect(session.sendText('after exit')).toBe(false);
  });

  it('ends a session when output outruns the renderer instead of growing memory without bound', async () => {
    const { session, socket, writes } = harness();
    await session.start();
    socket.ready();
    socket.message(new ArrayBuffer(4 * 1024 * 1024));
    socket.message(new ArrayBuffer(1));
    expect(writes).toHaveLength(1);
    expect(session.getSnapshot().phase).toBe('error');
    expect(session.getSnapshot().message).toContain('more output');
    expect(socket.close).toHaveBeenCalledOnce();
  });

  it('ignores late renderer callbacks and detaches socket callbacks after unmount', async () => {
    const { session, socket, writes } = harness();
    await session.start();
    socket.ready();
    socket.message(new ArrayBuffer(1));
    socket.message(new ArrayBuffer(1));
    session.dispose();
    writes[0].done();
    expect(writes).toHaveLength(1);
    expect(socket.onmessage).toBeNull();
    expect(socket.onerror).toBeNull();
    expect(socket.onclose).toBeNull();
  });

  it('surfaces server capacity errors and unexpected disconnections without reconnecting', async () => {
    const first = harness();
    await first.session.start();
    first.socket.message('{"error":"All four sessions are in use."}');
    expect(first.session.getSnapshot().message).toBe(
      'All four sessions are in use.',
    );
    expect(first.session.getSnapshot().phase).toBe('error');
    const second = harness();
    await second.session.start();
    second.socket.ready();
    second.socket.end(1006);
    await second.session.start();
    expect(second.session.getSnapshot().phase).toBe('error');
    expect(second.options.createSocket).toHaveBeenCalledOnce();
  });

  it.each(['not json', '{}', '{"ready":false}', '{"error":2}'])(
    'rejects an invalid server control: %s',
    async (control) => {
      const { session, socket } = harness();
      await session.start();
      socket.message(control);
      expect(session.getSnapshot().phase).toBe('error');
      expect(socket.close).toHaveBeenCalledOnce();
    },
  );
});
