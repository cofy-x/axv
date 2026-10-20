import { afterEach, describe, expect, it, vi } from 'vitest';
import { connectionAPI, readyURL } from './api';
import type { ConnectionInfo } from './types';

const info: ConnectionInfo = {
  local: true,
  remote: { enabled: true, supported: true, state: 'needs-login' },
};

afterEach(() => vi.unstubAllGlobals());

describe('Tailscale connection API', () => {
  it('fetches only the sanitized summary with no browser cache', async () => {
    const fetch = vi.fn(async () => Response.json(info));
    vi.stubGlobal('fetch', fetch);
    const controller = new AbortController();
    await expect(connectionAPI.status(controller.signal)).resolves.toEqual(
      info,
    );
    expect(fetch).toHaveBeenCalledExactlyOnceWith('/api/connection', {
      signal: controller.signal,
      cache: 'no-store',
      headers: {},
    });
  });

  it('never requests a local enrollment route from a remote viewer or another AXV state', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    for (const candidate of [
      { ...info, local: false },
      { ...info, remote: { ...info.remote, state: 'disabled' as const } },
      { ...info, remote: { ...info.remote, state: 'ready' as const } },
    ])
      await expect(
        connectionAPI.authorizationURL(candidate),
      ).resolves.toBeUndefined();
    expect(fetch).not.toHaveBeenCalled();
  });

  it('saves only an explicit enabled boolean locally and preserves server errors', async () => {
    const fetch = vi.fn(async () => Response.json(info.remote));
    vi.stubGlobal('fetch', fetch);
    const controller = new AbortController();
    await expect(
      connectionAPI.setEnabled(info, true, controller.signal),
    ).resolves.toEqual(info.remote);
    expect(fetch).toHaveBeenCalledExactlyOnceWith('/api/remote', {
      method: 'PUT',
      body: '{"enabled":true}',
      signal: controller.signal,
      headers: { 'Content-Type': 'application/json' },
    });
    fetch.mockResolvedValueOnce(
      Response.json(
        { code: 'remote_config_failed', error: 'Could not save the setting.' },
        { status: 500 },
      ),
    );
    await expect(connectionAPI.setEnabled(info, false)).rejects.toThrow(
      'Could not save the setting.',
    );
    fetch.mockClear();
    for (const candidate of [
      { ...info, local: false },
      { ...info, remote: { ...info.remote, supported: false } },
    ])
      await expect(connectionAPI.setEnabled(candidate, true)).rejects.toThrow(
        'Manage remote access',
      );
    expect(fetch).not.toHaveBeenCalled();
  });

  it('accepts the current local login capability but rejects unsafe authorization addresses', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(async () =>
      Response.json({
        state: 'needs-login',
        authUrl: 'https://login.tailscale.com/a/capability',
      }),
    );
    vi.stubGlobal('fetch', fetch);
    await expect(connectionAPI.authorizationURL(info)).resolves.toBe(
      'https://login.tailscale.com/a/capability',
    );
    expect(fetch.mock.calls[0][0]).toBe('/api/remote/status');
    for (const authUrl of [
      'javascript:alert(1)',
      '?',
      'http://login.tailscale.com/a/token',
      'https://login.tailscale.com.attacker.example/a/token',
      'https://user:secret@login.tailscale.com/a/token',
      'https://login.tailscale.com/admin/dns',
      'https://login.tailscale.com/a/token?next=elsewhere',
      'https://login.tailscale.com/a/token#fragment',
      'https://login.tailscale.com/a/token/',
      'https://login.tailscale.com/a/token%2Felsewhere',
    ]) {
      fetch.mockResolvedValueOnce(
        Response.json({ state: 'needs-login', authUrl }),
      );
      await expect(connectionAPI.authorizationURL(info)).rejects.toThrow(
        'authorization address is unavailable',
      );
    }
    fetch.mockResolvedValueOnce(
      Response.json({
        state: 'ready',
        authUrl: 'https://login.tailscale.com/a/old',
      }),
    );
    await expect(connectionAPI.authorizationURL(info)).resolves.toBeUndefined();
  });

  it('shows a desktop URL only when AXV actually reports a valid ready HTTPS address', () => {
    const remote = (state: ConnectionInfo['remote']['state'], url: string) => ({
      ...info,
      remote: { ...info.remote, state, url },
    });
    expect(readyURL(remote('ready', 'https://axv.example.ts.net'))).toBe(
      'https://axv.example.ts.net',
    );
    expect(
      readyURL(remote('starting', 'https://axv.example.ts.net')),
    ).toBeUndefined();
    for (const url of [
      'http://axv.example.ts.net',
      'https://axv.example.ts.net.attacker.example',
      'https://secret@axv.example.ts.net',
      'https://axv.example.ts.net:444',
      'https://axv.example.ts.net/?token=secret',
      'https://axv.example.ts.net/?',
      'https://axv.example.ts.net/#',
      'javascript:alert(1)',
    ])
      expect(readyURL(remote('ready', url))).toBeUndefined();
  });
});
