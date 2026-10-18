import { expect, it } from 'vitest';
import { readableSize } from './format';

it('formats upload limits without rounding bytes to zero', () => {
  expect(readableSize(1)).toBe('1 bytes');
  expect(readableSize(10 * 1024 * 1024)).toBe('10.0 MB');
});
