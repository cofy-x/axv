import { afterEach, describe, expect, it, vi } from 'vitest';
import { createDesktopDatabase } from './desktop';
import { request } from './http';

afterEach(() => vi.unstubAllGlobals());

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('page database binding', () => {
  it('requires bootstrap and never replaces the accepted database', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const database = createDesktopDatabase();
    await expect(database.request('/entries')).rejects.toMatchObject({
      code: 'database_identity_required',
    });
    expect(fetch).not.toHaveBeenCalled();
    database.bind('original');
    database.bind('original');
    expect(() => database.bind('replacement')).toThrow('cannot switch');
    expect(database.getDatabaseId()).toBe('original');
    await expect(
      database.request('/host/codex/sessions', {
        headers: { 'X-AXV-Database-ID': 'pending-request-database' },
      }),
    ).rejects.toMatchObject({ code: 'database_changed' });
    expect(fetch).not.toHaveBeenCalled();
  });

  it('binds JSON and binary requests without binding independent APIs', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(Response.json({ entries: [] }))
      .mockResolvedValueOnce(new Response('source text'))
      .mockResolvedValueOnce(Response.json({ machines: [] }));
    vi.stubGlobal('fetch', fetch);
    const database = createDesktopDatabase();
    database.bind('original');
    await expect(database.request('/entries')).resolves.toEqual({
      entries: [],
    });
    expect(
      await (await database.blob('/projects/host/download?path=a')).text(),
    ).toBe('source text');
    await request('/compute/machines');
    for (const index of [0, 1]) {
      const headers = fetch.mock.calls[index][1].headers as Headers;
      expect(headers.get('X-AXV-Database-ID')).toBe('original');
      expect(headers.get('X-Requested-With')).toBe('XMLHttpRequest');
    }
    expect(
      (fetch.mock.calls[2][1].headers as Headers).has('X-AXV-Database-ID'),
    ).toBe(false);
  });

  it('keeps replacement sticky and rejects both late responses and further writes', async () => {
    const slowResponse = deferred<Response>();
    const slowBody = deferred<unknown>();
    const fetch = vi
      .fn()
      .mockImplementationOnce(() => slowResponse.promise)
      .mockResolvedValueOnce({ ok: true, json: () => slowBody.promise })
      .mockResolvedValueOnce(
        Response.json(
          { code: 'database_changed', error: 'The database changed.' },
          { status: 409 },
        ),
      );
    vi.stubGlobal('fetch', fetch);
    const database = createDesktopDatabase();
    database.bind('original');
    const listener = vi.fn();
    database.subscribe(listener);
    const read = database.request('/entries');
    const save = database.request('/entries/readme', {
      method: 'PATCH',
      body: '{}',
    });
    const readRejected = expect(read).rejects.toMatchObject({
      code: 'database_changed',
    });
    const saveRejected = expect(save).rejects.toMatchObject({
      code: 'database_changed',
    });
    await expect(
      database.request('/layout', { method: 'PUT' }),
    ).rejects.toMatchObject({ code: 'database_changed' });
    slowResponse.resolve(Response.json({ entries: ['late-original-data'] }));
    slowBody.resolve({ entry: { revision: 2 } });
    await Promise.all([readRejected, saveRejected]);
    await expect(
      database.request('/layout', { method: 'PUT' }),
    ).rejects.toMatchObject({ code: 'database_changed' });
    database.bind('original');
    expect(database.getSnapshot()?.code).toBe('database_changed');
    expect(fetch).toHaveBeenCalledTimes(3);
    expect(listener).toHaveBeenCalledOnce();
    // Image URLs are constructed during rendering, so a blocked desktop must
    // keep producing the original URL rather than throw and unmount the image.
    expect(database.resourceURL('/entries/image/content?preview=1')).toBe(
      '/api/entries/image/content?preview=1&databaseId=original',
    );
  });

  it.each([
    [401, 'cloudflare_unauthorized'],
    [403, 'remote_forbidden'],
    [409, 'revision_conflict'],
    [503, 'remote_identity_unavailable'],
  ])('does not rebind or freeze for %s %s', async (status, code) => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(
          Response.json(
            { code, error: 'Retry the original operation.' },
            { status },
          ),
        )
        .mockResolvedValueOnce(Response.json({ entries: [] })),
    );
    const database = createDesktopDatabase();
    database.bind('original');
    await expect(database.request('/entries')).rejects.toBeInstanceOf(Error);
    expect(database.getSnapshot()).toBeNull();
    await expect(database.request('/entries')).resolves.toEqual({
      entries: [],
    });
    expect(database.getDatabaseId()).toBe('original');
  });

  it('preserves the page if an upgraded server rejects missing identity support', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        Response.json(
          {
            code: 'database_identity_required',
            error: 'Keep this page open.',
          },
          { status: 428 },
        ),
      ),
    );
    const database = createDesktopDatabase();
    database.bind('original');
    await expect(database.request('/entries')).rejects.toMatchObject({
      status: 428,
    });
    expect(database.getSnapshot()?.code).toBe('database_identity_required');
  });
});
