export interface PollContext {
  signal: AbortSignal;
  isCurrent: () => boolean;
}

interface PollOptions {
  read: (context: PollContext) => Promise<void>;
  onStart?: () => void;
  onError: (error: unknown, context: PollContext) => void;
  onSettled?: () => void;
  intervalMs?: number | (() => number);
  timeoutMs: number;
}

type PollState =
  | { phase: 'stopped' | 'idle' }
  | { phase: 'waiting'; timer: ReturnType<typeof setTimeout> }
  | {
      phase: 'reading';
      controller: AbortController;
      refreshQueued: boolean;
    };

/** Owns read scheduling only; features retain data, identities and mutations. */
export class SerialPoller {
  private state: PollState = { phase: 'stopped' };

  constructor(private readonly options: PollOptions) {}

  start() {
    if (this.state.phase !== 'stopped') return;
    this.state = { phase: 'idle' };
    void this.run();
  }

  stop() {
    const previous = this.state;
    // Retire before aborting: abort listeners may immediately start new work.
    this.state = { phase: 'stopped' };
    if (previous.phase === 'waiting') clearTimeout(previous.timer);
    if (previous.phase === 'reading') previous.controller.abort();
  }

  refresh() {
    const state = this.state;
    if (state.phase === 'stopped') return;
    if (state.phase === 'reading') {
      state.refreshQueued = true;
      return;
    }
    if (state.phase === 'waiting') clearTimeout(state.timer);
    this.state = { phase: 'idle' };
    void this.run();
  }

  reschedule() {
    if (this.state.phase !== 'waiting') return;
    clearTimeout(this.state.timer);
    this.wait();
  }

  private wait() {
    const interval =
      typeof this.options.intervalMs === 'function'
        ? this.options.intervalMs()
        : this.options.intervalMs;
    if (!interval) {
      this.state = { phase: 'idle' };
      return;
    }
    const timer = setTimeout(() => {
      if (this.state.phase !== 'waiting' || this.state.timer !== timer) return;
      this.state = { phase: 'idle' };
      void this.run();
    }, interval);
    this.state = { phase: 'waiting', timer };
  }

  private async run() {
    if (this.state.phase !== 'idle') return;
    const reading = {
      phase: 'reading' as const,
      controller: new AbortController(),
      refreshQueued: false,
    };
    this.state = reading;
    const context: PollContext = {
      signal: AbortSignal.any([
        reading.controller.signal,
        AbortSignal.timeout(this.options.timeoutMs),
      ]),
      isCurrent: () =>
        this.state === reading && !reading.controller.signal.aborted,
    };
    try {
      this.options.onStart?.();
      if (!context.isCurrent()) return;
      await this.options.read(context);
      context.signal.throwIfAborted();
    } catch (error) {
      if (context.isCurrent()) this.options.onError(error, context);
    } finally {
      if (this.state === reading) {
        const idle: PollState = { phase: 'idle' };
        this.state = idle;
        reading.controller.abort();
        if (this.state === idle) this.options.onSettled?.();
        // Notifications can synchronously stop, restart or refresh this owner.
        // Only this exact idle transition may schedule its next read.
        if (this.state === idle) {
          if (reading.refreshQueued) void this.run();
          else this.wait();
        }
      }
    }
  }
}
