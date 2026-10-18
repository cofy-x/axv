import { describe, expect, it, vi } from 'vitest';
import { APIError } from '../../shared/api/http';
import { OperationRequest } from './operation-requests';

describe('compute form request identity', () => {
  it('retries the original snapshot after polling or caller state changes', async () => {
    const send = vi
      .fn<
        (
          input: { revision: number; spec: { size: number } },
          id: string,
        ) => Promise<string>
      >()
      .mockRejectedValueOnce(new TypeError('Connection lost'))
      .mockResolvedValue('accepted');
    const requests = new OperationRequest(send);
    const input = { revision: 1, spec: { size: 8 } };
    await expect(requests.submit(input, 'Grow project')).rejects.toThrow(
      'Connection lost',
    );
    input.revision = 3;
    input.spec.size = 16;
    await expect(requests.submit(input, 'Another change')).rejects.toThrow(
      'Resolve the previous request',
    );
    await expect(requests.retry()).resolves.toBe('accepted');
    expect(send.mock.calls[1]).toEqual(send.mock.calls[0]);
    expect(send.mock.calls[1][0]).toEqual({ revision: 1, spec: { size: 8 } });
    expect(requests.pending).toBeNull();
  });

  it('keeps server-error attempts but releases definitive conflicts', async () => {
    const send = vi
      .fn<(input: { revision: number }, id: string) => Promise<string>>()
      .mockRejectedValueOnce(
        new APIError('Write outcome unknown', 503, 'host_unavailable'),
      )
      .mockRejectedValueOnce(
        new APIError('Revision changed', 409, 'revision_conflict'),
      )
      .mockResolvedValue('accepted');
    const requests = new OperationRequest(send);
    await expect(
      requests.submit({ revision: 1 }, 'Rename volume'),
    ).rejects.toThrow();
    const original = requests.pending!.requestId;
    await expect(requests.retry()).rejects.toThrow('Revision changed');
    expect(requests.pending).toBeNull();
    await requests.submit({ revision: 2 }, 'Rename volume');
    expect(send.mock.calls[1][1]).toBe(original);
    expect(send.mock.calls[2][1]).not.toBe(original);
    expect(send.mock.calls[2][0].revision).toBe(2);
  });

  it('deduplicates concurrent submit and retry calls', async () => {
    let finish!: (value: string) => void;
    const send = vi.fn(
      () =>
        new Promise<string>((resolve) => {
          finish = resolve;
        }),
    );
    const requests = new OperationRequest(send);
    const first = requests.submit({ imageId: 'image-one' }, 'Remove image');
    expect(requests.submit({ imageId: 'image-two' }, 'Remove image')).toBe(
      first,
    );
    expect(requests.retry()).toBe(first);
    await Promise.resolve();
    finish('accepted');
    await expect(first).resolves.toBe('accepted');
    expect(send).toHaveBeenCalledTimes(1);
  });

  it.each(['binding_changed', 'compute_forbidden'])(
    'retains an original attempt when the gateway returns %s',
    async (code) => {
      const send = vi
        .fn<(input: { hostId: string }, id: string) => Promise<string>>()
        .mockRejectedValueOnce(new TypeError('Response lost'))
        .mockRejectedValueOnce(
          new APIError('Gateway refused routing', 409, code),
        )
        .mockResolvedValue('original operation');
      const requests = new OperationRequest(send);
      await expect(
        requests.submit({ hostId: 'host-original' }, 'Remove volume'),
      ).rejects.toThrow('Response lost');
      const original = requests.pending;
      await expect(requests.retry()).rejects.toThrow('Gateway refused routing');
      expect(requests.pending).toBe(original);
      await expect(requests.retry()).resolves.toBe('original operation');
      expect(send.mock.calls[2]).toEqual(send.mock.calls[0]);
    },
  );
});
