import { machinesApi } from './api';
import type {
  PreparationCheck,
  PreparationJob,
  PreparationPlan,
  PreparationRequest,
} from './api';
import { computeStore } from './compute-store';
import { OperationRequest, operationError } from './operation-requests';

type PreparationAPI = Pick<
  typeof machinesApi,
  | 'preparationCheck'
  | 'preparationJob'
  | 'preparationPlan'
  | 'startPreparation'
  | 'preparationAction'
>;

type PreparationMutation =
  | { kind: 'start'; planId: string; instanceId: string }
  | {
      kind: 'retry' | 'cancel';
      jobId: string;
      revision: number;
      instanceId: string;
    };

export interface PreparationSnapshot {
  check?: PreparationCheck;
  job?: PreparationJob;
  loading: boolean;
  refreshing: boolean;
  connectionError?: string;
  mutation?: 'start' | 'retry' | 'cancel';
  error?: string;
  pending?: { label: string; kind: PreparationMutation['kind'] };
}

export function preparationRunning(job?: PreparationJob) {
  return job?.status === 'queued' || job?.status === 'running';
}

/** Preparation continues on the server; views share one job and never own it. */
export class PreparationStore {
  private snapshot: PreparationSnapshot = { loading: true, refreshing: false };
  private readonly listeners = new Set<() => void>();
  private timer?: ReturnType<typeof setTimeout>;
  private request?: AbortController;
  private refreshQueued = false;
  private checkNeeded = true;
  private mutationFlight?: Promise<void>;
  private readonly mutations: OperationRequest<
    PreparationMutation,
    PreparationJob
  >;

  constructor(
    private readonly api: PreparationAPI = machinesApi,
    private readonly onChanged: () => void = () => {},
  ) {
    this.mutations = new OperationRequest((input, id) =>
      input.kind === 'start'
        ? this.api.startPreparation(input.planId, id, input.instanceId)
        : this.api.preparationAction(
            input.jobId,
            input.kind,
            input.revision,
            id,
            input.instanceId,
          ),
    );
  }

  getSnapshot = () => this.snapshot;

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    if (this.listeners.size === 1) {
      this.checkNeeded = true;
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
    this.checkNeeded = true;
    if (!this.listeners.size) return;
    clearTimeout(this.timer);
    if (this.request) this.refreshQueued = true;
    else void this.load();
  };

  review = (
    request: PreparationRequest,
    signal?: AbortSignal,
  ): Promise<PreparationPlan> => {
    const instance = this.snapshot.check?.instanceId;
    if (!instance)
      return Promise.reject(
        new Error('Check the VM environment before reviewing preparation.'),
      );
    return this.api.preparationPlan(structuredClone(request), instance, signal);
  };

  start = (plan: PreparationPlan) =>
    this.mutate({
      kind: 'start',
      planId: plan.id,
      instanceId: this.snapshot.check?.instanceId || '',
    });

  retry = (job: PreparationJob) =>
    this.mutate({
      kind: 'retry',
      jobId: job.id,
      revision: job.revision,
      instanceId: this.snapshot.check?.instanceId || '',
    });

  cancel = (job: PreparationJob) =>
    this.mutate({
      kind: 'cancel',
      jobId: job.id,
      revision: job.revision,
      instanceId: this.snapshot.check?.instanceId || '',
    });

  resolve = () => this.mutate();

  private publish(update: Partial<PreparationSnapshot>) {
    this.snapshot = { ...this.snapshot, ...update };
    for (const listener of this.listeners) listener();
  }

  private accept(job?: PreparationJob) {
    const previous = this.snapshot.job;
    if (job && previous?.id === job.id && previous.revision > job.revision)
      return;
    this.publish({ job });
    if (
      job &&
      !preparationRunning(job) &&
      (previous?.id !== job.id || previous.revision !== job.revision)
    )
      this.onChanged();
  }

  private mutate(input?: PreparationMutation): Promise<void> {
    if (this.mutationFlight) return this.mutationFlight;
    const original = input || this.mutations.pending?.input;
    if (!original?.instanceId) {
      this.publish({ error: 'Check the VM environment before preparing it.' });
      return Promise.resolve();
    }
    const label =
      original.kind === 'start'
        ? 'Start preparation'
        : original.kind === 'retry'
          ? 'Retry preparation'
          : 'Cancel preparation';
    clearTimeout(this.timer);
    this.request?.abort();
    this.request = undefined;
    this.publish({
      mutation: original.kind,
      error: undefined,
      refreshing: false,
    });
    this.mutationFlight = (
      input ? this.mutations.submit(input, label) : this.mutations.retry()
    )
      .then((job) => {
        this.accept(job);
        this.onChanged();
      })
      .catch((error: unknown) => {
        this.publish({ error: operationError(error) });
      })
      .finally(() => {
        const pending = this.mutations.pending;
        this.publish({
          mutation: undefined,
          pending: pending
            ? { label: pending.label, kind: pending.input.kind }
            : undefined,
        });
        this.mutationFlight = undefined;
        this.refresh();
      });
    return this.mutationFlight;
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
    try {
      const job = this.snapshot.job;
      if (!this.checkNeeded && preparationRunning(job)) {
        const result = await this.api.preparationJob(job!.id, signal);
        if (!current()) return;
        signal.throwIfAborted();
        this.accept(result);
        if (!preparationRunning(result)) {
          this.checkNeeded = true;
          this.refreshQueued = true;
        }
      } else {
        const check = await this.api.preparationCheck(signal);
        if (!current()) return;
        signal.throwIfAborted();
        this.checkNeeded = false;
        this.publish({ check });
        this.accept(check.activeJob || check.latestJob);
      }
      this.publish({ connectionError: undefined });
    } catch (error) {
      if (!current()) return;
      this.publish({ connectionError: operationError(error) });
    } finally {
      if (this.request === controller) {
        this.request = undefined;
        controller.abort();
        this.publish({ loading: false, refreshing: false });
        if (this.refreshQueued) {
          this.refreshQueued = false;
          void this.load();
        } else if (this.listeners.size)
          this.timer = setTimeout(
            () => void this.load(),
            preparationRunning(this.snapshot.job) &&
              !this.snapshot.connectionError
              ? 1000
              : 5000,
          );
      }
    }
  }
}

export const preparationStore = new PreparationStore(
  machinesApi,
  computeStore.refresh,
);
