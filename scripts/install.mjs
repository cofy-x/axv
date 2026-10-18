import { binary, go, root } from './lib.mjs';
import {
  installProductionExecutable,
  installationPathDiagnostics,
} from './install-lib.mjs';

const controller = new AbortController();
let interrupted = 0;

function interrupt(signal) {
  interrupted = signal === 'SIGINT' ? 130 : 143;
  controller.abort(new Error(`Installation interrupted by ${signal}.`));
}

const onInterrupt = () => interrupt('SIGINT');
const onTerminate = () => interrupt('SIGTERM');
process.once('SIGINT', onInterrupt);
process.once('SIGTERM', onTerminate);

try {
  if (process.argv.length !== 2)
    throw new Error(
      'Usage: node scripts/install.mjs (install the existing production build).',
    );
  const installed = await installProductionExecutable({
    source: binary,
    go,
    cwd: root,
    signal: controller.signal,
  });
  console.log(`Installed ${installed.version} at ${installed.path}`);
  for (const message of await installationPathDiagnostics(installed.path))
    console.log(message);
  console.log(
    'Run axv to open your workstation. Running services keep their current executable until explicitly restarted.',
  );
} catch (error) {
  const reason = error.signal
    ? `${error.message.trim()} (terminated by ${error.signal})`
    : error.message;
  console.error(`Installation failed: ${reason}`);
  process.exitCode = interrupted || 1;
} finally {
  process.removeListener('SIGINT', onInterrupt);
  process.removeListener('SIGTERM', onTerminate);
}
