import { describe, expect, it } from 'vitest';
import { cacheHitRate, formatTrafficBytes } from './traffic-format';

describe('formatTrafficBytes', () => {
  it('formats zero, bytes and binary IEC units cleanly', () => {
    expect(formatTrafficBytes(0)).toBe('0 B');
    expect(formatTrafficBytes(512)).toBe('512 B');
    expect(formatTrafficBytes(1536)).toBe('1.50 KiB');
    expect(formatTrafficBytes(24 * 1024 * 1024)).toBe('24.0 MiB');
    expect(formatTrafficBytes(3 * 1024 * 1024 * 1024)).toBe('3.00 GiB');
  });
});

describe('cacheHitRate', () => {
  it('calculates bounded percentages safely', () => {
    expect(cacheHitRate(0, 0)).toBe('0%');
    expect(cacheHitRate(200, 150)).toBe('75%');
    expect(cacheHitRate(200, 5)).toBe('2.5%');
    expect(cacheHitRate(100, 140)).toBe('100%');
  });
});
