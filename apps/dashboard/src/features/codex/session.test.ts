import { afterEach, describe, expect, it, vi } from 'vitest';
import { APIError } from '../../shared/api/http';
import { CodexConnection } from './session';
import type { CodexSocket } from './session';
import type { CodexSessionRecord } from './api';
import { publishSessionActivity, subscribeSessionActivity } from './activity';

class FakeSocket implements CodexSocket {
  readyState: CodexSocket['readyState'] = 0;
  bufferedAmount = 0;
  binaryType: BinaryType = 'blob';
  onopen: CodexSocket['onopen'] = null;
  onmessage: CodexSocket['onmessage'] = null;
  onerror: CodexSocket['onerror'] = null;
  onclose: CodexSocket['onclose'] = null;
  send = vi.fn<CodexSocket['send']>();
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

  disconnected(code = 1006, reason = '') {
    this.readyState = 3;
    this.onclose?.call(
      this as unknown as WebSocket,
      { code, reason } as CloseEvent,
    );
  }
}

const record: CodexSessionRecord = {
  id: 'session-id',
  runId: 'run-id',
  target: 'host',
  projectPath: '/home/user/project',
  title: '',
  metadataRevision: 1,
  tmuxSession: 'axv-test-session',
  tmuxSocket: 'axv-test-socket',
  attachCommand: 'tmux -L axv-test-socket attach-session -t axv-test-session',
  status: 'running',
  createdAt: '2026-10-16T00:00:00Z',
  connections: 0,
  continuation: { state: 'active', activeRunId: 'run-id' },
};
const connections: CodexConnection[] = [];

function harness(
  overrides: Partial<ConstructorParameters<typeof CodexConnection>[0]> = {},
) {
  const sockets: FakeSocket[] = [];
  const writes: { data: Uint8Array; done: () => void }[] = [];
  const options = {
    sessionId: record.id,
    runId: record.runId,
    baseURL: 'https://localhost:3001/desktop',
    getSession: vi.fn(async (_signal: AbortSignal) => record),
    createSocket: vi.fn((_url: string) => {
      const socket = new FakeSocket();
      sockets.push(socket);
      return socket;
    }),
    write: vi.fn((data: Uint8Array, done: () => void) => {
      writes.push({ data, done });
    }),
    reset: vi.fn(),
    onState: vi.fn(),
    onInputRejected: vi.fn(),
    ...overrides,
  };
  const connection = new CodexConnection(options);
  connections.push(connection);
  return { connection, options, sockets, writes };
}

afterEach(() => {
  for (const connection of connections.splice(0)) connection.dispose();
  vi.useRealTimers();
});

