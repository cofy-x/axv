import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { createReadStream } from 'node:fs';
import {
  access,
  chmod,
  copyFile,
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  rename,
  rm,
  writeFile,
} from 'node:fs/promises';
import http from 'node:http';
import { createConnection, createServer, isIPv4 } from 'node:net';
import { availableParallelism, networkInterfaces } from 'node:os';
import { basename, dirname, isAbsolute, join, resolve } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { dispose, go, root, scratch, spawnChild, stop } from './lib.mjs';
import { verifyProjectWorkflow } from './lib-vm-project-workflow.mjs';
import { verifyProjectInitialization } from './lib-vm-project-initialization.mjs';
import { verifyProjectBackupWorkflow } from './lib-vm-project-backup.mjs';
import { verifyVMProjectPreview } from './lib-vm-project-preview.mjs';
import { verifyVMCodexWorkflow } from './lib-vm-codex-workflow.mjs';

export function machineJSONArguments(args) {
  const separator = args.indexOf('--');
  return separator < 0
    ? [...args, '--json']
    : [...args.slice(0, separator), '--json', ...args.slice(separator)];
}

// Await process-group cleanup before returning, including an interrupted CLI
// mutation whose request may already have been accepted by the host.
export async function runAcceptanceCommand(
  program,
  args,
  {
    cwd = root,
    env = process.env,
    timeout = 30_000,
    allowFailure = false,
    signal,
    onStderr,
  } = {},
) {
  signal?.throwIfAborted();
  const child = spawnChild(program, args, {
    cwd,
    env,
    stdio: ['ignore', 'pipe', 'pipe'],
    ownProcessGroup: true,
  });
  let stdout = '';
  let stderr = '';
  let failure;
  let stopping;
  let hardStop;
  const terminate = (error) => {
    failure ||= error;
    if (!hardStop && child.pid) {
      // Native Go helpers handle SIGTERM and can need a fresh connection to End
      // their exact guest terminal. Give that cleanup time before group cleanup.
      child.kill('SIGTERM');
      hardStop = setTimeout(() => {
        stopping = stop(child);
        void stopping.catch(() => {});
      }, 12_000);
    }
  };
  const abort = () => terminate(signal.reason);
  signal?.addEventListener('abort', abort, { once: true });
  if (signal?.aborted) abort();
  const timer = setTimeout(
    () => terminate(new Error(`Command timed out after ${timeout} ms.`)),
    timeout,
  );
  child.stdout.setEncoding('utf8');
  child.stderr.setEncoding('utf8');
  child.stdout.on('data', (chunk) => {
    const output = stdout + chunk;
    stdout = output.slice(0, 4 * 1024 * 1024);
    if (output.length > 4 * 1024 * 1024)
      terminate(new Error('Command stdout exceeded 4 MiB.'));
  });
  child.stderr.on('data', (chunk) => {
    stderr = (stderr + chunk).slice(-64 * 1024);
    try {
      onStderr?.(stderr);
    } catch (error) {
      terminate(error);
    }
  });
  const result = await new Promise((resolve) => {
    child.once('error', (error) => {
      failure ||= error;
    });
    child.once('close', (code, signal) => resolve({ code, signal }));
  });
  clearTimeout(timer);
  clearTimeout(hardStop);
  signal?.removeEventListener('abort', abort);
  try {
    await (stopping || stop(child));
  } catch (error) {
    failure = failure
      ? new AggregateError(
          [failure, error],
          'Command interruption and cleanup failed.',
        )
      : error;
  }
  if (!failure && (result.signal || (!allowFailure && result.code !== 0)))
    failure = new Error(`Command exited with ${result.signal || result.code}.`);
  if (failure)
    throw Object.assign(
      new Error(
        `${program} ${args.join(' ')}: ${failure.message}\n${stderr}\n${stdout.slice(-64 * 1024)}`,
        { cause: failure },
      ),
      { ...result, stdout, stderr, exitCode: failure.exitCode },
    );
  signal?.throwIfAborted();
  return { ...result, stdout, stderr };
}

// A fresh connection verifies runtime and sends its application request on that
// same connection. Refuse reconnection after verification, even at a reused path.
export async function hostJSON(
  { socket, hostId, configFingerprint, signal },
  path,
  method = 'GET',
  body,
) {
  const agent = new http.Agent({
    keepAlive: true,
    maxSockets: 1,
    maxFreeSockets: 1,
  });
  let connections = 0;
  agent.createConnection = (_options, callback) => {
    if (++connections !== 1) {
      queueMicrotask(() =>
        callback(
          new Error(
            'The verified host connection closed; refusing to reconnect this request.',
          ),
        ),
      );
      return undefined;
    }
    return createConnection({ path: socket });
  };
  const request = (path, method = 'GET', body) =>
    new Promise((resolve, reject) => {
      const req = http.request(
        {
          host: 'host',
          agent,
          path: `/v1${path}`,
          method,
          signal,
          headers: {
            'Content-Type': 'application/json',
            ...(hostId ? { 'X-AXV-Host-ID': hostId } : {}),
          },
          timeout: Math.max(15_000, (body?.timeoutSeconds || 0) * 1000 + 5000),
        },
        (response) => {
          let text = '';
          response.setEncoding('utf8');
          response.on('data', (chunk) => {
            text += chunk;
            if (text.length > 4 * 1024 * 1024)
              req.destroy(new Error('Host response exceeded 4 MiB.'));
          });
          response.on('error', reject);
          response.on('aborted', () =>
            reject(
              new Error(`Host response was interrupted: ${method} ${path}`),
            ),
          );
          response.on('end', () => {
            try {
              const value = JSON.parse(text);
              if (response.statusCode >= 300)
                throw Object.assign(new Error(`${method} ${path}: ${text}`), {
                  statusCode: response.statusCode,
                  code: value.code,
                });
              resolve(value);
            } catch (error) {
              reject(error);
            }
          });
        },
      );
      req.on('timeout', () =>
        req.destroy(new Error(`Host request timed out: ${method} ${path}`)),
      );
      req.on('error', reject);
      req.end(body === undefined ? undefined : JSON.stringify(body));
    });
  try {
    const runtime = await request('/runtime');
    assert.equal(runtime.ready, true, 'The host controller is not ready.');
    assert.equal(runtime.protocolVersion, 3, 'The host protocol changed.');
    assert.match(runtime.hostId || '', /^host-[a-f0-9]{32}$/);
    assert.ok(
      runtime.configFingerprint,
      'The host omitted its configuration identity.',
    );
    if (hostId)
      assert.equal(
        runtime.hostId,
        hostId,
        'The original host identity changed.',
      );
    if (configFingerprint)
      assert.equal(
        runtime.configFingerprint,
        configFingerprint,
        'The host configuration changed.',
      );
    return path === '/runtime' ? runtime : await request(path, method, body);
  } finally {
    agent.destroy();
  }
}

const usage = `Explicit native Linux/Firecracker acceptance (run as root):
node scripts/verify-vm-firecracker.mjs --yes \\
  --image-bundle /absolute/bundle/manifest.json \\
  --kernel /absolute/vmlinux --kernel-sha256 <reviewed-sha256> \\
  --firecracker /absolute/firecracker --jailer /absolute/jailer \\
  --version <pinned-version> --subnet <unused-private-/22>
Optional: --executable /absolute/axv --uid 60000 --gid 60000 --dns 1.1.1.1
          --docker-uplink eth0
          --real-codex (uses the installed CLI, existing login and model settings)
This creates real VMs, TAPs, cgroups and one owned nftables table. It never
downloads VM inputs, installs host dependencies or changes host sysctls.
The project workflow downloads a pinned public repository and installs its
dependencies inside the disposable VM's project volume.
With --docker-uplink, --yes also authorizes two temporary owner-commented
DOCKER-USER rules for this run's TAP prefix/subnet and the selected uplink.
Only their exact handles are removed; existing policies remain unchanged. Set
AXV_VM_VERIFY_KEEP_FAILURE=1 to retain owned VMs and profiles after a failure.`;

export function acceptanceOptions(args) {
  const { values } = parseArgs({
    args,
    options: {
      yes: { type: 'boolean', default: false },
      help: { type: 'boolean', default: false },
      'image-bundle': { type: 'string' },
      kernel: { type: 'string' },
      'kernel-sha256': { type: 'string' },
      firecracker: { type: 'string' },
      jailer: { type: 'string' },
      version: { type: 'string' },
      subnet: { type: 'string' },
      executable: { type: 'string' },
      uid: { type: 'string', default: '60000' },
      gid: { type: 'string', default: '60000' },
      dns: { type: 'string', default: '1.1.1.1' },
      'docker-uplink': { type: 'string' },
      'real-codex': { type: 'boolean', default: false },
    },
    allowPositionals: false,
    strict: true,
  });
  if (values.help) return values;
  assert.ok(
    values.yes,
    'Pass --yes to authorize temporary native VM resources.',
  );
  for (const key of ['image-bundle', 'kernel', 'firecracker', 'jailer'])
    assert.ok(
      values[key] && isAbsolute(values[key]),
      `--${key} must be an absolute path.`,
    );
  if (values.executable)
    assert.ok(isAbsolute(values.executable), '--executable must be absolute.');
  assert.match(
    values['kernel-sha256'] || '',
    /^[a-f0-9]{64}$/,
    'Supply the reviewed --kernel-sha256.',
  );
  assert.match(
    values.version || '',
    /^\d+\.\d+\.\d+$/,
    'Supply an explicit --version pin.',
  );
  assert.ok(
    values.subnet,
    'Supply an explicit unused private --subnet (/16 to /22).',
  );
  const subnet = /^(\d+\.\d+\.\d+\.\d+)\/(1[6-9]|2[0-2])$/.exec(values.subnet);
  assert.ok(
    subnet && isIPv4(subnet[1]),
    '--subnet must be one aligned private IPv4 /16 to /22 network.',
  );
  const octets = subnet[1].split('.').map(Number);
  const address = octets.reduce((value, byte) => value * 256 + byte, 0);
  const size = 2 ** (32 - Number(subnet[2]));
  assert.ok(
    address % size === 0 &&
      (octets[0] === 10 ||
        (octets[0] === 172 && octets[1] >= 16 && octets[1] <= 31) ||
        (octets[0] === 192 && octets[1] === 168)),
    '--subnet must be one aligned private IPv4 /16 to /22 network.',
  );
  assert.ok(isIPv4(values.dns), '--dns must be an IPv4 address.');
  if (values['docker-uplink'])
    assert.match(
      values['docker-uplink'],
      /^[A-Za-z0-9_.:-]{1,15}$/,
      '--docker-uplink must be one interface name.',
    );
  for (const key of ['uid', 'gid']) {
    assert.match(values[key], /^\d+$/, `--${key} must be a positive integer.`);
    values[key] = Number(values[key]);
    assert.ok(
      Number.isSafeInteger(values[key]) &&
        values[key] > 0 &&
        values[key] <= 2147483547,
      `--${key} is outside the jailer range.`,
    );
  }
  return values;
}

