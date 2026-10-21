import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { APIError } from '../../shared/api/http';
import { ExtensionRuntime, draftKey, prepareExtensionClose } from './runtime';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { ExtensionData, ExtensionRecord } from './types';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

class TestPort {
  onmessage?: (event: MessageEvent<unknown>) => void;
  messages: Record<string, unknown>[] = [];
  closed = false;
  start() {}
  close() {
    this.closed = true;
  }
  postMessage(value: Record<string, unknown>) {
    this.messages.push(value);
  }
  receive(value: unknown) {
    this.onmessage?.({ data: value } as MessageEvent<unknown>);
  }
}

const channels: TestMessageChannel[] = [];

class TestMessageChannel {
  port1 = new TestPort();
  port2 = new TestPort();
  constructor() {
    channels.push(this);
  }
}

const app: ExtensionRecord = {
  id: 'example.notes',
  title: 'Example Notes',
  version: '1.0.0',
  apiVersion: 1,
  capabilities: ['storage'],
  digest: 'a'.repeat(64),
  generation: 1,
  installed: true,
  enabled: true,
};
const storeId = 'store-one';
const key = draftKey(storeId, 'browser-one', 'extension.example.notes:main');
const liveRuntimes: ExtensionRuntime[] = [];
let stored: Map<string, string>;
let storage: Storage;

function harness(runtimeKey = key, recoveryKey?: string) {
  let sessionNumber = 0;
  const api = {
    session: vi.fn(async (record: ExtensionRecord) => ({
      sessionId: `session-${++sessionNumber}`,
      contentUrl: `/api/extensions/${record.id}/content/${storeId}/${record.digest}`,
    })),
    rpc: vi.fn(
      async (
        _storeId: string,
        _appId: string,
        _sessionId: string,
        _method: 'storage.get' | 'storage.set',
        _params?: { value: unknown; expectedRevision: number },
      ): Promise<ExtensionData> => ({ value: { text: 'Saved' }, revision: 4 }),
    ),
    release: vi.fn(async () => ({ ok: true })),
  };
  const runtime = new ExtensionRuntime(
    app.id,
    'extension.example.notes:main',
    storeId,
    'browser-one',
    runtimeKey,
    api,
    recoveryKey,
  );
  liveRuntimes.push(runtime);
  const focus = vi.fn();
  const connect = () => {
    let nonce = '';
    const frame = {
      postMessage: (data: { nonce: string }) => {
        nonce = data.nonce;
      },
    } as Window;
    runtime.connect(frame, focus, runtime.getSnapshot().epoch);
    const port = channels.at(-1)!.port1;
    port.receive({ type: 'ready', nonce });
    return port;
  };
  const start = async (record = app, restore = false) => {
    await runtime.start(record, restore);
    return connect();
  };
  return { runtime, api, connect, start, focus };
}

async function response(port: TestPort, id: string) {
  await vi.waitFor(() =>
    expect(
      port.messages.some((item) => item.type === 'response' && item.id === id),
    ).toBe(true),
  );
  return port.messages.find(
    (item) => item.type === 'response' && item.id === id,
  )!;
}

async function readBaseline(port: TestPort, id = 'read') {
  port.receive({ type: 'request', id, method: 'storage.get' });
  await response(port, id);
}

beforeEach(() => {
  channels.length = 0;
  stored = new Map();
  storage = {
    getItem: vi.fn((name: string) => stored.get(name) ?? null),
    setItem: vi.fn(
      (name: string, value: string) => void stored.set(name, value),
    ),
    removeItem: vi.fn((name: string) => void stored.delete(name)),
    clear: vi.fn(() => stored.clear()),
    key: vi.fn(() => null),
    get length() {
      return stored.size;
    },
  };
  vi.stubGlobal('localStorage', storage);
  vi.stubGlobal('location', { origin: 'http://localhost:3001' });
  vi.stubGlobal('MessageChannel', TestMessageChannel);
});

afterEach(() => {
  liveRuntimes.splice(0).forEach((runtime) => runtime.dispose());
  vi.unstubAllGlobals();
});

