import { extensionsApi } from './api';
import type { ExtensionCatalog, ExtensionRecord } from './types';

type DefinitionApplication = Pick<
  ExtensionRecord,
  'id' | 'title' | 'icon' | 'installed'
>;

export interface ExtensionSnapshot extends ExtensionCatalog {
  definitionApps: DefinitionApplication[];
  loading: boolean;
  error: string;
}

export function createExtensionStore(read = extensionsApi.list) {
  let snapshot: ExtensionSnapshot = {
    storeId: '',
    apps: [],
    definitionApps: [],
    loading: true,
    error: '',
  };
  let pending: Promise<void> | undefined;
  let timer: ReturnType<typeof setInterval> | undefined;
  const listeners = new Set<() => void>();
  const knownApps = new Map<string, DefinitionApplication>();
  const publish = (next: ExtensionSnapshot) => {
    snapshot = next;
    listeners.forEach((listener) => listener());
  };
  const refresh = (force = false): Promise<void> => {
    if (pending) return force ? pending.then(() => refresh()) : pending;
    pending = read()
      .then((catalog) => {
        const currentIds = new Set(catalog.apps.map((app) => app.id));
        for (const { id, title, icon, installed } of catalog.apps)
          knownApps.set(id, {
            id,
            title,
            ...(icon ? { icon } : {}),
            installed,
          });
        const definitionApps = [...knownApps.values()].map((app) =>
          currentIds.has(app.id) ? app : { ...app, installed: false },
        );
        publish({ ...catalog, definitionApps, loading: false, error: '' });
      })
      .catch((error: unknown) =>
        publish({
          ...snapshot,
          loading: false,
          error: error instanceof Error ? error.message : String(error),
        }),
      )
      .finally(() => {
        pending = undefined;
      });
    return pending;
  };
  const onFocus = () => void refresh();
  return {
    getSnapshot: () => snapshot,
    refresh,
    subscribe(listener: () => void) {
      listeners.add(listener);
      if (listeners.size === 1) {
        void refresh();
        timer = setInterval(onFocus, 15_000);
        globalThis.addEventListener?.('focus', onFocus);
      }
      return () => {
        listeners.delete(listener);
        if (!listeners.size) {
          clearInterval(timer);
          globalThis.removeEventListener?.('focus', onFocus);
        }
      };
    },
  };
}

export const extensionStore = createExtensionStore();