export async function sha256(path) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest('hex');
}

export async function verifiedBundle(path, arch) {
  const metadata = await lstat(path);
  assert.ok(
    metadata.isFile() && metadata.size <= 64 * 1024,
    'Bundle manifest must be a regular file of at most 64 KiB.',
  );
  const bundle = JSON.parse(await readFile(path, 'utf8'));
  assert.equal(bundle.version, 1);
  assert.equal(bundle.provider, 'firecracker');
  assert.equal(bundle.arch, arch);
  assert.match(bundle.id || '', /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/);
  assert.ok(typeof bundle.name === 'string' && bundle.name.trim());
  const file = bundle.rootfs?.file;
  assert.ok(
    typeof file === 'string' &&
      file !== '.' &&
      file !== '..' &&
      file.length > 0 &&
      basename(file) === file &&
      !/[\\\0\r\n]/.test(file),
    'Rootfs must be a filename beside the bundle manifest.',
  );
  assert.match(bundle.rootfs.sha256 || '', /^[a-f0-9]{64}$/);
  const rootfs = join(dirname(path), file);
  const state = await lstat(rootfs);
  assert.ok(
    state.isFile() && state.size > 0 && state.size <= 8 * 2 ** 30,
    'Rootfs must be a nonempty regular file of at most 8 GiB.',
  );
  assert.equal(
    await sha256(rootfs),
    bundle.rootfs.sha256,
    'Bundle rootfs checksum mismatch.',
  );
  assert.equal(bundle.guest?.imageVersion, 1);
  assert.equal(bundle.guest?.protocolVersion, 3);
  for (const capability of [
    'exec',
    'terminals',
    'ssh',
    'network',
    'project-files',
    'project-tasks',
    'project-previews',
  ])
    assert.ok(
      bundle.guest?.capabilities?.includes(capability),
      `Enhanced acceptance requires ${capability}.`,
    );
  return {
    ...bundle,
    rootfsPath: rootfs,
    minRootGiB: Math.max(2, Math.ceil(state.size / 2 ** 30)),
  };
}

function quote(value) {
  return `'${String(value).replaceAll("'", "'\\''")}'`;
}

// Registered projects protect their volumes from online deletion. Only the
// invocation's detached project volumes may remain for final directory cleanup,
// after all VM deletion and controller/cgroup shutdown checks have succeeded.
export function ownedProtectedProjectVolumes({
  projects,
  volumes,
  machines,
  ownedMachines,
  registrations,
  hostId,
}) {
  assert.equal(
    machines.length,
    0,
    'Live or uncertain VMs block volume cleanup.',
  );
  const protectedVolumes = new Set();
  for (const project of projects) {
    const registration = registrations.find(
      (item) =>
        item.volumeId === project.volumeId &&
        item.root === project.root &&
        (!item.id || item.id === project.id),
    );
    assert.ok(registration, 'Unexpected registered project in the owned host.');
    assert.equal(project.hostId, hostId, 'Project host ownership changed.');
    const volume = volumes.find((item) => item.id === project.volumeId);
    assert.ok(
      volume && ownedMachines.some((item) => item.volumeId === volume.id),
      'Project volume is not owned by this invocation.',
    );
    assert.ok(
      volume.retain &&
        volume.status === 'ready' &&
        !volume.machineId &&
        !volume.pendingMachineId &&
        !volume.pendingAction &&
        !volume.pendingSizeGiB,
      'A project volume is attached, unavailable or uncertain.',
    );
    protectedVolumes.add(volume.id);
  }
  return protectedVolumes;
}

