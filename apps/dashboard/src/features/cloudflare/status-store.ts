import { cloudflareAPI } from './api';
import type { CloudflareSnapshot } from './types';
import { SerialPoller } from '../../shared/api/serial-poller';

/** The utility owns read-only polling only while its content is interactive. */
export class CloudflareStore {
  private snapshot: CloudflareSnapshot = { loading: true };
  private readonly listeners = new Set<() => void>();
  private readonly poller: SerialPoller;

  constructor(
    private readonly api: Pick<typeof cloudflareAPI, 'status'> = cloudflareAPI,
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
            ? 'The Cloudflare status check timed out. Refresh to retry.'
            : error instanceof Error
              ? error.message
              : 'Could not check Cloudflare status.',
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

  private publish(update: Partial<CloudflareSnapshot>) {
    this.snapshot = { ...this.snapshot, ...update };
    for (const listener of this.listeners) listener();
  }
}
