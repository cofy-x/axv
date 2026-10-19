import { mkdir } from 'node:fs/promises';
import { delimiter, isAbsolute, join } from 'node:path';
import { captureCommand, command, exitAfterCleanup, go, root } from './lib.mjs';
import { initializeTestTemp } from './test-temp.mjs';

const version = 'v1.9.0';
const temporary = initializeTestTemp();
let stopping = false;

async function shutdown(code) {
  if (stopping) return;
  stopping = true;
  await exitAfterCleanup(code);
}

process.once('SIGINT', () => void shutdown(130));
process.once('SIGTERM', () => void shutdown(143));

try {
  const bin = join(temporary.directory, 'bin');
  await mkdir(bin, { mode: 0o700 });
  const env = {
    ...process.env,
    CGO_ENABLED: '0',
    GOTOOLCHAIN: 'local',
    GOWORK: 'off',
  };
  // An inherited GOVERSION would override the scanner's actual Go detection.
  delete env.GOVERSION;
  const { stdout } = await captureCommand(go, ['env', 'GOROOT'], { env });
  const goroot = stdout.trim();
  if (!isAbsolute(goroot)) throw new Error('Go returned an invalid GOROOT.');
  // govulncheck resolves its own go command from PATH, independently of GO.
  env.PATH = `${join(goroot, 'bin')}${delimiter}${env.PATH || ''}`;
  console.log(
    `Installing govulncheck ${version} into private temporary storage.`,
  );
  await command(
    go,
    ['install', `golang.org/x/vuln/cmd/govulncheck@${version}`],
    {
      env: { ...env, GOBIN: bin },
      ownProcessGroup: true,
    },
  );
  // Text output preserves nonzero vulnerability exits; JSON/SARIF do not.
  await command(
    join(bin, `govulncheck${process.platform === 'win32' ? '.exe' : ''}`),
    ['-test', '-tags', 'production', './...'],
    { cwd: join(root, 'apps/server'), env, ownProcessGroup: true },
  );
  await shutdown(0);
} catch (error) {
  if (!stopping) console.error(error);
  await shutdown(1);
}
