import { APIError } from '../../shared/api/http';
import { sessionIsLive } from './api';
import type { CodexSessionRecord } from './api';
import { mergeSessionMetadata } from './metadata';
import {
  mergeSessionActivity,
  mergeSessionSnapshot,
  publishSessionActivity,
} from './activity';

export type CodexSocket = Pick<
  WebSocket,
  | 'readyState'
  | 'bufferedAmount'
  | 'binaryType'
  | 'send'
  | 'close'
  | 'onopen'
  | 'onmessage'
  | 'onerror'
  | 'onclose'
>;

export interface CodexConnectionState {
  phase:
    | 'connecting'
    | 'connected'
    | 'reconnecting'
    | 'stopping'
    | 'ended'
    | 'error';
  session?: CodexSessionRecord;
  message?: string;
  retryable?: boolean;
}

interface Options {
  databaseId: string;
  sessionId: string;
  runId: string;
  baseURL: string;
  getSession: (signal: AbortSignal) => Promise<CodexSessionRecord>;
  createSocket: (url: string) => CodexSocket;
  write: (data: Uint8Array, done: () => void) => void;
  reset: () => void;
  onState: (state: CodexConnectionState) => void;
  onInputRejected: () => void;
}

const INPUT_LIMIT = 1024 * 1024;
const OUTPUT_LIMIT = 4 * 1024 * 1024;
const HIGH_WATER = 64 * 1024;

function dimension(value: number, min: number, max: number) {
  return Math.max(
    min,
    Math.min(max, Number.isFinite(value) ? Math.floor(value) : min),
  );
}

/** A connection only attaches to an existing server session; disposal never ends it. */
export class CodexConnection {
  private state: CodexConnectionState = { phase: 'connecting' };
  private metadata?: CodexSessionRecord;
  private socket?: CodexSocket;
  private abort?: AbortController;
  private started = false;
  private disposed = false;
  private generation = 0;
  private outputGeneration = 0;
  private attempts = 0;
  private cols = 80;
  private rows = 24;
  private resizePending = true;
  private input: Uint8Array<ArrayBuffer>[] = [];
  private inputBytes = 0;
  private output: Uint8Array[] = [];
  private outputBytes = 0;
  private writing = false;
  private timeout?: ReturnType<typeof setTimeout>;
  private retryTimer?: ReturnType<typeof setTimeout>;
  private inputTimer?: ReturnType<typeof setTimeout>;

  constructor(private readonly options: Options) {}

  getSnapshot() {
    return this.state;
  }

  updateMetadata(session: CodexSessionRecord) {
    if (session.id !== this.options.sessionId || this.disposed) return;
    this.metadata = this.metadata
      ? mergeSessionMetadata(this.metadata, session)
      : session;
    if (!this.state.session) return;
    const updated = mergeSessionMetadata(this.state.session, this.metadata);
    if (updated !== this.state.session)
      this.publish({ ...this.state, session: updated });
  }

  updateActivity(session: CodexSessionRecord) {
    if (
      this.disposed ||
      !this.state.session ||
      session.id !== this.options.sessionId ||
      session.runId !== this.options.runId
    )
      return false;
    const updated = mergeSessionActivity(this.state.session, session);
    if (updated === this.state.session) return false;
    this.publish({ ...this.state, session: updated });
    return true;
  }

  updateRunInfo(session: CodexSessionRecord) {
    if (
      this.disposed ||
      session.id !== this.options.sessionId ||
      session.runId !== this.options.runId
    )
      return;
    let updated = this.metadata
      ? mergeSessionMetadata(session, this.metadata)
      : session;
    const previous = this.state.session;
    if (previous) {
      updated = mergeSessionSnapshot(previous, updated);
      if (
        !sessionIsLive(previous) &&
        (sessionIsLive(updated) ||
          (previous.status !== 'stopping' && updated.status === 'stopping'))
      )
        updated = {
          ...updated,
          status: previous.status,
          reason: previous.reason,
          endedAt: previous.endedAt,
        };
    }
    this.metadata = updated;
    if (!sessionIsLive(updated)) {
      this.ended(updated);
      return;
    }
    this.publish({ ...this.state, session: updated });
  }

  async start() {
    if (this.started || this.disposed) return;
    this.started = true;
    await this.connect();
  }

  reconnect() {
    if (
      this.disposed ||
      this.state.phase === 'ended' ||
      this.state.retryable === false
    )
      return;
    this.attempts = 0;
    void this.connect();
  }

  resize(cols: number, rows: number) {
    this.cols = dimension(cols, 2, 500);
    this.rows = dimension(rows, 1, 200);
    this.resizePending = true;
    this.flushInput();
  }

