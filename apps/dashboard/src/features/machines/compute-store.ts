import { machinesApi } from './api';
import type {
  ComputeEnvironment,
  HostInfo,
  ImageInfo,
  Machine,
  Operation,
  Volume,
} from './api';

export interface ComputeSnapshot {
  environment?: ComputeEnvironment;
  info?: HostInfo;
  machines: Machine[];
  volumes: Volume[];
  operations: Operation[];
  images: ImageInfo[];
  loading: boolean;
  refreshing: boolean;
  error?: string;
  lastUpdated?: number;
}

type ComputeAPI = Pick<
  typeof machinesApi,
  'environment' | 'info' | 'machines' | 'volumes' | 'operations' | 'images'
>;

function connectionKey(environment?: ComputeEnvironment) {
  const runtime = environment?.runtime;
  return JSON.stringify([
    environment?.configured,
    runtime?.hostId,
    runtime?.generation,
    runtime?.state,
    runtime?.readOnly,
  ]);
}

/** One inventory and poller for every VM window in this browser. */
export class ComputeStore {
  private snapshot: ComputeSnapshot = {
    machines: [],
    volumes: [],
    operations: [],
    images: [],
    loading: true,
    refreshing: false,
  };
  private readonly listeners = new Set<() => void>();
  private timer?: ReturnType<typeof setTimeout>;
  private request?: AbortController;
  private probeNeeded = true;
  private refreshQueued = false;

  constructor(private readonly api: ComputeAPI = machinesApi) {}

  getSnapshot = () => this.snapshot;

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    if (this.listeners.size === 1) {
      this.probeNeeded = true;
      void this.load();
    }
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
    this.probeNeeded = true;
    if (!this.listeners.size) return;
    clearTimeout(this.timer);
    if (this.request) this.refreshQueued = true;
    else void this.load();
  };

  private publish(update: Partial<ComputeSnapshot>) {
    this.snapshot = { ...this.snapshot, ...update };
    for (const listener of this.listeners) listener();
  }

  private async load() {
    if (this.request || !this.listeners.size) return;
    const controller = new AbortController();
    this.request = controller;
    const signal = AbortSignal.any([
      controller.signal,
      AbortSignal.timeout(15000),
    ]);
    const current = () =>
      this.request === controller && !controller.signal.aborted;
    this.publish({ refreshing: true });
    let info = this.snapshot.info;
    try {
      // The environment is a cached runtime snapshot, not a provider probe.
      const environment = await this.api.environment(signal);
      if (!current()) return;
      signal.throwIfAborted();
      const binding = environment.runtime;
      if (!this.snapshot.lastUpdated) this.publish({ environment });
      if (
        this.probeNeeded ||
        !info ||
        connectionKey(environment) !== connectionKey(this.snapshot.environment)
      ) {
        this.probeNeeded = false;
        info = await this.api.info(signal, binding);
        if (!current()) return;
        signal.throwIfAborted();
      }
      if (binding?.hostId && info.configured && info.hostId !== binding.hostId)
        throw new Error(
          'The VM host changed while refreshing. Refresh this view to reconnect.',
        );
      if (!info.configured && this.snapshot.info?.configured)
        throw new Error(info.reason || 'The VM host is no longer configured.');
      let inventory: Partial<ComputeSnapshot> = {};
      if (info.configured) {
        const [machines, volumes, operations, images] = await Promise.all([
          this.api.machines(signal, binding),
          this.api.volumes(signal, binding),
          this.api.operations(signal, binding),
          info.providers?.some((provider) => provider.images)
            ? this.api.images(signal, binding)
            : Promise.resolve({ images: info.images || [] }),
        ]);
        if (!current()) return;
        signal.throwIfAborted();
        const hostId = info.hostId;
        if (
          hostId &&
          machines.machines.some((machine) => machine.hostId !== hostId)
        )
          throw new Error(
            'The VM inventory belongs to a different host. Refresh this view to reconnect.',
          );
        inventory = {
          ...machines,
          ...volumes,
          ...operations,
          ...images,
          info: {
            ...info,
            images: images.images.filter(
              (image) =>
                !image.status ||
                image.status === 'ready' ||
                image.status === 'configured',
            ),
          },
        };
      }
      const after = await this.api.environment(signal);
      if (!current()) return;
      signal.throwIfAborted();
      if (connectionKey(after) !== connectionKey(environment))
        throw new Error(
          'The VM service connection changed while refreshing. Refresh this view to reconnect.',
        );
      if (current())
        this.publish({
          environment: after,
          info,
          ...inventory,
          error: undefined,
          lastUpdated: Date.now(),
        });
    } catch (error) {
      if (this.request !== controller || controller.signal.aborted) return;
      this.publish({
        error: error instanceof Error ? error.message : String(error),
      });
    } finally {
      if (this.request === controller) {
        this.request = undefined;
        controller.abort();
        this.publish({ loading: false, refreshing: false });
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

export const computeStore = new ComputeStore();
