import { command, exitAfterCleanup, go } from './lib.mjs';
import { initializeTestTemp } from './test-temp.mjs';

initializeTestTemp();

let stopping = false;

async function shutdown(code) {
  if (stopping) return;
  stopping = true;
  await exitAfterCleanup(code);
}

process.once('SIGINT', () => void shutdown(130));
process.once('SIGTERM', () => void shutdown(143));

try {
  await command(
    go,
    ['build', '-o', 'dist/server-dev', './apps/server/cmd/server'],
    { ownProcessGroup: true },
  );
} catch (error) {
  if (!stopping) console.error(error);
  await shutdown(1);
}
