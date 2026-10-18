import { exitAfterCleanup, go, root, spawnChild } from './lib.mjs';
import { initializeTestTemp } from './test-temp.mjs';

initializeTestTemp({ isolatedCache: true });

let stopping = false;

async function shutdown(code) {
  if (stopping) return;
  stopping = true;
  await exitAfterCleanup(code);
}

process.once('SIGINT', () => void shutdown(130));
process.once('SIGTERM', () => void shutdown(143));

try {
  const args = process.argv.slice(2);
  const child = spawnChild(
    go,
    ['test', ...(args.length ? args : ['./apps/server/...'])],
    {
      cwd: root,
      stdio: 'inherit',
      ownProcessGroup: true,
    },
  );
  const code = await new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('exit', (code, signal) => {
      if (signal && !stopping) console.error(`${go} exited with ${signal}`);
      resolve(code ?? 1);
    });
  });
  if (!stopping) await shutdown(code);
} catch (error) {
  if (!stopping) console.error(error);
  await shutdown(1);
}
