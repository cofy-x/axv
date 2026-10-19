import { open } from 'node:fs/promises';
import { join } from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { reportRuntimeDiagnostics } from './test-e2e-diagnostics.mjs';

vi.mock('node:fs/promises', () => ({ open: vi.fn() }));

const profile = join('isolated-test-profile', 'axv-home');
const maximumBytes = 64 * 1024;

function logFile(content) {
  const file = {
    stat: vi.fn(async () => ({ size: content.length })),
    read: vi.fn(async (buffer, offset, length, position) => ({
      bytesRead: content.copy(buffer, offset, position, position + length),
    })),
    close: vi.fn(async () => {}),
  };
  vi.mocked(open).mockResolvedValue(file);
  return file;
}

beforeEach(() => {
  vi.mocked(open).mockReset();
});

describe('failed browser runtime diagnostics', () => {
  it('reads only the final 64 KiB of this profile log and closes its handle', async () => {
    const content = Buffer.from(
      'old record\n'.repeat(maximumBytes) + 'last record\n',
    );
    const file = logFile(content);
    const report = vi.fn();
    await reportRuntimeDiagnostics(profile, report);

    expect(open).toHaveBeenCalledExactlyOnceWith(
      join(profile, 'logs', 'daemon.log'),
      'r',
    );
    expect(file.read).toHaveBeenCalledExactlyOnceWith(
      expect.any(Buffer),
      0,
      maximumBytes,
      content.length - maximumBytes,
    );
    expect(report).toHaveBeenCalledExactlyOnceWith(
      `--- Runtime daemon diagnostics (last 64 KiB) ---\n${content.subarray(-maximumBytes).toString()}`,
    );
    expect(file.close).toHaveBeenCalledOnce();
  });

  it('keeps a partial UTF-8 character outside the printed byte limit', async () => {
    const suffix = 'a'.repeat(maximumBytes - 2);
    const file = logFile(Buffer.from(`prefix中${suffix}`));
    const report = vi.fn();
    await reportRuntimeDiagnostics(profile, report);

    expect(report).toHaveBeenCalledExactlyOnceWith(
      `--- Runtime daemon diagnostics (last 64 KiB) ---\n${suffix}`,
    );
    expect(file.close).toHaveBeenCalledOnce();
  });

  it('silently tolerates a profile with no daemon log', async () => {
    vi.mocked(open).mockRejectedValue(
      Object.assign(new Error('missing'), { code: 'ENOENT' }),
    );
    const report = vi.fn();
    await expect(
      reportRuntimeDiagnostics(profile, report),
    ).resolves.toBeUndefined();
    expect(report).not.toHaveBeenCalled();
  });

  it('reports a read failure without rejecting and still closes the handle', async () => {
    const file = logFile(Buffer.from('runtime diagnostic\n'));
    const error = Object.assign(new Error('read failed'), { code: 'EIO' });
    file.read.mockRejectedValue(error);
    const report = vi.fn();
    await expect(
      reportRuntimeDiagnostics(profile, report),
    ).resolves.toBeUndefined();
    expect(report).toHaveBeenCalledExactlyOnceWith(
      'Runtime daemon diagnostics unavailable:',
      error,
    );
    expect(file.close).toHaveBeenCalledOnce();
  });

  it('reports a close failure without rejecting the original failed shutdown', async () => {
    const file = logFile(Buffer.alloc(0));
    const error = Object.assign(new Error('close failed'), { code: 'EIO' });
    file.close.mockRejectedValue(error);
    const report = vi.fn();
    await expect(
      reportRuntimeDiagnostics(profile, report),
    ).resolves.toBeUndefined();
    expect(report).toHaveBeenCalledExactlyOnceWith(
      'Runtime daemon diagnostics unavailable:',
      error,
    );
    expect(file.close).toHaveBeenCalledOnce();
  });
});
