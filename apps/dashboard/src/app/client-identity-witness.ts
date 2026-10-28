export interface ClientIdentityWitness {
  id: string;
  state: 'pending' | 'confirmed';
}

export interface IdentityWitnessStore {
  claim: (createId: () => string) => Promise<ClientIdentityWitness>;
  confirm: (id: string) => Promise<void>;
}

function invalidWitness() {
  return new Error(
    'AXV cannot use this browser’s saved identity data. It has been preserved. Open AXV in another browser profile without clearing this browser’s recovery data.',
  );
}

function unavailableWitness() {
  return new Error(
    'Browser storage is unavailable. Allow site storage, then retry.',
  );
}

function witness(value: unknown): ClientIdentityWitness | undefined {
  if (value === undefined) return undefined;
  if (
    !value ||
    typeof value !== 'object' ||
    !('id' in value) ||
    typeof value.id !== 'string' ||
    !value.id ||
    new TextEncoder().encode(value.id).byteLength > 128 ||
    !('state' in value) ||
    (value.state !== 'pending' && value.state !== 'confirmed')
  )
    throw invalidWitness();
  return { id: value.id, state: value.state };
}

/** This election record is separate from draft storage and never changes draft namespaces. */
async function updateWitness(
  update: (current: ClientIdentityWitness | undefined) => ClientIdentityWitness,
) {
  const db = await new Promise<IDBDatabase>((resolve, reject) => {
    let opening: IDBOpenDBRequest;
    try {
      opening = indexedDB.open('axv-browser-identity', 1);
    } catch {
      reject(unavailableWitness());
      return;
    }
    let rejected = false;
    opening.onupgradeneeded = (event) => {
      if (event.oldVersion !== 0) {
        opening.transaction?.abort();
        return;
      }
      opening.result.createObjectStore('initialization');
    };
    opening.onsuccess = () => {
      if (rejected) opening.result.close();
      else if (
        opening.result.objectStoreNames.length !== 1 ||
        !opening.result.objectStoreNames.contains('initialization')
      ) {
        opening.result.close();
        reject(invalidWitness());
      } else resolve(opening.result);
    };
    opening.onerror = () => reject(unavailableWitness());
    opening.onblocked = () => {
      rejected = true;
      reject(
        new Error(
          'Another AXV page is using browser storage. Keep windows with unsaved work open, then retry after other AXV pages finish opening.',
        ),
      );
    };
  });
  try {
    return await new Promise<ClientIdentityWitness>((resolve, reject) => {
      const transaction = db.transaction('initialization', 'readwrite');
      const store = transaction.objectStore('initialization');
      const request = store.get('client');
      let result: ClientIdentityWitness;
      let failure: unknown;
      request.onsuccess = () => {
        try {
          const current = witness(request.result);
          result = update(current);
          if (result !== current) store.put(result, 'client');
        } catch (error) {
          failure = error;
          transaction.abort();
        }
      };
      transaction.oncomplete = () => resolve(result);
      transaction.onabort = () =>
        reject(
          failure ||
            new Error(
              'AXV could not finish saving this browser’s identity. Retry without clearing browser data.',
            ),
        );
    });
  } finally {
    db.close();
  }
}

export const identityWitnessStore: IdentityWitnessStore = {
  claim: (createId) =>
    updateWitness((current) => {
      if (current) return current;
      return witness({ id: createId(), state: 'pending' })!;
    }),
  confirm: async (id) => {
    await updateWitness((current) => {
      if (!current || current.id !== id) throw invalidWitness();
      return current.state === 'confirmed'
        ? current
        : { ...current, state: 'confirmed' };
    });
  },
};
