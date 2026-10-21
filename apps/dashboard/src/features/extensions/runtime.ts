import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { APIError } from '../../shared/api/http';
import { extensionsApi } from './api';
import type { ExtensionData, ExtensionDraft, ExtensionRecord } from './types';

const maxDataBytes = 128 * 1024;
const message = (error: unknown) =>
  error instanceof Error ? error.message : String(error);

export function cloneExtensionValue(value: unknown): unknown {
  const raw = JSON.stringify(value);
  if (raw === undefined || new TextEncoder().encode(raw).length > maxDataBytes)
    throw new Error('Application data must be JSON no larger than 128 KiB.');
  return JSON.parse(raw) as unknown;
}

export function draftKey(storeId: string, clientId: string, windowId: string) {
  return `axv-extension-draft:${storeId}:${clientId}:${windowId}`;
}

function readDraft(key: string): ExtensionDraft | undefined {
  const raw = localStorage.getItem(key);
  if (!raw || raw === 'null') return;
  const value = JSON.parse(raw) as ExtensionDraft;
  if (
    !value ||
    !Number.isSafeInteger(value.expectedRevision) ||
    value.expectedRevision < 0 ||
    typeof value.digest !== 'string'
  )
    throw new Error('The recovered application draft cannot be read.');
  return { ...value, value: cloneExtensionValue(value.value) };
}

interface RuntimeSnapshot {
  phase: 'initial' | 'recovery' | 'loading' | 'ready' | 'error';
  contentUrl: string;
  epoch: number;
  digest: string;
  generation: number;
  version: string;
  draft?: ExtensionDraft;
  saving: boolean;
  error: string;
  recoveryError: boolean;
  rejectedDraft: string;
}

type RuntimeAPI = Pick<typeof extensionsApi, 'session' | 'rpc' | 'release'>;
const runtimes = new Map<string, ExtensionRuntime>();

/** A window owns its port, exact installed version and independent recovery draft. */
export class ExtensionRuntime {
  private snapshot: RuntimeSnapshot = {
    phase: 'initial',
    contentUrl: '',
    epoch: 0,
    digest: '',
    generation: 0,
    version: '',
    saving: false,
    error: '',
    recoveryError: false,
    rejectedDraft: '',
  };
  private listeners = new Set<() => void>();
  private sessionId = '';
  private generation = 0;
  private port?: MessagePort;
  private timer?: ReturnType<typeof setTimeout>;
  private connected = false;
  private ready = false;
  private lifecycle = { visible: false, interactive: false };
  private baseline?: ExtensionData;
  private saving?: Promise<void>;
  private disposed = false;
  private requests = new Set<string>();
  private recovered = false;
  private rejectedValue?: {
    value: unknown;
    expectedRevision: unknown;
    digest: string;
  };

  constructor(
    readonly appId: string,
    readonly windowId: string,
    private storeId: string,
    private clientId: string,
    private key: string,
    private api: RuntimeAPI = extensionsApi,
    private recoveryKey?: string,
  ) {
    try {
      const draft =
        readDraft(key) ??
        (localStorage.getItem(key) === null && recoveryKey
          ? readDraft(recoveryKey)
          : undefined);
      if (draft) this.snapshot = { ...this.snapshot, phase: 'recovery', draft };
    } catch (error) {
      this.snapshot = {
        ...this.snapshot,
        phase: 'recovery',
        error: `${message(error)} Export or discard the stored draft to continue.`,
        recoveryError: true,
      };
    }
  }