  sendText(text: string) {
    return this.sendBytes(new TextEncoder().encode(text));
  }

  sendBinary(text: string) {
    return this.sendBytes(
      Uint8Array.from(text, (character) => character.charCodeAt(0) & 255),
    );
  }

  scroll(lines: number) {
    if (
      this.disposed ||
      this.state.phase !== 'connected' ||
      !this.socket ||
      !Number.isFinite(lines)
    )
      return false;
    try {
      this.socket.send(
        JSON.stringify({
          scroll: Math.max(-200, Math.min(200, Math.trunc(lines))),
        }),
      );
      return true;
    } catch {
      this.retry(
        'The connection was interrupted. Reconnecting to the same session…',
      );
      return false;
    }
  }

  ended(session: CodexSessionRecord) {
    if (
      session.id !== this.options.sessionId ||
      session.runId !== this.options.runId
    ) {
      this.fail(
        'The host returned a different Codex run. Open Sessions to select it explicitly.',
        false,
      );
      return;
    }
    this.closeConnection();
    const observed = publishSessionActivity(session);
    const snapshot = this.state.session
      ? mergeSessionSnapshot(this.state.session, observed)
      : observed;
    this.publish({
      phase: session.status === 'stopping' ? 'stopping' : 'ended',
      session: this.metadata
        ? mergeSessionMetadata(snapshot, this.metadata)
        : snapshot,
      message:
        session.reason ||
        (session.status === 'stopping'
          ? 'This session is stopping. Retry End Session if it remains active.'
          : 'This Codex session has ended.'),
      retryable: false,
    });
  }

  dispose() {
    if (this.disposed) return;
    this.disposed = true;
    this.closeConnection();
    this.clearOutput();
  }

  private publish(state: CodexConnectionState) {
    if (this.disposed) return;
    this.state = state;
    this.options.onState(state);
  }

  private closeConnection() {
    this.generation++;
    clearTimeout(this.timeout);
    clearTimeout(this.retryTimer);
    clearTimeout(this.inputTimer);
    this.abort?.abort();
    this.input = [];
    this.inputBytes = 0;
    this.resizePending = true;
    const socket = this.socket;
    this.socket = undefined;
    if (socket) {
      socket.onopen = socket.onmessage = socket.onerror = socket.onclose = null;
      if (socket.readyState < 2) socket.close(1000, 'View detached');
    }
  }

  private clearOutput() {
    this.outputGeneration++;
    this.output = [];
    this.outputBytes = 0;
    this.writing = false;
  }

  private async connect() {
    if (this.disposed) return;
    this.closeConnection();
    this.clearOutput();
    const generation = this.generation;
    const current = () => !this.disposed && generation === this.generation;
    const controller = new AbortController();
    this.abort = controller;
    this.publish({
      ...this.state,
      phase: this.attempts ? 'reconnecting' : 'connecting',
      retryable: true,
    });
    this.timeout = setTimeout(() => {
      if (current())
        this.retry(
          'The host did not respond. Reconnecting to the same session…',
        );
    }, 15_000);
    try {
      const received = await this.options.getSession(controller.signal);
      if (!current()) return;
      if (
        received.id !== this.options.sessionId ||
        received.runId !== this.options.runId
      ) {
        this.fail(
          'The host returned a different Codex run. Open Sessions to select it explicitly.',
          false,
        );
        return;
      }
      const observed = publishSessionActivity(received);
      let session = this.metadata
        ? mergeSessionMetadata(observed, this.metadata)
        : observed;
      if (this.state.session)
        session = mergeSessionSnapshot(this.state.session, session);
      this.metadata = session;
      if (!sessionIsLive(session)) {
        this.ended(session);
        return;
      }
      this.publish({ ...this.state, session });
      const url = new URL(
        `/api/host/codex/sessions/${encodeURIComponent(this.options.sessionId)}/runs/${encodeURIComponent(this.options.runId)}/terminal`,
        this.options.baseURL,
      );
      url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
      url.searchParams.set('cols', String(this.cols));
      url.searchParams.set('rows', String(this.rows));
      url.searchParams.set('databaseId', this.options.databaseId);
      this.options.reset();
      const socket = this.options.createSocket(url.href);
      this.socket = socket;
      socket.binaryType = 'arraybuffer';
      socket.onmessage = (event) => {
        if (current()) this.receive(event.data);
      };
      socket.onerror = () => {
        if (current())
          this.retry(
            'Connection interrupted. Reconnecting to the same session…',
          );
      };
      socket.onclose = (event) => {
        if (!current()) return;
        if (event.code === 1008)
          this.fail(
            event.reason || 'Access to this session was denied.',
            false,
          );
        else this.retry('Disconnected. Reconnecting to the same session…');
      };
    } catch (error) {
      if (!current()) return;
      const message =
        error instanceof Error
          ? error.message
          : 'Could not connect to host Codex.';
      if (
        error instanceof APIError &&
        error.status >= 400 &&
        error.status < 500
      )
        this.fail(message, false);
      else this.retry(message);
    }
  }

