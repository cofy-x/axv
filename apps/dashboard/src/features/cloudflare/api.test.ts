import { afterEach, describe, expect, it, vi } from 'vitest';
import { cloudflareAPI, cloudflareURL } from './api';
import type { CloudflareConnection } from './types';

const info: CloudflareConnection = {
  local: true,
  cloudflare: { enabled: true, configured: true, state: 'listening' },
};

afterEach(() => vi.unstubAllGlobals());

describe('Cloudflare utility API boundaries', () => {
  it('accepts only a clean HTTPS origin while the entrypoint is listening', () => {
    const candidate = { ...info.cloudflare, url: 'https://axv.example.com' };
    expect(cloudflareURL(candidate)).toBe('https://axv.example.com');
    expect(cloudflareURL({ ...candidate, state: 'stopped' })).toBeUndefined();
    for (const url of [
      'http://axv.example.com',
      'javascript:alert(1)',
      'https://user:secret@axv.example.com',
      'https://axv.example.com:8443',
      'https://axv.example.com/path',
      'https://axv.example.com/?token=secret',
      'https://axv.example.com/#token',
    ])
      expect(cloudflareURL({ ...candidate, url })).toBeUndefined();
  });

  it('never requests local diagnostics or settings for a remote client', () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const remote = { ...info, local: false };
    expect(() => cloudflareAPI.localStatus(remote)).toThrow(
      /computer running AXV/,
    );
    expect(() => cloudflareAPI.setEnabled(remote, false)).toThrow(
      /computer running AXV/,
    );
    expect(fetch).not.toHaveBeenCalled();
  });

  it('writes only the enabled preference without credentials or setup fields', async () => {
    const fetch = vi.fn(
      async () => new Response(JSON.stringify(info.cloudflare)),
    );
    vi.stubGlobal('fetch', fetch);
    await cloudflareAPI.setEnabled(info, false);
    expect(fetch).toHaveBeenCalledWith(
      '/api/cloudflare',
      expect.objectContaining({
        method: 'PUT',
        body: '{"enabled":false}',
      }),
    );
  });
});

it('keeps local configuration, validation and token rotation behind the local boundary', () => {
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  const remote = { local: false };
  expect(() => cloudflareAPI.config(remote)).toThrow(/computer running AXV/);
  expect(() =>
    cloudflareAPI.validate(remote, { step: 'token', token: 'secret' }),
  ).toThrow(/computer running AXV/);
  expect(() =>
    cloudflareAPI.rotateToken(remote, {
      instanceId: 'instance',
      expectedRevision: 'revision',
      token: 'secret',
    }),
  ).toThrow(/computer running AXV/);
  expect(fetch).not.toHaveBeenCalled();
});

it('binds all publication mutations to their original host and preserves the original request body', async () => {
  const fetch = vi.fn(
    async (_path: string, _init?: RequestInit) => new Response('{}'),
  );
  vi.stubGlobal('fetch', fetch);
  const binding = {
    hostId: 'original-host',
    projectId: 'project-1',
    projectRevision: 1,
    volumeId: 'volume-1',
    volumeRevision: 2,
    machineId: 'machine-1',
    bootId: 'boot-1',
    instanceId: 'instance-1',
  };
  const payload = {
    instanceId: 'instance-1',
    requestId: 'request-1',
    binding,
    port: 3000,
    name: 'app',
    domain: 'example.net',
    visibility: 'public' as const,
  };
  const record = {
    ...payload,
    id: 'publication/1',
    domain: 'example.net',
    generation: '0123456789abcdef0123456789abcdef',
    hostname: 'app-0123456789abcdef0123456789abcdef.example.net',
    state: 'error' as const,
    phase: 'ingress',
    cleanupPending: false,
    createdAt: 'now',
    updatedAt: 'now',
  };
  await cloudflareAPI.publish(payload);
  await cloudflareAPI.unpublish(record);
  await cloudflareAPI.retry(record);
  expect(fetch.mock.calls.map((call) => call[0])).toEqual([
    '/api/cloudflare-publications',
    '/api/cloudflare-publications/publication%2F1/unpublish',
    '/api/cloudflare-publications/publication%2F1/retry',
  ]);
  for (const [, init] of fetch.mock.calls as unknown as [string, RequestInit][])
    expect(new Headers(init.headers).get('X-AXV-Host-ID')).toBe(
      'original-host',
    );
  expect(
    (fetch.mock.calls[0] as unknown as [string, RequestInit])[1].body,
  ).toBe(JSON.stringify(payload));
});
