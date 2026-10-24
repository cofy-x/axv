import { afterEach, describe, expect, it, vi } from 'vitest';
import { machinesApi } from './api';
import type { ImageInfo, Machine, MachineSpec, Volume } from './api';

const machine = {
  id: 'vm-one',
  hostId: 'host-original',
  revision: 3,
} as Machine;
const volume = { id: 'vol-one', revision: 2 } as Volume;
const image = { id: 'image-one', revision: 1 } as ImageInfo;
const spec = {} as MachineSpec;
const imageSpec = { name: 'Image', arch: 'aarch64', sha256: 'digest' };
const mutations = [
  {
    name: 'preparation start',
    send: () =>
      machinesApi.startPreparation(
        'plan-one',
        'original-request',
        'instance-one',
      ),
  },
  {
    name: 'preparation retry',
    send: () =>
      machinesApi.preparationAction(
        'job-one',
        'retry',
        7,
        'original-request',
        'instance-one',
      ),
  },
  {
    name: 'preparation cancellation',
    send: () =>
      machinesApi.preparationAction(
        'job-one',
        'cancel',
        7,
        'original-request',
        'instance-one',
      ),
  },
  {
    name: 'VM creation',
    send: () => machinesApi.create(spec, true, 'original-request'),
  },
  {
    name: 'VM action',
    send: () => machinesApi.action(machine, 'restart', 'original-request'),
  },
  {
    name: 'VM configuration',
    send: () => machinesApi.update(machine, spec, false, 'original-request'),
  },
  {
    name: 'volume creation',
    send: () =>
      machinesApi.createVolume(
        { name: 'Volume', provider: 'lima', sizeGiB: 4, retain: true },
        'original-request',
      ),
  },
  {
    name: 'volume action',
    send: () =>
      machinesApi.volumeAction(
        volume,
        { action: 'rename', name: 'Changed' },
        'original-request',
      ),
  },
  {
    name: 'volume deletion',
    send: () => machinesApi.deleteVolume(volume, 'original-request'),
  },
  {
    name: 'image download',
    send: () => machinesApi.downloadImage(imageSpec, 'original-request'),
  },
  {
    name: 'image deletion',
    send: () => machinesApi.deleteImage(image, 'original-request'),
  },
  {
    name: 'terminal creation',
    send: () =>
      machinesApi.createTerminal('vm-one', 'original-request', 'host-original'),
  },
  {
    name: 'terminal termination',
    send: () =>
      machinesApi.endTerminal('vm-one', 'terminal-one', 'host-original'),
  },
];

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('compute mutation deadlines', () => {
  it.each(mutations)(
    'bounds $name requests and surfaces timeout errors to the caller',
    async ({ send, name }) => {
      const abort = new AbortController();
      const timeout = vi
        .spyOn(AbortSignal, 'timeout')
        .mockReturnValue(abort.signal);
      const fetch = vi.fn(
        (_input: RequestInfo | URL, init?: RequestInit) =>
          new Promise<Response>((_resolve, reject) => {
            init?.signal?.addEventListener(
              'abort',
              () => reject(init.signal?.reason),
              { once: true },
            );
          }),
      );
      vi.stubGlobal('fetch', fetch);
      const request = send();
      const failure = expect(request).rejects.toMatchObject({
        name: 'TimeoutError',
      });
      expect(timeout).toHaveBeenCalledExactlyOnceWith(15000);
      expect(fetch.mock.calls[0][1]?.signal).toBe(abort.signal);
      if (
        name.startsWith('terminal') ||
        name === 'VM action' ||
        name === 'VM configuration'
      )
        expect(
          new Headers(fetch.mock.calls[0][1]?.headers).get('X-AXV-Host-ID'),
        ).toBe('host-original');
      const body = fetch.mock.calls[0][1]?.body;
      if (body)
        expect(JSON.parse(body as string)).toMatchObject({
          requestId: 'original-request',
        });
      abort.abort(new DOMException('The request timed out.', 'TimeoutError'));
      await failure;
    },
  );

  it.each(['caller', 'deadline'] as const)(
    'allows the upload %s to cancel the longer multipart request',
    async (source) => {
      const caller = new AbortController();
      const deadline = new AbortController();
      const timeout = vi
        .spyOn(AbortSignal, 'timeout')
        .mockReturnValue(deadline.signal);
      const fetch = vi.fn(
        (_input: RequestInfo | URL, init?: RequestInit) =>
          new Promise<Response>((_resolve, reject) => {
            init?.signal?.addEventListener(
              'abort',
              () => reject(init.signal?.reason),
              { once: true },
            );
          }),
      );
      vi.stubGlobal('fetch', fetch);
      const request = machinesApi.importImage(
        new File(['image'], 'linux.raw'),
        imageSpec,
        'upload-request',
        caller.signal,
      );
      const failure = expect(request).rejects.toThrow('Upload interrupted');
      expect(timeout).toHaveBeenCalledExactlyOnceWith(30 * 60 * 1000);
      expect(fetch.mock.calls[0][1]?.body).toBeInstanceOf(FormData);
      (source === 'caller' ? caller : deadline).abort(
        new Error('Upload interrupted'),
      );
      await failure;
      expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
    },
  );
});