  getSnapshot = () => this.snapshot;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };
  register() {
    this.disposed = false;
    runtimes.set(this.windowId, this);
    return () => {
      if (runtimes.get(this.windowId) === this) runtimes.delete(this.windowId);
      this.dispose();
    };
  }
  private publish(patch: Partial<RuntimeSnapshot>) {
    this.snapshot = { ...this.snapshot, ...patch };
    this.listeners.forEach((listener) => listener());
  }
  private post(value: unknown) {
    if (this.ready) this.port?.postMessage(value);
  }
  setLifecycle(visible: boolean, interactive: boolean) {
    this.lifecycle = { visible, interactive };
    this.post({ type: 'lifecycle', ...this.lifecycle });
  }
  hasUnsaved() {
    return (
      !!this.snapshot.draft ||
      this.snapshot.recoveryError ||
      !!this.snapshot.rejectedDraft ||
      !!this.saving
    );
  }
  private saveDraft(draft: ExtensionDraft | undefined) {
    // Update memory first: a browser storage failure must still protect typing.
    this.publish({ draft });
    if (draft) localStorage.setItem(this.key, JSON.stringify(draft));
    else if (this.recoveryKey) localStorage.setItem(this.key, 'null');
    else localStorage.removeItem(this.key);
  }
  setDraft(value: unknown, expectedRevision: number) {
    let next: unknown;
    try {
      if (!Number.isSafeInteger(expectedRevision) || expectedRevision < 0)
        throw new Error('A valid saved revision is required.');
      next = cloneExtensionValue(value);
    } catch (error) {
      this.rejectDraft(error, value, expectedRevision);
      throw error;
    }
    const clean =
      !this.saving &&
      this.baseline?.revision === expectedRevision &&
      JSON.stringify(this.baseline.value) === JSON.stringify(next);
    this.rejectedValue = undefined;
    this.publish({ rejectedDraft: '' });
    this.saveDraft(
      clean
        ? undefined
        : { value: next, expectedRevision, digest: this.snapshot.digest },
    );
  }
  discard() {
    if (this.recoveryKey) localStorage.setItem(this.key, 'null');
    else localStorage.removeItem(this.key);
    this.rejectedValue = undefined;
    this.publish({
      draft: undefined,
      recoveryError: false,
      rejectedDraft: '',
      error: '',
    });
    if (this.baseline) this.post({ type: 'discarded', ...this.baseline });
  }
  exportDraft() {
    if (this.snapshot.rejectedDraft) {
      if (
        !this.rejectedValue ||
        JSON.stringify(this.rejectedValue.value) === undefined
      )
        throw new Error('The rejected draft cannot be exported as JSON.');
      return JSON.stringify(this.rejectedValue);
    }
    return this.snapshot.draft
      ? JSON.stringify(this.snapshot.draft)
      : (localStorage.getItem(this.key) ??
          (this.recoveryKey ? localStorage.getItem(this.recoveryKey) : null));
  }
  async start(app: ExtensionRecord, restore = false) {
    if (!app.installed || !app.enabled)
      throw new Error(
        'Enable this application in Applications before opening it.',
      );
    if (this.snapshot.recoveryError)
      throw new Error('Export or discard the unreadable draft before opening.');
    if (this.snapshot.rejectedDraft)
      throw new Error(
        'Correct or explicitly discard the rejected draft before reloading.',
      );
    const current = ++this.generation;
    this.closePort();
    this.releaseSession();
    this.saving = undefined;
    this.recovered = restore;
    this.baseline = undefined;
    this.publish({
      phase: 'loading',
      saving: false,
      error: '',
      contentUrl: '',
      digest: app.digest,
      generation: app.generation,
      version: app.version,
      epoch: this.snapshot.epoch + 1,
    });
    try {
      const session = await this.api.session(
        app,
        this.storeId,
        this.clientId,
        this.windowId,
      );
      if (this.disposed || current !== this.generation) {
        void this.api
          .release(this.storeId, this.appId, session.sessionId)
          .catch(() => {
            // Session expiry also releases abandoned sessions after disconnection.
          });
        return;
      }
      this.sessionId = session.sessionId;
      const url = new URL(session.contentUrl, location.origin);
      const expected = `/api/extensions/${encodeURIComponent(this.appId)}/content/${encodeURIComponent(this.storeId)}/${app.digest}`;
      if (
        url.origin !== location.origin ||
        url.pathname !== expected ||
        url.search ||
        url.hash
      )
        throw new Error(
          'The application returned an unexpected content address.',
        );
      this.publish({ contentUrl: url.pathname });
    } catch (error) {
      if (!this.disposed && current === this.generation) {
        this.releaseSession();
        this.publish({ phase: 'error', error: message(error) });
      }
    }
  }
  connect(frame: Window, focus: () => void, expectedEpoch: number) {
    if (this.disposed || expectedEpoch !== this.snapshot.epoch) return;
    if (this.connected || !this.sessionId) {
      this.fail('The application navigated away. Reload it to reconnect.');
      return;
    }
    this.connected = true;
    const nonce = crypto.randomUUID();
    const channel = new MessageChannel();
    this.port = channel.port1;
    const epoch = this.generation;
    channel.port1.onmessage = (event: MessageEvent<unknown>) => {
      if (this.disposed || epoch !== this.generation) return;
      const data = event.data;
      if (!data || typeof data !== 'object') return;
      const input = data as Record<string, unknown>;
      if (!this.ready) {
        if (input.type !== 'ready' || input.nonce !== nonce) return;
        clearTimeout(this.timer);
        this.ready = true;
        this.publish({ phase: 'ready' });
        this.post({
          type: 'init',
          ...this.lifecycle,
          ...(this.recovered && this.snapshot.draft
            ? { draft: this.snapshot.draft }
            : {}),
        });
        return;
      }
      if (
        input.type !== 'request' ||
        typeof input.id !== 'string' ||
        !/^[A-Za-z0-9._-]{1,64}$/.test(input.id) ||
        typeof input.method !== 'string' ||
        this.requests.has(input.id)
      )
        return;
      const id = input.id;
      if (this.requests.size >= 128) {
        this.post({
          type: 'response',
          id,
          error: { code: 'busy', message: 'Too many pending requests.' },
        });
        return;
      }
      this.requests.add(id);
      void this.request(input.method, input.params, focus)
        .then((result) => {
          if (epoch === this.generation)
            this.post({ type: 'response', id, result });
        })
        .catch((error: unknown) => {
          if (epoch !== this.generation) return;
          this.publish({ error: message(error) });
          this.post({
            type: 'response',
            id,
            error: {
              code: error instanceof APIError ? error.code : 'extension_error',
              message: message(error),
            },
          });
        })
        .finally(() => {
          if (epoch === this.generation) this.requests.delete(id);
        });
    };
    channel.port1.start();
    // Opaque origins have no targetOrigin. Only this captured frame receives the port.
    frame.postMessage({ axv: 'connect', version: 1, nonce }, '*', [
      channel.port2,
    ]);
    this.timer = setTimeout(
      () => this.fail('The application did not connect. Reload to retry.'),
      10_000,
    );
  }
  private async request(method: string, params: unknown, focus: () => void) {
    if (
      (method === 'window.focus' || method === 'storage.get') &&
      params !== undefined
    )
      throw new Error('This application method does not accept parameters.');
    if (method === 'window.focus') {
      if (this.lifecycle.visible && this.lifecycle.interactive) focus();
      return { ok: true };
    }
    if (method === 'storage.get') {
      const current = this.generation;
      const session = this.sessionId;
      const value = await this.api.rpc(
        this.storeId,
        this.appId,
        session,
        method,
      );
      if (
        this.disposed ||
        current !== this.generation ||
        session !== this.sessionId
      )
        throw new Error('This application connection was replaced.');
      if (!this.baseline || value.revision >= this.baseline.revision)
        this.baseline = value;
      return this.baseline;
    }
    if (method !== 'draft.set' && method !== 'storage.set')
      throw new Error('This application method is not available.');
    if (method === 'storage.set' && this.saving)
      throw new Error(
        'A save is already in progress. Wait before saving another snapshot.',
      );
    let input: Record<string, unknown>;
    try {
      if (!params || typeof params !== 'object')
        throw new Error('Provide application data and its saved revision.');
      input = params as Record<string, unknown>;
      if (
        Object.keys(input).some(
          (key) => key !== 'value' && key !== 'expectedRevision',
        )
      )
        throw new Error(
          'Provide only application data and its saved revision.',
        );
      if (
        !Number.isSafeInteger(input.expectedRevision) ||
        (input.expectedRevision as number) < 0
      )
        throw new Error('A valid saved revision is required.');
    } catch (error) {
      const rejected = params as Record<string, unknown> | null;
      this.rejectDraft(error, rejected?.value, rejected?.expectedRevision);
      throw error;
    }
    const expectedRevision = input.expectedRevision as number;
    if (method === 'draft.set') {
      this.setDraft(input.value, expectedRevision);
      return { ok: true };
    }
    let value: unknown;
    try {
      value = cloneExtensionValue(input.value);
    } catch (error) {
      this.rejectDraft(error, input.value, expectedRevision);
      throw error;
    }
    // A save names an explicit snapshot. Later draft messages may already have
    // arrived, so never replace their recovery copy with this older snapshot.
    if (!this.snapshot.draft) this.setDraft(value, expectedRevision);
    await this.save({ value, expectedRevision });
    return this.baseline;
  }
  async save(
    snapshot?: Pick<ExtensionDraft, 'value' | 'expectedRevision'>,
  ): Promise<void> {
    if (this.snapshot.rejectedDraft)
      throw new Error(
        'The latest draft was rejected and is still in the application. Correct it there, or explicitly discard it before continuing.',
      );
    if (this.saving) {
      return this.saving;
    }
    const draft = snapshot ?? this.snapshot.draft;
    if (!draft) return;
    if (!this.sessionId || !this.ready)
      throw new Error(
        'Reconnect the application before saving. Its draft is retained.',
      );
    const current = this.generation;
    const sessionId = this.sessionId;
    this.publish({ saving: true, error: '' });
    const operation = Promise.resolve().then(async () => {
      try {
        const value = await this.api.rpc(
          this.storeId,
          this.appId,
          sessionId,
          'storage.set',
          {
            value: draft.value,
            expectedRevision: draft.expectedRevision,
          },
        );
        if (this.disposed || current !== this.generation) return;
        this.baseline = value;
        const latest = this.snapshot.draft;
        if (
          latest &&
          JSON.stringify(latest.value) === JSON.stringify(draft.value) &&
          latest.expectedRevision === draft.expectedRevision
        ) {
          this.saveDraft(undefined);
        } else if (latest?.expectedRevision === draft.expectedRevision) {
          this.saveDraft({ ...latest, expectedRevision: value.revision });
        }
        this.post({ type: 'saved', ...value });
      } catch (error) {
        if (!this.disposed && current === this.generation)
          this.publish({ error: message(error) });
        throw error;
      } finally {
        if (this.saving === operation) this.saving = undefined;
        if (!this.disposed && current === this.generation)
          this.publish({ saving: false });
      }
    });
    this.saving = operation;
    return operation;
  }
  private fail(error: string) {
    this.suspend();
    this.publish({ phase: 'error', error });
  }
  private rejectDraft(
    error: unknown,
    value: unknown,
    expectedRevision: unknown,
  ) {
    // A rejected value is never persisted or saved. Keep the received copy in
    // memory so JSON input can still be exported before a close or replacement.
    this.rejectedValue = {
      value,
      expectedRevision,
      digest: this.snapshot.digest,
    };
    this.publish({
      rejectedDraft: `${message(error)} The latest draft could not be stored for recovery. Keep this window open to correct or export it, or explicitly discard it.`,
    });
  }
  suspend() {
    this.generation++;
    this.closePort();
    this.releaseSession();
    this.saving = undefined;
    this.publish({ phase: 'error', saving: false });
  }
  reportError(error: unknown) {
    this.publish({ error: message(error) });
  }
  private closePort() {
    clearTimeout(this.timer);
    this.port?.close();
    this.port = undefined;
    this.ready = false;
    this.connected = false;
    this.requests.clear();
  }
  private releaseSession() {
    const sessionId = this.sessionId;
    this.sessionId = '';
    if (sessionId)
      void this.api.release(this.storeId, this.appId, sessionId).catch(() => {
        // Revoked and expired sessions need no further cleanup. The server bounds their lifetime.
      });
  }
  dispose() {
    this.disposed = true;
    this.generation++;
    this.closePort();
    this.releaseSession();
  }
}

