import { cloudflareAPI } from './api';
import type { CloudflareStatus } from './types';

interface DiagnosticsSnapshot {
  status?: CloudflareStatus;
  error?: string;
  loading: boolean;
}

/** Local diagnostics finish each read before polling again. */
export class CloudflareDiagnosticsStore {
  private snapshot: DiagnosticsSnapshot = { loading: true };
  private readonly listeners = new Set<() => void>();
  private timer?: ReturnType<typeof setTimeout>;
  private request?: AbortController;
  private refreshQueued = false;

  constructor(
    private readonly read: (
      signal?: AbortSignal,
    ) => Promise<CloudflareStatus> = (signal) =>
      cloudflareAPI.localStatus({ local: true }, signal),
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

  private publish(update: Partial<DiagnosticsSnapshot>) {
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
      const status = await this.read(signal);
      if (!current()) return;
      signal.throwIfAborted();
      this.publish({ status, error: undefined });
    } catch (error) {
      if (!current()) return;
      this.publish({
        error: signal.aborted
          ? 'The Cloudflare diagnostics check timed out. Refresh to retry.'
          : error instanceof Error
            ? error.message
            : 'Could not load diagnostics. Refresh to retry.',
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
