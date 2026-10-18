import { defineConfig } from 'vitest/config';
import { initializeTestTemp } from './scripts/test-temp.mjs';

initializeTestTemp({ isolatedCache: true });

export default defineConfig({
  test: {
    include: [
      'packages/**/*.test.ts',
      'apps/**/*.test.ts',
      'scripts/**/*.test.mjs',
    ],
  },
});
