import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { createConnection, createServer } from 'node:net';
import {
  access,
  mkdir,
  mkdtemp,
  readFile,
  rm,
  writeFile,
} from 'node:fs/promises';
import { isAbsolute, join } from 'node:path';
import { promisify } from 'node:util';
import {
  dispose,
  go,
  launchServer,
  root,
  scratch,
  spawnChild,
  stop,
} from './lib.mjs';

if (process.platform !== 'darwin')
  throw new Error('Native VM preparation verification requires macOS/VZ.');
if (process.argv.slice(2).join(' ') !== '--yes')
  throw new Error(
    'Run with --yes to install pinned Lima and import a built AXV image bundle into an isolated profile and create temporary VMs for Web, CLI and SSH verification.',
  );

const imageBundle = process.env.AXV_VM_VERIFY_IMAGE_BUNDLE;
const preserveOnFailure = process.env.AXV_VM_VERIFY_KEEP_FAILURE === '1';
assert.ok(
  imageBundle && isAbsolute(imageBundle),
  'Set AXV_VM_VERIFY_IMAGE_BUNDLE to an absolute built Lima bundle manifest path.',
);
await access(imageBundle);
// Every artifact uses the shared test root; Lima alone needs a short socket path.
const dir = await scratch('axv-vm-');
const profile = join(dir, 'profile');
const runtimeTemporaryDirectory = join(dir, 'runtime-tmp');
const shortLimaRuntime = Buffer.byteLength(join(profile, 'lima')) > 41;
let limaDirectory;
try {
  limaDirectory = shortLimaRuntime
    ? await mkdtemp('/tmp/axv-lima-')
    : join(profile, 'lima');
} catch (error) {
  await rm(dir, { recursive: true, force: true });
  throw error;
}
console.log(`Owned verification directory: ${dir}`);
if (shortLimaRuntime)
  console.log(
    `Owned short Lima runtime directory (macOS socket-path limit): ${limaDirectory}`,
  );
const installDirectory = join(dir, 'go-bin');
const executable = join(installDirectory, 'axv');
const browserDirectory = join(dir, 'browser-bin');
const execute = promisify(execFile);
const environment = {
  ...process.env,
  PATH: `${browserDirectory}:/usr/bin:/bin:/usr/sbin:/sbin`,
  AXV_HOME: profile,
  // Lima may reuse this environment after the verifier has preserved a failure.
  TMPDIR: runtimeTemporaryDirectory,
  GOTMPDIR: runtimeTemporaryDirectory,
  AXV_COMPUTE_SOCKET: '',
  AXV_WORKSPACE_DIR: '',
  AXV_TMUX_SOCKET: `axv-vm-${randomUUID()}`,
  DATA_DIR: '',
  ADDR: '127.0.0.1:0',
  PORT: '',
};
let server;
let base;
let instanceId;
let daemonStarted = false;
let machineId;
let createRequest;
let cliMachineId;
let cliCreateRequestId;
let hostId;
let cleaning;
let lastPhase;
let verificationFailed = false;
const probes = new Set();
const sockets = new Set();
let echoServer;

function probe(program, args, options = {}) {
  const { stdin = 'pipe', ...spawnOptions } = options;
  const child = spawnChild(program, args, {
    cwd: dir,
    env: environment,
    stdio: [stdin, 'pipe', 'pipe'],
    ...spawnOptions,
  });
  probes.add(child);
  const result = {
    child,
    stdout: '',
    stderr: '',
    error: undefined,
    closed: undefined,
  };
  child.stdout.on('data', (chunk) => {
    result.stdout += chunk.toString();
  });
  child.stderr.on('data', (chunk) => {
    result.stderr += chunk.toString();
  });
  child.once('error', (error) => {
    result.error = error;
  });
  result.closed = new Promise((resolve) =>
    child.once('close', (code) => {
      probes.delete(child);
      resolve(code);
    }),
  );
  return result;
}

async function run(program, args, options = {}) {
  const { input = '', timeout = 60_000, exitCode = 0, onOutput } = options;
  const result = probe(program, args);
  if (onOutput) result.child.stderr.on('data', () => onOutput(result));
  result.child.stdin.on('error', () => {});
  result.child.stdin.end(input);
  const timer = setTimeout(() => {
    result.error = new Error(`${program} ${args[0]} timed out`);
    void stop(result.child);
  }, timeout);
  const code = await result.closed;
  clearTimeout(timer);
  if (result.error) throw result.error;
  if (exitCode !== null)
    assert.equal(
      code,
      exitCode,
      `${program} ${args.join(' ')}: ${result.stderr}\n${result.stdout}`,
    );
  return { ...result, code };
}

