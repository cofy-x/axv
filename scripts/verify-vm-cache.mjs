import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { mkdir, mkdtemp, readFile, rm, stat } from 'node:fs/promises';
import { join } from 'node:path';
import { captureCommand, dispose, go, root, scratch } from './lib.mjs';

if (
  process.platform !== 'darwin' ||
  process.argv.slice(2).join(' ') !== '--yes'
)
  throw new Error(
    'Run on macOS with --yes to prepare two isolated basic Ubuntu VM profiles using the shared Go artifact cache.',
  );

const directory = await scratch('vc-');
const executable = join(directory, 'axv');
const profiles = [];
let cleaning;
let failed = false;
console.log(`Owned verification directory: ${directory}`);

async function run(
  program,
  args,
  env = process.env,
  timeout = 900_000,
  allowFailure = false,
) {
  return captureCommand(program, args, {
    cwd: root,
    env,
    timeout,
    allowFailure,
  });
}

function cli(profile, args, allowFailure = false) {
  return run(executable, args, profile.env, 900_000, allowFailure);
}

async function json(profile, args) {
  return JSON.parse((await cli(profile, [...args, '--json'])).stdout);
}

async function inspect(profile) {
  return json(profile, ['vm', 'inspect', profile.machineId]);
}

async function imageDigest(path) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest('hex');
}

async function packageSources(profile) {
  return (
    await run(
      profile.host.lima.binary,
      [
        '--tty=false',
        'shell',
        '--workdir=/',
        profile.machineId,
        '--',
        'cat',
        '/etc/apt/sources.list.d/ubuntu.sources',
      ],
      { ...profile.env, LIMA_HOME: profile.limaHome },
    )
  ).stdout;
}

function assertBasic(machine, status) {
  assert.equal(machine.status, status);
  assert.equal(machine.ready, false);
  if (status === 'running') assert.equal(machine.guestState, 'not-installed');
  assert.equal(machine.guestCapabilities?.length || 0, 0);
}

async function cleanup() {
  cleaning ??= (async () => {
    let safe = true;
    for (const profile of profiles.toReversed()) {
      try {
        if (profile.configured) {
          const operations = await json(profile, ['vm', 'operation', 'list']);
          for (const operation of operations.operations.filter((item) =>
            ['queued', 'running'].includes(item.status),
          )) {
            const observed = await cli(
              profile,
              ['vm', 'operation', 'wait', operation.id, '--json'],
              true,
            );
            const final = JSON.parse(observed.stdout);
            assert.ok(
              ['succeeded', 'failed'].includes(final.status),
              'Owned operation must settle before cleanup.',
            );
          }
          const inventory = await json(profile, ['vm', 'list']);
          for (const machine of inventory.machines.filter(
            (item) => item.status !== 'deleted',
          ))
            await cli(profile, ['vm', 'rm', machine.id, '--yes']);
          await cli(profile, ['daemon', 'stop']);
        }
        if (profile.limaHome)
          await rm(profile.limaHome, { recursive: true, force: true });
      } catch (error) {
        safe = false;
        console.error(
          `Cleanup failed for owned profile ${profile.home}:`,
          error.message,
        );
      }
    }
    await dispose();
    if (safe) await rm(directory, { recursive: true, force: true });
    else
      throw new Error(
        `Preserved owned profiles at ${directory}; shared cache was preserved.`,
      );
  })();
  return cleaning;
}

for (const signal of ['SIGINT', 'SIGTERM'])
  process.once(signal, () => {
    void cleanup().finally(() => process.exit(1));
  });

