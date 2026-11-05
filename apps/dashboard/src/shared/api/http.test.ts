import { afterEach, describe, expect, it, vi } from 'vitest';
import { request } from './http';

afterEach(() => vi.unstubAllGlobals());

describe('authorization failures preserve the current desktop', () => {
  it('asks Access for an AJAX status instead of following a login page flow', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(
      async () => new Response('{"ok":true}'),
    );
    vi.stubGlobal('fetch', fetch);
    await expect(request('/health')).resolves.toEqual({ ok: true });
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/health');
    expect(
      new Headers(fetch.mock.calls[0]?.[1]?.headers).get('X-Requested-With'),
    ).toBe('XMLHttpRequest');
  });

  it.each([
    new Headers({ 'Content-Type': 'text/plain', 'X-AXV-Test': 'retained' }),
    [
      ['Content-Type', 'text/plain'],
      ['X-AXV-Test', 'retained'],
    ],
    { 'content-type': 'text/plain', 'X-AXV-Test': 'retained' },
  ] satisfies HeadersInit[])(
    'preserves every supported caller header format',
    async (headers) => {
      const fetch = vi.fn<typeof globalThis.fetch>(
        async () => new Response('{}'),
      );
      vi.stubGlobal('fetch', fetch);
      await request('/entries/document', {
        method: 'PATCH',
        body: 'draft',
        headers,
      });
      const sent = new Headers(fetch.mock.calls[0]?.[1]?.headers);
      expect(sent.get('Content-Type')).toBe('text/plain');
      expect(sent.get('X-AXV-Test')).toBe('retained');
      expect(sent.get('X-Requested-With')).toBe('XMLHttpRequest');
    },
  );

  it.each(['null', '[]', '42', '"error"', '{"error":42,"code":null}'])(
    'reports a malformed JSON error body (%s) as an HTTP failure',
    async (body) => {
      vi.stubGlobal(
        'fetch',
        vi.fn(async () => new Response(body, { status: 502 })),
      );
      await expect(request('/connection')).rejects.toMatchObject({
        status: 502,
        code: 'http_error',
        message: 'The server could not complete this operation.',
      });
    },
  );

  it('makes a JSON edge authorization failure actionable', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('null', { status: 401 })),
    );
    await expect(request('/connection')).rejects.toMatchObject({
      status: 401,
      code: 'authentication_required',
    });
  });

  it.each(['edge', 'origin'])(
    'makes %s authorization failure actionable without replaying a write',
    async (source) => {
      const fetch = vi.fn(
        async () =>
          new Response(
            source === 'origin'
              ? '{"code":"cloudflare_unauthorized","error":"Authentication required."}'
              : '<html>Sign in</html>',
            { status: 401, statusText: 'Unauthorized' },
          ),
      );
      vi.stubGlobal('fetch', fetch);
      await expect(
        request('/entries/document', {
          method: 'PATCH',
          body: '{"content":"draft"}',
        }),
      ).rejects.toMatchObject({
        status: 401,
        code: 'authentication_required',
        message: 'Authorization is required. Restore access, then retry.',
      });
      expect(fetch).toHaveBeenCalledTimes(1);
    },
  );
});
