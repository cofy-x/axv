import type { DraftNamespace } from './context';

export interface DraftRecord extends DraftNamespace {
  key: string;
  draftId: string;
  entryId: string;
  name: string;
  text: string;
  saved: string;
  baseRevision: number;
  updatedAt: number;
}

export interface DraftRemovalGuard {
  assertCurrent: () => void;
  subscribe?: (listener: () => void) => () => void;
}

export interface DraftRemoval {
  restore: () => Promise<void>;
}

let database: Promise<IDBDatabase> | undefined;

function openDatabase() {
  database ??= new Promise<IDBDatabase>((resolve, reject) => {
    const request = indexedDB.open('nextstep-drafts', 1);
    request.onupgradeneeded = () => {
      request.result.createObjectStore('drafts', { keyPath: 'key' });
    };
    request.onsuccess = () => {
      const db = request.result;
      db.onversionchange = () => {
        db.close();
        database = undefined;
      };
      resolve(db);
    };
    request.onerror = () =>
      reject(request.error ?? new Error('Local draft storage is unavailable.'));
    request.onblocked = () =>
      reject(
        new Error('Close other desktop tabs to update local draft storage.'),
      );
  }).catch((error: unknown) => {
    database = undefined;
    throw error;
  });
  return database;
}

function draftKey(namespace: DraftNamespace, draftId: string) {
  return JSON.stringify([namespace.databaseId, namespace.clientId, draftId]);
}

async function transaction<T>(
  mode: IDBTransactionMode,
  action: (store: IDBObjectStore) => IDBRequest<T>,
  guard?: DraftRemovalGuard,
): Promise<T> {
  guard?.assertCurrent();
  const db = await openDatabase();
  guard?.assertCurrent();
  return new Promise<T>((resolve, reject) => {
    const tx = db.transaction('drafts', mode);
    let invalidation: unknown;
    let request: IDBRequest<T>;
    const validate = () => {
      try {
        guard?.assertCurrent();
      } catch (error) {
        invalidation = error;
        try {
          tx.abort();
        } catch {
          // Completion can precede its event. The caller restores that removal.
        }
      }
    };
    tx.oncomplete = () => {
      unsubscribe?.();
      try {
        guard?.assertCurrent();
        resolve(request.result);
      } catch (error) {
        reject(error);
      }
    };
    const failed = () => {
      unsubscribe?.();
      reject(
        invalidation ??
          tx.error ??
          new Error('The local draft could not be stored.'),
      );
    };
    tx.onabort = failed;
    tx.onerror = failed;
    const unsubscribe = guard?.subscribe?.(validate);
    validate();
    if (invalidation === undefined) request = action(tx.objectStore('drafts'));
  });
}

export function draftStorage(namespace: DraftNamespace, draftId: string) {
  const key = draftKey(namespace, draftId);
  return {
    read: () =>
      transaction<DraftRecord | undefined>(
        'readonly',
        (store) => store.get(key) as IDBRequest<DraftRecord | undefined>,
      ),
    write: (
      record: Omit<DraftRecord, 'key' | 'databaseId' | 'clientId' | 'draftId'>,
    ) =>
      transaction('readwrite', (store) =>
        store.put({ ...record, ...namespace, draftId, key }),
      ),
    remove: async (
      guard?: DraftRemovalGuard,
    ): Promise<DraftRemoval | undefined> => {
      if (!guard) {
        await transaction('readwrite', (store) => store.delete(key));
        return;
      }
      let removed: DraftRecord | undefined;
      const restore = async () => {
        if (!removed) return;
        await transaction('readwrite', (store) => {
          const current = store.get(key);
          current.onsuccess = () => {
            // A newer draft always wins. Restore the exact removed record and
            // identity only if cleanup left this editor's slot empty.
            if (current.result === undefined) store.add(removed);
          };
          return current;
        });
      };
      try {
        await transaction(
          'readwrite',
          (store) => {
            const previous = store.get(key);
            previous.onsuccess = () => {
              removed = previous.result as DraftRecord | undefined;
              store.delete(key);
            };
            return previous;
          },
          guard,
        );
        guard.assertCurrent();
        return { restore };
      } catch (error) {
        await restore();
        throw error;
      }
    },
  };
}

export async function listDrafts(
  namespace: DraftNamespace,
): Promise<DraftRecord[]> {
  const records = await transaction<DraftRecord[]>(
    'readonly',
    (store) => store.getAll() as IDBRequest<DraftRecord[]>,
  );
  return records
    .filter(
      (record) =>
        record.databaseId === namespace.databaseId &&
        record.clientId === namespace.clientId &&
        record.text !== record.saved,
    )
    .sort((a, b) => b.updatedAt - a.updatedAt);
}
