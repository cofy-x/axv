import { afterEach, describe, expect, it, vi } from 'vitest';
import { extensionsApi } from './api';
import type { ExtensionPackage, ExtensionRecord } from './types';

const storeId = 'af638af4-b7ef-4a4d-b770-9a253defa493';
const app: ExtensionRecord = {
  id: 'local.boundary',
  title: 'Boundary',
  version: '1.0.0',
  apiVersion: 1,
  capabilities: ['storage'],
  digest: 'a'.repeat(64),
  generation: 4,
  enabled: true,
  installed: true,
};
const pkg: ExtensionPackage = {
  manifest: {
    schemaVersion: 1,
    id: app.id,
    title: app.title,
    version: app.version,
    apiVersion: 1,
    capabilities: ['storage'],
  },
  html: '<!doctype html><title>Boundary</title>',
};
const operations = [
  {
    name: 'install',
    invoke: (id: string) => extensionsApi.install(id, pkg, 4),
  },
  {
    name: 'enable or disable',
    invoke: (id: string) => extensionsApi.setEnabled(id, app, false),
  },
  {
    name: 'uninstall',
    invoke: (id: string) => extensionsApi.uninstall(id, app),
  },
  { name: 'read data', invoke: (id: string) => extensionsApi.data(id, app.id) },
  {
    name: 'clear data',
    invoke: (id: string) => extensionsApi.clearData(id, app.id, 3),
  },
  {
    name: 'create a session',
    invoke: (id: string) => extensionsApi.session(app, id, 'browser', 'window'),
  },
  {
    name: 'read session data',
    invoke: (id: string) =>
      extensionsApi.rpc(id, app.id, 'captured-session', 'storage.get'),
  },
  {
    name: 'save session data',
    invoke: (id: string) =>
      extensionsApi.rpc(id, app.id, 'captured-session', 'storage.set', {
        value: { text: 'captured snapshot' },
        expectedRevision: 3,
      }),
  },
  {
    name: 'release a session',
    invoke: (id: string) =>
      extensionsApi.release(id, app.id, 'captured-session'),
  },
];

function mockFetch() {
  const fetchMock = vi.fn(
    async (_input: RequestInfo | URL, _init?: RequestInit) =>
      new Response(JSON.stringify({ storeId, apps: [] }), {
        headers: { 'Content-Type': 'application/json' },
      }),
  );
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

afterEach(() => vi.unstubAllGlobals());

describe('extension store identity at the HTTP boundary', () => {
  it('discovers the catalog without requiring a previously captured identity', async () => {
    const fetchMock = mockFetch();
    expect(await extensionsApi.list()).toEqual({ storeId, apps: [] });
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(fetchMock.mock.calls[0][0]).toBe('/api/extensions');
    expect(
      new Headers(fetchMock.mock.calls[0][1]?.headers).has(
        'X-AXV-Extension-Store',
      ),
    ).toBe(false);
  });

  it.each(operations)(
    'carries the captured store through $name',
    async ({ invoke }) => {
      const fetchMock = mockFetch();
      await invoke(storeId);
      expect(fetchMock).toHaveBeenCalledOnce();
      const init = fetchMock.mock.calls[0][1];
      const headers = new Headers(init?.headers);
      expect(headers.get('X-AXV-Extension-Store')).toBe(storeId);
      if (init?.body)
        expect(headers.get('Content-Type')).toBe('application/json');
    },
  );

  it('refuses all scoped operations without sending a request when the identity is missing', async () => {
    const fetchMock = mockFetch();
    for (const operation of operations)
      await expect(operation.invoke('')).rejects.toThrow(
        'The application store is unavailable',
      );
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
