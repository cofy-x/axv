import { identityWitnessStore } from './client-identity-witness';
import type { IdentityWitnessStore } from './client-identity-witness';

export const clientIdentityKey = 'nextstep-browser-id';

interface IdentityEnvironment {
  storage: () => Pick<Storage, 'getItem' | 'setItem'>;
  locked: (work: () => Promise<string>) => Promise<string>;
  randomId: () => string;
  witness?: IdentityWitnessStore;
  observe?: (expected: string) => Promise<void>;
}

function validateIdentity(value: string | null) {
  if (
    value !== null &&
    (!value || new TextEncoder().encode(value).byteLength > 128)
  )
    throw new Error(
      'This browser’s saved identity is invalid. It has been preserved. Open AXV in another browser profile without clearing this browser’s recovery data.',
    );
  return value;
}

function changedIdentity() {
  return new Error(
    'This browser’s saved identity changed or is missing. Keep existing AXV windows open and retry. If the problem persists, use another browser profile without clearing recovery data.',
  );
}

/** Wait only for an elected value to become visible; never repair or replace storage. */
async function observeIdentity(expected: string) {
  await new Promise<void>((resolve) => {
    const done = () => {
      clearTimeout(timer);
      globalThis.removeEventListener('storage', onStorage);
      resolve();
    };
    const check = () => {
      try {
        if (localStorage.getItem(clientIdentityKey) === expected) done();
      } catch {
        done();
      }
    };
    const onStorage = (event: StorageEvent) => {
      if (event.key === clientIdentityKey || event.key === null) check();
    };
    const timer = setTimeout(done, 1000);
    globalThis.addEventListener('storage', onStorage);
    check();
  });
}

/** Existing browser identities remain authoritative; first creation requires one committed election. */
export function createClientIdentity(environment: IdentityEnvironment) {
  let pending: Promise<string> | undefined;
  const witness = environment.witness ?? identityWitnessStore;
  const observe = environment.observe ?? observeIdentity;
  const read = () => {
    let value: string | null;
    try {
      value = environment.storage().getItem(clientIdentityKey);
    } catch {
      throw new Error(
        'AXV cannot read this browser’s saved identity. Allow site storage, then retry.',
      );
    }
    return validateIdentity(value);
  };
  return () => {
    pending ??= Promise.resolve()
      .then(async () => {
        const existing = read();
        if (existing !== null) return existing;
        return environment.locked(async () => {
          const appeared = read();
          if (appeared !== null) return appeared;
          // Web Locks do not flush another process’s localStorage snapshots.
          // IndexedDB elects one ID before any page publishes it to localStorage.
          const elected = await witness.claim(environment.randomId);
          let current = read();
          if (elected.state === 'pending' && current === null) {
            try {
              environment.storage().setItem(clientIdentityKey, elected.id);
            } catch {
              throw new Error(
                'AXV could not save this browser’s identity. Allow site storage, then retry.',
              );
            }
            current = read();
          }
          if (current !== elected.id) {
            await observe(elected.id);
            if (read() !== elected.id) throw changedIdentity();
          }
          await witness.confirm(elected.id);
          if (read() !== elected.id) throw changedIdentity();
          return elected.id;
        });
      })
      .catch((error: unknown) => {
        pending = undefined;
        throw error;
      });
    return pending;
  };
}

export const initializeClientId = createClientIdentity({
  storage: () => localStorage,
  locked: async (work) => {
    if (!navigator.locks)
      throw new Error(
        'AXV cannot safely save a browser identity here. Use a supported browser at an HTTPS or localhost address, then retry.',
      );
    return navigator.locks.request('axv-browser-client-id', work);
  },
  randomId: () => crypto.randomUUID(),
});
