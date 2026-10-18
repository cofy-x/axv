import { join } from 'node:path';
import {
  createDevelopmentRun,
  developmentEnvironment,
  developmentProfile,
} from './dev-profile.mjs';
import {
  command,
  exitAfterCleanup,
  go,
  launchServer,
  root,
  spawnChild,
} from './lib.mjs';

let dir;
let stopping = false;

async function shutdown(code = 0) {
  if (stopping) return;
  stopping = true;
  await exitAfterCleanup(code, dir);
}

process.on('SIGINT', () => void shutdown());
process.on('SIGTERM', () => void shutdown());
try {
  const requested = await developmentProfile({ checkout: root });
  const profile = await createDevelopmentRun(requested.profileHome);
  dir = profile.directory;
  const environment = developmentEnvironment();
  console.log(`AXV development profile: ${profile.profileHome}`);
  const executable = join(
    dir,
    process.platform === 'win32' ? 'server.exe' : 'server',
  );
  await command(go, ['build', '-o', executable, './apps/server/cmd/server'], {
    env: environment,
    ownProcessGroup: true,
  });
  const server = await launchServer({
    executable,
    cwd: root,
    profileHome: profile.profileHome,
    dataDir: profile.dataDir,
    addr: process.env.ADDR || `127.0.0.1:${process.env.PORT || 0}`,
    env: environment,
  });
  const vite = spawnChild(
    process.execPath,
    [join(root, 'node_modules/vite/bin/vite.js')],
    {
      cwd: join(root, 'apps/dashboard'),
      stdio: 'inherit',
      ownProcessGroup: true,
      env: {
        ...environment,
        API_TARGET: server.base,
        WEB_PORT: process.env.WEB_PORT || '0',
      },
    },
  );
  for (const child of [server.child, vite]) {
    child.on('error', (error) => {
      console.error(error);
      void shutdown(1);
    });
    child.on('exit', (code) => {
      if (!stopping) void shutdown(code || 0);
    });
  }
} catch (error) {
  if (!stopping) console.error(error);
  await shutdown(1);
}