async function prepareRuntime(desktop: DesktopAPI, runtime: ExtensionRuntime) {
  if (!runtime.hasUnsaved()) return true;
  const result = await desktop.dialog({
    title: 'Unsaved application data',
    message: 'Save the application draft before continuing, or discard it.',
    buttons: [
      { label: 'Cancel', value: 'cancel', cancel: true },
      { label: 'Discard', value: 'discard' },
      { label: 'Save', value: 'save', default: true },
    ],
  });
  if (result?.button === 'discard') {
    try {
      runtime.discard();
      return true;
    } catch (error) {
      runtime.reportError(error);
      await desktop.dialog({
        title: 'Draft retained',
        message: message(error),
        buttons: [{ label: 'OK', value: 'ok', default: true }],
      });
      return false;
    }
  }
  if (result?.button !== 'save') return false;
  try {
    await runtime.save();
    if (runtime.hasUnsaved()) {
      runtime.reportError(
        'Newer edits remain unsaved. Save them before continuing.',
      );
      await desktop.dialog({
        title: 'Draft retained',
        message: 'Newer edits remain unsaved. Save them before continuing.',
        buttons: [{ label: 'OK', value: 'ok', default: true }],
      });
      return false;
    }
    return true;
  } catch (error) {
    runtime.reportError(error);
    await desktop.dialog({
      title: 'Application not saved',
      message: message(error),
      buttons: [{ label: 'OK', value: 'ok', default: true }],
    });
    return false;
  }
}

export async function prepareExtensionChange(
  desktop: DesktopAPI,
  appId: string,
) {
  for (const runtime of runtimes.values())
    if (runtime.appId === appId && !(await prepareRuntime(desktop, runtime)))
      return false;
  return true;
}

export const prepareExtensionClose = prepareRuntime;