describe('extension draft snapshots', () => {
  it('protects rejected oversized input, exports its latest value and requires correction or explicit discard', async () => {
    const { runtime, api, start } = harness();
    const port = await start();
    await readBaseline(port);
    const oversized = 'x'.repeat(129 * 1024);
    port.receive({
      type: 'request',
      id: 'too-large',
      method: 'draft.set',
      params: { value: oversized, expectedRevision: 4 },
    });
    expect((await response(port, 'too-large')).error).toBeDefined();
    expect(runtime.hasUnsaved()).toBe(true);
    expect(runtime.getSnapshot().rejectedDraft).toMatch(/128 KiB/);
    expect(JSON.parse(runtime.exportDraft()!)).toEqual({
      value: oversized,
      expectedRevision: 4,
      digest: app.digest,
    });
    expect(stored.has(key)).toBe(false);
    await expect(runtime.save()).rejects.toThrow(/latest draft was rejected/i);
    await expect(runtime.start(app)).rejects.toThrow(/rejected draft/i);
    const desktop = {
      dialog: vi
        .fn()
        .mockResolvedValueOnce({ button: 'save' })
        .mockResolvedValueOnce({ button: 'ok' }),
    } as unknown as DesktopAPI;
    expect(await prepareExtensionClose(desktop, runtime)).toBe(false);
    expect(runtime.hasUnsaved()).toBe(true);
    expect(api.rpc).toHaveBeenCalledTimes(1);
    runtime.setDraft({ text: 'Corrected input' }, 4);
    expect(runtime.getSnapshot().rejectedDraft).toBe('');
    expect(JSON.parse(runtime.exportDraft()!).value).toEqual({
      text: 'Corrected input',
    });
    runtime.discard();
    expect(runtime.hasUnsaved()).toBe(false);
  });

  it('does not allow a save of an older accepted draft to hide newer rejected input', async () => {
    const { runtime, api, start } = harness();
    await start();
    runtime.setDraft('Accepted draft', 4);
    const write = deferred<ExtensionData>();
    api.rpc.mockReturnValueOnce(write.promise);
    const saving = runtime.save();
    const oversized = 'x'.repeat(129 * 1024);
    expect(() => runtime.setDraft(oversized, 4)).toThrow(/128 KiB/);
    write.resolve({ value: 'Accepted draft', revision: 5 });
    await saving;
    expect(runtime.getSnapshot().draft).toBeUndefined();
    expect(runtime.hasUnsaved()).toBe(true);
    expect(JSON.parse(runtime.exportDraft()!).value).toBe(oversized);
    runtime.suspend();
    expect(JSON.parse(runtime.exportDraft()!).value).toBe(oversized);
    runtime.discard();
    expect(runtime.hasUnsaved()).toBe(false);
  });

  it('protects malformed storage writes and does not export an older accepted value as the rejected draft', async () => {
    const { runtime, start } = harness();
    const port = await start();
    runtime.setDraft('Previous valid draft', 4);
    port.receive({
      type: 'request',
      id: 'invalid-save',
      method: 'storage.set',
    });
    await response(port, 'invalid-save');
    expect(runtime.getSnapshot().rejectedDraft).not.toBe('');
    expect(() => runtime.exportDraft()).toThrow(/cannot be exported/);
    expect(runtime.hasUnsaved()).toBe(true);
    runtime.discard();
    expect(runtime.hasUnsaved()).toBe(false);
  });

  it('keeps parallel window drafts independent when one window saves or discards', async () => {
    const firstKey = draftKey(
      'store-one',
      'browser-one',
      'first-editor-session',
    );
    const secondKey = draftKey(
      'store-one',
      'browser-one',
      'second-editor-session',
    );
    const first = harness(firstKey);
    const second = harness(secondKey);
    await first.start();
    await second.start();
    first.runtime.setDraft({ text: 'First tab edit' }, 4);
    second.runtime.setDraft({ text: 'Second tab edit' }, 4);
    first.api.rpc.mockResolvedValueOnce({
      value: { text: 'First tab edit' },
      revision: 5,
    });
    await first.runtime.save();
    expect(stored.has(firstKey)).toBe(false);
    expect(JSON.parse(stored.get(secondKey)!).value).toEqual({
      text: 'Second tab edit',
    });
    first.runtime.discard();
    expect(second.runtime.getSnapshot().draft?.value).toEqual({
      text: 'Second tab edit',
    });
  });

  it('writes recovery copies independently and leaves a tombstone instead of replaying the source on reopen', async () => {
    const sourceKey = draftKey(
      'store-one',
      'browser-one',
      'source-editor-session',
    );
    const copyKey = draftKey('store-one', 'browser-one', 'copy-editor-session');
    const original = {
      value: { text: 'Original recovery source' },
      expectedRevision: 4,
      digest: app.digest,
    };
    const source = JSON.stringify(original);
    stored.set(sourceKey, source);
    const { runtime, start } = harness(copyKey, sourceKey);
    expect(runtime.getSnapshot().draft).toEqual(original);
    await start(app, true);
    runtime.setDraft({ text: 'Independent recovered typing' }, 4);
    expect(stored.get(sourceKey)).toBe(source);
    expect(JSON.parse(stored.get(copyKey)!).value).toEqual({
      text: 'Independent recovered typing',
    });
    runtime.discard();
    expect(stored.get(copyKey)).toBe('null');
    expect(stored.get(sourceKey)).toBe(source);
    const reopened = harness(copyKey, sourceKey).runtime;
    expect(reopened.getSnapshot().phase).toBe('initial');
    expect(reopened.getSnapshot().draft).toBeUndefined();
    const anotherCopy = harness('another-copy', sourceKey).runtime;
    expect(anotherCopy.getSnapshot().draft).toEqual(original);
  });

  it('keeps the recovery source after successfully saving a copy', async () => {
    const sourceKey = 'original-recovery-key';
    const copyKey = 'independent-recovery-key';
    const source = JSON.stringify({
      value: 'Recover this',
      expectedRevision: 4,
      digest: app.digest,
    });
    stored.set(sourceKey, source);
    const { runtime, api, start } = harness(copyKey, sourceKey);
    await start(app, true);
    api.rpc.mockResolvedValueOnce({ value: 'Recover this', revision: 5 });
    await runtime.save();
    expect(stored.get(sourceKey)).toBe(source);
    expect(stored.get(copyKey)).toBe('null');
    expect(harness(copyKey, sourceKey).runtime.hasUnsaved()).toBe(false);
  });

  it('coalesces repeated host saves without automatically capturing subsequent typing', async () => {
    const { runtime, api, start } = harness();
    await start();
    runtime.setDraft({ text: 'Explicit snapshot' }, 4);
    const write = deferred<ExtensionData>();
    api.rpc.mockReturnValueOnce(write.promise);
    const firstSave = runtime.save();
    runtime.setDraft({ text: 'Typing at second click' }, 4);
    const joinedSave = runtime.save();
    runtime.setDraft({ text: 'Typing after both clicks' }, 4);
    write.resolve({ value: { text: 'Explicit snapshot' }, revision: 5 });
    await firstSave;
    await joinedSave;
    expect(api.rpc).toHaveBeenCalledTimes(1);
    expect(runtime.getSnapshot().draft).toMatchObject({
      value: { text: 'Typing after both clicks' },
      expectedRevision: 5,
    });
  });

  it('rejects concurrent storage.set requests without replacing newer typing or writing another snapshot', async () => {
    const { runtime, api, start } = harness();
    const port = await start();
    const write = deferred<ExtensionData>();
    api.rpc.mockReturnValueOnce(write.promise);
    port.receive({
      type: 'request',
      id: 'first-save',
      method: 'storage.set',
      params: { value: 'Explicit snapshot', expectedRevision: 4 },
    });
    await Promise.resolve();
    runtime.setDraft('Newer typing', 4);
    port.receive({
      type: 'request',
      id: 'concurrent-save',
      method: 'storage.set',
      params: { value: 'Another snapshot', expectedRevision: 4 },
    });
    const rejected = await response(port, 'concurrent-save');
    expect(rejected.error).toMatchObject({
      message: expect.stringMatching(/save.*progress/i),
    });
    expect(runtime.getSnapshot().draft?.value).toBe('Newer typing');
    write.resolve({ value: 'Explicit snapshot', revision: 5 });
    await response(port, 'first-save');
    expect(api.rpc).toHaveBeenCalledTimes(1);
    expect(runtime.getSnapshot().draft).toMatchObject({
      value: 'Newer typing',
      expectedRevision: 5,
    });
  });

  it('preserves already-received newer typing when an older explicit save snapshot arrives later', async () => {
    const { runtime, api, start } = harness();
    const port = await start();
    await readBaseline(port);
    runtime.setDraft({ text: 'Latest typed content' }, 4);
    const write = deferred<ExtensionData>();
    api.rpc.mockReturnValueOnce(write.promise);
    port.receive({
      type: 'request',
      id: 'delayed-save',
      method: 'storage.set',
      params: {
        value: { text: 'Previously captured snapshot' },
        expectedRevision: 4,
      },
    });
    await Promise.resolve();
    expect(runtime.getSnapshot().draft?.value).toEqual({
      text: 'Latest typed content',
    });
    write.resolve({
      value: { text: 'Previously captured snapshot' },
      revision: 5,
    });
    await response(port, 'delayed-save');
    expect(api.rpc).toHaveBeenLastCalledWith(
      storeId,
      app.id,
      'session-1',
      'storage.set',
      { value: { text: 'Previously captured snapshot' }, expectedRevision: 4 },
    );
    expect(runtime.getSnapshot().draft).toMatchObject({
      value: { text: 'Latest typed content' },
      expectedRevision: 5,
    });
  });

  it('acknowledges an explicit save snapshot while keeping newer typing dirty', async () => {
    const { runtime, api, start } = harness();
    const port = await start();
    await readBaseline(port);
    runtime.setDraft({ text: 'First edit' }, 4);
    const write = deferred<ExtensionData>();
    api.rpc.mockReturnValueOnce(write.promise);
    const saving = runtime.save();
    runtime.setDraft({ text: 'Newer typing' }, 4);
    write.resolve({ value: { text: 'First edit' }, revision: 5 });
    await saving;
    expect(api.rpc).toHaveBeenLastCalledWith(
      storeId,
      app.id,
      'session-1',
      'storage.set',
      {
        value: { text: 'First edit' },
        expectedRevision: 4,
      },
    );
    expect(runtime.getSnapshot().draft).toEqual({
      value: { text: 'Newer typing' },
      expectedRevision: 5,
      digest: app.digest,
    });
    expect(runtime.hasUnsaved()).toBe(true);
    expect(JSON.parse(stored.get(key)!)).toEqual(runtime.getSnapshot().draft);
    expect(port.messages).toContainEqual({
      type: 'saved',
      value: { text: 'First edit' },
      revision: 5,
    });
  });

  it('retains typing that returns to the old baseline while a different snapshot is being saved', async () => {
    const { runtime, api, start } = harness();
    const port = await start();
    await readBaseline(port);
    runtime.setDraft({ text: 'Snapshot being saved' }, 4);
    const write = deferred<ExtensionData>();
    api.rpc.mockReturnValueOnce(write.promise);
    const saving = runtime.save();
    runtime.setDraft({ text: 'Saved' }, 4);
    expect(runtime.getSnapshot().draft).toMatchObject({
      value: { text: 'Saved' },
      expectedRevision: 4,
    });
    write.resolve({ value: { text: 'Snapshot being saved' }, revision: 5 });
    await saving;
    expect(runtime.getSnapshot().draft).toMatchObject({
      value: { text: 'Saved' },
      expectedRevision: 5,
    });
    expect(runtime.hasUnsaved()).toBe(true);
    expect(JSON.parse(stored.get(key)!)).toEqual(runtime.getSnapshot().draft);
  });

  it('clears only the acknowledged snapshot after a successful save', async () => {
    const { runtime, api, start } = harness();
    await start();
    runtime.setDraft({ text: 'Edit' }, 4);
    api.rpc.mockResolvedValueOnce({ value: { text: 'Edit' }, revision: 5 });
    await runtime.save();
    expect(runtime.getSnapshot().draft).toBeUndefined();
    expect(runtime.hasUnsaved()).toBe(false);
    expect(stored.has(key)).toBe(false);
  });

  it('retains newer typing and the original revision after failed or conflicting writes', async () => {
    const { runtime, api, start } = harness();
    await start();
    runtime.setDraft({ text: 'First edit' }, 4);
    const write = deferred<ExtensionData>();
    api.rpc.mockReturnValueOnce(write.promise);
    const saving = runtime.save();
    runtime.setDraft({ text: 'Newer typing' }, 4);
    const failure = new APIError(
      'The saved application data changed.',
      409,
      'revision_conflict',
    );
    write.reject(failure);
    await expect(saving).rejects.toBe(failure);
    expect(runtime.getSnapshot()).toMatchObject({
      draft: { value: { text: 'Newer typing' }, expectedRevision: 4 },
      saving: false,
      error: failure.message,
    });
    expect(runtime.hasUnsaved()).toBe(true);
    api.rpc.mockRejectedValueOnce(new Error('Disconnected'));
    await expect(runtime.save()).rejects.toThrow('Disconnected');
    expect(api.rpc).toHaveBeenLastCalledWith(
      storeId,
      app.id,
      'session-1',
      'storage.set',
      {
        value: { text: 'Newer typing' },
        expectedRevision: 4,
      },
    );
    expect(JSON.parse(stored.get(key)!).expectedRevision).toBe(4);
  });

  it('restores the recorded draft without rebasing it onto a newer server revision', async () => {
    const recovered = {
      value: { text: 'Recovered edit' },
      expectedRevision: 4,
      digest: 'older-package',
    };
    stored.set(key, JSON.stringify(recovered));
    const { runtime, api, start } = harness();
    expect(runtime.getSnapshot()).toMatchObject({
      phase: 'recovery',
      draft: recovered,
    });
    expect(api.session).not.toHaveBeenCalled();
    const port = await start(app, true);
    expect(port.messages).toContainEqual({
      type: 'init',
      visible: false,
      interactive: false,
      draft: recovered,
    });
    api.rpc.mockResolvedValueOnce({
      value: { text: 'Another writer' },
      revision: 9,
    });
    await readBaseline(port);
    expect(runtime.getSnapshot().draft).toEqual(recovered);
    api.rpc.mockRejectedValueOnce(
      new APIError('Conflict', 409, 'revision_conflict'),
    );
    await expect(runtime.save()).rejects.toThrow('Conflict');
    expect(api.rpc).toHaveBeenLastCalledWith(
      storeId,
      app.id,
      'session-1',
      'storage.set',
      {
        value: recovered.value,
        expectedRevision: 4,
      },
    );
    expect(runtime.getSnapshot().draft).toEqual(recovered);
  });

  it('keeps in-memory edits exportable when browser storage cannot accept newer typing', async () => {
    const { runtime, start } = harness();
    await start();
    runtime.setDraft({ text: 'Older recovery copy' }, 4);
    vi.mocked(storage.setItem).mockImplementationOnce(() => {
      throw new Error('Storage is full');
    });
    expect(() => runtime.setDraft({ text: 'Newest typing' }, 4)).toThrow(
      'Storage is full',
    );
    expect(runtime.getSnapshot().draft?.value).toEqual({
      text: 'Newest typing',
    });
    expect(runtime.hasUnsaved()).toBe(true);
    expect(JSON.parse(runtime.exportDraft()!).value).toEqual({
      text: 'Newest typing',
    });
  });

  it('preserves an unreadable recovery record until an explicit discard succeeds', () => {
    stored.set(key, '{damaged');
    const { runtime } = harness();
    expect(runtime.getSnapshot()).toMatchObject({
      phase: 'recovery',
      recoveryError: true,
    });
    expect(runtime.exportDraft()).toBe('{damaged');
    vi.mocked(storage.removeItem).mockImplementationOnce(() => {
      throw new Error('Unavailable');
    });
    expect(() => runtime.discard()).toThrow('Unavailable');
    expect(runtime.hasUnsaved()).toBe(true);
    runtime.discard();
    expect(runtime.hasUnsaved()).toBe(false);
  });

  it('rejects unavailable applications without consuming their saved draft', async () => {
    const recovered = {
      value: { text: 'Keep me' },
      expectedRevision: 4,
      digest: app.digest,
    };
    stored.set(key, JSON.stringify(recovered));
    const { runtime, api } = harness();
    await expect(runtime.start({ ...app, enabled: false })).rejects.toThrow(
      'Enable',
    );
    await expect(runtime.start({ ...app, installed: false })).rejects.toThrow(
      'Enable',
    );
    expect(api.session).not.toHaveBeenCalled();
    expect(runtime.getSnapshot().draft).toEqual(recovered);
    expect(stored.get(key)).toBe(JSON.stringify(recovered));
  });
});

