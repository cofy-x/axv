import { machinesApi } from './api';
import { SerialPoller } from '../../shared/api/serial-poller';
import type { PollContext } from '../../shared/api/serial-poller';
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

export type DesktopComputeSnapshot = Pick<
  ComputeSnapshot,
  'machines' | 'info' | 'error'
>;

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

function sameDesktopInventory(
  previous: DesktopComputeSnapshot,
  next: ComputeSnapshot,
) {
  return (
    previous.info?.hostId === next.info?.hostId &&
    !!previous.error === !!next.error &&
    previous.machines.length === next.machines.length &&
    previous.machines.every((item, index) => {
      const machine = next.machines[index];
      return (
        item.id === machine.id &&
        item.hostId === machine.hostId &&
        item.status === machine.status &&
        item.ready === machine.ready &&
        item.spec?.name === machine.spec?.name &&
        item.createdAt === machine.createdAt
      );
    })
  );
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
  private desktopSnapshot: DesktopComputeSnapshot = { machines: [] };
  private readonly listeners = new Set<() => void>();
  private readonly desktopListeners = new Set<() => void>();
  private readonly poller: SerialPoller;
  private pendingProbe: symbol | undefined = Symbol();

  constructor(private readonly api: ComputeAPI = machinesApi) {
    this.poller = new SerialPoller({
      timeoutMs: 15000,
      intervalMs: () => this.pollInterval(),
      onStart: () => this.publish({ refreshing: true }),
      read: (context) => this.load(context),
      onError: (error) =>
        this.publish({
          error: error instanceof Error ? error.message : String(error),
        }),
      onSettled: () => this.publish({ loading: false, refreshing: false }),
    });
  }

  getSnapshot = () => this.snapshot;

  getDesktopSnapshot = () => this.desktopSnapshot;

  subscribe = (listener: () => void) => this.listen(this.listeners, listener);

  subscribeDesktop = (listener: () => void) =>
    this.listen(this.desktopListeners, listener);

  private get observed() {
    return this.listeners.size > 0 || this.desktopListeners.size > 0;
  }

  private listen(set: Set<() => void>, listener: () => void) {
    const first = !this.observed;
    set.add(listener);
    if (first) {
      this.pendingProbe = Symbol();
      this.poller.start();
    } else if (set === this.listeners && set.size === 1) this.refresh();
    return () => {
      if (!set.delete(listener)) return;
      if (!this.observed) {
        this.poller.stop();
        if (this.snapshot.refreshing) this.publish({ refreshing: false });
      } else if (set === this.listeners && !set.size) this.poller.reschedule();
    };
  }

  private pollInterval() {
    if (!this.listeners.size) return 30_000;
    const { error, machines, operations } = this.snapshot;
    const transitioning =
      !error &&
      (operations.some(
        (op) => op.status === 'queued' || op.status === 'running',
      ) ||
        machines.some(
          (vm) =>
            vm.status === 'running' &&
            !vm.ready &&
            vm.guestState === 'unavailable' &&
            Date.now() - Date.parse(vm.updatedAt) < 30_000,
        ));
    return transitioning ? 1000 : 5000;
  }

  refresh = () => {
    this.pendingProbe = Symbol();
    this.poller.refresh();
  };

  receiveMachineOperation(machine: Machine, operation: Operation) {
    const { info, machines, operations } = this.snapshot;
    if (info?.hostId !== machine.hostId) return;
    const observed = machines.find((item) => item.id === machine.id);
    if (
      operation.machineId !== machine.id ||
      operations.some((item) => item.id === operation.id) ||
      (observed && observed.revision > machine.revision)
    ) {
      this.refresh();
      return;
    }
    // A read issued before acknowledgement must not hide the accepted work.
    this.poller.stop();
    this.pendingProbe = Symbol();
    this.publish({
      machines:
        operation.action === 'create' && !observed
          ? [...machines, machine]
          : machines,
      operations: [operation, ...operations].sort(
        (a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt),
      ),
    });
    if (this.observed) this.poller.start();
  }

  private publish(update: Partial<ComputeSnapshot>) {
    this.snapshot = { ...this.snapshot, ...update };
    if (!sameDesktopInventory(this.desktopSnapshot, this.snapshot)) {
      const { machines, info, error } = this.snapshot;
      this.desktopSnapshot = { machines, info, error };
      for (const listener of this.desktopListeners) listener();
    }
    for (const listener of this.listeners) listener();
  }

  private async load({ signal, isCurrent: current }: PollContext) {
    let info = this.snapshot.info;
    // The environment is a cached runtime snapshot, not a provider probe.
    const environment = await this.api.environment(signal);
    if (!current()) return;
    signal.throwIfAborted();
    const binding = environment.runtime;
    if (!this.snapshot.lastUpdated) this.publish({ environment });
    if (!current()) return;
    let probe = this.pendingProbe;
    if (
      probe ||
      !info ||
      connectionKey(environment) !== connectionKey(this.snapshot.environment)
    ) {
      probe ??= Symbol();
      this.pendingProbe = probe;
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
    if (current()) {
      // Keep failed probes and newer refreshes pending until their own snapshot is verified.
      if (this.pendingProbe === probe) this.pendingProbe = undefined;
      this.publish({
        environment: after,
        info,
        ...inventory,
        error: undefined,
        lastUpdated: Date.now(),
      });
    }
  }
}

export const computeStore = new ComputeStore();
