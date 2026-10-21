export interface TerminalInfo {
  supported: boolean;
  shell?: string;
  reason?: string;
}

export type TerminalPhase =
  'connecting' | 'connected' | 'ended' | 'error' | 'unsupported';

export interface TerminalState {
  phase: TerminalPhase;
  shell?: string;
  message?: string;
}

export type TerminalSocket = Pick<
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

interface SessionOptions {
  baseURL: string;
  projectPath?: string;
  getInfo: (signal: AbortSignal) => Promise<TerminalInfo>;
  createSocket: (url: string) => TerminalSocket;
  write: (data: Uint8Array, done: () => void) => void;
  onState: (state: TerminalState) => void;
  onInputRejected: () => void;
}

const MAX_INPUT = 1024 * 1024;
const MAX_OUTPUT = 4 * 1024 * 1024;
const INPUT_FRAME = 16 * 1024;
const SOCKET_HIGH_WATER = 64 * 1024;

function dimension(value: number, min: number, max: number) {
  return Math.max(
    min,
    Math.min(max, Number.isFinite(value) ? Math.floor(value) : min),
  );
}

/** One window owns one ephemeral PTY. A failed or ended session never reconnects. */
export class TerminalSession {
  private state: TerminalState = { phase: 'connecting' };
  private socket?: TerminalSocket;
  private readonly abort = new AbortController();
  private started = false;
  private disposed = false;
  private ready = false;
  private cols = 80;
  private rows = 24;
  private resizePending = false;
  private input: Uint8Array<ArrayBuffer>[] = [];
  private inputBytes = 0;
  private output: Uint8Array[] = [];
  private outputBytes = 0;
  private writing = false;
  private connectTimer?: ReturnType<typeof setTimeout>;
  private inputTimer?: ReturnType<typeof setTimeout>;

  constructor(private readonly options: SessionOptions) {}

  getSnapshot() {
    return this.state;
  }

  async start() {
    if (this.started || this.disposed) return;
    this.started = true;
    this.connectTimer = setTimeout(
      () =>
        this.fail(
          'The host shell did not respond. Choose New Session to try again.',
        ),
      15_000,
    );
    try {
      const info = await this.options.getInfo(this.abort.signal);
      if (this.disposed || this.state.phase !== 'connecting') return;
      if (!info.supported) {
        this.stopConnection();
        this.publish({
          phase: 'unsupported',
          message:
            info.reason || 'Host terminals are not supported on this server.',
        });
        return;
      }
      this.publish({ phase: 'connecting', shell: info.shell });
      const url = new URL('/api/terminal', this.options.baseURL);
      url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
      url.searchParams.set('cols', String(this.cols));
      url.searchParams.set('rows', String(this.rows));
      if (this.options.projectPath !== undefined)
        url.searchParams.set('projectPath', this.options.projectPath);
      const socket = this.options.createSocket(url.href);
      this.socket = socket;
      socket.binaryType = 'arraybuffer';
      socket.onmessage = (event) => this.receive(event.data);
      socket.onerror = () =>
        this.fail(
          'The connection to the host shell failed. Choose New Session to try again.',
        );
      socket.onclose = (event) => {
        if (this.disposed) return;
        const ended = this.ready && event.code === 1000;
        this.stopConnection();
        this.publish({
          ...this.state,
          phase: ended ? 'ended' : 'error',
          message: ended
            ? undefined
            : event.reason ||
              'The host shell disconnected. Choose New Session to try again.',
        });
      };
    } catch (error) {
      if (!this.disposed && !this.abort.signal.aborted)
        this.fail(
          error instanceof Error
            ? error.message
            : 'The host shell could not be started.',
        );
    }
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

  dispose() {
    if (this.disposed) return;
    this.disposed = true;
    this.stopConnection();
    this.output = [];
    this.outputBytes = 0;
  }

  private publish(state: TerminalState) {
    if (this.disposed) return;
    this.state = state;
    this.options.onState(state);
  }

  private fail(message: string) {
    if (this.disposed || this.state.phase === 'error') return;
    this.stopConnection();
    this.publish({ ...this.state, phase: 'error', message });
  }

  private stopConnection() {
    clearTimeout(this.connectTimer);
    clearTimeout(this.inputTimer);
    this.inputTimer = undefined;
    this.abort.abort();
    this.ready = false;
    this.input = [];
    this.inputBytes = 0;
    const socket = this.socket;
    this.socket = undefined;
    if (socket) {
      socket.onopen = socket.onmessage = socket.onerror = socket.onclose = null;
      if (socket.readyState < 2) socket.close(1000, 'Session closed');
    }
  }

  private receive(data: unknown) {
    if (this.disposed) return;
    if (typeof data === 'string') {
      try {
        const control: unknown = JSON.parse(data);
        if (!control || typeof control !== 'object')
          throw new Error('Invalid control');
        if ('error' in control && typeof control.error === 'string') {
          this.fail(control.error);
          return;
        }
        if ('ready' in control && control.ready === true && !this.ready) {
          clearTimeout(this.connectTimer);
          this.ready = true;
          this.publish({
            ...this.state,
            phase: 'connected',
            message: undefined,
          });
          this.flushInput();
          return;
        }
      } catch {
        this.fail('The terminal server sent an invalid message.');
        return;
      }
      this.fail('The terminal server sent an unexpected message.');
      return;
    }
    if (!(data instanceof ArrayBuffer)) {
      this.fail('The terminal server sent an invalid output frame.');
      return;
    }
    if (this.outputBytes + data.byteLength > MAX_OUTPUT) {
      this.fail(
        'The shell produced more output than this browser could display. The session was ended.',
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
    const data = this.output.shift()!;
    this.writing = true;
    this.options.write(data, () => {
      if (this.disposed) return;
      this.outputBytes -= data.byteLength;
      this.writing = false;
      this.flushOutput();
    });
  }

  private sendBytes(data: Uint8Array) {
    if (this.disposed || !this.ready || !this.socket) return false;
    if (
      this.inputBytes + this.socket.bufferedAmount + data.byteLength >
      MAX_INPUT
    ) {
      this.options.onInputRejected();
      return false;
    }
    for (let offset = 0; offset < data.byteLength; offset += INPUT_FRAME)
      this.input.push(data.slice(offset, offset + INPUT_FRAME));
    this.inputBytes += data.byteLength;
    this.flushInput();
    return true;
  }

  private flushInput() {
    const socket = this.socket;
    if (this.disposed || !this.ready || !socket || socket.readyState !== 1)
      return;
    clearTimeout(this.inputTimer);
    this.inputTimer = undefined;
    try {
      if (socket.bufferedAmount < SOCKET_HIGH_WATER && this.resizePending) {
        socket.send(JSON.stringify({ resize: [this.cols, this.rows] }));
        this.resizePending = false;
      }
      while (this.input.length && socket.bufferedAmount < SOCKET_HIGH_WATER) {
        const data = this.input.shift()!;
        this.inputBytes -= data.byteLength;
        socket.send(data);
      }
      if (this.input.length || this.resizePending)
        this.inputTimer = setTimeout(() => this.flushInput(), 16);
    } catch {
      this.fail(
        'Input could not be sent to the host shell. The session was ended.',
      );
    }
  }
}
