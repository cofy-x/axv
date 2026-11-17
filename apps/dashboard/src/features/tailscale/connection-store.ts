import { connectionAPI } from './api';
import type { ConnectionSnapshot } from './types';
import { SerialPoller } from '../../shared/api/serial-poller';

/** Read-only status polling while a connection view is interactive. */
export class ConnectionStore {
  private snapshot: ConnectionSnapshot = { loading: true };
  private readonly listeners = new Set<() => void>();
  private readonly poller: SerialPoller;

  constructor(
    private readonly api: Pick<typeof connectionAPI, 'status'> = connectionAPI,
  ) {
    this.poller = new SerialPoller({
      timeoutMs: 8000,
      intervalMs: 5000,
      onStart: () => this.publish({ loading: true }),
      read: async ({ signal, isCurrent }) => {
        const info = await this.api.status(signal);
        if (!isCurrent()) return;
        signal.throwIfAborted();
        this.publish({ info, error: undefined, updatedAt: Date.now() });
      },
      onError: (error, { signal }) =>
        this.publish({
          error: signal.aborted
            ? 'The connection status check timed out. Refresh to retry.'
            : error instanceof Error
              ? error.message
              : 'Could not check connection status.',
        }),
      onSettled: () => this.publish({ loading: false }),
    });
  }

  getSnapshot = () => this.snapshot;

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    if (this.listeners.size === 1) this.poller.start();
    return () => {
      this.listeners.delete(listener);
      if (this.listeners.size) return;
      this.poller.stop();
    };
  };

  refresh = () => {
    if (!this.listeners.size) return;
    this.poller.refresh();
  };

  private publish(update: Partial<ConnectionSnapshot>) {
    this.snapshot = { ...this.snapshot, ...update };
    for (const listener of this.listeners) listener();
  }
}
