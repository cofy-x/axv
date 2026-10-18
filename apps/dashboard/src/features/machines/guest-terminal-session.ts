import { APIError } from '../../shared/api/http';
import type { TerminalState } from '../terminal/terminal-session';
import { machinesApi } from './api';

interface Options {
  hostId: string;
  machineId: string;
  terminalId: string;
  bootId: string;
  reset: () => Promise<void>;
  write: (data: Uint8Array, done: () => void) => void;
  onState: (state: TerminalState) => void;
  onInputRejected: () => void;
}

/** Attachments reconnect to one guest process; they never create a replacement. */
export class GuestTerminalSession {
  private state: TerminalState = { phase: 'connecting' };
  private socket?: WebSocket;
  private readonly abort = new AbortController();
  private started = false;
  private disposed = false;
  private attempt = 0;
  private generation = 0;
  private cols = 80;
  private rows = 24;
  private outputBytes = 0;
  private timer?: ReturnType<typeof setTimeout>;
  private deadline?: ReturnType<typeof setTimeout>;

  constructor(private readonly options: Options) {}

  getSnapshot() {
    return this.state;
  }

  async start() {
    if (this.started || this.disposed) return;
    this.started = true;
    if (!this.options.hostId) {
      this.fail(
        'This saved window has no verified VM host. Reopen the existing terminal from Virtual Machines.',
      );
      return;
    }
    await this.connect();
  }

  private publish(state: TerminalState) {
    if (this.disposed) return;
    this.state = state;
    this.options.onState(state);
  }

  private disconnect() {
    clearTimeout(this.deadline);
    this.generation++;
    const socket = this.socket;
    this.socket = undefined;
    if (socket) {
      socket.onopen = socket.onmessage = socket.onerror = socket.onclose = null;
      if (socket.readyState < 2) socket.close(1000, 'Attachment detached');
    }
  }

  private fail(message: string) {
    this.disconnect();
    this.publish({ phase: 'error', message });
  }

  private retry(message: string) {
    this.disconnect();
    if (this.disposed) return;
    if (++this.attempt > 8) {
      this.publish({
        phase: 'error',
        message: `${message} Choose Reconnect to try this same terminal again.`,
      });
      return;
    }
    this.publish({
      phase: 'connecting',
      message: `${message} Reconnecting to the same guest terminal…`,
    });
    this.timer = setTimeout(
      () => void this.connect(),
      Math.min(10000, 1000 * 2 ** (this.attempt - 1)),
    );
  }

  private async connect() {
    if (this.disposed) return;
    try {
      const info = await machinesApi.terminal(
        this.options.machineId,
        this.options.terminalId,
        this.options.hostId,
        AbortSignal.any([this.abort.signal, AbortSignal.timeout(10000)]),
      );
      if (this.disposed) return;
      if (info.bootId !== this.options.bootId) {
        this.fail(
          'The guest has restarted. This terminal cannot be recovered.',
        );
        return;
      }
      await this.options.reset();
      if (this.disposed) return;
      this.outputBytes = 0;
      const url = new URL(
        `/api/compute/machines/${encodeURIComponent(this.options.machineId)}/guest/terminals/${encodeURIComponent(this.options.terminalId)}/attach`,
        globalThis.location.href,
      );
      url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
      url.searchParams.set('hostId', this.options.hostId);
      const socket = new WebSocket(url);
      this.socket = socket;
      const generation = ++this.generation;
      socket.binaryType = 'arraybuffer';
      this.deadline = setTimeout(
        () => this.retry('Guest attachment timed out.'),
        15000,
      );
      socket.onmessage = (event) => {
        if (generation === this.generation) this.receive(event.data);
      };
      socket.onerror = () =>
        this.retry(
          'Guest attachment is unavailable or already open elsewhere.',
        );
      socket.onclose = () => this.retry('Guest connection was interrupted.');
    } catch (error) {
      if (this.disposed) return;
      const message =
        error instanceof Error ? error.message : 'Guest connection failed.';
      if (
        error instanceof APIError &&
        error.status >= 400 &&
        error.status < 500
      )
        this.fail(message);
      else this.retry(message);
    }
  }

  private receive(data: unknown) {
    if (typeof data === 'string') {
      try {
        const control = JSON.parse(data) as {
          ready?: boolean;
          bootId?: string;
          replayTruncated?: boolean;
          ended?: boolean;
        };
        if (control.ready && control.bootId === this.options.bootId) {
          clearTimeout(this.deadline);
          // Keep the retry count until a useful connection survives for a while.
          this.deadline = setTimeout(() => {
            this.attempt = 0;
          }, 30000);
          this.publish({
            phase: 'connected',
            message: control.replayTruncated
              ? 'Earlier output was dropped. Replaying the retained output tail.'
              : undefined,
          });
          this.resize(this.cols, this.rows);
          return;
        }
        if (control.ended) {
          this.disconnect();
          this.publish({
            phase: 'ended',
            message:
              'Guest terminal ended. Create another terminal from Virtual Machines.',
          });
          return;
        }
      } catch {
        /* Invalid control messages stop this attachment. */
      }
      this.fail('The guest sent an incompatible terminal message.');
      return;
    }
    if (
      !(data instanceof ArrayBuffer) ||
      this.outputBytes + data.byteLength > 4 * 1024 * 1024
    ) {
      this.fail(
        'Output exceeded the browser buffer. Reconnect to replay recent output. The guest process continues.',
      );
      return;
    }
    const generation = this.generation;
    this.outputBytes += data.byteLength;
    this.options.write(new Uint8Array(data), () => {
      if (generation === this.generation) this.outputBytes -= data.byteLength;
    });
  }

  resize(cols: number, rows: number) {
    this.cols = Math.max(2, Math.min(500, Math.floor(cols)));
    this.rows = Math.max(1, Math.min(200, Math.floor(rows)));
    if (
      this.state.phase === 'connected' &&
      this.socket?.readyState === WebSocket.OPEN
    ) {
      try {
        this.socket.send(JSON.stringify({ resize: [this.cols, this.rows] }));
      } catch {
        this.retry('Terminal resize could not be sent.');
      }
    }
  }

  sendText(text: string) {
    return this.sendBytes(new TextEncoder().encode(text));
  }

  sendBinary(text: string) {
    return this.sendBytes(
      Uint8Array.from(text, (character) => character.charCodeAt(0) & 255),
    );
  }

  private sendBytes(data: Uint8Array) {
    const socket = this.socket;
    if (
      this.disposed ||
      this.state.phase !== 'connected' ||
      socket?.readyState !== WebSocket.OPEN
    )
      return false;
    if (data.byteLength + socket.bufferedAmount > 256 * 1024) {
      this.options.onInputRejected();
      return false;
    }
    try {
      for (let offset = 0; offset < data.byteLength; offset += 16 * 1024)
        socket.send(data.slice(offset, offset + 16 * 1024));
      return true;
    } catch {
      this.retry(
        'Terminal input was interrupted; check which input reached the guest before retrying.',
      );
      return false;
    }
  }

  dispose() {
    this.disposed = true;
    this.abort.abort();
    clearTimeout(this.timer);
    this.disconnect();
  }
}