describe('persistent Codex connections', () => {
  it('replays a review accepted before the initial attachment lookup finishes', async () => {
    let resolve!: (session: CodexSessionRecord) => void;
    const unread: CodexSessionRecord = {
      ...record,
      activity: {
        state: 'failed',
        eventId: 'completion',
        sequence: 2,
        observation: { instanceId: 'service', revision: 1 },
        needsAttention: true,
      },
    };
    const { connection, sockets } = harness({
      getSession: () =>
        new Promise((done) => {
          resolve = done;
        }),
    });
    const unsubscribe = subscribeSessionActivity(vi.fn());
    try {
      const starting = connection.start();
      publishSessionActivity({
        ...unread,
        activity: {
          ...unread.activity!,
          observation: { instanceId: 'service', revision: 2 },
          needsAttention: false,
        },
      });
      resolve(unread);
      await starting;
      expect(connection.getSnapshot().session?.activity).toMatchObject({
        eventId: 'completion',
        needsAttention: false,
      });
      expect(sockets).toHaveLength(1);
    } finally {
      unsubscribe();
    }
  });

  it('preserves an accepted activity review when an older end response arrives', async () => {
    const unread: CodexSessionRecord = {
      ...record,
      activity: {
        observation: { instanceId: 'service', revision: 1 },
        state: 'failed',
        eventId: 'completion',
        sequence: 2,
        needsAttention: true,
      },
    };
    const { connection } = harness({ getSession: async () => unread });
    await connection.start();
    connection.updateActivity({
      ...unread,
      activity: {
        ...unread.activity!,
        observation: { instanceId: 'service', revision: 2 },
        needsAttention: false,
      },
    });
    connection.ended({ ...unread, status: 'ended' });
    expect(connection.getSnapshot()).toMatchObject({
      phase: 'ended',
      session: { activity: { needsAttention: false } },
    });
  });

  it('requires a newer accepted observation before certifying a shared activity refresh', async () => {
    const current: CodexSessionRecord = {
      ...record,
      activity: {
        observation: { instanceId: 'service', revision: 2 },
        state: 'working',
        sequence: 1,
        needsAttention: false,
      },
    };
    const { connection } = harness({ getSession: async () => current });
    await connection.start();
    expect(connection.updateActivity(current)).toBe(false);
    expect(
      connection.updateActivity({
        ...current,
        activity: {
          ...current.activity!,
          observation: { instanceId: 'service', revision: 1 },
        },
      }),
    ).toBe(false);
    expect(
      connection.updateActivity({
        ...current,
        activity: {
          ...current.activity!,
          observation: { instanceId: 'service', revision: 3 },
        },
      }),
    ).toBe(true);
  });

  it('rejects another run returned during connection and never follows its terminal', async () => {
    const { connection, sockets } = harness({
      getSession: async () => ({ ...record, runId: 'different-run' }),
    });
    await connection.start();
    expect(connection.getSnapshot()).toMatchObject({
      phase: 'error',
      retryable: false,
    });
    expect(sockets).toHaveLength(0);
  });

  it('refreshes continuation for the bound run without reviving it or accepting another run', async () => {
    const ended = {
      ...record,
      status: 'ended' as const,
      continuation: { state: 'available' as const },
    };
    const { connection, sockets } = harness({ getSession: async () => ended });
    await connection.start();
    connection.updateMetadata({
      ...ended,
      title: 'Latest title',
      metadataRevision: 2,
    });
    connection.updateRunInfo({
      ...ended,
      conversationId: 'official-id',
      continuation: { state: 'active', activeRunId: 'new-run' },
    });
    expect(connection.getSnapshot()).toMatchObject({
      phase: 'ended',
      session: {
        runId: 'run-id',
        title: 'Latest title',
        metadataRevision: 2,
        conversationId: 'official-id',
        continuation: { activeRunId: 'new-run' },
      },
    });
    connection.updateRunInfo({ ...record, runId: 'new-run' });
    connection.updateRunInfo(record);
    connection.reconnect();
    expect(connection.getSnapshot().session?.runId).toBe('run-id');
    expect(connection.getSnapshot().session?.status).toBe('ended');
    expect(sockets).toHaveLength(0);
  });

  it('updates names and archive metadata without replacing the terminal or dropping input', async () => {
    const { connection, options, sockets } = harness();
    await connection.start();
    sockets[0].ready();
    connection.updateMetadata({
      ...record,
      title: 'Release work',
      archivedAt: '2026-10-16T01:00:00Z',
      metadataRevision: 2,
      status: 'starting',
    });
    expect(connection.getSnapshot()).toMatchObject({
      phase: 'connected',
      session: {
        title: 'Release work',
        metadataRevision: 2,
        status: 'running',
      },
    });
    expect(connection.sendText('still connected')).toBe(true);
    expect(options.createSocket).toHaveBeenCalledOnce();
    expect(options.reset).toHaveBeenCalledOnce();
    expect(sockets[0].close).not.toHaveBeenCalled();
    connection.updateMetadata(record);
    expect(connection.getSnapshot().session?.title).toBe('Release work');
  });

  it('preserves a metadata update that arrives during the initial session fetch', async () => {
    let resolve!: (session: CodexSessionRecord) => void;
    const { connection } = harness({
      getSession: () =>
        new Promise((done) => {
          resolve = done;
        }),
    });
    const starting = connection.start();
    connection.updateMetadata({
      ...record,
      title: 'New name',
      metadataRevision: 2,
    });
    resolve(record);
    await starting;
    expect(connection.getSnapshot().session?.title).toBe('New name');
  });

  it('attaches once to the existing server ID, with current dimensions and no input before readiness', async () => {
    const { connection, options, sockets } = harness({
      sessionId: 'existing session',
      runId: 'existing run',
      getSession: vi.fn(async () => ({
        ...record,
        id: 'existing session',
        runId: 'existing run',
      })),
    });
    connection.resize(900, -1);
    await Promise.all([connection.start(), connection.start()]);
    expect(options.getSession).toHaveBeenCalledOnce();
    expect(options.createSocket).toHaveBeenCalledExactlyOnceWith(
      'wss://localhost:3001/api/host/codex/sessions/existing%20session/runs/existing%20run/terminal?cols=500&rows=1',
    );
    expect(connection.sendText('early')).toBe(false);
    connection.resize(112.5, 36.9);
    sockets[0].ready();
    expect(sockets[0].send).toHaveBeenCalledExactlyOnceWith(
      '{"resize":[112,36]}',
    );
    expect(connection.getSnapshot()).toMatchObject({
      phase: 'connected',
      session: { ...record, id: 'existing session', runId: 'existing run' },
    });
  });

  it.each(['ended', 'lost', 'failed'] as const)(
    'never reopens or replaces a %s session',
    async (status) => {
      vi.useFakeTimers();
      const { connection, sockets, options } = harness({
        getSession: vi.fn(async () => ({
          ...record,
          status,
          reason: 'Process unavailable',
        })),
      });
      await connection.start();
      connection.reconnect();
      await vi.runAllTimersAsync();
      expect(connection.getSnapshot()).toMatchObject({
        phase: 'ended',
        retryable: false,
        message: 'Process unavailable',
      });
      expect(sockets).toHaveLength(0);
      expect(options.getSession).toHaveBeenCalledOnce();
    },
  );

  it('keeps a stopping session actionable without attaching or reporting it ended', async () => {
    const { connection, sockets } = harness({
      getSession: async () => ({ ...record, status: 'stopping' }),
    });
    await connection.start();
    expect(connection.getSnapshot()).toMatchObject({
      phase: 'stopping',
      retryable: false,
      session: { status: 'stopping' },
    });
    expect(connection.getSnapshot().message).toContain('Retry End Session');
    expect(sockets).toHaveLength(0);
  });

  it.each([401, 403, 404])(
    'stops after HTTP %s instead of retrying denied or missing sessions',
    async (status) => {
      vi.useFakeTimers();
      const { connection, options, sockets } = harness({
        getSession: vi.fn(async () => {
          throw new APIError('Access unavailable', status, 'forbidden');
        }),
      });
      await connection.start();
      connection.reconnect();
      await vi.runAllTimersAsync();
      expect(connection.getSnapshot()).toMatchObject({
        phase: 'error',
        retryable: false,
      });
      expect(options.getSession).toHaveBeenCalledOnce();
      expect(sockets).toHaveLength(0);
    },
  );

  it('rechecks the same session after a transient server failure before opening a connection', async () => {
    vi.useFakeTimers();
    const getSession = vi
      .fn()
      .mockRejectedValueOnce(new APIError('Restarting', 503, 'unavailable'))
      .mockResolvedValue(record);
    const { connection, sockets } = harness({ getSession });
    await connection.start();
    expect(connection.getSnapshot().phase).toBe('reconnecting');
    await vi.advanceTimersByTimeAsync(999);
    expect(sockets).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(1);
    expect(getSession).toHaveBeenCalledTimes(2);
    expect(sockets).toHaveLength(1);
    sockets[0].ready();
    expect(connection.getSnapshot().phase).toBe('connected');
  });

  it('drops unsent input at disconnect and never replays it into a resumed session', async () => {
    vi.useFakeTimers();
    const { connection, sockets, options } = harness();
    await connection.start();
    sockets[0].ready();
    sockets[0].send.mockClear();
    sockets[0].bufferedAmount = 70_000;
    connection.sendText('do not replay\r');
    connection.resize(120, 50);
    sockets[0].disconnected();
    expect(connection.sendText('offline')).toBe(false);
    await vi.advanceTimersByTimeAsync(1000);
    sockets[1].ready();
    expect(options.getSession).toHaveBeenCalledTimes(2);
    expect(
      vi
        .mocked(options.createSocket)
        .mock.calls.every(([url]) =>
          url.includes('/session-id/runs/run-id/terminal?'),
        ),
    ).toBe(true);
    expect(sockets[0].send).not.toHaveBeenCalled();
    expect(sockets[1].send).toHaveBeenCalledExactlyOnceWith(
      '{"resize":[120,50]}',
    );
  });

  it('recovers from one attachment failure on the same run and ignores its old socket', async () => {
    vi.useFakeTimers();
    const { connection, sockets, options, writes } = harness();
    await connection.start();
    const oldMessage = sockets[0].onmessage!;
    sockets[0].message(
      '{"error":"Could not attach to the persistent Codex session.","code":"attach_failed"}',
    );
    expect(connection.getSnapshot().phase).toBe('reconnecting');
    expect(connection.sendText('never send before ready\r')).toBe(false);
    await vi.advanceTimersByTimeAsync(1000);
    expect(sockets).toHaveLength(2);
    expect(options.getSession).toHaveBeenCalledTimes(2);
    expect(vi.mocked(options.createSocket).mock.calls[1][0]).toBe(
      vi.mocked(options.createSocket).mock.calls[0][0],
    );
    sockets[1].ready();
    oldMessage.call(
      sockets[0] as unknown as WebSocket,
      {
        data: '{"error":"Old attachment failed","code":"attach_failed"}',
      } as MessageEvent,
    );
    oldMessage.call(
      sockets[0] as unknown as WebSocket,
      {
        data: new TextEncoder().encode('old screen').buffer,
      } as MessageEvent,
    );
    expect(connection.getSnapshot()).toMatchObject({
      phase: 'connected',
      session: { id: record.id, runId: record.runId },
    });
    expect(writes).toHaveLength(0);
    expect(sockets[1].send).toHaveBeenCalledExactlyOnceWith(
      '{"resize":[80,24]}',
    );
    expect(connection.sendText('new input\r')).toBe(true);
    expect(sockets[1].send).toHaveBeenLastCalledWith(
      new TextEncoder().encode('new input\r'),
    );
    expect(sockets[0].send).not.toHaveBeenCalled();
  });

  it('bounds repeated attachment failures and keeps their final error visible', async () => {
    vi.useFakeTimers();
    const { connection, sockets, options } = harness();
    await connection.start();
    for (let attempt = 0; attempt < 6; attempt++) {
      expect(sockets).toHaveLength(attempt + 1);
      sockets[attempt].message(
        '{"error":"Could not attach to the persistent Codex session.","code":"attach_failed"}',
      );
      if (attempt < 5) await vi.advanceTimersToNextTimerAsync();
    }
    await vi.runAllTimersAsync();
    expect(sockets).toHaveLength(6);
    expect(options.getSession).toHaveBeenCalledTimes(6);
    expect(
      vi
        .mocked(options.createSocket)
        .mock.calls.every(([url]) =>
          url.includes('/session-id/runs/run-id/terminal?'),
        ),
    ).toBe(true);
    expect(connection.getSnapshot()).toMatchObject({
      phase: 'error',
      retryable: true,
      message:
        'Could not attach to the persistent Codex session. Choose Reconnect to try again.',
    });
  });

  it('discards queued input across a disconnect followed by an attachment failure', async () => {
    vi.useFakeTimers();
    const { connection, sockets } = harness();
    await connection.start();
    sockets[0].ready();
    sockets[0].send.mockClear();
    sockets[0].bufferedAmount = 70_000;
    expect(connection.sendText('do not replay\r')).toBe(true);
    sockets[0].disconnected();
    await vi.advanceTimersToNextTimerAsync();
    sockets[1].message(
      '{"error":"Attachment unavailable","code":"attach_failed"}',
    );
    await vi.advanceTimersToNextTimerAsync();
    sockets[2].ready();
    expect(sockets[0].send).not.toHaveBeenCalled();
    expect(sockets[1].send).not.toHaveBeenCalled();
    expect(sockets[2].send).toHaveBeenCalledExactlyOnceWith(
      '{"resize":[80,24]}',
    );
  });

  it('cancels attachment recovery when detached and refuses a different run during retry', async () => {
    vi.useFakeTimers();
    const detached = harness();
    await detached.connection.start();
    detached.sockets[0].message(
      '{"error":"Attachment unavailable","code":"attach_failed"}',
    );
    detached.connection.dispose();
    const changed = harness({
      getSession: vi
        .fn()
        .mockResolvedValueOnce(record)
        .mockResolvedValue({ ...record, runId: 'newer-run' }),
    });
    await changed.connection.start();
    changed.sockets[0].message(
      '{"error":"Attachment unavailable","code":"attach_failed"}',
    );
    await vi.runAllTimersAsync();
    expect(detached.options.getSession).toHaveBeenCalledOnce();
    expect(detached.sockets).toHaveLength(1);
    expect(changed.sockets).toHaveLength(1);
    expect(changed.connection.getSnapshot()).toMatchObject({
      phase: 'error',
      retryable: false,
    });
    expect(changed.connection.getSnapshot().message).toContain(
      'different Codex run',
    );
  });

  it('aborts lookups and ignores late responses on detach without ending the host process', async () => {
    let resolve!: (session: CodexSessionRecord) => void;
    let signal!: AbortSignal;
    const { connection, options, sockets } = harness({
      getSession: (next) => {
        signal = next;
        return new Promise((done) => {
          resolve = done;
        });
      },
    });
    const starting = connection.start();
    connection.dispose();
    const notifications = vi.mocked(options.onState).mock.calls.length;
    resolve(record);
    await starting;
    expect(signal.aborted).toBe(true);
    expect(sockets).toHaveLength(0);
    expect(options.onState).toHaveBeenCalledTimes(notifications);
  });

  it('does not let an in-flight attachment win against an explicit end', async () => {
    let resolve!: (session: CodexSessionRecord) => void;
    const { connection, sockets } = harness({
      getSession: () =>
        new Promise((done) => {
          resolve = done;
        }),
    });
    const starting = connection.start();
    connection.ended({ ...record, status: 'ended' });
    resolve(record);
    await starting;
    expect(connection.getSnapshot().phase).toBe('ended');
    expect(sockets).toHaveLength(0);
  });

  it('cancels retries after explicit end and ignores callbacks from the old socket', async () => {
    vi.useFakeTimers();
    const { connection, sockets, options } = harness();
    await connection.start();
    sockets[0].ready();
    const oldMessage = sockets[0].onmessage!;
    sockets[0].disconnected();
    connection.ended({ ...record, status: 'ended' });
    oldMessage.call(
      sockets[0] as unknown as WebSocket,
      { data: '{"ready":true}' } as MessageEvent,
    );
    await vi.runAllTimersAsync();
    expect(connection.getSnapshot().phase).toBe('ended');
    expect(options.getSession).toHaveBeenCalledOnce();
  });

  it('drains already received final output after the ended control arrives', async () => {
    const { connection, sockets, writes } = harness();
    await connection.start();
    sockets[0].ready();
    sockets[0].message(new TextEncoder().encode('Final answer: ').buffer);
    sockets[0].message(new TextEncoder().encode('completed').buffer);
    sockets[0].message('{"ended":true}');
    expect(writes).toHaveLength(1);
    expect(connection.getSnapshot().phase).toBe('ended');
    writes[0].done();
    expect(writes).toHaveLength(2);
    expect(new TextDecoder().decode(writes[1].data)).toBe('completed');
    writes[1].done();
    expect(connection.sendText('after exit')).toBe(false);
  });

  it('does not let late old-screen render acknowledgments corrupt the new attachment queue', async () => {
    vi.useFakeTimers();
    const { connection, sockets, writes, options } = harness();
    await connection.start();
    sockets[0].ready();
    sockets[0].message(new TextEncoder().encode('old').buffer);
    sockets[0].message(
      new TextEncoder().encode('discard queued old screen').buffer,
    );
    sockets[0].disconnected();
    await vi.advanceTimersByTimeAsync(1000);
    sockets[1].ready();
    sockets[1].message(new TextEncoder().encode('new').buffer);
    sockets[1].message(new TextEncoder().encode('new tail').buffer);
    expect(options.reset).toHaveBeenCalledTimes(2);
    expect(writes).toHaveLength(2);
    writes[0].done();
    expect(writes).toHaveLength(2);
    writes[1].done();
    expect(writes).toHaveLength(3);
    expect(new TextDecoder().decode(writes[2].data)).toBe('new tail');
  });

  it('bounds automatic reconnect attempts and leaves an explicit retry action', async () => {
    vi.useFakeTimers();
    const { connection, sockets } = harness();
    await connection.start();
    await vi.runAllTimersAsync();
    expect(sockets).toHaveLength(6);
    expect(connection.getSnapshot()).toMatchObject({
      phase: 'error',
      retryable: true,
    });
    expect(connection.getSnapshot().message).toContain('Choose Reconnect');
    connection.reconnect();
    await vi.advanceTimersByTimeAsync(0);
    expect(sockets).toHaveLength(7);
    sockets[6].ready();
    expect(connection.getSnapshot().phase).toBe('connected');
  });

  it('stops on explicit server access denial, including a websocket policy close', async () => {
    vi.useFakeTimers();
    const first = harness();
    await first.connection.start();
    first.sockets[0].message(
      '{"error":"Local access required","code":"forbidden"}',
    );
    first.connection.reconnect();
    const second = harness();
    await second.connection.start();
    second.sockets[0].disconnected(1008, 'Local access required');
    second.connection.reconnect();
    await vi.runAllTimersAsync();
    for (const item of [first, second]) {
      expect(item.connection.getSnapshot()).toMatchObject({
        phase: 'error',
        retryable: false,
      });
      expect(item.sockets).toHaveLength(1);
    }
  });

  it.each(['forbidden', 'permission_denied', 'run_changed', 'unknown_error'])(
    'does not automatically retry a %s control',
    async (code) => {
      vi.useFakeTimers();
      const { connection, sockets, options } = harness();
      await connection.start();
      sockets[0].message(JSON.stringify({ error: 'Access unavailable', code }));
      await vi.runAllTimersAsync();
      expect(connection.getSnapshot()).toMatchObject({
        phase: 'error',
        message: 'Access unavailable',
      });
      expect(options.getSession).toHaveBeenCalledOnce();
      expect(sockets).toHaveLength(1);
    },
  );

  it('caps input atomically and retains exact UTF-8/control bytes in accepted frames', async () => {
    const { connection, sockets, options } = harness();
    await connection.start();
    sockets[0].ready();
    sockets[0].send.mockClear();
    expect(connection.sendText('a'.repeat(1024 * 1024 + 1))).toBe(false);
    expect(options.onInputRejected).toHaveBeenCalledOnce();
    expect(sockets[0].send).not.toHaveBeenCalled();
    const text = '你好🙂'.repeat(5000) + '\u0003\r';
    expect(connection.sendText(text)).toBe(true);
    const frames = sockets[0].send.mock.calls.map(
      ([frame]) => frame as Uint8Array,
    );
    expect(frames.every((frame) => frame.byteLength <= 16 * 1024)).toBe(true);
    expect(
      new TextDecoder().decode(
        Uint8Array.from(frames.flatMap((frame) => Array.from(frame))),
      ),
    ).toBe(text);
    connection.sendBinary('\u001b[M\u00ff');
    expect(sockets[0].send).toHaveBeenLastCalledWith(
      new Uint8Array([27, 91, 77, 255]),
    );
  });

  it('uses bounded tmux scroll controls without synthesizing Codex key input', async () => {
    const { connection, sockets } = harness();
    await connection.start();
    expect(connection.scroll(20)).toBe(false);
    sockets[0].ready();
    sockets[0].send.mockClear();
    connection.scroll(700);
    connection.scroll(-12.5);
    connection.scroll(0);
    expect(connection.scroll(Number.NaN)).toBe(false);
    expect(sockets[0].send.mock.calls).toEqual([
      ['{"scroll":200}'],
      ['{"scroll":-12}'],
      ['{"scroll":0}'],
    ]);
  });

  it('disconnects on renderer overflow with a recoverable message without ending the server session', async () => {
    const { connection, sockets } = harness();
    await connection.start();
    sockets[0].ready();
    sockets[0].message(new ArrayBuffer(4 * 1024 * 1024));
    sockets[0].message(new ArrayBuffer(1));
    expect(connection.getSnapshot()).toMatchObject({
      phase: 'error',
      retryable: true,
    });
    expect(connection.getSnapshot().message).toContain('still running');
    expect(sockets[0].close).toHaveBeenCalledExactlyOnceWith(
      1000,
      'View detached',
    );
  });
});
