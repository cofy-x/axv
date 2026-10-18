import { mkdir, realpath } from 'node:fs/promises';
import { join } from 'node:path';

/** Host Terminal starts a login shell; fixture HOME must not load user profiles. */
export async function createShellEnvironment(directory) {
  if (process.platform === 'win32') return {};
  const home = join(directory, 'shell-home');
  await mkdir(home, { mode: 0o700 });
  return {
    HOME: await realpath(home),
    SHELL: '/bin/sh',
    ENV: '',
    BASH_ENV: '',
  };
}
