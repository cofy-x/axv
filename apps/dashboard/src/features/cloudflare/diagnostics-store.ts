import { cloudflareAPI } from './api';
import type { CloudflareStatus } from './types';
import { SerialPoller } from '../../shared/api/serial-poller';

interface DiagnosticsSnapshot {
  status?: CloudflareStatus;
  error?: string;
  loading: boolean;
}

/** Local diagnostics finish each read before polling again. */
export class CloudflareDiagnosticsStore {
  private snapshot: DiagnosticsSnapshot = { loading: true };
  private readonly listeners = new Set<() => void>();
  private readonly poller: SerialPoller;

  constructor(
    private readonly read: (
      signal?: AbortSignal,
    ) => Promise<CloudflareStatus> = (signal) =>
      cloudflareAPI.localStatus({ local: true }, signal),
  ) {
    this.poller = new SerialPoller({
      timeoutMs: 8000,
      intervalMs: 5000,
      onStart: () => this.publish({ loading: true }),
      read: async ({ signal, isCurrent }) => {
        const status = await this.read(signal);
        if (!isCurrent()) return;
        signal.throwIfAborted();
        this.publish({ status, error: undefined });
      },
      onError: (error, { signal }) =>
        this.publish({
          error: signal.aborted
            ? 'The Cloudflare diagnostics check timed out. Refresh to retry.'
            : error instanceof Error
              ? error.message
              : 'Could not load diagnostics. Refresh to retry.',
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

  private publish(update: Partial<DiagnosticsSnapshot>) {
    this.snapshot = { ...this.snapshot, ...update };
    for (const listener of this.listeners) listener();
  }
}
