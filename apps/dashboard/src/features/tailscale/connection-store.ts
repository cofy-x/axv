import { connectionAPI } from './api';
import type { ConnectionSnapshot } from './types';

/** Read-only status polling while a connection view is interactive. */
export class ConnectionStore {
  private snapshot: ConnectionSnapshot = { loading: true };
  private readonly listeners = new Set<() => void>();
  private timer?: ReturnType<typeof setTimeout>;
  private request?: AbortController;
  private refreshQueued = false;

  constructor(
    private readonly api: Pick<typeof connectionAPI, 'status'> = connectionAPI,
  ) {}

  getSnapshot = () => this.snapshot;

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    if (this.listeners.size === 1) void this.load();
    return () => {
      this.listeners.delete(listener);
      if (this.listeners.size) return;
      clearTimeout(this.timer);
      this.request?.abort();
      this.request = undefined;
      this.refreshQueued = false;
    };
  };

  refresh = () => {
    if (!this.listeners.size) return;
    clearTimeout(this.timer);
    if (this.request) this.refreshQueued = true;
    else void this.load();
  };

  private publish(update: Partial<ConnectionSnapshot>) {
    this.snapshot = { ...this.snapshot, ...update };
    for (const listener of this.listeners) listener();
  }

  private async load() {
    if (this.request || !this.listeners.size) return;
    const controller = new AbortController();
    this.request = controller;
    const signal = AbortSignal.any([
      controller.signal,
      AbortSignal.timeout(8000),
    ]);
    const current = () =>
      this.request === controller && !controller.signal.aborted;
    this.publish({ loading: true });
    try {
      const info = await this.api.status(signal);
      if (!current()) return;
      signal.throwIfAborted();
      this.publish({ info, error: undefined, updatedAt: Date.now() });
    } catch (error) {
      if (!current()) return;
      this.publish({
        error: signal.aborted
          ? 'The connection status check timed out. Refresh to retry.'
          : error instanceof Error
            ? error.message
            : 'Could not check connection status.',
      });
    } finally {
      if (this.request === controller) {
        this.request = undefined;
        controller.abort();
        this.publish({ loading: false });
        if (this.refreshQueued) {
          this.refreshQueued = false;
          void this.load();
        } else if (this.listeners.size) {
          this.timer = setTimeout(() => void this.load(), 5000);
        }
      }
    }
  }
}
