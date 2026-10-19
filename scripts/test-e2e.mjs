import { initializeTestTemp } from './test-temp.mjs';
import { basename, join } from 'node:path';
import { createCodexFixture } from './codex-fixture.mjs';
import { createShellEnvironment } from './shell-fixture.mjs';
import { reportRuntimeDiagnostics } from './test-e2e-diagnostics.mjs';
import {
  command,
  exitAfterCleanup,
  launchServer,
  root,
  scratch,
  spawnChild,
  stop,
} from './lib.mjs';

initializeTestTemp({ isolatedCache: true });

const dir = await scratch();
let profileHome = join(dir, 'axv-home');
let stopping = false;
let codex;
let server;
let testProcess;

async function shutdown(code = 0) {
  if (stopping) return;
  stopping = true;
  try {
    if (testProcess) await stop(testProcess);
    if (server) await stop(server.child);
    try {
      if (code !== 0) {
        const diagnostics = await codex?.diagnostics();
        if (diagnostics) console.error(diagnostics);
      }
      await codex?.verifyIsolation();
    } finally {
      await codex?.cleanup();
    }
  } catch (error) {
    console.error(error);
    code = 1;
  }
  if (code !== 0) await reportRuntimeDiagnostics(profileHome);
  await exitAfterCleanup(code, dir);
}

process.on('SIGINT', () => void shutdown(130));
process.on('SIGTERM', () => void shutdown(143));

try {
  if (process.env.E2E_SKIP_BUILD !== '1')
    await command(process.execPath, [join(root, 'scripts/build.mjs')], {
      ownProcessGroup: true,
    });
  codex = await createCodexFixture(dir);
  const shellEnvironment = await createShellEnvironment(dir);
  profileHome = codex?.env.AXV_HOME || profileHome;
  server = await launchServer({
    cwd: dir,
    profileHome,
    dataDir: join(dir, 'data'),
    addr: `127.0.0.1:${process.env.E2E_PORT || 0}`,
    env: {
      ...shellEnvironment,
      AXV_WORKSPACE_DIR: join(dir, 'axv-home', 'workspace'),
      AXV_TMUX_SOCKET: `axv-test-${basename(dir)}`,
      ...codex?.env,
    },
  });
  testProcess = spawnChild(
    process.execPath,
    [
      join(root, 'node_modules/@playwright/test/cli.js'),
      'test',
      ...process.argv.slice(2),
    ],
    {
      cwd: root,
      stdio: 'inherit',
      ownProcessGroup: true,
      env: {
        ...process.env,
        E2E_BASE_URL: server.base,
        E2E_CODEX_PROJECT: codex?.projectPath || '',
        E2E_CODEX_DEFAULT_PROJECT: codex?.defaultProjectPath || '',
        E2E_CODEX_CONFIG: codex?.configPath || '',
        E2E_CODEX_NETWORK_DIGEST: codex?.networkDigest || '',
        E2E_CODEX_TMUX_SOCKET: codex?.tmuxSocket || '',
      },
    },
  );
  server.child.once('exit', () => {
    if (!stopping) void shutdown(1);
  });
  const code = await new Promise((resolve, reject) => {
    testProcess.once('error', reject);
    testProcess.once('exit', (exitCode) => resolve(exitCode ?? 1));
  });
  await shutdown(code);
} catch (error) {
  if (!stopping) console.error(error);
  await shutdown(1);
}
