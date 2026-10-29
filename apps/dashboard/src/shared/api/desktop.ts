import { APIError, requestResponse } from './http';

const identityHeader = 'X-AXV-Database-ID';

/** One immutable database binding per document, independent of service incarnations. */
export function createDesktopDatabase() {
  let databaseId: string | undefined;
  let failure: APIError | null = null;
  const listeners = new Set<() => void>();
  const getDatabaseId = () => {
    if (!databaseId)
      throw new APIError(
        'Open the workspace before accessing its documents or sessions.',
        428,
        'database_identity_required',
      );
    return databaseId;
  };
  const assertUnchanged = () => {
    if (failure) throw failure;
  };
  const assertActive = () => {
    assertUnchanged();
    return getDatabaseId();
  };

  async function read<T>(
    path: string,
    init: RequestInit,
    consume: (response: Response) => Promise<T>,
  ): Promise<T> {
    const id = assertActive();
    const headers = new Headers(init.headers);
    if (headers.has(identityHeader) && headers.get(identityHeader) !== id)
      throw new APIError(
        'This request belongs to another workspace database. Preserve its original identity and recovery record.',
        409,
        'database_changed',
      );
    headers.set(identityHeader, id);
    try {
      const response = await requestResponse(path, { ...init, headers });
      assertActive();
      const result = await consume(response);
      // A different in-flight request may have detected replacement while this
      // response was being read. Never acknowledge an old save after that point.
      assertActive();
      return result;
    } catch (error) {
      if (
        !failure &&
        error instanceof APIError &&
        (error.code === 'database_changed' ||
          error.code === 'database_identity_required')
      ) {
        failure = error;
        for (const listener of listeners) listener();
      }
      if (failure) throw failure;
      throw error;
    }
  }

  return {
    bind(id: string) {
      if (!id || (databaseId !== undefined && databaseId !== id))
        throw new Error(
          'A page cannot switch its workspace database. Open AXV in a new window.',
        );
      databaseId = id;
    },
    getDatabaseId,
    assertActive,
    assertUnchanged,
    getSnapshot: () => failure,
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    request<T>(path: string, init: RequestInit = {}) {
      return read<T>(path, init, (response) => response.json() as Promise<T>);
    },
    blob(path: string, init: RequestInit = {}) {
      return read(path, init, (response) => response.blob());
    },
    resourceURL(path: string) {
      // Render-time image URLs remain usable even after replacement is detected;
      // they still address only the original database and cannot return new data.
      return `/api${path}${path.includes('?') ? '&' : '?'}databaseId=${encodeURIComponent(getDatabaseId())}`;
    },
  };
}

export const desktopDatabase = createDesktopDatabase();
export const requestDesktop = desktopDatabase.request;
export const requestDesktopBlob = desktopDatabase.blob;
export const desktopResourceURL = desktopDatabase.resourceURL;
