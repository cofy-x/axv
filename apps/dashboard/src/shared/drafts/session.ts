import type { DraftNamespace } from './context';

interface SessionStore {
  getItem: (key: string) => string | null;
  setItem: (key: string, value: string) => void;
}

interface DraftLocks {
  request: (
    name: string,
    options: { ifAvailable: true },
    callback: (lock: object | null) => Promise<void>,
  ) => Promise<unknown>;
}

export interface EditorSession {
  draftId: string;
  recoveryId?: string;
}

interface ClaimOptions {
  storage?: SessionStore;
  locks?: DraftLocks;
  randomId?: () => string;
}

/** A copied sessionStorage must not grant two live pages ownership of one draft. */
export async function claimEditorSession(
  namespace: DraftNamespace,
  windowId: string,
  options: ClaimOptions = {},
): Promise<EditorSession> {
  const randomId = options.randomId ?? (() => crypto.randomUUID());
  const key = JSON.stringify([
    'nextstep-editor',
    namespace.databaseId,
    namespace.clientId,
    windowId,
  ]);
  let storage: SessionStore | undefined;
  try {
    storage = options.storage ?? sessionStorage;
  } catch {
    /* Independent draft identities remain available without session storage. */
  }
  let candidate = randomId();
  try {
    candidate = storage?.getItem(key) || candidate;
  } catch {
    /* Start with an independent identity. */
  }
  const retain = (id: string) => {
    try {
      storage?.setItem(key, id);
    } catch {
      /* The application persists its recoverable draft separately. */
    }
  };
  if (options.locks) {
    try {
      for (let attempt = 0; attempt < 8; attempt++) {
        const available = await new Promise<boolean>((resolve, reject) => {
          void options
            .locks!.request(
              JSON.stringify([
                'nextstep-draft',
                namespace.databaseId,
                namespace.clientId,
                candidate,
              ]),
              { ifAvailable: true },
              async (lock) => {
                resolve(Boolean(lock));
                // A page-lifetime lock is released by the browser on navigation/close.
                if (lock) await new Promise<void>(() => {});
              },
            )
            .catch(reject);
        });
        if (available) {
          retain(candidate);
          return { draftId: candidate };
        }
        candidate = randomId();
      }
    } catch {
      /* Private-mode restrictions use the independent fallback below. */
    }
  }
  // Without locks, never trust a possibly cloned tab identity. Offer its old
  // recovery as a copy, and write all new changes under a fresh identity.
  const independent = randomId();
  retain(independent);
  return { draftId: independent, recoveryId: candidate };
}

const sessions = new Map<string, Promise<EditorSession>>();

export function getEditorSession(namespace: DraftNamespace, windowId: string) {
  const key = JSON.stringify([
    namespace.databaseId,
    namespace.clientId,
    windowId,
  ]);
  let session = sessions.get(key);
  if (!session) {
    session = claimEditorSession(namespace, windowId, {
      locks: navigator.locks,
    });
    sessions.set(key, session);
  }
  return session;
}
