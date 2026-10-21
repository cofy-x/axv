import { request } from '../../shared/api/http';
import type {
  ExtensionCatalog,
  ExtensionData,
  ExtensionPackage,
  ExtensionRecord,
} from './types';

const path = (id: string) => `/extensions/${encodeURIComponent(id)}`;
const json = (value: unknown) => JSON.stringify(value);
const timed = <T>(url: string, init: RequestInit = {}) =>
  request<T>(url, { ...init, signal: AbortSignal.timeout(15_000) });

function scoped<T>(storeId: string, url: string, init: RequestInit = {}) {
  if (!storeId)
    return Promise.reject<T>(
      new Error('The application store is unavailable. Refresh to reconnect.'),
    );
  const headers = new Headers(init.headers);
  headers.set('X-AXV-Extension-Store', storeId);
  return timed<T>(url, { ...init, headers: Object.fromEntries(headers) });
}

export const extensionsApi = {
  list: () => timed<ExtensionCatalog>('/extensions'),
  install: (
    storeId: string,
    pkg: ExtensionPackage,
    expectedGeneration?: number,
  ) =>
    scoped<{ app: ExtensionRecord }>(storeId, '/extensions/install', {
      method: 'POST',
      body: json({ package: pkg, expectedGeneration }),
    }),
  setEnabled: (storeId: string, app: ExtensionRecord, enabled: boolean) =>
    scoped<{ app: ExtensionRecord }>(storeId, path(app.id), {
      method: 'PUT',
      body: json({ enabled, expectedGeneration: app.generation }),
    }),
  uninstall: (storeId: string, app: ExtensionRecord) =>
    scoped<{ app: ExtensionRecord }>(
      storeId,
      `${path(app.id)}?generation=${app.generation}`,
      { method: 'DELETE' },
    ),
  data: (storeId: string, id: string) =>
    scoped<ExtensionData>(storeId, `${path(id)}/data`),
  clearData: (storeId: string, id: string, revision: number) =>
    scoped<ExtensionData>(storeId, `${path(id)}/data?revision=${revision}`, {
      method: 'DELETE',
    }),
  session: (
    app: ExtensionRecord,
    storeId: string,
    clientId: string,
    windowId: string,
  ) =>
    scoped<{ sessionId: string; contentUrl: string }>(
      storeId,
      `${path(app.id)}/sessions`,
      {
        method: 'POST',
        body: json({
          digest: app.digest,
          generation: app.generation,
          clientId,
          windowId,
        }),
      },
    ),
  rpc: (
    storeId: string,
    id: string,
    sessionId: string,
    method: 'storage.get' | 'storage.set',
    params?: { value: unknown; expectedRevision: number },
  ) =>
    scoped<ExtensionData>(
      storeId,
      `${path(id)}/sessions/${encodeURIComponent(sessionId)}/rpc`,
      { method: 'POST', body: json({ method, params }) },
    ),
  release: (storeId: string, id: string, sessionId: string) =>
    scoped<{ ok: boolean }>(
      storeId,
      `${path(id)}/sessions/${encodeURIComponent(sessionId)}`,
      { method: 'DELETE' },
    ),
};
