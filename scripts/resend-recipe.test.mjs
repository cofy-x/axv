import { execFile } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { describe, expect, it } from 'vitest';

const execute = promisify(execFile);
const root = fileURLToPath(new URL('../', import.meta.url));

describe.skipIf(process.platform === 'win32')(
  'Resend development recipe',
  () => {
    it('preserves send identity and fails closed across provider and clock failures', async () => {
      const result = await execute(
        'python3',
        [
          '-B',
          '-m',
          'unittest',
          'discover',
          '-s',
          'recipes/resend',
          '-p',
          'test_send.py',
          '-v',
        ],
        { cwd: root, env: process.env, timeout: 15_000 },
      );
      expect(result.stderr).toMatch(/Ran \d+ tests/);
      expect(result.stderr).toMatch(/\bOK\b/);
    });
  },
);
