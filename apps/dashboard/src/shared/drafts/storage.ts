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
): Promise<T> {
  const db = await openDatabase();
  return new Promise<T>((resolve, reject) => {
    const tx = db.transaction('drafts', mode);
    const request = action(tx.objectStore('drafts'));
    tx.oncomplete = () => resolve(request.result);
    tx.onabort = () =>
      reject(tx.error ?? new Error('The local draft could not be stored.'));
    tx.onerror = () =>
      reject(tx.error ?? new Error('The local draft could not be stored.'));
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
    remove: () => transaction('readwrite', (store) => store.delete(key)),
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