function cli(args, options) {
  return run(executable, ['vm', ...args], options);
}

async function cliJSON(args, options) {
  return JSON.parse((await cli([...args, '--json'], options)).stdout);
}

function shellQuote(value) {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

async function liveProbe(result) {
  if (result.error) throw result.error;
  if (result.child.exitCode !== null || result.child.signalCode !== null) {
    await result.closed;
    throw new Error(
      `SSH forwarding probe exited before readiness (${result.child.signalCode || result.child.exitCode}): ${result.stderr || result.stdout || 'no diagnostic output'}`,
    );
  }
}

async function tcpRead(port, expected) {
  return new Promise((resolve, reject) => {
    const socket = createConnection({ host: '127.0.0.1', port });
    sockets.add(socket);
    let output = '';
    let complete = false;
    socket.setTimeout(3000, () =>
      socket.destroy(new Error('TCP probe timed out')),
    );
    socket.on('error', reject);
    socket.on('data', (data) => {
      output += data.toString();
      if (output.includes(expected)) {
        complete = true;
        socket.destroy();
        resolve(output);
      }
    });
    socket.once('close', () => {
      sockets.delete(socket);
      if (!complete)
        reject(new Error('TCP probe closed before its expected response'));
    });
  });
}

async function unusedPort() {
  const listener = createServer();
  await new Promise((resolve, reject) => {
    listener.once('error', reject);
    listener.listen(0, '127.0.0.1', resolve);
  });
  const port = listener.address().port;
  await new Promise((resolve) => listener.close(resolve));
  return port;
}

async function api(path, method = 'GET', body, address = base) {
  const response = await fetch(address + path, {
    method,
    headers: {
      Origin: address,
      'Content-Type': 'application/json',
      ...(instanceId ? { 'X-AXV-Instance-ID': instanceId } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(30_000),
  });
  const value = await response.json();
  assert.ok(
    response.ok,
    `${method} ${path}: HTTP ${response.status}: ${JSON.stringify(value)}`,
  );
  return value;
}

async function until(check, message, timeout = 120_000) {
  const deadline = Date.now() + timeout;
  let value;
  while (Date.now() < deadline) {
    value = await check();
    if (value) return value;
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  throw new Error(`Timed out: ${message}`);
}

async function daemon(action) {
  const result = await execute(executable, ['daemon', action], {
    cwd: dir,
    env: environment,
    timeout: 60_000,
  });
  if (result.stdout.trim()) console.log(result.stdout.trim());
}

async function install() {
  await execute(process.execPath, [join(root, 'scripts/install.mjs')], {
    cwd: dir,
    env: { ...process.env, GOBIN: installDirectory },
    timeout: 45_000,
  });
}

async function reopen() {
  const result = await execute(executable, [], {
    cwd: dir,
    env: environment,
    timeout: 60_000,
  });
  console.log(result.stdout.trim());
}

async function guestExec(argv) {
  const result = await api(
    `/api/compute/machines/${machineId}/guest/exec`,
    'POST',
    {
      argv,
      timeoutSeconds: 10,
    },
  );
  assert.equal(result.exitCode, 0, result.stderr);
  return result.stdout;
}

async function discover() {
  const state = JSON.parse(
    await readFile(join(profile, 'run', 'desktop.json'), 'utf8'),
  );
  base = state.url;
  instanceId = state.instanceId;
  assert.equal((await api('/api/runtime')).instanceId, instanceId);
  return state;
}

async function operation(id) {
  return until(
    async () => {
      const value = await api(`/api/compute/operations/${id}`);
      if (value.status === 'failed')
        throw new Error(`VM operation failed: ${JSON.stringify(value)}`);
      return value.status === 'succeeded' && value;
    },
    'VM operation completes',
    10 * 60_000,
  );
}

async function machineAction(action) {
  const current = await api(`/api/compute/machines/${machineId}`);
  const accepted = await api(
    `/api/compute/machines/${machineId}/actions`,
    'POST',
    { action, revision: current.revision, requestId: randomUUID() },
  );
  await operation(accepted.operation.id);
}

async function ready() {
  return until(async () => {
    const value = await api('/api/compute/environment');
    return value.runtime?.state === 'ready' && value;
  }, 'the configured VM service is ready');
}

async function verifyCLI(info, image) {
  await daemon('stop');
  base = undefined;
  await assert.rejects(access(join(profile, 'run', 'desktop.json')), {
    code: 'ENOENT',
  });
  const listed = await cliJSON(['list']);
  assert.equal(
    listed.machines.find((item) => item.id === machineId).status,
    'stopped',
  );
  await assert.rejects(access(join(profile, 'run', 'desktop.json')), {
    code: 'ENOENT',
  });
  console.log('CLI activated the VM host without opening the desktop.');
  const images = await cliJSON(['images']);
  assert.ok(images.images.some((item) => item.id === image.id));
  const name = `CLI verification ${randomUUID().slice(0, 8)}`;
  const created = await cliJSON(
    ['create', `  ${name}  `, '--image', image.id, '--kind', 'task'],
    {
      timeout: 11 * 60_000,
      onOutput: (result) => {
        cliCreateRequestId ||= /Request: (cli-[0-9a-f]{32})/.exec(
          result.stderr,
        )?.[1];
      },
    },
  );
  cliMachineId = created.machine.id;
  assert.equal(created.operation.requestId, cliCreateRequestId);
  assert.equal(created.operation.status, 'succeeded');
  assert.equal(created.machine.hostId, hostId);
  assert.equal(created.machine.spec.name, name);
  assert.equal(created.machine.status, 'running');
  assert.equal(created.machine.ready, true);
  assert.equal(
    created.machine.spec.resources.cpus,
    info.defaults.spec.resources.cpus,
  );
  assert.equal(
    created.machine.spec.resources.memoryMiB,
    info.defaults.spec.resources.memoryMiB,
  );
  assert.equal(
    created.machine.spec.resources.diskGiB,
    Math.max(info.defaults.spec.resources.diskGiB, image.minRootGiB || 0),
  );
  assert.equal(created.machine.spec.projectGiB, info.defaults.spec.projectGiB);
  assert.equal(
    created.machine.spec.retainVolume,
    info.defaults.taskRetainVolume,
  );
  assert.equal(created.machine.spec.ttlSeconds, info.defaults.taskTtlSeconds);
  assert.equal((await cliJSON(['inspect', name])).id, cliMachineId);
  assert.equal(
    (await cliJSON(['operation', 'get', created.operation.id])).requestId,
    cliCreateRequestId,
  );
  assert.equal(
    (
      await cliJSON([
        'operation',
        'wait',
        '--host-id',
        hostId,
        '--request-id',
        cliCreateRequestId,
      ])
    ).id,
    created.operation.id,
  );
  const network = await cliJSON(['ip', cliMachineId]);
  assert.equal(network.hostId, hostId);
  assert.equal(network.machineId, cliMachineId);
  assert.ok(network.bootId && network.observedAt);
  assert.ok(network.addresses.length > 0);
  for (const address of network.addresses) {
    assert.ok(address.interface && address.address && address.family);
    assert.equal(address.reachability.from, 'deployment-host');
    assert.ok(address.reachability.state && address.reachability.reason);
  }
  console.log(
    'CLI creation, shared defaults, exact request lookup and current network metadata passed.',
  );
  const token = `axv-ssh-${randomUUID()}`;
  const remote = (command, options) =>
    cli(['ssh', '-T', cliMachineId, '--', command], options);
  assert.equal(
    (await remote(`printf '%s' ${shellQuote(token)}`)).stdout,
    token,
  );
  await remote('exit 42', { exitCode: 42 });
  const shell = await cli(['ssh', '-T', cliMachineId], {
    input: `printf '%s\\n' ${shellQuote(token)}\nexit\n`,
  });
  assert.ok(shell.stdout.includes(token), shell.stdout);

  const configuration = (await cli(['ssh-config', cliMachineId])).stdout;
  const alias = /^Host (\S+)$/m.exec(configuration)?.[1];
  assert.ok(alias, configuration);
  const configPath = join(dir, 'ssh config');
  const source = join(dir, 'transfer source.txt');
  const scpResult = join(dir, 'scp result.txt');
  const sftpResult = join(dir, 'sftp result.txt');
  const content = `AXV standard SSH transfer\n${randomUUID()}\n`;
  await writeFile(configPath, configuration, { mode: 0o600 });
  await writeFile(source, content);
  const guestSCP = `/projects/axv-scp-${randomUUID()}.txt`;
  const guestSFTP = `/projects/axv-sftp-${randomUUID()}.txt`;
  await run('/usr/bin/scp', ['-F', configPath, source, `${alias}:${guestSCP}`]);
  await run('/usr/bin/scp', [
    '-F',
    configPath,
    `${alias}:${guestSCP}`,
    scpResult,
  ]);
  assert.equal(await readFile(scpResult, 'utf8'), content);
  const batch = join(dir, 'sftp.batch');
  await writeFile(
    batch,
    `put "${source}" "${guestSFTP}"\nget "${guestSFTP}" "${sftpResult}"\n`,
  );
  await run('/usr/bin/sftp', ['-F', configPath, '-b', batch, alias]);
  assert.equal(await readFile(sftpResult, 'utf8'), content);

  console.log(
    'Standard SSH shell, exec status, saved configuration, scp and sftp passed.',
  );
  const ptyCheck = join(dir, 'verify-vm-pty');
  await execute(
    go,
    ['build', '-o', ptyCheck, join(root, 'scripts/verify-vm-pty.go')],
    {
      cwd: join(root, 'apps/server'),
      env: process.env,
      timeout: 60_000,
    },
  );
  const tty = await run(ptyCheck, [executable, cliMachineId], {
    timeout: 3 * 60_000,
  });
  console.log(tty.stdout.trim());

  const stopped = await cliJSON(['stop', cliMachineId], {
    timeout: 11 * 60_000,
  });
  assert.equal(stopped.machine.status, 'stopped');
  const refused = await cli(['ssh', '-T', cliMachineId, '--', 'true'], {
    exitCode: 1,
  });
  assert.match(refused.stderr, /not running/);
  assert.equal((await cliJSON(['inspect', cliMachineId])).status, 'stopped');
  assert.equal(
    (
      await cli(
        [
          'ssh',
          '--start',
          '-T',
          cliMachineId,
          '--',
          `printf '%s' ${shellQuote(token)}`,
        ],
        { timeout: 11 * 60_000 },
      )
    ).stdout,
    token,
  );
  const nextBoot = await cliJSON(['ip', cliMachineId]);
  assert.notEqual(nextBoot.bootId, network.bootId);
  assert.equal(
    (
      await run('/usr/bin/ssh', [
        '-F',
        configPath,
        '-T',
        alias,
        `cat ${guestSCP}`,
      ])
    ).stdout,
    content,
  );
  await cliJSON(['stop', cliMachineId], { timeout: 11 * 60_000 });
  const started = await cliJSON(['start', cliMachineId], {
    timeout: 11 * 60_000,
  });
  assert.equal(started.machine.status, 'running');
  const sshConnection = (
    await remote('printf "%s\\n" "$SSH_CONNECTION"')
  ).stdout
    .trim()
    .split(/\s+/);
  const guestSSHPort = Number(sshConnection.at(-1));
  assert.ok(guestSSHPort > 0);
  const localPort = await unusedPort();
  const localArgs = [
    'vm',
    'ssh',
    '-N',
    '-T',
    '-L',
    `127.0.0.1:${localPort}:127.0.0.1:${guestSSHPort}`,
    cliMachineId,
  ];
  console.log(
    `Forwarding probe with stdin=/dev/null: ${executable} ${localArgs.join(' ')}`,
  );
  const local = probe(executable, localArgs, { stdin: 'ignore' });
  await until(
    async () => {
      await liveProbe(local);
      try {
        return await tcpRead(localPort, 'SSH-2.0-');
      } catch {
        return false;
      }
    },
    'standard SSH local forwarding',
    30_000,
  );
  await stop(local.child);
  console.log('Standard SSH local forwarding passed.');

  echoServer = createServer((socket) => {
    sockets.add(socket);
    socket.once('close', () => sockets.delete(socket));
    socket.pipe(socket);
  });
  await new Promise((resolve, reject) => {
    echoServer.once('error', reject);
    echoServer.listen(0, '127.0.0.1', resolve);
  });
  const reverseArgs = [
    'vm',
    'ssh',
    '-N',
    '-T',
    '-R',
    `127.0.0.1:0:127.0.0.1:${echoServer.address().port}`,
    cliMachineId,
  ];
  console.log(
    `Forwarding probe with stdin=/dev/null: ${executable} ${reverseArgs.join(' ')}`,
  );
  const reverse = probe(executable, reverseArgs, { stdin: 'ignore' });
  const remotePort = await until(
    async () => {
      await liveProbe(reverse);
      return /Allocated port (\d+)/.exec(reverse.stderr)?.[1];
    },
    'standard SSH remote forwarding',
    30_000,
  );
  const echoCommand = `exec 3<>/dev/tcp/127.0.0.1/${remotePort}; printf '%s\\n' ${shellQuote(token)} >&3; IFS= read -r reply <&3; printf '%s' "$reply"`;
  assert.equal(
    (await remote(`/bin/bash -c ${shellQuote(echoCommand)}`)).stdout,
    token,
  );
  await stop(reverse.child);
  for (const socket of sockets) socket.destroy();
  await new Promise((resolve) => echoServer.close(resolve));
  echoServer = undefined;
  console.log('Standard SSH remote forwarding passed.');

  const removed = await cliJSON(['rm', cliMachineId, '--yes'], {
    timeout: 11 * 60_000,
  });
  assert.equal(removed.machine.status, 'deleted');
  const deletedID = cliMachineId;
  cliMachineId = undefined;
  cliCreateRequestId = undefined;
  assert.ok(
    !(await cliJSON(['list'])).machines.some((item) => item.id === deletedID),
  );
  await assert.rejects(access(join(profile, 'run', 'desktop.json')), {
    code: 'ENOENT',
  });
  console.log(
    'Native CLI and SSH passed: host-only activation, shared defaults, lifecycle, request lookup, current IP metadata, shell/exec status, saved configuration, scp/sftp, local/remote forwarding and PTY resize.',
  );
}

function cleanup(preserve = false) {
  cleaning ??= (async () => {
    let safeToRemove = true;
    try {
      await Promise.all([...probes].map(stop));
      for (const socket of sockets) socket.destroy();
      if (echoServer) await new Promise((resolve) => echoServer.close(resolve));
      // Recover acceptance by the original request, without submitting a new mutation.
      for (const owned of [
        { id: cliMachineId, requestId: cliCreateRequestId },
        { id: machineId, requestId: createRequest?.requestId },
      ]) {
        if (preserve) continue;
        if (!owned.id && !owned.requestId) continue;
        assert.ok(
          hostId,
          'The original VM host identity must be known for cleanup.',
        );
        if (owned.requestId) {
          const recovered = await cliJSON(
            [
              'operation',
              'wait',
              '--host-id',
              hostId,
              '--request-id',
              owned.requestId,
              '--timeout',
              '2m',
            ],
            { timeout: 130_000, exitCode: null },
          );
          assert.ok(
            ['succeeded', 'failed'].includes(recovered.status),
            `Creation outcome is still unknown: ${JSON.stringify(recovered)}`,
          );
          if (owned.id) assert.equal(recovered.machineId, owned.id);
          owned.id = recovered.machineId;
        }
        assert.ok(owned.id, 'Accepted creation must identify its exact VM.');
        const current = await cliJSON(['inspect', owned.id]);
        assert.equal(current.hostId, hostId);
        if (current.status !== 'deleted')
          await cliJSON(['rm', owned.id, '--yes'], { timeout: 11 * 60_000 });
      }
      if (!preserve) {
        machineId = undefined;
        createRequest = undefined;
        cliMachineId = undefined;
        cliCreateRequestId = undefined;
      }
    } catch (error) {
      safeToRemove = false;
      console.error('VM cleanup needs attention:', error);
    }
    try {
      if (daemonStarted) await daemon('stop');
      if (server) await stop(server.child);
      await dispose();
    } catch (error) {
      safeToRemove = false;
      console.error('Service cleanup needs attention:', error);
    }
    if (preserve) {
      const prefix = `AXV_HOME=${shellQuote(profile)} ${shellQuote(executable)}`;
      console.error(
        `Debug preservation requested. Owned verification profile: ${dir}`,
      );
      console.error(`Host: ${hostId || 'not prepared'}`);
      if (shortLimaRuntime)
        console.error(
          `Owned short Lima runtime retained for diagnosis: ${limaDirectory}`,
        );
      for (const owned of [
        { id: cliMachineId, requestId: cliCreateRequestId },
        { id: machineId, requestId: createRequest?.requestId },
      ]) {
        if (owned.requestId)
          console.error(
            `Recover exact creation: ${prefix} vm operation wait --host-id ${shellQuote(hostId || '')} --request-id ${shellQuote(owned.requestId)}`,
          );
        if (owned.id)
          console.error(
            `Delete owned VM: ${prefix} vm rm ${shellQuote(owned.id)} --yes`,
          );
      }
      console.error(`After deleting owned VMs: ${prefix} daemon stop`);
      console.error(
        `Then remove only the owned verification directory: ${dir}`,
      );
      if (shortLimaRuntime)
        console.error(
          `Also remove only the owned Lima runtime directory: ${limaDirectory}`,
        );
    } else if (safeToRemove) {
      if (shortLimaRuntime)
        await rm(limaDirectory, { recursive: true, force: true });
      await rm(dir, { recursive: true, force: true });
    }
    if (!safeToRemove)
      throw new Error(
        `Cleanup was incomplete. Retained the verification profile at ${dir}${shortLimaRuntime ? ` and its Lima runtime at ${limaDirectory}` : ''}.`,
      );
  })();
  return cleaning;
}

for (const signal of ['SIGINT', 'SIGTERM'])
  process.once(signal, () => {
    void cleanup().finally(() => process.exit(1));
  });

try {
  await mkdir(runtimeTemporaryDirectory, { mode: 0o700 });
  await install();
  await mkdir(browserDirectory, { mode: 0o700 });
  await writeFile(join(browserDirectory, 'open'), '#!/bin/sh\nexit 17\n', {
    mode: 0o700,
  });
  server = await launchServer({
    executable,
    cwd: dir,
    profileHome: profile,
    dataDir: join(profile, 'data'),
    env: environment,
  });
  base = server.base;
  const originalDesktop = await discover();
  await access(join(profile, 'data', 'desktop.sqlite'));
  const initial = await api('/api/compute/preparation/check');
  assert.equal(initial.configured, false);
  assert.equal((await api('/api/compute/info')).configured, false);
  const document = await api('/api/entries', 'POST', {
    parentId: 'documents',
    kind: 'file',
    name: 'VM preparation continuity.txt',
    content: 'The desktop remains available during VM preparation.',
  });
  const planInput = {
    imageSource: 'bundle',
    imageBundle,
    storageRoot: profile,
    limaHome: limaDirectory,
    cpus: 2,
    memoryMiB: 4096,
    diskGiB: 64,
    machines: 2,
  };
  const plan = await api('/api/compute/preparation/plan', 'POST', planInput);
  assert.equal(plan.canStart, true, JSON.stringify(plan.checks));
  assert.ok(
    plan.downloads.some((item) => /lima/i.test(item.name)),
    'A fresh profile with a system-only PATH must plan private Lima installation using a verified cache hit or a download.',
  );
  console.log(
    'Native preparation plan:',
    JSON.stringify({ actions: plan.actions, downloads: plan.downloads }),
  );
  const request = {
    requestId: randomUUID(),
    planId: plan.id,
    confirmed: true,
  };
  const job = await api('/api/compute/preparation/jobs', 'POST', request);
  const duplicate = await api('/api/compute/preparation/jobs', 'POST', request);
  assert.equal(duplicate.id, job.id);
  const follower = spawnChild(executable, ['setup'], {
    cwd: dir,
    env: environment,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let followerOutput = '';
  follower.stdout.on('data', (data) => {
    followerOutput += data.toString();
  });
  follower.stderr.on('data', (data) => {
    followerOutput += data.toString();
  });
  await until(() => {
    if (follower.exitCode !== null) throw new Error(followerOutput);
    return followerOutput.includes(job.id);
  }, 'CLI follows the same Web preparation task');
  follower.kill('SIGINT');
  await until(
    () => follower.exitCode !== null || follower.signalCode !== null,
    'CLI detaches',
  );
  assert.notEqual(
    (await api(`/api/compute/preparation/jobs/${job.id}`)).status,
    'cancelled',
  );
  console.log(
    'CLI followed the same preparation task and detached without cancelling it.',
  );
  await until(
    async () => {
      const task = await api(`/api/compute/preparation/jobs/${job.id}`);
      if (task.phase !== lastPhase) {
        console.log(`Preparation: ${task.phase} (${task.status})`);
        lastPhase = task.phase;
      }
      if (['failed', 'interrupted', 'cancelled'].includes(task.status))
        throw new Error(`Preparation did not finish: ${JSON.stringify(task)}`);
      assert.equal(
        (await api('/api/runtime')).instanceId,
        originalDesktop.instanceId,
      );
      return task.status === 'succeeded' && task;
    },
    'private runtime, image and service preparation',
    30 * 60_000,
  );
  const environmentReady = await ready();
  hostId = environmentReady.runtime.hostId;
  assert.equal(
    (await api('/api/runtime')).instanceId,
    originalDesktop.instanceId,
  );
  assert.equal(
    (await api(`/api/entries/${document.entry.id}`)).content,
    'The desktop remains available during VM preparation.',
  );
  const hostConfig = JSON.parse(
    await readFile(join(profile, 'host.json'), 'utf8'),
  );
  assert.ok(hostConfig.lima.binary.startsWith(join(profile, 'tools', 'lima')));
  const info = await api('/api/compute/info');
  const image = info.images.find((item) => item.provider === 'lima');
  assert.ok(image, 'Verified setup must expose a native Lima image.');
  createRequest = {
    requestId: randomUUID(),
    start: true,
    spec: {
      name: `Preparation verification ${randomUUID().slice(0, 8)}`,
      provider: 'lima',
      imageId: image.id,
      kind: 'task',
      resources: {
        cpus: 2,
        memoryMiB: 2048,
        diskGiB: Math.max(10, image.minRootGiB || 0),
      },
      projectGiB: 2,
      retainVolume: false,
      ttlSeconds: 3600,
    },
  };
  const created = await api('/api/compute/machines', 'POST', createRequest);
  machineId = created.machine.id;
  await operation(created.operation.id);
  const health = await api(`/api/compute/machines/${machineId}/guest/health`);
  assert.equal(health.machineId, machineId);
  assert.ok(health.bootId);
  const volumeId = (await api(`/api/compute/machines/${machineId}`)).spec
    .volumeId;
  assert.ok(volumeId);
  const projectToken = randomUUID();
  await guestExec([
    'sh',
    '-c',
    'printf "%s" "$1" > axv-install-check.txt',
    'sh',
    projectToken,
  ]);
  console.log(`Created a ready native VM: ${machineId}`);

  await install();
  await reopen();
  assert.equal((await discover()).instanceId, originalDesktop.instanceId);
  assert.equal(await guestExec(['cat', 'axv-install-check.txt']), projectToken);
  assert.equal(
    (await api(`/api/compute/machines/${machineId}/guest/health`)).bootId,
    health.bootId,
    'Reinstallation must leave the foreground service and VM running.',
  );

  await stop(server.child);
  assert.equal(server.child.exitCode, 0);
  server = undefined;
  base = undefined;
  daemonStarted = true;
  await reopen();
  await discover();
  const daemonReady = await ready();
  assert.equal(daemonReady.runtime.hostId, environmentReady.runtime.hostId);
  assert.equal(
    (await api(`/api/compute/machines/${machineId}/guest/health`)).bootId,
    health.bootId,
    'Foreground shutdown and a new host must preserve the VM boot.',
  );
  await daemon('start');
  await daemon('restart');
  await discover();
  await ready();
  assert.equal(
    (await api(`/api/compute/machines/${machineId}`)).spec.volumeId,
    volumeId,
  );
  assert.equal(await guestExec(['cat', 'axv-install-check.txt']), projectToken);
  assert.equal(
    (await api(`/api/compute/machines/${machineId}/guest/health`)).bootId,
    health.bootId,
    'Desktop restart must preserve the VM and independent host.',
  );
  await machineAction('stop');
  await daemon('stop');
  base = undefined;
  await daemon('start');
  await discover();
  await ready();
  await until(async () => {
    const machine = await api(`/api/compute/machines/${machineId}`);
    assert.equal(machine.desired, 'stopped');
    return machine.status === 'stopped';
  }, 'a stopped VM remains stopped across service startup');
  const tasks = await api('/api/compute/preparation/check');
  assert.equal(tasks.activeJob, undefined);
  assert.equal(tasks.latestJob.id, job.id);
  await verifyCLI(info, image);
  console.log(
    'Native VM preparation passed: installed entry and default data, private Lima, verified image, live activation, CLI/Web task sharing, VM/guest creation, reinstall and restart preserving boot and project data, foreground ownership, daemon reuse and explicit VM startup.',
  );
} catch (error) {
  verificationFailed = true;
  throw error;
} finally {
  await cleanup(verificationFailed && preserveOnFailure);
}