try {
  await run(go, [
    'build',
    '-trimpath',
    '-o',
    executable,
    './apps/server/cmd/server',
  ]);
  for (const label of ['a', 'b']) {
    const home = join(directory, label);
    const runtime = join(directory, `${label}-tmp`);
    const profile = {
      home,
      limaHome: undefined,
      configured: false,
      env: {
        ...process.env,
        PATH: '/usr/bin:/bin:/usr/sbin:/sbin',
        AXV_HOME: home,
        AXV_IMAGE_BUNDLE: '',
        AXV_COMPUTE_SOCKET: '',
        DATA_DIR: '',
        TMPDIR: runtime,
        GOTMPDIR: runtime,
      },
    };
    profiles.push(profile);
    await mkdir(runtime, { mode: 0o700 });
    const limaHome = await mkdtemp('/tmp/axv-lima-');
    profile.limaHome = limaHome;
    console.log(`Preparing profile ${label}: ${home}; Lima state ${limaHome}`);
    const prepared = await cli(profile, [
      'setup',
      '--lima-home',
      limaHome,
      '--cpus',
      '2',
      '--memory-mib',
      '4096',
      '--disk-gib',
      '32',
      '--machines',
      '2',
      '--yes',
    ]);
    profile.configured = true;
    console.log(prepared.stdout);
    if (label === 'b')
      assert.equal(
        (prepared.stdout.match(/verified cache/g) || []).length,
        2,
        'The second profile must use verified cache hits for both large artifacts.',
      );
    profile.host = JSON.parse(await readFile(join(home, 'host.json'), 'utf8'));
    const cache = await json(profile, ['cache', 'verify']);
    profile.cacheHome = cache.cacheHome;
    profile.artifacts = cache.artifacts;
    assert.ok(
      cache.artifacts.filter((item) => item.state === 'ready').length >= 2,
    );
    const created = await json(profile, [
      'vm',
      'create',
      `Cache profile ${label}`,
      '--kind',
      'task',
      '--ttl',
      '30m',
      '--cpus',
      '1',
      '--memory',
      '1024',
      '--disk',
      '8',
      '--project-disk',
      '1',
    ]);
    profile.machineId = created.machine.id;
    assert.equal(created.operation.status, 'succeeded');
    assertBasic(created.machine, 'running');
    profile.originalSources = await packageSources(profile);
    const imageInfo = await stat(profile.host.images[0].rootfs);
    profile.imageInfo = imageInfo;
    const cachedImage = cache.artifacts.find(
      (item) => item.sha256 === profile.host.images[0].rootfsSHA256,
    );
    assert.ok(cachedImage);
    assert.notEqual(
      (await stat(cachedImage.path)).ino,
      imageInfo.ino,
      'The profile image must be an independent copy.',
    );
    await cli(profile, ['vm', 'stop', profile.machineId]);
    assertBasic(await inspect(profile), 'stopped');
  }
  const [first, second] = profiles;
  assert.equal(first.cacheHome, second.cacheHome);
  assert.notEqual(first.host.dataDir, second.host.dataDir);
  assert.notEqual(first.host.lima.home, second.host.lima.home);
  assert.notEqual(first.host.lima.binary, second.host.lima.binary);
  assert.notEqual(first.host.images[0].rootfs, second.host.images[0].rootfs);
  assert.notEqual(first.imageInfo.ino, second.imageInfo.ino);
  for (const action of ['start', 'restart']) {
    await cli(first, ['vm', action, first.machineId]);
    assertBasic(await inspect(first), 'running');
    assertBasic(await inspect(second), 'stopped');
  }
  const sourceChanged = await json(first, [
    'vm',
    'package-source',
    first.machineId,
    '--source',
    'aliyun',
  ]);
  assert.equal(sourceChanged.operation.status, 'succeeded');
  const architecture = first.host.images[0].arch;
  assert.ok(['arm64', 'amd64'].includes(architecture));
  const aliyunURL = `https://mirrors.aliyun.com/${architecture === 'arm64' ? 'ubuntu-ports' : 'ubuntu'}`;
  const officialURLs =
    architecture === 'arm64'
      ? ['https://ports.ubuntu.com/ubuntu-ports']
      : [
          'https://archive.ubuntu.com/ubuntu',
          'https://security.ubuntu.com/ubuntu',
        ];
  const aliyunSources = await packageSources(first);
  assert.ok(aliyunSources.includes(`URIs: ${aliyunURL}\n`));
  assertBasic(await inspect(first), 'running');
  assertBasic(await inspect(second), 'stopped');
  await cli(first, ['vm', 'restart', first.machineId]);
  assertBasic(await inspect(first), 'running');
  assert.equal(
    await packageSources(first),
    aliyunSources,
    'The explicitly selected Aliyun source must survive a VM restart.',
  );
  const sourceRestored = await json(first, [
    'vm',
    'package-source',
    first.machineId,
    '--source',
    'official',
  ]);
  assert.equal(sourceRestored.operation.status, 'succeeded');
  const officialSources = await packageSources(first);
  for (const url of officialURLs)
    assert.ok(officialSources.includes(`URIs: ${url}\n`));
  await cli(first, ['vm', 'restart', first.machineId]);
  assertBasic(await inspect(first), 'running');
  assert.equal(
    await packageSources(first),
    officialSources,
    'The explicitly selected official source must survive a VM restart.',
  );
  for (const profile of profiles) {
    assert.equal(
      await imageDigest(profile.host.images[0].rootfs),
      profile.host.images[0].rootfsSHA256,
      'VM source maintenance must preserve immutable profile images.',
    );
  }
  await json(first, ['cache', 'verify']);
  await cli(first, ['vm', 'stop', first.machineId]);
  await cli(second, ['vm', 'start', second.machineId]);
  assert.equal(
    await packageSources(second),
    second.originalSources,
    'Other profile VM package sources changed.',
  );
  await cli(second, ['vm', 'stop', second.machineId]);
  console.log(
    'Explicit package source changes passed: one VM, official and aliyun persisted across restart, immutable images/cache and other VM preserved.',
  );
  console.log(
    JSON.stringify(
      {
        cache:
          'verified shared artifacts; second preparation made two warm hits',
        basicVMs:
          'two independent profiles created, started and stopped VMs; restart and source maintenance preserved the other profile',
        enhancedGuest:
          'not verified: official AXV image publication is pending',
        cacheHome: first.cacheHome,
      },
      null,
      2,
    ),
  );
} catch (error) {
  failed = true;
  console.error(error);
} finally {
  try {
    await cleanup();
    console.log(
      'Owned VMs, services, profiles and temporary files removed. Shared cache preserved.',
    );
  } catch (error) {
    failed = true;
    console.error(error);
  }
}
if (failed) process.exitCode = 1;
