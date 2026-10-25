import { cp, mkdir, rm } from 'node:fs/promises';
import { join } from 'node:path';
import {
  binary,
  command,
  exitAfterCleanup,
  go,
  productionTags,
  root,
} from './lib.mjs';
import { initializeTestTemp } from './test-temp.mjs';

const temporary = initializeTestTemp();

let stopping = false;

async function shutdown(code) {
  if (stopping) return;
  stopping = true;
  await exitAfterCleanup(code);
}

process.on('SIGINT', () => void shutdown(130));
process.on('SIGTERM', () => void shutdown(143));

try {
  const version = process.env.VERSION ?? 'dev';
  if (
    version.trim() !== version ||
    !/^[A-Za-z0-9][A-Za-z0-9._+-]*$/.test(version)
  ) {
    throw new Error(
      'VERSION must start with a letter or digit and contain only letters, digits, dots, underscores, pluses, or hyphens.',
    );
  }
  await command(
    process.execPath,
    [join(root, 'node_modules/vite/bin/vite.js'), 'build'],
    {
      cwd: join(root, 'apps/dashboard'),
      ownProcessGroup: true,
      env: {
        ...process.env,
        AXV_BUNDLE_REPORT:
          process.env.AXV_BUNDLE_REPORT ||
          join(temporary.root, 'dashboard-bundle.json'),
      },
    },
  );
  const destination = join(root, 'apps/server/internal/webui/dist');
  await rm(destination, { recursive: true, force: true });
  await cp(join(root, 'apps/dashboard/dist'), destination, { recursive: true });
  await mkdir(join(root, 'dist'), { recursive: true });

  await command(
    go,
    [
      'build',
      '-trimpath',
      '-tags',
      productionTags,
      '-ldflags',
      `-X main.version=${version}`,
      '-o',
      binary,
      './apps/server/cmd/server',
    ],
    { ownProcessGroup: true, env: { ...process.env, CGO_ENABLED: '0' } },
  );
  console.log(`Built ${binary}`);
} catch (error) {
  if (!stopping) console.error(error);
  await shutdown(1);
}