describe('preparation request boundaries', () => {
  it('preserves explicit host and generation guards for every inventory mutation', async () => {
    const fetch = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response('{}'),
    );
    vi.stubGlobal('fetch', fetch);
    const binding = { hostId: 'original-host', generation: 3 };
    await machinesApi.create(spec, false, 'create-request', binding);
    await machinesApi.createVolume(
      { name: 'Project', provider: 'lima', sizeGiB: 4, retain: true },
      'create-volume-request',
      binding,
    );
    await machinesApi.volumeAction(
      volume,
      { action: 'rename', name: 'Updated' },
      'volume-request',
      binding,
    );
    await machinesApi.deleteVolume(volume, 'delete-volume-request', binding);
    await machinesApi.downloadImage(imageSpec, 'download-request', binding);
    await machinesApi.importImage(
      new File(['image'], 'linux.raw'),
      imageSpec,
      'import-request',
      undefined,
      binding,
    );
    await machinesApi.deleteImage(image, 'delete-image-request', binding);
    expect(fetch).toHaveBeenCalledTimes(7);
    for (const call of fetch.mock.calls) {
      const headers = new Headers(call[1]?.headers);
      expect(headers.get('X-AXV-Host-ID')).toBe('original-host');
      expect(headers.get('X-AXV-Compute-Generation')).toBe('3');
    }
  });
  it('binds inventory reads to one runtime generation', async () => {
    const fetch = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response('{}'),
    );
    vi.stubGlobal('fetch', fetch);
    const binding = { hostId: 'host-exact', generation: 7 };
    for (const read of [
      machinesApi.info,
      machinesApi.machines,
      machinesApi.volumes,
      machinesApi.operations,
      machinesApi.images,
    ]) {
      await read(undefined, binding);
    }
    expect(fetch).toHaveBeenCalledTimes(5);
    for (const call of fetch.mock.calls) {
      const headers = new Headers(call[1]?.headers);
      expect(headers.get('X-AXV-Host-ID')).toBe('host-exact');
      expect(headers.get('X-AXV-Compute-Generation')).toBe('7');
    }
  });
  it('binds confirmed mutations to the observed server and reviewed plan', async () => {
    const fetch = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response('{}', {
          status: 202,
          headers: { 'Content-Type': 'application/json' },
        }),
    );
    vi.stubGlobal('fetch', fetch);
    await machinesApi.startPreparation(
      'reviewed-plan',
      'stable-request',
      'observed-server',
    );
    expect(fetch.mock.calls[0][0]).toBe('/api/compute/preparation/jobs');
    expect(
      new Headers(fetch.mock.calls[0][1]?.headers).get('X-AXV-Instance-ID'),
    ).toBe('observed-server');
    expect(JSON.parse(String(fetch.mock.calls[0][1]?.body))).toEqual({
      planId: 'reviewed-plan',
      requestId: 'stable-request',
      confirmed: true,
    });
    await machinesApi.preparationAction(
      'job/exact',
      'retry',
      7,
      'stable-retry',
      'observed-server',
    );
    expect(fetch.mock.calls[1][0]).toBe(
      '/api/compute/preparation/jobs/job%2Fexact/retry',
    );
    expect(
      new Headers(fetch.mock.calls[1][1]?.headers).get('X-AXV-Instance-ID'),
    ).toBe('observed-server');
    expect(JSON.parse(String(fetch.mock.calls[1][1]?.body))).toEqual({
      expectedRevision: 7,
      requestId: 'stable-retry',
    });
  });
});
