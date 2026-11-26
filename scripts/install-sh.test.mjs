import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import {
  chmod,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, expect, test } from 'vitest';
import { captureCommand, root } from './lib.mjs';

const dirs = [];
const servers = [];

afterEach(async () => {
  await Promise.all(
    servers.splice(0).map(
      (server) =>
        new Promise((resolve) => {
          server.close(() => resolve());
        }),
    ),
  );
  await Promise.all(
    dirs.splice(0).map((dir) => rm(dir, { recursive: true, force: true })),
  );
});

function hostTarget() {
  const os = process.platform === 'darwin' ? 'darwin' : 'linux';
  const arch = process.arch === 'arm64' ? 'arm64' : 'amd64';
  return { os, arch };
}

async function createFakeArchive(workDir, version, os, arch) {
  const bundle = `axv_${version}_${os}_${arch}`;
  const bundleDir = join(workDir, bundle);
  await mkdir(bundleDir, { recursive: true });
  const binPath = join(bundleDir, 'axv');
  await writeFile(
    binPath,
    `#!/usr/bin/env sh\nif [ "\${1:-}" = "version" ]; then echo "axv ${version}"; exit 0; fi\n`,
  );
  await chmod(binPath, 0o755);
  const archiveName = `${bundle}.tar.gz`;
  const archivePath = join(workDir, archiveName);
  await captureCommand('tar', ['-czf', archivePath, '-C', workDir, bundle]);
  const bytes = await readFile(archivePath);
  const sha256 = createHash('sha256').update(bytes).digest('hex');
  return { archiveName, bytes, sha256 };
}

async function startFixtureServer(handler) {
  const server = createServer(handler);
  servers.push(server);
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address();
  return `http://127.0.0.1:${port}`;
}

test('install.sh falls back to prereleases when /releases/latest returns 404, skipping guest, draft and other-platform releases', async () => {
  if (process.platform !== 'linux' && process.platform !== 'darwin') return;
  const workDir = await mkdtemp(join(tmpdir(), 'axv-install-test-'));
  dirs.push(workDir);
  const binDir = join(workDir, 'bin');
  const { os, arch } = hostTarget();
  const archive = await createFakeArchive(workDir, '0.1.0-rc.8', os, arch);
  const checksums = `${archive.sha256}  ${archive.archiveName}\n`;
  const otherTarget = os === 'linux' ? 'darwin_arm64' : 'linux_amd64';

  const baseURL = await startFixtureServer((req, res) => {
    if (req.url === '/api/releases/latest') {
      res.writeHead(404);
      res.end('{"message":"Not Found"}');
      return;
    }
    if (req.url === '/api/releases?per_page=30') {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(
        JSON.stringify([
          {
            tag_name: 'v0.1.0-guest.3',
            draft: false,
            prerelease: true,
            assets: [{ name: `axv_guest_0.1.0-guest.3_lima_arm64.tar.gz` }],
          },
          {
            tag_name: 'v0.1.0-rc.9',
            draft: true,
            prerelease: true,
            assets: [{ name: `axv_0.1.0-rc.9_${os}_${arch}.tar.gz` }],
          },
          {
            tag_name: 'v0.1.0-rc.8-other',
            draft: false,
            prerelease: true,
            assets: [{ name: `axv_0.1.0-rc.8-other_${otherTarget}.tar.gz` }],
          },
          {
            tag_name: 'v0.1.0-rc.8',
            draft: false,
            prerelease: true,
            assets: [
              { name: `axv_0.1.0-rc.8_${otherTarget}.tar.gz` },
              { name: archive.archiveName },
              { name: 'SHA256SUMS' },
            ],
          },
        ]),
      );
      return;
    }
    if (req.url === `/dl/v0.1.0-rc.8/${archive.archiveName}`) {
      res.writeHead(200);
      res.end(archive.bytes);
      return;
    }
    if (req.url === '/dl/v0.1.0-rc.8/SHA256SUMS') {
      res.writeHead(200);
      res.end(checksums);
      return;
    }
    res.writeHead(404);
    res.end();
  });

  const result = await captureCommand(
    'sh',
    [join(root, 'scripts/install.sh'), '--bin-dir', binDir],
    {
      env: {
        ...process.env,
        AXV_RELEASE_API_BASE: `${baseURL}/api`,
        AXV_RELEASE_DOWNLOAD_BASE: `${baseURL}/dl`,
        AXV_TEST_ALLOW_HTTP: '1',
      },
    },
  );
  expect(result.stdout).toContain(`Installed axv 0.1.0-rc.8 to ${binDir}/axv`);
  const versionCheck = await captureCommand(join(binDir, 'axv'), ['version']);
  expect(versionCheck.stdout.trim()).toBe('axv 0.1.0-rc.8');
});

test('install.sh prefers a matching stable release from /releases/latest and rejects checksum mismatches', async () => {
  if (process.platform !== 'linux' && process.platform !== 'darwin') return;
  const workDir = await mkdtemp(join(tmpdir(), 'axv-install-stable-'));
  dirs.push(workDir);
  const binDir = join(workDir, 'bin');
  const { os, arch } = hostTarget();
  const archive = await createFakeArchive(workDir, '1.2.0', os, arch);
  let corrupt = false;

  const baseURL = await startFixtureServer((req, res) => {
    if (req.url === '/api/releases/latest') {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(
        JSON.stringify({
          tag_name: 'v1.2.0',
          draft: false,
          prerelease: false,
          assets: [{ name: archive.archiveName }, { name: 'SHA256SUMS' }],
        }),
      );
      return;
    }
    if (req.url === `/dl/v1.2.0/${archive.archiveName}`) {
      res.writeHead(200);
      res.end(archive.bytes);
      return;
    }
    if (req.url === '/dl/v1.2.0/SHA256SUMS') {
      res.writeHead(200);
      const digest = corrupt ? '0'.repeat(64) : archive.sha256;
      res.end(`${digest}  ${archive.archiveName}\n`);
      return;
    }
    res.writeHead(404);
    res.end();
  });

  const env = {
    ...process.env,
    AXV_RELEASE_API_BASE: `${baseURL}/api`,
    AXV_RELEASE_DOWNLOAD_BASE: `${baseURL}/dl`,
    AXV_TEST_ALLOW_HTTP: '1',
  };
  const ok = await captureCommand(
    'sh',
    [join(root, 'scripts/install.sh'), '--bin-dir', binDir],
    { env },
  );
  expect(ok.stdout).toContain(`Installed axv 1.2.0 to ${binDir}/axv`);

  corrupt = true;
  const bad = await captureCommand(
    'sh',
    [join(root, 'scripts/install.sh'), '--bin-dir', join(workDir, 'bad-bin')],
    { env, allowFailure: true },
  );
  expect(bad.code).toBe(1);
  expect(bad.stderr).toContain('checksum mismatch');
});