async function acceptance(options) {
  assert.equal(
    process.platform,
    'linux',
    'Native Firecracker acceptance requires Linux.',
  );
  assert.equal(
    process.getuid(),
    0,
    'Run native Firecracker acceptance as root.',
  );
  // Resolve the optional real-model harness before creating native resources.
  const verifyLive = options['real-codex']
    ? (await import('./lib-vm-codex-live.mjs')).verifyVMCodexLive
    : undefined;
  const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
  assert.ok(arch, 'Only native amd64 and arm64 are supported.');
  const bundle = await verifiedBundle(options['image-bundle'], arch);
  assert.equal(
    await sha256(options.kernel),
    options['kernel-sha256'],
    'Kernel checksum mismatch.',
  );
  for (const key of ['firecracker', 'jailer', 'kernel'])
    await access(options[key]);
  assert.equal(
    (await readFile('/proc/sys/net/ipv4/ip_forward', 'utf8')).trim(),
    '1',
    'Enable IPv4 forwarding explicitly before native acceptance.',
  );
  const directory = await scratch('fc-');
  const profile = join(directory, 'profile');
  // The per-machine jail and its vsock path must remain below the Unix limit.
  let shortRuntime;
  try {
    shortRuntime = await mkdtemp('/tmp/axv-fc-');
  } catch (error) {
    await rm(directory, { recursive: true, force: true });
    throw error;
  }
  const configPath = join(directory, 'host.json');
  const socket = join(shortRuntime, 'socket', 'control.sock');
  const dataDir = join(shortRuntime, 'd');
  const runtimeTemporaryDirectory = join(directory, 'runtime-tmp');
  const pinnedFirecracker = join(directory, 'fc');
  const pinnedJailer = join(directory, 'jailer');
  const runId = randomUUID();
  const unit = `axv-fc-test-${runId}.service`;
  const unitDescription = `AXV native acceptance ${runId}`;
  const executable = join(directory, 'axv');
  const env = {
    ...process.env,
    AXV_HOME: profile,
    AXV_COMPUTE_SOCKET: socket,
    AXV_IMAGE_BUNDLE: '',
    AXV_WORKSPACE_DIR: '',
    DATA_DIR: '',
    ADDR: '',
    PORT: '',
    TMPDIR: runtimeTemporaryDirectory,
    GOTMPDIR: runtimeTemporaryDirectory,
  };
  const config = {
    dataDir,
    socket,
    capacity: {
      cpus: Math.min(4096, Math.max(2, availableParallelism())),
      memoryMiB: 4096,
      diskGiB: 48,
      machines: 4,
      workers: 2,
    },
    firecracker: {
      binary: pinnedFirecracker,
      jailer: pinnedJailer,
      version: options.version,
      uid: options.uid,
      gid: options.gid,
      subnet: options.subnet,
      dns: options.dns,
      cgroupParent: `system.slice/${unit}`,
    },
    images: [
      {
        id: bundle.id,
        name: bundle.name,
        provider: 'firecracker',
        arch,
        rootfs: bundle.rootfsPath,
        rootfsSHA256: bundle.rootfs.sha256,
        kernel: options.kernel,
        kernelSHA256: options['kernel-sha256'],
        minRootGiB: bundle.minRootGiB,
        guest: bundle.guest,
      },
    ],
  };
  const state = {
    hostId: undefined,
    runId,
    unit,
    unitDescription,
    machines: [],
    operations: [],
    projects: [],
    volumes: [],
  };
  let hostRunning = false;
  let unitSubmitted = false;
  let cleaning;
  let interrupted = false;
  let failed = false;
  let inCleanup = false;
  let failure;
  let codexWorkflow;
  let closeLiveCodex;
  const work = new AbortController();
  const probes = new Set();
  console.log(`Owned verification directory: ${directory}`);
  console.log(`Owned short Firecracker runtime directory: ${shortRuntime}`);
  const hostArguments = [
    '--collect',
    '--service-type=exec',
    `--unit=${unit}`,
    `--description=${unitDescription}`,
    '--property=Delegate=yes',
    '--property=DelegateSubgroup=controller',
    '--property=KillMode=process',
    '--property=UMask=0077',
    '--property=TimeoutStopSec=30',
    `--property=WorkingDirectory=${directory}`,
    `--property=StandardOutput=append:${join(directory, 'host.log')}`,
    `--property=StandardError=append:${join(directory, 'host.log')}`,
    `--setenv=AXV_HOME=${profile}`,
    `--setenv=TMPDIR=${runtimeTemporaryDirectory}`,
    `--setenv=GOTMPDIR=${runtimeTemporaryDirectory}`,
    executable,
    'host',
    '--config',
    configPath,
  ];

  let saving = Promise.resolve();

  function save() {
    const snapshot = JSON.stringify(state, null, 2) + '\n';
    saving = saving.then(async () => {
      await writeFile(join(directory, 'ownership.json.partial'), snapshot, {
        mode: 0o600,
      });
      await rename(
        join(directory, 'ownership.json.partial'),
        join(directory, 'ownership.json'),
      );
    });
    return saving;
  }

  function captureCommand(program, args, options = {}) {
    return runAcceptanceCommand(program, args, {
      ...options,
      signal: inCleanup ? undefined : work.signal,
    });
  }

  async function unitState() {
    const result = await captureCommand(
      'systemctl',
      ['show', '--property=LoadState', '--property=Description', unit],
      { allowFailure: true },
    );
    const values = Object.fromEntries(
      result.stdout
        .trim()
        .split('\n')
        .map((line) => {
          const i = line.indexOf('=');
          return [line.slice(0, i), line.slice(i + 1)];
        }),
    );
    if (values.LoadState === 'not-found') return false;
    assert.equal(result.code, 0, result.stderr);
    assert.equal(
      values.Description,
      unitDescription,
      'Refusing to change a systemd unit owned by another invocation.',
    );
    return true;
  }

  async function markRuntimeOwner() {
    await writeFile(join(shortRuntime, 'acceptance-owner'), runId + '\n', {
      mode: 0o600,
      flag: 'wx',
    });
  }

  function request(path, method = 'GET', body) {
    return hostJSON(
      {
        socket,
        hostId: state.hostId,
        configFingerprint: state.configFingerprint,
        signal: inCleanup ? undefined : work.signal,
      },
      path,
      method,
      body,
    );
  }

  async function until(check, description, timeout = 120_000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      if (!inCleanup) work.signal.throwIfAborted();
      const result = await check();
      if (result) return result;
      await delay(250);
    }
    throw new Error(`Timed out waiting for ${description}.`);
  }

  async function assertHost() {
    const runtime = await request('/runtime');
    assert.equal(
      runtime.hostId,
      state.hostId,
      'The original host identity changed.',
    );
    return runtime;
  }

  async function startHost(restart = false) {
    const existing = await unitState();
    if (!restart && existing && !unitSubmitted)
      throw new Error('The requested native test unit already exists.');
    unitSubmitted = true;
    if (existing)
      await captureCommand('systemctl', [restart ? 'restart' : 'start', unit]);
    else await captureCommand('systemd-run', hostArguments);
    hostRunning = true;
    await until(
      async () => {
        const active = await captureCommand('systemctl', ['is-active', unit], {
          allowFailure: true,
        });
        if (active.code !== 0)
          throw new Error(
            `Owned host is ${active.stdout.trim()}:\n${await readFile(join(directory, 'host.log'), 'utf8')}`,
          );
        let runtime;
        try {
          runtime = await request('/runtime');
        } catch (error) {
          if (
            ['ENOENT', 'ECONNREFUSED', 'ECONNRESET'].includes(error.code) ||
            error.statusCode === 503
          )
            return false;
          throw error;
        }
        if (state.hostId) assert.equal(runtime.hostId, state.hostId);
        else {
          state.hostId = runtime.hostId;
          state.configFingerprint = runtime.configFingerprint;
          await save();
          console.log(
            `Owned host identity: ${state.hostId}; firewall/TAP prefix: axv${state.hostId.slice(-6)}`,
          );
          console.log(
            `Owned controller unit: ${unit}; host configuration: ${configPath}`,
          );
        }
        return true;
      },
      'owned host readiness',
      30_000,
    );
  }

  async function cli(args, { allowFailure = false, timeout = 660_000 } = {}) {
    return captureCommand(executable, ['vm', ...args], {
      cwd: directory,
      env,
      timeout,
      allowFailure,
    });
  }

  async function cliJSON(args, settings) {
    return JSON.parse((await cli(machineJSONArguments(args), settings)).stdout);
  }

  async function operation(id) {
    return until(
      async () => {
        await assertHost();
        const result = await request(`/operations/${id}`);
        assert.notEqual(
          result.status,
          'failed',
          `Operation failed: ${JSON.stringify(result)}`,
        );
        return result.status === 'succeeded' && result;
      },
      `operation ${id}`,
      600_000,
    );
  }

  async function create(
    name,
    { retain = false, ttl = 1800, start = true, volumeId } = {},
  ) {
    await assertHost();
    const owned = { requestId: `native-${randomUUID()}`, name };
    state.machines.push(owned);
    await save();
    const mutation = { requestId: owned.requestId, kind: 'create' };
    state.operations.push(mutation);
    await save();
    let receipt;
    try {
      receipt = await request('/machines', 'POST', {
        requestId: owned.requestId,
        start,
        spec: {
          name,
          provider: 'firecracker',
          imageId: bundle.id,
          kind: 'task',
          resources: {
            cpus: 1,
            memoryMiB: 1024,
            diskGiB: Math.max(4, bundle.minRootGiB),
          },
          projectGiB: 1,
          retainVolume: retain,
          ttlSeconds: ttl,
          ...(volumeId ? { volumeId } : {}),
        },
      });
    } catch (error) {
      if (error.statusCode >= 400 && error.statusCode < 500) {
        mutation.rejected = true;
        owned.rejected = true;
        await save();
      }
      throw error;
    }
    owned.id = receipt.machine.id;
    owned.volumeId = receipt.machine.spec.volumeId;
    mutation.id = receipt.operation.id;
    mutation.machineId = owned.id;
    await save();
    assert.equal(receipt.machine.hostId, state.hostId);
    assert.match(owned.id || '', /^vm-[a-f0-9]{32}$/);
    assert.match(owned.volumeId || '', /^vol-[a-f0-9]{32}$/);
    assert.equal(receipt.operation.requestId, mutation.requestId);
    await operation(receipt.operation.id);
    return owned;
  }

  async function ready(id) {
    let lastMachine;
    try {
      return await until(async () => {
        const machine = await cliJSON(['inspect', id]);
        lastMachine = machine;
        if (machine.status !== 'running' || !machine.ready) return false;
        assert.equal(machine.guestState, 'ready', machine.guestReason);
        for (const capability of [
          'exec',
          'terminals',
          'ssh',
          'network',
          'project-files',
          'project-tasks',
          'project-previews',
        ])
          assert.ok(machine.guestCapabilities.includes(capability));
        const health = await request(`/machines/${id}/guest/health`);
        assert.equal(health.machineId, id);
        assert.ok(health.bootId && health.ready);
        return health;
      }, `guest ${id} readiness`);
    } catch (error) {
      throw new Error(
        `Guest readiness failed; last machine observation: ${JSON.stringify(lastMachine)}. Host log: ${join(directory, 'host.log')}`,
        { cause: error },
      );
    }
  }

  async function exec(id, argv, timeoutSeconds = 10) {
    await assertHost();
    const result = await request(`/machines/${id}/guest/exec`, 'POST', {
      argv,
      timeoutSeconds,
    });
    assert.equal(result.exitCode, 0, result.stderr);
    return result.stdout;
  }

  async function configureDocker() {
    if (!options['docker-uplink']) return;
    const prefix = `axv${state.hostId.slice(-6)}`;
    // The provider's earlier isolation chain must exist before Docker egress is allowed.
    const ownedTable = JSON.parse(
      (await captureCommand('nft', ['-j', 'list', 'table', 'inet', prefix]))
        .stdout,
    );
    assert.ok(
      ownedTable.nftables.some((row) => row.table?.comment === state.hostId),
      'The provider isolation firewall is missing.',
    );
    state.dockerRuleComment = `AXV native acceptance ${state.hostId}`;
    await save();
    const base = ['insert', 'rule', 'ip', 'filter', 'DOCKER-USER'];
    await captureCommand('nft', [
      ...base,
      'iifname',
      JSON.stringify(`${prefix}*`),
      'oifname',
      JSON.stringify(options['docker-uplink']),
      'ip',
      'saddr',
      options.subnet,
      'ct',
      'state',
      'new,established,related',
      'accept',
      'comment',
      JSON.stringify(state.dockerRuleComment),
    ]);
    await captureCommand('nft', [
      ...base,
      'iifname',
      JSON.stringify(options['docker-uplink']),
      'oifname',
      JSON.stringify(`${prefix}*`),
      'ip',
      'daddr',
      options.subnet,
      'ct',
      'state',
      'established,related',
      'accept',
      'comment',
      JSON.stringify(state.dockerRuleComment),
    ]);
    console.log(
      `Installed two temporary DOCKER-USER rules for ${prefix}*, ${options.subnet} and ${options['docker-uplink']}.`,
    );
  }

  async function removeDockerRules() {
    if (!state.dockerRuleComment) return;
    const rules = JSON.parse(
      (
        await captureCommand('nft', [
          '-j',
          '-a',
          'list',
          'chain',
          'ip',
          'filter',
          'DOCKER-USER',
        ])
      ).stdout,
    );
    for (const row of rules.nftables) {
      const rule = row.rule;
      if (rule?.comment !== state.dockerRuleComment) continue;
      assert.equal(rule.family, 'ip');
      assert.equal(rule.table, 'filter');
      assert.equal(rule.chain, 'DOCKER-USER');
      assert.ok(Number.isSafeInteger(rule.handle));
      await captureCommand('nft', [
        'delete',
        'rule',
        'ip',
        'filter',
        'DOCKER-USER',
        'handle',
        String(rule.handle),
      ]);
    }
  }

  async function verifyNetwork(id) {
    const dns = await exec(id, [
      'python3',
      '-c',
      'import json,socket; print(json.dumps(sorted({item[4][0] for item in socket.getaddrinfo("example.com",443,socket.AF_INET,socket.SOCK_STREAM)})))',
    ]);
    assert.ok(
      JSON.parse(dns).length,
      'Guest DNS returned no public addresses.',
    );
    const page = (
      await cli([
        'ssh',
        '-T',
        id,
        '--',
        "curl --noproxy '*' --fail --silent --show-error --connect-timeout 10 --max-time 30 https://example.com/",
      ])
    ).stdout;
    assert.match(
      page,
      /Example Domain/,
      'Public HTTPS egress returned unexpected content.',
    );
    const gateway = (
      await exec(id, [
        'python3',
        '-c',
        'import json,subprocess; print(json.loads(subprocess.check_output(["ip","-j","-4","route","show","default"]))[0]["gateway"])',
      ])
    ).trim();
    const targets = [gateway];
    for (const address of Object.values(networkInterfaces()).flat()) {
      if (
        address.internal ||
        address.family !== 'IPv4' ||
        targets.includes(address.address)
      )
        continue;
      const parts = address.address.split('.').map(Number);
      if (
        parts[0] === 10 ||
        (parts[0] === 172 && parts[1] >= 16 && parts[1] <= 31) ||
        (parts[0] === 192 && parts[1] === 168)
      ) {
        targets.push(address.address);
        break;
      }
    }
    let accepted = 0;
    const listener = createServer((socket) => {
      accepted++;
      socket.on('error', () => {});
      socket.destroy();
    });
    await new Promise((resolve, reject) => {
      listener.once('error', reject);
      listener.listen(0, '0.0.0.0', resolve);
    });
    try {
      const port = listener.address().port;
      const result = await exec(
        id,
        [
          'python3',
          '-c',
          `import json,socket\nfor target in json.loads(${JSON.stringify(JSON.stringify(targets))}):\n try:\n  with socket.create_connection((target,${port}),timeout=2):\n   raise RuntimeError("Guest reached protected host address "+target)\n except TimeoutError:\n  pass\nprint("Host gateway/private-address isolation passed")`,
        ],
        15,
      );
      assert.match(result, /isolation passed/);
      assert.equal(
        accepted,
        0,
        'Guest traffic reached the protected host listener.',
      );
    } finally {
      await new Promise((resolve) => listener.close(resolve));
    }
    console.log(
      `Real guest DNS, public HTTPS egress and blocked host listener access (${targets.join(', ')}) passed.`,
    );
  }

  async function action(owned, command) {
    const mutation = { kind: 'action', machineId: owned.id, action: command };
    state.operations.push(mutation);
    await save();
    let parsedRequest;
    let result;
    try {
      const captured = await captureCommand(
        executable,
        [
          'vm',
          command,
          owned.id,
          ...(command === 'rm' ? ['--yes'] : []),
          '--json',
        ],
        {
          cwd: directory,
          env,
          timeout: 660_000,
          onStderr: (output) => {
            const requestId =
              /(?:^|\n)Request: (cli-[a-f0-9]{32})(?:\n|$)/.exec(output)?.[1];
            if (requestId && !parsedRequest) {
              parsedRequest = requestId;
              mutation.requestId = requestId;
            }
          },
        },
      );
      result = JSON.parse(captured.stdout);
      mutation.requestId ||= result.operation.requestId;
      mutation.id = result.operation.id;
    } finally {
      await save();
    }
    assert.equal(result.operation.status, 'succeeded');
    assert.equal(result.machine.id, owned.id);
    return result.machine;
  }

  async function deleteOwnedMachine(owned, machine) {
    const mutation = {
      requestId: `cleanup-${randomUUID()}`,
      kind: 'action',
      machineId: owned.id,
      action: 'delete',
    };
    state.operations.push(mutation);
    await save();
    const receipt = await request(`/machines/${owned.id}/actions`, 'POST', {
      requestId: mutation.requestId,
      revision: machine.revision,
      action: 'delete',
    });
    mutation.id = receipt.operation.id;
    await save();
    await operation(mutation.id);
  }

  async function verifyForwarding(id, token) {
    const forwarding = new Set();
    const sockets = new Set();
    let echoServer;

    function probe(args) {
      const child = spawnChild(
        executable,
        ['vm', 'ssh', '-N', '-T', ...args, id],
        {
          cwd: directory,
          env,
          stdio: ['ignore', 'pipe', 'pipe'],
          ownProcessGroup: true,
        },
      );
      probes.add(child);
      forwarding.add(child);
      const result = { child, stderr: '', error: undefined };
      child.stdout.resume();
      child.stderr.on('data', (chunk) => {
        result.stderr += chunk.toString();
      });
      child.once('error', (error) => {
        result.error = error;
      });
      child.once('close', () => {
        probes.delete(child);
        forwarding.delete(child);
      });
      return result;
    }

    function live(result) {
      if (result.error) throw result.error;
      assert.ok(
        result.child.exitCode === null && result.child.signalCode === null,
        `SSH forwarding exited before readiness: ${result.stderr}`,
      );
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

    function tcpBanner(port) {
      return new Promise((resolve, reject) => {
        const socket = createConnection({ host: '127.0.0.1', port });
        sockets.add(socket);
        let output = '';
        let complete = false;
        socket.setTimeout(2000, () =>
          socket.destroy(new Error('Forwarded TCP banner timed out.')),
        );
        socket.on('error', reject);
        socket.on('data', (chunk) => {
          output += chunk.toString();
          if (output.includes('SSH-2.0-')) {
            complete = true;
            socket.destroy();
            resolve(output);
          }
        });
        socket.once('close', () => {
          sockets.delete(socket);
          if (!complete)
            reject(
              new Error('Forwarded TCP stream closed before its SSH banner.'),
            );
        });
      });
    }

    try {
      const sshConnection = (
        await cli(['ssh', '-T', id, '--', 'printf "%s\\n" "$SSH_CONNECTION"'])
      ).stdout
        .trim()
        .split(/\s+/);
      const guestSSHPort = Number(sshConnection.at(-1));
      assert.ok(guestSSHPort > 0);
      const localPort = await unusedPort();
      const local = probe([
        '-L',
        `127.0.0.1:${localPort}:127.0.0.1:${guestSSHPort}`,
      ]);
      await until(
        async () => {
          live(local);
          try {
            return await tcpBanner(localPort);
          } catch {
            return false;
          }
        },
        'SSH local TCP forwarding',
        30_000,
      );
      await stop(local.child);

      echoServer = createServer((socket) => {
        sockets.add(socket);
        // A peer reset ends this probe connection; its data assertion below
        // still has to succeed, and cleanup must retain control of the host.
        socket.on('error', () => socket.destroy());
        socket.once('close', () => sockets.delete(socket));
        socket.pipe(socket);
      });
      await new Promise((resolve, reject) => {
        echoServer.once('error', reject);
        echoServer.listen(0, '127.0.0.1', resolve);
      });
      const reverse = probe([
        '-R',
        `127.0.0.1:0:127.0.0.1:${echoServer.address().port}`,
      ]);
      const remotePort = await until(
        async () => {
          live(reverse);
          return /Allocated port (\d+)/.exec(reverse.stderr)?.[1];
        },
        'SSH remote TCP forwarding',
        30_000,
      );
      const echo = `exec 3<>/dev/tcp/127.0.0.1/${remotePort}; printf '%s\\n' ${quote(token)} >&3; IFS= read -r reply <&3; printf '%s' "$reply"`;
      const result = await cli(
        ['ssh', '-T', id, '--', `/bin/bash -c ${quote(echo)}`],
        { timeout: 30_000 },
      );
      assert.equal(result.stdout, token);
      await stop(reverse.child);
      console.log(
        'Real OpenSSH local and remote TCP forwarding with closed stdin passed.',
      );
    } finally {
      await Promise.all([...forwarding].map(stop));
      for (const socket of sockets) socket.destroy();
      if (echoServer) await new Promise((resolve) => echoServer.close(resolve));
    }
  }

  async function cleanup(preserve = false) {
    cleaning ??= (async () => {
      let safe = true;
      try {
        await closeLiveCodex?.();
      } catch (error) {
        safe = false;
        console.error('Real Codex cleanup needs attention:', error);
      }
      try {
        await codexWorkflow?.close();
      } catch (error) {
        safe = false;
        console.error('Codex workflow cleanup needs attention:', error);
      }
      try {
        await Promise.all([...probes].map(stop));
      } catch (error) {
        safe = false;
        console.error('SSH probe cleanup needs attention:', error);
      }
      try {
        await removeDockerRules();
      } catch (error) {
        safe = false;
        console.error('Docker egress rule cleanup needs attention:', error);
      }
      try {
        if (state.machines.length && !preserve) {
          const active = await captureCommand(
            'systemctl',
            ['is-active', unit],
            { allowFailure: true },
          );
          if (!hostRunning || active.code !== 0) await startHost();
          await assertHost();
          // Every submitted mutation is recovered by its original request before
          // a new cleanup intent is issued, including interrupted CLI actions.
          for (const submitted of state.operations) {
            if (submitted.rejected) continue;
            assert.ok(
              submitted.requestId,
              'A CLI mutation stopped before its original request could be confirmed; preserve the host for inspection.',
            );
            const result = await cliJSON(
              [
                'operation',
                'wait',
                '--host-id',
                state.hostId,
                '--request-id',
                submitted.requestId,
                '--timeout',
                '2m',
              ],
              { allowFailure: true, timeout: 130_000 },
            );
            assert.ok(
              ['succeeded', 'failed'].includes(result.status),
              'Original mutation has not settled.',
            );
            assert.equal(result.requestId, submitted.requestId);
            if (submitted.machineId)
              assert.equal(result.machineId, submitted.machineId);
            submitted.id = result.id;
            if (
              ['project-backup', 'project-restore'].includes(submitted.kind)
            ) {
              assert.ok(
                result.volumeId,
                'Original project copy must identify its volume.',
              );
              if (
                !state.volumes.some((volume) => volume.id === result.volumeId)
              )
                state.volumes.push({ id: result.volumeId });
            }
            if (submitted.kind === 'create') {
              assert.ok(
                result.machineId,
                'Original creation does not identify its VM.',
              );
              const owned = state.machines.find(
                (item) => item.requestId === submitted.requestId,
              );
              if (owned.id) assert.equal(result.machineId, owned.id);
              owned.id = result.machineId;
            }
          }
          await save();
          // Settle every accepted operation before issuing cleanup mutations.
          const { operations } = await request('/operations');
          for (const pending of operations.filter((item) =>
            ['queued', 'running'].includes(item.status),
          )) {
            assert.ok(
              state.machines.some(
                (owned) =>
                  (pending.machineId && owned.id === pending.machineId) ||
                  (pending.volumeId && owned.volumeId === pending.volumeId),
              ) ||
                state.volumes.some((volume) => volume.id === pending.volumeId),
              'An unexpected active operation is using this host; refusing cleanup mutations.',
            );
            const result = await cliJSON(
              [
                'operation',
                'wait',
                pending.id,
                '--host-id',
                state.hostId,
                '--timeout',
                '2m',
              ],
              { allowFailure: true, timeout: 130_000 },
            );
            assert.ok(['succeeded', 'failed'].includes(result.status));
          }
          for (const owned of state.machines) {
            if (owned.rejected) continue;
            const machine = await cliJSON(['inspect', owned.id]);
            assert.equal(machine.hostId, state.hostId);
            owned.volumeId ||= machine.spec.volumeId;
            if (machine.status !== 'deleted')
              await deleteOwnedMachine(owned, machine);
          }
          const { volumes } = await request('/volumes');
          const { projects } = await request('/projects');
          const protectedVolumes = ownedProtectedProjectVolumes({
            projects,
            volumes,
            machines: (await cliJSON(['list'])).machines,
            ownedMachines: state.machines,
            registrations: state.projects,
            hostId: state.hostId,
          });
          for (const volume of volumes) {
            assert.ok(
              state.machines.some((owned) => owned.volumeId === volume.id) ||
                state.volumes.some((owned) => owned.id === volume.id),
              'Unexpected volume in the owned host.',
            );
            if (volume.status === 'deleted') continue;
            // Keep protected data intact while the host runs. The verified
            // owned runtime directory is removed only after controller and
            // cgroup shutdown below; an uncertain shutdown retains everything.
            if (protectedVolumes.has(volume.id)) continue;
            const mutation = {
              requestId: `cleanup-${randomUUID()}`,
              kind: 'volume-delete',
              volumeId: volume.id,
            };
            state.operations.push(mutation);
            await save();
            const receipt = await request(
              `/volumes/${volume.id}/delete`,
              'POST',
              { requestId: mutation.requestId, revision: volume.revision },
            );
            mutation.id = receipt.id;
            await save();
            await operation(receipt.id);
          }
          assert.equal((await cliJSON(['list'])).machines.length, 0);
        }
      } catch (error) {
        safe = false;
        console.error('VM cleanup needs attention:', error);
      }
      try {
        if (unitSubmitted && (await unitState())) {
          const stopped = await captureCommand('systemctl', ['stop', unit], {
            allowFailure: true,
          });
          if (stopped.code !== 0) {
            const loaded = await captureCommand(
              'systemctl',
              ['show', '--property=LoadState', '--value', unit],
              { allowFailure: true },
            );
            assert.equal(loaded.stdout.trim(), 'not-found', stopped.stderr);
          }
        }
        hostRunning = false;
        const cgroupEvents = join(
          '/sys/fs/cgroup',
          config.firecracker.cgroupParent,
          'cgroup.events',
        );
        try {
          if (!preserve)
            assert.match(
              await readFile(cgroupEvents, 'utf8'),
              /^populated 0$/m,
              'The owned systemd cgroup still has surviving processes.',
            );
        } catch (error) {
          if (error.code !== 'ENOENT') throw error;
        }
      } catch (error) {
        safe = false;
        console.error('Host cleanup needs attention:', error);
      }
      // No global flushes: remove only an exact table with this host's owner comment.
      if (!preserve && safe && state.hostId) {
        try {
          const prefix = `axv${state.hostId.slice(-6)}`;
          const links = JSON.parse(
            (await captureCommand('ip', ['-j', 'link', 'show'])).stdout,
          );
          assert.ok(
            !links.some((link) => link.ifname.startsWith(prefix)),
            'Owned TAP cleanup is incomplete.',
          );
          const tables = JSON.parse(
            (await captureCommand('nft', ['-j', 'list', 'tables'])).stdout,
          );
          const table = tables.nftables.find(
            (row) => row.table?.family === 'inet' && row.table.name === prefix,
          )?.table;
          if (table) {
            assert.equal(
              table.comment,
              state.hostId,
              'Refusing to delete a firewall table owned by another host.',
            );
            await captureCommand('nft', ['delete', 'table', 'inet', prefix]);
          }
        } catch (error) {
          safe = false;
          console.error('Host resource cleanup needs attention:', error);
        }
      }
      try {
        await dispose();
      } catch (error) {
        safe = false;
        console.error('Probe cleanup needs attention:', error);
      }
      if (preserve || !safe) {
        await save();
        console.error(`Retained owned verification directory: ${directory}`);
        console.error(`Retained owned Firecracker runtime: ${shortRuntime}`);
        console.error(
          `Owned controller unit: ${unit}; configuration: ${configPath}`,
        );
        console.error(
          `Recover its exact delegated controller: bash ${quote(join(directory, 'recover-host.sh'))}`,
        );
        const prefix = `AXV_HOME=${quote(profile)} AXV_COMPUTE_SOCKET=${quote(socket)} ${quote(executable)}`;
        for (const owned of state.machines) {
          console.error(
            `Recover exact creation: ${prefix} vm operation wait --host-id ${quote(state.hostId || '')} --request-id ${quote(owned.requestId)}`,
          );
          if (owned.id)
            console.error(
              `Delete owned VM: ${prefix} vm rm ${quote(owned.id)} --yes`,
            );
        }
        console.error(
          'Delete unbound retained volumes through the owned host API. Registered project volumes are protected: first delete every owned VM and verify the owned controller/cgroup is stopped before explicitly removing only the printed owned test directories.',
        );
      } else {
        const runtimeState = await lstat(shortRuntime);
        assert.ok(
          runtimeState.isDirectory() &&
            !runtimeState.isSymbolicLink() &&
            runtimeState.uid === process.getuid(),
          'The owned runtime directory changed.',
        );
        assert.equal(
          (
            await readFile(join(shortRuntime, 'acceptance-owner'), 'utf8')
          ).trim(),
          runId,
          'Runtime cleanup ownership changed.',
        );
        assert.equal(
          JSON.parse(await readFile(join(directory, 'ownership.json'), 'utf8'))
            .runId,
          runId,
          'Profile cleanup ownership changed.',
        );
        await rm(shortRuntime, { recursive: true, force: true });
        await rm(directory, { recursive: true, force: true });
      }
      if (!safe)
        throw new Error(
          'Native cleanup is uncertain; owned state was retained.',
        );
    })();
    return cleaning;
  }

  const interrupt = () => {
    interrupted = true;
    if (!work.signal.aborted)
      work.abort(
        Object.assign(
          new Error(
            'Native acceptance interrupted; settling original requests before cleanup.',
          ),
          { exitCode: 130 },
        ),
      );
  };
  for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, interrupt);

  try {
    await markRuntimeOwner();
    assert.ok(
      Buffer.byteLength(
        join(
          dataDir,
          'firecracker',
          'jails',
          'fc',
          `vm-${'0'.repeat(32)}`,
          'root',
          'vsock.sock',
        ),
      ) <= 100,
      'The owned Firecracker runtime is too long for its Unix sockets.',
    );
    await mkdir(profile, { mode: 0o700 });
    await mkdir(runtimeTemporaryDirectory, { mode: 0o700 });
    // A stable short executable basename keeps the per-VM vsock path in range.
    await copyFile(options.firecracker, pinnedFirecracker);
    await chmod(pinnedFirecracker, 0o700);
    await copyFile(options.jailer, pinnedJailer);
    await chmod(pinnedJailer, 0o700);
    assert.equal(
      await sha256(pinnedFirecracker),
      await sha256(options.firecracker),
    );
    await writeFile(configPath, JSON.stringify(config, null, 2) + '\n', {
      mode: 0o600,
    });
    await writeFile(
      join(directory, 'recover-host.sh'),
      `#!/bin/sh\nset -eu\nif [ "$(systemctl show --property=LoadState --value ${quote(unit)})" != not-found ]; then\n  [ "$(systemctl show --property=Description --value ${quote(unit)})" = ${quote(unitDescription)} ] || { echo 'The test unit belongs to another invocation.' >&2; exit 1; }\n  exec systemctl start ${quote(unit)}\nfi\nexec systemd-run ${hostArguments.map(quote).join(' ')}\n`,
      { mode: 0o700 },
    );
    await save();
    if (options.executable) {
      await copyFile(options.executable, executable);
      await chmod(executable, 0o700);
    } else
      await captureCommand(
        go,
        ['build', '-trimpath', '-o', executable, './apps/server/cmd/server'],
        { cwd: root, timeout: 120_000 },
      );
    await startHost();
    const info = await request('/info');
    const provider = info.providers.find(
      (item) => item.provider === 'firecracker',
    );
    assert.ok(
      provider?.available,
      provider?.reason || 'Firecracker provider missing.',
    );
    console.log(
      'Pinned native host, provider availability and image registration passed.',
    );

    const owned = await create('AXV native retained project', { retain: true });
    let health = await ready(owned.id);
    await configureDocker();
    await verifyNetwork(owned.id);
    const token = `native-${randomUUID()}`;
    await exec(owned.id, [
      'sh',
      '-c',
      `printf '%s' ${quote(token)} > native-persistence.txt`,
    ]);
    assert.equal(
      await exec(owned.id, ['cat', 'native-persistence.txt']),
      token,
    );
    const network = await cliJSON(['ip', owned.id]);
    assert.equal(network.machineId, owned.id);
    assert.equal(network.bootId, health.bootId);
    assert.ok(network.addresses.length);
    console.log(
      'Real VM creation, guest capability handshake, exec, project write and network observation passed.',
    );

    await exec(owned.id, [
      'sh',
      '-ec',
      [
        'git init --initial-branch=main native-project-source',
        'git -C native-project-source config user.name "AXV acceptance"',
        'git -C native-project-source config user.email acceptance@example.invalid',
        "printf 'Native project baseline\\n' > native-project-source/README.md",
        'git -C native-project-source add README.md',
        'git -C native-project-source commit -m "Native acceptance baseline"',
        'git clone native-project-source workspace',
        'git -C workspace worktree add ../workspace-worktree -b acceptance-worktree',
      ].join('\n'),
    ]);
    const registration = {
      requestId: `native-project-${randomUUID()}`,
      volumeId: owned.volumeId,
      root: 'workspace',
    };
    state.projects.push(registration);
    await save();
    const { project } = await cliJSON([
      'project',
      'register',
      '--machine',
      owned.id,
      '--volume',
      owned.volumeId,
      '--root',
      registration.root,
      '--request-id',
      registration.requestId,
      '--name',
      'Native Git workspace',
    ]);
    registration.id = project.id;
    await save();
    assert.equal(project.volumeId, owned.volumeId);
    assert.equal(project.root, registration.root);
    const bindingPath = join(directory, 'project-binding.json');
    let projectBinding;

    async function captureProjectBinding() {
      projectBinding = await cliJSON(['project', 'binding', project.id]);
      assert.equal(projectBinding.project.id, project.id);
      await writeFile(bindingPath, JSON.stringify(projectBinding), {
        mode: 0o600,
      });
      return projectBinding;
    }

    const fileRequest = (binding, action, path, fields = {}) =>
      request(`/projects/${project.id}/files/${action}`, 'POST', {
        binding: binding.binding,
        path,
        ...fields,
      });
    const initialBinding = await captureProjectBinding();
    assert.equal(initialBinding.binding.machineId, owned.id);
    const listing = await cliJSON([
      'project',
      'files',
      project.id,
      '',
      '--binding-file',
      bindingPath,
    ]);
    assert.ok(listing.entries.some((entry) => entry.path === 'README.md'));
    const reviewed = await cliJSON([
      'project',
      'read',
      project.id,
      'README.md',
      '--binding-file',
      bindingPath,
    ]);
    assert.equal(reviewed.content, 'Native project baseline\n');
    const projectContent = `Native project changed through AXV: ${token}\n`;
    const projectContentPath = join(directory, 'reviewed-project-content.txt');
    await writeFile(projectContentPath, projectContent);
    const written = await cliJSON([
      'project',
      'write',
      project.id,
      'README.md',
      '--binding-file',
      bindingPath,
      '--expected-sha256',
      reviewed.sha256,
      '--file',
      projectContentPath,
    ]);
    assert.equal(written.content, projectContent);
    assert.notEqual(written.sha256, reviewed.sha256);
    await assert.rejects(
      fileRequest(initialBinding, 'write', 'README.md', {
        content: 'Unreviewed overwrite',
        expectedSha256: reviewed.sha256,
      }),
      (error) => error.statusCode === 409 && error.code === 'file_conflict',
    );
    await assert.rejects(
      fileRequest(initialBinding, 'read', '../native-persistence.txt'),
      (error) => error.statusCode === 400 && error.code === 'invalid_path',
    );
    await exec(owned.id, ['ln', '-s', '/etc/passwd', 'workspace/outside-link']);
    await assert.rejects(
      fileRequest(initialBinding, 'read', 'outside-link'),
      (error) => error.statusCode === 400 && error.code === 'not_file',
    );
    await exec(owned.id, ['rm', 'workspace/outside-link']);
    const folder = await cliJSON([
      'project',
      'mkdir',
      project.id,
      'notes',
      '--binding-file',
      bindingPath,
    ]);
    assert.equal(folder.path, 'notes');
    await cliJSON([
      'project',
      'write',
      project.id,
      'notes/acceptance.txt',
      '--binding-file',
      bindingPath,
      '--create',
      '--file',
      projectContentPath,
    ]);
    await assert.rejects(
      fileRequest(initialBinding, 'write', 'notes/acceptance.txt', {
        content: 'Create-only must preserve existing content',
        expectedSha256: '',
      }),
      (error) => error.statusCode === 409 && error.code === 'file_conflict',
    );
    let projectGitState = await exec(owned.id, [
      'git',
      '-C',
      'workspace',
      'status',
      '--porcelain=v1',
    ]);
    assert.match(projectGitState, / M README\.md/);
    assert.match(projectGitState, /\?\? notes\//);
    const worktreeState = await exec(owned.id, [
      'git',
      '-C',
      'workspace',
      'worktree',
      'list',
      '--porcelain',
    ]);
    console.log(
      'Real Git clone/worktree, retained-volume project registration, CLI list/read/write/mkdir, reviewed hash, create-only and outside-path/symlink protection passed.',
    );

    const taskFinal = (status) =>
      ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(status);
    const taskPath = (taskId) => `/projects/${project.id}/tasks/${taskId}`;
    const taskReceipt = (requestId) =>
      request(`/projects/${project.id}/task-requests/${requestId}`);

    async function submitProjectTask(argv, flags = []) {
      const requestId = `native-task-${randomUUID()}`;
      const result = await cliJSON([
        'project',
        'task',
        'submit',
        project.id,
        '--binding-file',
        bindingPath,
        '--request-id',
        requestId,
        '--timeout',
        '2m',
        '--memory',
        '64',
        '--max-processes',
        '16',
        '--max-log-bytes',
        '65536',
        ...flags,
        '--',
        ...argv,
      ]);
      assert.equal(result.task.requestId, requestId);
      assert.equal(
        result.attempt.binding.bootId,
        projectBinding.binding.bootId,
      );
      return result;
    }

    async function waitProjectTask(
      task,
      predicate = (attempt) => taskFinal(attempt.status),
    ) {
      return until(
        async () => {
          const result = await request(taskPath(task.task.id));
          assert.equal(result.attempt.id, task.attempt.id);
          return predicate(result.attempt) && result;
        },
        `project task ${task.task.id}/${task.attempt.id}`,
        150_000,
      );
    }

    async function projectTaskLog(task) {
      const result = await waitProjectTask(
        task,
        (attempt) =>
          taskFinal(attempt.status) && attempt.cachedBytes === attempt.logBytes,
      );
      const chunks = [];
      let offset = 0;
      while (offset < result.attempt.cachedBytes) {
        const chunk = await request(
          `${taskPath(task.task.id)}/attempts/${task.attempt.id}/logs?offset=${offset}&limit=65536`,
        );
        assert.equal(chunk.taskId, task.task.id);
        assert.equal(chunk.attemptId, task.attempt.id);
        assert.equal(chunk.offset, offset);
        assert.ok(chunk.nextOffset > offset);
        const bytes = Buffer.from(chunk.data, 'base64');
        assert.equal(chunk.nextOffset, offset + bytes.length);
        assert.ok(chunk.nextOffset <= result.attempt.cachedBytes);
        chunks.push(bytes);
        offset = chunk.nextOffset;
      }
      return { result, data: Buffer.concat(chunks) };
    }

    const cancelProjectTask = (task) =>
      cliJSON([
        'project',
        'task',
        'cancel',
        project.id,
        task.task.id,
        task.attempt.id,
        '--host-id',
        state.hostId,
      ]);
    const pythonTask = (source, flags) =>
      submitProjectTask(['python3', '-c', source], flags);

    const durableTask = await pythonTask(
      'from pathlib import Path; import time; p=Path("notes/task-once.txt"); p.open("a").write("once\\n"); print("started",flush=True); time.sleep(12); print("finished",flush=True)',
    );
    await waitProjectTask(
      durableTask,
      (attempt) => attempt.status === 'running',
    );
    const replay = await request(`/projects/${project.id}/tasks`, 'POST', {
      requestId: durableTask.task.requestId,
      binding: initialBinding.binding,
      spec: durableTask.task.spec,
    });
    assert.equal(replay.attempt.id, durableTask.attempt.id);
    await startHost(true);
    assert.equal((await ready(owned.id)).bootId, health.bootId);
    const recoveredTask = await taskReceipt(durableTask.task.requestId);
    assert.equal(recoveredTask.task.id, durableTask.task.id);
    assert.equal(recoveredTask.attempt.id, durableTask.attempt.id);
    const durableLog = await projectTaskLog(durableTask);
    assert.equal(durableLog.result.attempt.status, 'succeeded');
    assert.equal(durableLog.data.toString('utf8'), 'started\nfinished\n');
    assert.equal(
      (await fileRequest(initialBinding, 'read', 'notes/task-once.txt'))
        .content,
      'once\n',
    );

    const binaryTask = await pythonTask(
      'import os; os.write(1, bytes([255,0,10,228,184,150,231,149,140])); os.write(2,b"stderr\\x00")',
    );
    const binaryLog = await projectTaskLog(binaryTask);
    assert.equal(binaryLog.result.attempt.status, 'succeeded');
    assert.deepEqual(
      binaryLog.data,
      Buffer.from([
        255, 0, 10, 228, 184, 150, 231, 149, 140, 115, 116, 100, 101, 114, 114,
        0,
      ]),
    );

    // The descendant starts a fresh session; exact cancellation must use its
    // owned cgroup rather than relying on process-group membership.
    const descendant =
      'import os,time; from pathlib import Path; os.setsid(); Path("notes/task-descendant.pid").write_text(str(os.getpid())); time.sleep(120)';
    const longTask = await pythonTask(
      `import subprocess,time; subprocess.Popen(["python3","-c",${JSON.stringify(descendant)}]); time.sleep(120)`,
    );
    const otherTask = await pythonTask(
      'import time; time.sleep(4); print("independent")',
    );
    const descendantPID = await until(async () => {
      try {
        const file = await fileRequest(
          initialBinding,
          'read',
          'notes/task-descendant.pid',
        );
        return /^\d+$/.test(file.content) && file.content;
      } catch (error) {
        if (error.statusCode === 404) return false;
        throw error;
      }
    }, 'task descendant session');
    await cancelProjectTask(longTask);
    const cancelled = await waitProjectTask(longTask);
    assert.equal(cancelled.attempt.status, 'cancelled');
    await until(
      async () =>
        (
          await exec(owned.id, [
            'python3',
            '-c',
            `from pathlib import Path; print(Path("/proc/${descendantPID}").exists())`,
          ])
        ).trim() === 'False',
      'cancelled descendant exit',
    );
    const independent = await projectTaskLog(otherTask);
    assert.equal(independent.result.attempt.status, 'succeeded');
    assert.equal(independent.data.toString('utf8'), 'independent\n');

    const retried = await cliJSON([
      'project',
      'task',
      'retry',
      project.id,
      longTask.task.id,
      '--binding-file',
      bindingPath,
      '--request-id',
      `native-retry-${randomUUID()}`,
    ]);
    assert.notEqual(retried.attempt.id, longTask.attempt.id);
    assert.equal(retried.attempt.number, 2);
    await waitProjectTask(retried, (attempt) => attempt.status === 'running');
    await cancelProjectTask(longTask);
    const afterOldCancel = await request(taskPath(longTask.task.id));
    assert.equal(afterOldCancel.attempt.id, retried.attempt.id);
    assert.equal(afterOldCancel.attempt.cancelRequested, false);
    assert.ok(!taskFinal(afterOldCancel.attempt.status));
    assert.equal(
      (await taskReceipt(longTask.task.requestId)).attempt.id,
      longTask.attempt.id,
    );
    await cancelProjectTask(retried);
    assert.equal((await waitProjectTask(retried)).attempt.status, 'cancelled');

    const cgroupTask = await pythonTask(
      'import json; from pathlib import Path; p=next(x.split(":",2)[2] for x in Path("/proc/self/cgroup").read_text().splitlines() if x.startswith("0::")); root=Path("/sys/fs/cgroup")/p.lstrip("/"); print(json.dumps({name:(root/name).read_text().strip() for name in ["cpu.max","memory.max","pids.max"]}))',
    );
    const cgroupLog = await projectTaskLog(cgroupTask);
    assert.equal(cgroupLog.result.attempt.status, 'succeeded');
    assert.deepEqual(JSON.parse(cgroupLog.data.toString('utf8')), {
      'cpu.max': '100000 100000',
      'memory.max': String(64 << 20),
      'pids.max': '16',
    });
    const nestedGroupTask = await pythonTask(
      'from pathlib import Path; p=next(x.split(":",2)[2] for x in Path("/proc/self/cgroup").read_text().splitlines() if x.startswith("0::")); root=Path("/sys/fs/cgroup")/p.lstrip("/"); (root/"nested"/"leaf").mkdir(parents=True); print(p)',
    );
    const nestedGroupLog = await projectTaskLog(nestedGroupTask);
    assert.equal(nestedGroupLog.result.attempt.status, 'succeeded');
    const nestedGroupPath = nestedGroupLog.data.toString('utf8').trim();
    assert.match(
      nestedGroupPath,
      /^\/system\.slice\/axv-guest\.service\/attempt-[a-f0-9]{64}$/,
    );
    assert.equal(
      (
        await exec(owned.id, [
          'python3',
          '-c',
          `from pathlib import Path; print(Path(${JSON.stringify('/sys/fs/cgroup' + nestedGroupPath)}).exists())`,
        ])
      ).trim(),
      'False',
    );
    for (const [reason, source, flags] of [
      ['timeout', 'import time; time.sleep(30)', ['--timeout', '1s']],
      [
        'log_limit',
        'import os; os.write(1,b"x"*2048)',
        ['--max-log-bytes', '1024'],
      ],
      [
        'memory_limit',
        'import time; data=bytearray(256*1024*1024); time.sleep(30)',
        [],
      ],
      [
        'process_limit',
        'import subprocess,time\nchildren=[]\ntry:\n while True: children.append(subprocess.Popen(["sleep","30"]))\nexcept OSError:\n time.sleep(30)',
        ['--max-processes', '8'],
      ],
    ]) {
      const limited = await pythonTask(source, flags);
      const outcome = await waitProjectTask(limited);
      assert.equal(
        outcome.attempt.status,
        'failed',
        JSON.stringify(outcome.attempt),
      );
      assert.equal(
        outcome.attempt.reason,
        reason,
        JSON.stringify(outcome.attempt),
      );
      if (reason === 'log_limit') {
        const log = await projectTaskLog(limited);
        assert.equal(log.result.attempt.logLimited, true);
        assert.equal(log.data.length, 1024);
      }
    }

    // Exhaust only this invocation's finite project volume. The task journal
    // remains on the guest root disk so failed execution stays observable.
    const diskFill = 'notes/task-disk-fill';
    try {
      const filling = await pythonTask(
        `import os; f=open(${JSON.stringify(diskFill)},"wb",buffering=0); s=os.fstatvfs(f.fileno()); os.posix_fallocate(f.fileno(),0,max(0,s.f_bavail*s.f_frsize-16777216)); f.seek(0,2)\nwhile True: f.write(b"x"*4096)`,
      );
      const filled = await projectTaskLog(filling);
      assert.equal(filled.result.attempt.status, 'failed');
      assert.match(filled.data.toString('utf8'), /No space left on device/);
      await assert.rejects(
        request(`/projects/${project.id}/files/write`, 'POST', {
          binding: initialBinding.binding,
          path: 'notes/disk-rejected-write',
          expectedSha256: '',
          content: 'Disk exhaustion must be visible.',
        }),
        (error) => error.statusCode === 507 && error.code === 'disk_full',
      );
      let rejected;
      try {
        rejected = await request(`/projects/${project.id}/tasks`, 'POST', {
          requestId: `native-disk-full-${randomUUID()}`,
          binding: initialBinding.binding,
          spec: {
            argv: ['python3', '-c', 'print("must not execute")'],
            directory: '',
            limits: {
              timeoutSeconds: 30,
              cpus: 1,
              memoryMiB: 64,
              maxProcesses: 16,
              maxLogBytes: 65536,
            },
          },
        });
      } catch (error) {
        assert.equal(error.statusCode, 507);
        assert.equal(error.code, 'disk_full');
      }
      if (rejected) {
        const rejectedResult = await waitProjectTask(rejected);
        assert.equal(rejectedResult.attempt.status, 'failed');
        assert.match(
          rejectedResult.attempt.reason,
          /disk_full|Insufficient real free space/,
        );
        assert.equal(rejectedResult.attempt.logBytes, 0);
      }
    } finally {
      await exec(owned.id, ['rm', '-f', `workspace/${diskFill}`]);
    }
    const recoveredDiskTask = await pythonTask('print("disk-recovered")');
    const recoveredDiskLog = await projectTaskLog(recoveredDiskTask);
    assert.equal(recoveredDiskLog.result.attempt.status, 'succeeded');
    assert.equal(recoveredDiskLog.data.toString('utf8'), 'disk-recovered\n');
    assert.equal(
      (await fileRequest(initialBinding, 'read', 'README.md')).sha256,
      written.sha256,
    );

    // Kill only this task's own guest supervisor as the same unprivileged user.
    // systemd restarts it on the same boot; recovery must never relaunch work.
    const executorCrash = await pythonTask(
      'import os,signal,time; from pathlib import Path; Path("notes/executor-once.txt").open("a").write("once\\n"); print("before-executor-exit",flush=True); time.sleep(2); os.kill(os.getppid(),signal.SIGKILL); time.sleep(120)',
    );
    const executorHealth = await until(
      async () => {
        try {
          const current = await request(`/machines/${owned.id}/guest/health`);
          return (
            current.ready && current.instanceId !== health.instanceId && current
          );
        } catch (error) {
          if (error.statusCode === 503 || error.statusCode === 409)
            return false;
          throw error;
        }
      },
      'guest executor recovery after process exit',
      90_000,
    );
    assert.equal(executorHealth.bootId, health.bootId);
    assert.notEqual(executorHealth.instanceId, health.instanceId);
    health = executorHealth;
    assert.equal(
      (await waitProjectTask(executorCrash)).attempt.status,
      'interrupted',
    );
    await captureProjectBinding();
    assert.equal(
      (await fileRequest(projectBinding, 'read', 'notes/executor-once.txt'))
        .content,
      'once\n',
    );
    const afterExecutorCrash = await pythonTask(
      'import time; time.sleep(4); print("new-executor")',
    );
    await cancelProjectTask(executorCrash);
    assert.equal(
      (await request(taskPath(afterExecutorCrash.task.id))).attempt
        .cancelRequested,
      false,
    );
    const recoveredExecutorLog = await projectTaskLog(afterExecutorCrash);
    assert.equal(recoveredExecutorLog.result.attempt.status, 'succeeded');
    assert.equal(recoveredExecutorLog.data.toString('utf8'), 'new-executor\n');

    const interruptedTask = await pythonTask(
      'import time; print("before-restart",flush=True); time.sleep(120)',
    );
    await waitProjectTask(
      interruptedTask,
      (attempt) => attempt.status === 'running',
    );
    await action(owned, 'restart');
    const taskRestartHealth = await ready(owned.id);
    assert.notEqual(taskRestartHealth.bootId, health.bootId);
    health = taskRestartHealth;
    const interruptedTaskResult = await waitProjectTask(interruptedTask);
    assert.equal(interruptedTaskResult.attempt.status, 'interrupted');
    assert.equal(
      interruptedTaskResult.attempt.binding.bootId,
      initialBinding.binding.bootId,
    );
    const cachedInterrupted = await request(
      `${taskPath(interruptedTask.task.id)}/attempts/${interruptedTask.attempt.id}/logs?offset=0&limit=65536`,
    );
    assert.equal(cachedInterrupted.status, 'interrupted');
    const offlineBinary = await request(
      `${taskPath(binaryTask.task.id)}/attempts/${binaryTask.attempt.id}/logs?offset=2&limit=6`,
    );
    assert.deepEqual(
      Buffer.from(offlineBinary.data, 'base64'),
      binaryLog.data.subarray(2, 8),
    );
    assert.equal(offlineBinary.nextOffset, 8);
    const replayAfterReplacementBoot = await request(
      `/projects/${project.id}/tasks`,
      'POST',
      {
        requestId: durableTask.task.requestId,
        binding: initialBinding.binding,
        spec: durableTask.task.spec,
      },
    );
    assert.equal(replayAfterReplacementBoot.attempt.id, durableTask.attempt.id);
    await captureProjectBinding();
    console.log(
      'Durable task receipts, controller restart without duplicate execution, binary log offsets, exact cancellation including setsid descendants, independent tasks, explicit retry, cgroup CPU/memory/PID limits and nested cleanup, timeout/log enforcement, project ENOSPC recovery, guest executor crash recovery and VM restart interruption passed.',
    );
    await verifyProjectWorkflow({
      cliJSON,
      request,
      project,
      bindingPath,
      directory,
      token,
    });
    const volumeWorkspace = { volumeId: project.volumeId, root: '' };
    state.projects.push(volumeWorkspace);
    await save();
    const workspaceRegistration = await request('/projects', 'POST', {
      requestId: `volume-workspace-${randomUUID()}`,
      volumeId: project.volumeId,
      volumeRevision: projectBinding.binding.volumeRevision,
      machineId: owned.id,
      root: '',
      name: 'Native volume workspace',
    });
    Object.assign(volumeWorkspace, workspaceRegistration.project);
    await save();
    const workspaceBinding = await request(
      `/projects/${volumeWorkspace.id}/binding`,
    );
    await verifyProjectInitialization({
      request,
      project: volumeWorkspace,
      binding: workspaceBinding.binding,
      directory,
      token,
      recordProject: async (registration) => {
        const prior = state.projects.find(
          (p) =>
            p.volumeId === registration.volumeId &&
            p.root === registration.root,
        );
        if (prior) Object.assign(prior, registration);
        else state.projects.push(registration);
        await save();
      },
    });
    console.log(
      'Native empty project creation, HTTPS clone, durable initialization/finish replay and existing-directory protection passed.',
    );
    await verifyVMProjectPreview({
      executable,
      directory,
      profileHome: join(directory, 'preview-profile'),
      socket,
      env,
      project,
      binding: projectBinding.binding,
      request,
      cleanupRequest: (path, method, body) =>
        hostJSON(
          {
            socket,
            hostId: state.hostId,
            configFingerprint: state.configFingerprint,
          },
          path,
          method,
          body,
        ),
      signal: work.signal,
    });
    console.log(
      'Native independent-origin development preview, modules, WebSocket, control-origin isolation, stale binding refusal and exact preview revocation passed.',
    );
    codexWorkflow = await verifyVMCodexWorkflow({
      executable,
      directory,
      profileHome: join(shortRuntime, 'cx'),
      socket,
      env,
      project,
      binding: projectBinding.binding,
      run: captureCommand,
      signal: work.signal,
    });
    if (verifyLive) {
      await verifyLive({
        executable,
        directory,
        profileHome: join(shortRuntime, 'live'),
        socket,
        env,
        project,
        binding: projectBinding.binding,
        run: captureCommand,
        signal: work.signal,
        registerCleanup: (close) => {
          closeLiveCodex = close;
        },
      });
    }
    projectGitState = await exec(owned.id, [
      'git',
      '-C',
      'workspace',
      'status',
      '--porcelain=v1',
    ]);

    const remote = (command) => cli(['ssh', '-T', owned.id, '--', command]);
    assert.equal((await remote(`printf '%s' ${quote(token)}`)).stdout, token);
    assert.equal(
      (
        await cli(['ssh', '-T', owned.id, '--', 'exit 42'], {
          allowFailure: true,
        })
      ).code,
      42,
    );
    const sshConfig = (await cli(['ssh-config', owned.id])).stdout;
    const alias = /^Host (\S+)$/m.exec(sshConfig)?.[1];
    assert.ok(alias, sshConfig);
    const sshPath = join(directory, 'ssh config');
    const source = join(directory, 'transfer source.txt');
    const downloaded = join(directory, 'scp result.txt');
    const sftpDownloaded = join(directory, 'sftp result.txt');
    await writeFile(sshPath, sshConfig, { mode: 0o600 });
    await writeFile(source, token);
    await captureCommand(
      'scp',
      ['-F', sshPath, source, `${alias}:/projects/native-scp.txt`],
      { env },
    );
    await captureCommand(
      'scp',
      ['-F', sshPath, `${alias}:/projects/native-scp.txt`, downloaded],
      { env },
    );
    assert.equal(await readFile(downloaded, 'utf8'), token);
    const batch = join(directory, 'sftp.batch');
    await writeFile(
      batch,
      `put "${source}" "/projects/native-sftp.txt"\nget "/projects/native-sftp.txt" "${sftpDownloaded}"\n`,
    );
    await captureCommand('sftp', ['-F', sshPath, '-b', batch, alias], { env });
    assert.equal(await readFile(sftpDownloaded, 'utf8'), token);
    console.log(
      'Real OpenSSH exec, remote exit status, saved configuration, SCP and SFTP passed.',
    );

    const ptyHelper = join(directory, 'verify-vm-pty');
    await captureCommand(
      go,
      ['build', '-o', ptyHelper, join(root, 'scripts/verify-vm-pty.go')],
      { cwd: join(root, 'apps/server'), env, timeout: 120_000 },
    );
    const pty = await captureCommand(
      ptyHelper,
      ['--pty-only', executable, owned.id],
      { cwd: directory, env, timeout: 60_000 },
    );
    console.log(pty.stdout.trim());
    const terminalHelper = join(directory, 'verify-vm-terminal');
    await captureCommand(
      go,
      [
        'build',
        '-o',
        terminalHelper,
        join(root, 'scripts/verify-vm-terminal.go'),
      ],
      { cwd: join(root, 'apps/server'), env, timeout: 120_000 },
    );
    const terminal = await captureCommand(
      terminalHelper,
      [socket, state.hostId, owned.id],
      { cwd: directory, env, timeout: 120_000 },
    );
    console.log(terminal.stdout.trim());
    await verifyForwarding(owned.id, token);

    await action(owned, 'stop');
    assert.equal((await cliJSON(['inspect', owned.id])).status, 'stopped');
    const refused = await cli(['ssh', '-T', owned.id, '--', 'true'], {
      allowFailure: true,
    });
    assert.equal(refused.code, 1);
    assert.match(refused.stderr, /not running/);
    await action(owned, 'start');
    const nextHealth = await ready(owned.id);
    assert.notEqual(nextHealth.bootId, health.bootId);
    await assert.rejects(
      fileRequest(initialBinding, 'write', 'README.md', {
        content: 'Old boot must not receive a write',
        expectedSha256: written.sha256,
      }),
      (error) => error.statusCode === 409 && error.code === 'binding_changed',
    );
    health = nextHealth;
    assert.equal(
      await exec(owned.id, ['cat', 'native-persistence.txt']),
      token,
    );
    await action(owned, 'restart');
    const restarted = await ready(owned.id);
    assert.notEqual(restarted.bootId, health.bootId);
    health = restarted;
    assert.equal(
      await exec(owned.id, ['cat', 'native-persistence.txt']),
      token,
    );
    assert.equal(
      (
        await captureCommand(
          'ssh',
          ['-F', sshPath, '-T', alias, 'cat /projects/native-scp.txt'],
          { env },
        )
      ).stdout,
      token,
    );
    console.log(
      'Stop, stopped SSH refusal, start, restart, new boot identities and persistent project/SSH identity passed.',
    );

    const beforeControllerRestart = await captureProjectBinding();
    await startHost(true);
    assert.equal((await ready(owned.id)).bootId, health.bootId);
    assert.equal(
      (await fileRequest(beforeControllerRestart, 'read', 'README.md')).content,
      projectContent,
    );
    assert.ok(
      (await cliJSON(['project', 'list'])).projects.some(
        (item) => item.id === project.id,
      ),
    );
    assert.equal(
      await exec(owned.id, ['cat', 'native-persistence.txt']),
      token,
    );
    await action(owned, 'stop');
    await startHost(true);
    assert.equal((await cliJSON(['inspect', owned.id])).status, 'stopped');
    const backupResult = await verifyProjectBackupWorkflow({
      request,
      operation,
      create,
      action,
      ready,
      exec,
      restartHost: () => startHost(true),
      source: owned,
      project,
      originalBinding: beforeControllerRestart.binding,
      recordMutation: async (mutation) => {
        if (!state.operations.includes(mutation))
          state.operations.push(mutation);
        await save();
      },
      recordVolume: async (volume) => {
        state.volumes.push(volume);
        await save();
      },
      recordProject: async (registration) => {
        const prior = state.projects.find(
          (p) =>
            p.volumeId === registration.volumeId &&
            p.root === registration.root,
        );
        if (prior) Object.assign(prior, registration);
        else state.projects.push(registration);
        await save();
      },
    });
    assert.equal(backupResult.git, projectGitState);
    assert.equal(backupResult.worktrees, worktreeState);
    console.log(
      'Native stopped-volume backup, copy recovery, immutable backup, restore into a new volume/VM/project and Git/worktree preservation passed.',
    );
    await action(owned, 'rm');
    const { volumes: retainedVolumes } = await request('/volumes');
    const retained = retainedVolumes.find(
      (volume) => volume.id === owned.volumeId,
    );
    assert.ok(
      retained &&
        retained.retain &&
        retained.status === 'ready' &&
        !retained.machineId,
      'Project volume must survive retained VM deletion.',
    );
    console.log(
      'Controller restart preserved a running boot and data; stopped VMs stayed stopped; retained project volume survived VM deletion.',
    );

    const recovered = await create('AXV native retained volume recovery', {
      retain: true,
      volumeId: retained.id,
    });
    await ready(recovered.id);
    for (const action of ['list', 'read', 'write', 'mkdir']) {
      await assert.rejects(
        fileRequest(
          beforeControllerRestart,
          action,
          action === 'list' ? '' : 'README.md',
          action === 'write'
            ? {
                content: 'Old VM must not receive a write',
                expectedSha256: written.sha256,
              }
            : {},
        ),
        (error) => error.statusCode === 409,
      );
    }
    const replacementBinding = await captureProjectBinding();
    assert.equal(replacementBinding.project.id, project.id);
    assert.equal(replacementBinding.binding.machineId, recovered.id);
    assert.notEqual(
      replacementBinding.binding.volumeRevision,
      beforeControllerRestart.binding.volumeRevision,
    );
    assert.equal(
      (await fileRequest(replacementBinding, 'read', 'README.md')).content,
      projectContent,
    );
    assert.equal(
      await exec(recovered.id, [
        'git',
        '-C',
        'workspace',
        'status',
        '--porcelain=v1',
      ]),
      projectGitState,
    );
    assert.equal(
      await exec(recovered.id, [
        'git',
        '-C',
        'workspace',
        'worktree',
        'list',
        '--porcelain',
      ]),
      worktreeState,
    );
    assert.equal(
      await exec(recovered.id, [
        'git',
        '-C',
        'workspace-worktree',
        'status',
        '--porcelain=v1',
      ]),
      '',
    );
    assert.equal(
      await exec(recovered.id, ['cat', 'native-persistence.txt']),
      token,
      'Retained volume metadata survived but its project data did not.',
    );
    await codexWorkflow.verifyReplacement(replacementBinding.binding);
    await codexWorkflow.close();
    await action(recovered, 'rm');
    console.log(
      'A new VM reattached the retained project volume: project identity, edited/untracked code and Git worktrees survived, and all old file bindings were rejected.',
    );

    const task = await create('AXV native expiring task', { ttl: 60 });
    await ready(task.id);
    console.log(
      'Waiting for the real 60-second task lease and controller expiry enforcement.',
    );
    await until(
      async () => (await cliJSON(['inspect', task.id])).status === 'deleted',
      'task TTL deletion',
      150_000,
    );
    const { volumes: expiredVolumes } = await request('/volumes');
    assert.ok(
      !expiredVolumes.some(
        (volume) => volume.id === task.volumeId && volume.status !== 'deleted',
      ),
      'Disposable task volume must be deleted after expiry.',
    );
    assert.ok(
      (await request('/volumes')).volumes.some(
        (volume) => volume.id === retained.id && volume.status === 'ready',
      ),
    );
    console.log(
      'Real TTL deleted its exact VM and disposable project volume while preserving retained storage.',
    );
    assert.equal(
      await sha256(bundle.rootfsPath),
      bundle.rootfs.sha256,
      'Immutable source rootfs changed.',
    );
    assert.equal(
      await sha256(options.kernel),
      options['kernel-sha256'],
      'Immutable source kernel changed.',
    );
  } catch (error) {
    failed = true;
    failure = interrupted
      ? Object.assign(
          new Error('Native acceptance interrupted.', { cause: error }),
          { exitCode: 130 },
        )
      : error;
    console.error('Native acceptance assertion failed:', error);
  } finally {
    // The interrupted main path has completely unwound before cleanup can start.
    inCleanup = true;
    try {
      await cleanup(
        (failed || interrupted) &&
          process.env.AXV_VM_VERIFY_KEEP_FAILURE === '1',
      );
    } catch (error) {
      failure = failure
        ? new AggregateError(
            [failure, error],
            'Native acceptance and cleanup failed.',
          )
        : error;
    }
    for (const signal of ['SIGINT', 'SIGTERM'])
      process.removeListener(signal, interrupt);
  }
  if (failure) throw failure;
  if (interrupted) throw work.signal.reason;
  console.log(
    'Native Linux Firecracker acceptance and owned-resource cleanup passed.',
  );
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    const options = acceptanceOptions(process.argv.slice(2));
    if (options.help) console.log(usage);
    else await acceptance(options);
  } catch (error) {
    console.error(error);
    process.exitCode = error.exitCode || 1;
  }
}