describe('extension runtime generation boundaries', () => {
  it('ignores a late load from an iframe belonging to a replaced connection', async () => {
    const { runtime, start, connect } = harness();
    await start();
    const oldEpoch = runtime.getSnapshot().epoch;
    await runtime.start({ ...app, generation: 2 });
    const staleFrame = { postMessage: vi.fn() } as unknown as Window;
    const channelsBefore = channels.length;
    runtime.connect(staleFrame, vi.fn(), oldEpoch);
    expect(staleFrame.postMessage).not.toHaveBeenCalled();
    expect(channels).toHaveLength(channelsBefore);
    expect(runtime.getSnapshot().phase).toBe('loading');
    connect();
    expect(runtime.getSnapshot().phase).toBe('ready');
  });

  it('keeps the original store scope and rejects content addressed to a replacement store', async () => {
    const { runtime, api } = harness();
    api.session.mockResolvedValueOnce({
      sessionId: 'wrong-store-session',
      contentUrl: `/api/extensions/${app.id}/content/replacement-store/${app.digest}`,
    });
    await runtime.start(app);
    expect(api.session).toHaveBeenCalledWith(
      app,
      storeId,
      'browser-one',
      'extension.example.notes:main',
    );
    expect(runtime.getSnapshot()).toMatchObject({
      phase: 'error',
      contentUrl: '',
      error: expect.stringMatching(/unexpected content address/i),
    });
    expect(api.release).toHaveBeenCalledWith(
      storeId,
      app.id,
      'wrong-store-session',
    );
  });

  it('retains the draft and permits reload when navigation interrupts an in-flight save', async () => {
    const { runtime, api, start } = harness();
    const port = await start();
    runtime.setDraft({ text: 'Keep this edit after navigation' }, 4);
    const draft = runtime.getSnapshot().draft;
    const write = deferred<ExtensionData>();
    api.rpc.mockReturnValueOnce(write.promise);
    const saving = runtime.save();
    await Promise.resolve();
    runtime.connect(
      { postMessage: vi.fn() } as unknown as Window,
      vi.fn(),
      runtime.getSnapshot().epoch,
    );
    expect(runtime.getSnapshot()).toMatchObject({
      phase: 'error',
      saving: false,
      draft,
      error: expect.stringMatching(/navigated away/i),
    });
    expect(port.closed).toBe(true);
    expect(api.release).toHaveBeenCalledWith(storeId, app.id, 'session-1');
    write.resolve({ value: draft!.value, revision: 5 });
    await saving;
    expect(runtime.getSnapshot().draft).toEqual(draft);
    expect(runtime.getSnapshot().saving).toBe(false);
    expect(port.messages.some((item) => item.type === 'saved')).toBe(false);
    const reloaded = await start(app, true);
    expect(runtime.getSnapshot().phase).toBe('ready');
    expect(reloaded.messages).toContainEqual({
      type: 'init',
      visible: false,
      interactive: false,
      draft,
    });
  });

  it('allows a new generation save independently and does not let old cleanup clear its saving state', async () => {
    const { runtime, api, start } = harness();
    await start();
    runtime.setDraft({ text: 'Old generation' }, 4);
    const oldWrite = deferred<ExtensionData>();
    const newWrite = deferred<ExtensionData>();
    api.rpc
      .mockReturnValueOnce(oldWrite.promise)
      .mockReturnValueOnce(newWrite.promise);
    const oldSaving = runtime.save();
    await Promise.resolve();
    await start({ ...app, generation: 2 });
    runtime.setDraft({ text: 'New generation' }, 9);
    const newSaving = runtime.save();
    await Promise.resolve();
    expect(api.rpc).toHaveBeenCalledTimes(2);
    expect(api.rpc).toHaveBeenLastCalledWith(
      storeId,
      app.id,
      'session-2',
      'storage.set',
      {
        value: { text: 'New generation' },
        expectedRevision: 9,
      },
    );
    oldWrite.resolve({ value: { text: 'Old generation' }, revision: 5 });
    await oldSaving;
    expect(runtime.getSnapshot()).toMatchObject({
      saving: true,
      draft: { value: { text: 'New generation' }, expectedRevision: 9 },
    });
    const joinedSaving = runtime.save();
    await Promise.resolve();
    expect(api.rpc).toHaveBeenCalledTimes(2);
    newWrite.resolve({ value: { text: 'New generation' }, revision: 10 });
    await newSaving;
    await joinedSaving;
    expect(runtime.getSnapshot()).toMatchObject({
      saving: false,
      draft: undefined,
    });
  });

  it('suspends the exact connection while preserving its draft and rejecting later stale work', async () => {
    const { runtime, api, start } = harness();
    const port = await start();
    runtime.setDraft({ text: 'Preserved after disable' }, 4);
    const savedDraft = runtime.getSnapshot().draft;
    const write = deferred<ExtensionData>();
    api.rpc.mockReturnValueOnce(write.promise);
    const saving = runtime.save();
    await Promise.resolve();
    runtime.suspend();
    expect(port.closed).toBe(true);
    expect(api.release).toHaveBeenCalledWith(storeId, app.id, 'session-1');
    expect(runtime.getSnapshot()).toMatchObject({
      phase: 'error',
      saving: false,
      draft: savedDraft,
    });
    port.receive({
      type: 'request',
      id: 'late-typing',
      method: 'draft.set',
      params: { value: 'Do not adopt', expectedRevision: 4 },
    });
    expect(runtime.getSnapshot().draft).toEqual(savedDraft);
    write.resolve({ value: savedDraft!.value, revision: 5 });
    await saving;
    expect(runtime.getSnapshot().draft).toEqual(savedDraft);
    await expect(runtime.save()).rejects.toThrow('Reconnect');
    const nextPort = await start({ ...app, generation: 2 }, true);
    expect(nextPort.messages).toContainEqual({
      type: 'init',
      visible: false,
      interactive: false,
      draft: savedDraft,
    });
  });

  it('does not let hidden, inactive or covered frames request focus', async () => {
    const { runtime, start, focus } = harness();
    const port = await start();
    const states = [
      [false, false],
      [true, false],
      [false, true],
    ];
    for (const [index, [visible, interactive]] of states.entries()) {
      runtime.setLifecycle(visible, interactive);
      const id = `blocked-focus-${index}`;
      port.receive({ type: 'request', id, method: 'window.focus' });
      await response(port, id);
    }
    expect(focus).not.toHaveBeenCalled();
    runtime.setLifecycle(true, true);
    port.receive({
      type: 'request',
      id: 'active-focus',
      method: 'window.focus',
    });
    await response(port, 'active-focus');
    expect(focus).toHaveBeenCalledOnce();
  });

  it('keeps duplicate-request protection scoped to the new connection when an old request finishes', async () => {
    const { api, start } = harness();
    const oldPort = await start();
    const oldRead = deferred<ExtensionData>();
    const currentRead = deferred<ExtensionData>();
    api.rpc
      .mockReturnValueOnce(oldRead.promise)
      .mockReturnValueOnce(currentRead.promise);
    oldPort.receive({
      type: 'request',
      id: 'reused-id',
      method: 'storage.get',
    });
    const newPort = await start({ ...app, generation: 2 });
    newPort.receive({
      type: 'request',
      id: 'reused-id',
      method: 'storage.get',
    });
    oldRead.resolve({ value: 'old', revision: 1 });
    for (let index = 0; index < 8; index++) await Promise.resolve();
    newPort.receive({
      type: 'request',
      id: 'reused-id',
      method: 'storage.get',
    });
    expect(api.rpc).toHaveBeenCalledTimes(2);
    currentRead.resolve({ value: 'current', revision: 2 });
    await response(newPort, 'reused-id');
  });

  it('releases a late old session without replacing the newer content binding', async () => {
    const { runtime, api } = harness();
    const first = deferred<{ sessionId: string; contentUrl: string }>();
    const second = deferred<{ sessionId: string; contentUrl: string }>();
    api.session
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const oldStart = runtime.start(app);
    const updated = {
      ...app,
      digest: 'b'.repeat(64),
      generation: 2,
      version: '2.0.0',
    };
    const newStart = runtime.start(updated);
    const currentUrl = `/api/extensions/${app.id}/content/${storeId}/${updated.digest}`;
    second.resolve({ sessionId: 'new-session', contentUrl: currentUrl });
    await newStart;
    first.resolve({
      sessionId: 'old-session',
      contentUrl: `/api/extensions/${app.id}/content/${storeId}/${app.digest}`,
    });
    await oldStart;
    expect(runtime.getSnapshot()).toMatchObject({
      contentUrl: currentUrl,
      digest: updated.digest,
      generation: 2,
    });
    expect(api.release).toHaveBeenCalledWith(storeId, app.id, 'old-session');
    expect(api.release).not.toHaveBeenCalledWith(
      storeId,
      app.id,
      'new-session',
    );
  });

  it('does not let a stale storage read replace the new session baseline or send a new-port response', async () => {
    const { runtime, api, start } = harness();
    const oldPort = await start();
    const oldRead = deferred<ExtensionData>();
    api.rpc.mockReturnValueOnce(oldRead.promise);
    oldPort.receive({ type: 'request', id: 'old-read', method: 'storage.get' });
    const newPort = await start({ ...app, generation: 2 });
    api.rpc.mockResolvedValueOnce({
      value: { text: 'New session saved data' },
      revision: 9,
    });
    await readBaseline(newPort, 'new-read');
    oldRead.resolve({ value: { text: 'Old session saved data' }, revision: 4 });
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
    runtime.setDraft({ text: 'Old session saved data' }, 4);
    expect(runtime.getSnapshot().draft).toMatchObject({
      value: { text: 'Old session saved data' },
      expectedRevision: 4,
    });
    expect(newPort.messages.some((item) => item.id === 'old-read')).toBe(false);
    expect(oldPort.closed).toBe(true);
    runtime.setDraft({ text: 'New session saved data' }, 9);
    expect(runtime.hasUnsaved()).toBe(false);
  });

  it('ignores an old save completion after reconnecting and resets its visible saving state', async () => {
    const { runtime, api, start } = harness();
    await start();
    runtime.setDraft({ text: 'Retain old attempt' }, 4);
    const write = deferred<ExtensionData>();
    api.rpc.mockReturnValueOnce(write.promise);
    const saving = runtime.save();
    const draft = runtime.getSnapshot().draft;
    const newPort = await start({ ...app, generation: 2 });
    expect(runtime.getSnapshot().saving).toBe(false);
    write.resolve({ value: { text: 'Retain old attempt' }, revision: 5 });
    await saving;
    expect(runtime.getSnapshot().draft).toEqual(draft);
    expect(newPort.messages.some((item) => item.type === 'saved')).toBe(false);
  });

  it('rejects old frame messages after a new port is connected', async () => {
    const { runtime, start } = harness();
    const oldPort = await start();
    const newPort = await start({ ...app, generation: 2 });
    oldPort.receive({
      type: 'request',
      id: 'old-edit',
      method: 'draft.set',
      params: { value: 'stale', expectedRevision: 0 },
    });
    expect(runtime.getSnapshot().draft).toBeUndefined();
    newPort.receive({
      type: 'request',
      id: 'new-edit',
      method: 'draft.set',
      params: { value: 'current', expectedRevision: 0 },
    });
    await response(newPort, 'new-edit');
    expect(runtime.getSnapshot().draft?.value).toBe('current');
  });
});