  private retry(message: string) {
    this.closeConnection();
    this.attempts++;
    if (this.attempts > 5) {
      this.publish({
        ...this.state,
        phase: 'error',
        message: `${message} Choose Reconnect to try again.`,
        retryable: true,
      });
      return;
    }
    this.publish({
      ...this.state,
      phase: 'reconnecting',
      message,
      retryable: true,
    });
    this.retryTimer = setTimeout(
      () => {
        void this.connect();
      },
      Math.min(1000 * 2 ** (this.attempts - 1), 15_000),
    );
  }

  private fail(message: string, retryable = true) {
    this.closeConnection();
    this.publish({ ...this.state, phase: 'error', message, retryable });
  }

  private receive(data: unknown) {
    if (typeof data === 'string') {
      try {
        const control: unknown = JSON.parse(data);
        if (!control || typeof control !== 'object') throw new Error();
        if ('error' in control && typeof control.error === 'string') {
          if ('code' in control && control.code === 'attach_failed') {
            this.retry(control.error);
            return;
          }
          this.fail(
            control.error,
            !(
              'code' in control &&
              /forbidden|not_found|ended|lost|access/.test(String(control.code))
            ),
          );
          return;
        }
        if ('ended' in control && control.ended === true) {
          this.closeConnection();
          this.publish({
            ...this.state,
            phase: 'ended',
            retryable: false,
            message:
              'message' in control && typeof control.message === 'string'
                ? control.message
                : 'This Codex session has ended.',
          });
          return;
        }
        if (
          'ready' in control &&
          control.ready === true &&
          this.state.phase !== 'connected'
        ) {
          clearTimeout(this.timeout);
          this.attempts = 0;
          this.publish({
            ...this.state,
            phase: 'connected',
            message: undefined,
            retryable: true,
          });
          this.flushInput();
          return;
        }
      } catch {
        /* Invalid controls never reach the terminal renderer. */
      }
      this.fail('The host sent an invalid terminal control message.');
      return;
    }
    if (!(data instanceof ArrayBuffer)) {
      this.fail('The host sent an invalid terminal output frame.');
      return;
    }
    if (this.outputBytes + data.byteLength > OUTPUT_LIMIT) {
      this.fail(
        'Output exceeded the browser buffer. The session is still running; choose Reconnect to restore its screen.',
      );
      return;
    }
    if (!data.byteLength) return;
    this.output.push(new Uint8Array(data));
    this.outputBytes += data.byteLength;
    this.flushOutput();
  }

  private flushOutput() {
    if (this.disposed || this.writing || !this.output.length) return;
    const generation = this.outputGeneration;
    const data = this.output.shift()!;
    this.writing = true;
    this.options.write(data, () => {
      if (this.disposed || generation !== this.outputGeneration) return;
      this.outputBytes -= data.byteLength;
      this.writing = false;
      this.flushOutput();
    });
  }

  private sendBytes(data: Uint8Array) {
    if (this.disposed || this.state.phase !== 'connected' || !this.socket)
      return false;
    if (
      this.inputBytes + this.socket.bufferedAmount + data.byteLength >
      INPUT_LIMIT
    ) {
      this.options.onInputRejected();
      return false;
    }
    for (let offset = 0; offset < data.byteLength; offset += 16 * 1024)
      this.input.push(data.slice(offset, offset + 16 * 1024));
    this.inputBytes += data.byteLength;
    this.flushInput();
    return true;
  }

  private flushInput() {
    const socket = this.socket;
    if (
      this.disposed ||
      this.state.phase !== 'connected' ||
      !socket ||
      socket.readyState !== 1
    )
      return;
    clearTimeout(this.inputTimer);
    try {
      if (socket.bufferedAmount < HIGH_WATER && this.resizePending) {
        socket.send(JSON.stringify({ resize: [this.cols, this.rows] }));
        this.resizePending = false;
      }
      while (this.input.length && socket.bufferedAmount < HIGH_WATER) {
        const data = this.input.shift()!;
        this.inputBytes -= data.byteLength;
        socket.send(data);
      }
      if (this.input.length || this.resizePending)
        this.inputTimer = setTimeout(() => this.flushInput(), 16);
    } catch {
      this.retry(
        'Input was interrupted. Unsent input was discarded; reconnecting to the same session…',
      );
    }
  }
}
