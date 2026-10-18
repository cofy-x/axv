import { describe, expect, it } from 'vitest';
import type { Machine } from './api';
import { guestTerminalReason } from './presentation';

const running: Machine = {
  id: 'vm-presentation',
  hostId: 'host-presentation',
  revision: 1,
  spec: {
    name: 'Presentation VM',
    provider: 'lima',
    imageId: 'linux',
    kind: 'development',
    resources: { cpus: 1, memoryMiB: 512, diskGiB: 8 },
    projectGiB: 1,
    retainVolume: true,
  },
  desired: 'running',
  status: 'running',
  ready: true,
  guestState: 'ready',
  guestCapabilities: ['exec'],
  reserved: true,
  createdAt: '',
  updatedAt: '',
};

describe('guest terminal availability', () => {
  it('explains a missing terminal capability while the guest is ready', () => {
    expect(guestTerminalReason(running)).toBe(
      'This image does not provide guest terminals.',
    );
    expect(
      guestTerminalReason({
        ...running,
        guestCapabilities: ['exec', 'terminals'],
      }),
    ).toBeUndefined();
  });
  it('keeps basic lifecycle, protocol and current run state distinct', () => {
    expect(
      guestTerminalReason({
        ...running,
        ready: false,
        guestState: 'not-installed',
      }),
    ).toContain('does not include');
    expect(
      guestTerminalReason({
        ...running,
        ready: false,
        guestState: 'incompatible',
      }),
    ).toContain('incompatible');
    expect(
      guestTerminalReason({
        ...running,
        desired: 'stopped',
        guestCapabilities: ['terminals'],
      }),
    ).toContain('Start this VM');
    expect(
      guestTerminalReason({
        ...running,
        ready: false,
        guestState: 'unavailable',
      }),
    ).toContain('not ready');
  });
});
