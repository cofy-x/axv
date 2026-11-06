import { createHash, randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, rename, rm } from 'node:fs/promises';
import { isAbsolute, join, dirname, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { pathToFileURL } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

const MAX_FILE = 65536;
const CONFIG_FIELDS = ['database_id', 'token', 'url'];
const BINDING_FIELDS = ['application_id', 'database_id', 'region'];
const RECEIPT_FIELDS = [
  'version',
  'run_id',
  'binding',
  'endpoint_digest',
  'credential_digest',
  'keys',
  'max_requests',
  'approval',
  'status',
  'phase',
  'error',
  'requests',
  'cleanup_status',
  'cleanup_error',
  'cleanup_requests',
];
const ERRORS = new Set([
  'auth_error',
  'quota_error',
  'timeout',
  'network_error',
  'provider_error',
  'command_budget_exhausted',
  'invalid_response',
  'closed',
  'cache_collision',
  'verification_failed',
  'verification_inconclusive',
  'local_state_error',
]);

export class RecipeError extends Error {
  constructor(code) {
    super(code);
    this.code = code;
  }
}

function canonical(value) {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value && typeof value === 'object') {
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${canonical(value[key])}`)
      .join(',')}}`;
  }
  return JSON.stringify(value);
}

function digest(value) {
  return createHash('sha256').update(canonical(value)).digest('hex');
}

function exactFields(value, fields) {
  return (
    value !== null &&
    typeof value === 'object' &&
    !Array.isArray(value) &&
    Object.keys(value).sort().join(',') === [...fields].sort().join(',')
  );
}

function validBinding(binding) {
  return (
    exactFields(binding, BINDING_FIELDS) &&
    typeof binding.database_id === 'string' &&
    /^[A-Za-z0-9_-]{1,80}$/.test(binding.database_id) &&
    typeof binding.region === 'string' &&
    /^[a-z0-9-]{1,48}$/.test(binding.region) &&
    typeof binding.application_id === 'string' &&
    /^[a-z0-9][a-z0-9_-]{0,47}$/.test(binding.application_id)
  );
}

function keysFor(binding, runId) {
  const prefix = `axv:recipe:upstash:${binding.application_id}:${runId}`;
  const identifier = 'synthetic-client';
  const ratePrefix = `${prefix}:rate`;
  return {
    cacheKey: `${prefix}:cache`,
    ratePrefix,
    identifier,
    rateKey: `${ratePrefix}:${identifier}`,
  };
}

async function privatePath(path, directory = false) {
  if (!isAbsolute(path) || resolve(path) !== path)
    throw new RecipeError('private_path_required');
  // Reject symlinked ancestors, including a symlink at the final component.
  for (let current = path; ; current = dirname(current)) {
    const info = await lstat(current);
    if (info.isSymbolicLink()) throw new RecipeError('private_path_required');
    if (dirname(current) === current) break;
  }
  const info = await lstat(path);
  if (
    !(directory ? info.isDirectory() : info.isFile()) ||
    info.uid !== process.getuid() ||
    (info.mode & 0o077) !== 0 ||
    (!directory && info.nlink !== 1)
  )
    throw new RecipeError('private_path_required');
}

async function readPrivate(path) {
  await privatePath(path);
  await privatePath(dirname(path), true);
  const file = await open(
    path,
    constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK,
  );
  try {
    const info = await file.stat();
    if (
      !info.isFile() ||
      info.uid !== process.getuid() ||
      (info.mode & 0o077) !== 0 ||
      info.nlink !== 1 ||
      info.size > MAX_FILE
    )
      throw new RecipeError('private_path_required');
    const buffer = Buffer.alloc(MAX_FILE + 1);
    const { bytesRead } = await file.read(buffer, 0, buffer.length, 0);
    if (bytesRead > MAX_FILE) throw new RecipeError('invalid_local_data');
    return JSON.parse(buffer.subarray(0, bytesRead).toString('utf8'));
  } finally {
    await file.close();
  }
}

async function readConfig(path) {
  const config = await readPrivate(path);
  if (
    !exactFields(config, CONFIG_FIELDS) ||
    typeof config.database_id !== 'string' ||
    typeof config.url !== 'string' ||
    typeof config.token !== 'string' ||
    !/^[\x21-\x7e]{16,4096}$/.test(config.token)
  )
    throw new RecipeError('invalid_config');
  let url;
  try {
    url = new URL(config.url);
  } catch {
    throw new RecipeError('invalid_config');
  }
  if (
    url.protocol !== 'https:' ||
    !/^[a-z0-9-]+\.upstash\.io$/.test(url.hostname) ||
    url.port ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    url.pathname !== '/' ||
    config.url !== url.origin
  )
    throw new RecipeError('invalid_config');
  return config;
}

function approvalFor(receipt) {
  return digest({
    run_id: receipt.run_id,
    binding: receipt.binding,
    endpoint_digest: receipt.endpoint_digest,
    credential_digest: receipt.credential_digest,
    keys: receipt.keys,
    max_requests: receipt.max_requests,
  });
}

async function syncDirectory(path) {
  const directory = await open(path, 'r');
  try {
    await directory.sync();
  } finally {
    await directory.close();
  }
}

async function save(state, receipt) {
  const temporary = join(state, `.receipt-${randomUUID()}`);
  const file = await open(temporary, 'wx', 0o600);
  try {
    try {
      await file.writeFile(canonical(receipt));
      await file.sync();
    } finally {
      await file.close();
    }
    await rename(temporary, join(state, 'receipt.json'));
    await syncDirectory(state);
  } finally {
    await rm(temporary, { force: true });
  }
}

async function locked(state, operation) {
  await privatePath(state, true);
  const lockPath = join(state, '.busy');
  let lock;
  try {
    lock = await open(lockPath, 'wx', 0o600);
  } catch (error) {
    if (error.code === 'EEXIST') throw new RecipeError('recipe_busy');
    throw error;
  }
  try {
    // Diagnostic only; a stale PID never authorizes automatic lock removal.
    await lock.writeFile(`${process.pid}\n`);
    await lock.sync();
    return await operation();
  } finally {
    await lock.close();
    await rm(lockPath);
  }
}

async function load(state) {
  const receipt = await readPrivate(join(state, 'receipt.json'));
  if (
    !exactFields(receipt, RECEIPT_FIELDS) ||
    receipt.version !== 1 ||
    !validBinding(receipt.binding) ||
    typeof receipt.run_id !== 'string' ||
    !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(
      receipt.run_id,
    ) ||
    !/^[a-f0-9]{64}$/.test(receipt.endpoint_digest) ||
    !/^[a-f0-9]{64}$/.test(receipt.credential_digest) ||
    canonical(receipt.keys) !==
      canonical(keysFor(receipt.binding, receipt.run_id)) ||
    !Number.isInteger(receipt.max_requests) ||
    receipt.max_requests < 1 ||
    receipt.max_requests > 16 ||
    approvalFor(receipt) !== receipt.approval ||
    !['prepared', 'running', 'passed', 'failed', 'uncertain'].includes(
      receipt.status,
    ) ||
    !['none', 'cache', 'limit', 'expiry', 'refill', 'complete'].includes(
      receipt.phase,
    ) ||
    !(receipt.error === null || ERRORS.has(receipt.error)) ||
    !Number.isInteger(receipt.requests) ||
    receipt.requests < 0 ||
    receipt.requests > receipt.max_requests ||
    !['pending', 'running', 'cleaned', 'uncertain'].includes(
      receipt.cleanup_status,
    ) ||
    !(receipt.cleanup_error === null || ERRORS.has(receipt.cleanup_error)) ||
    !Number.isInteger(receipt.cleanup_requests) ||
    receipt.cleanup_requests < 0 ||
    receipt.cleanup_requests > 1
  )
    throw new RecipeError('invalid_receipt');
  if (
    (receipt.status === 'prepared' &&
      (receipt.phase !== 'none' ||
        receipt.requests !== 0 ||
        receipt.error !== null)) ||
    (receipt.status === 'passed' && receipt.phase !== 'complete')
  )
    throw new RecipeError('invalid_receipt');
  return receipt;
}

function safeResult(receipt) {
  return {
    run_id: receipt.run_id,
    binding: receipt.binding,
    keys: receipt.keys,
    status: receipt.status,
    phase: receipt.phase,
    error: receipt.error,
    requests: receipt.requests,
    max_requests: receipt.max_requests,
    cleanup_status: receipt.cleanup_status,
    cleanup_error: receipt.cleanup_error,
    cleanup_requests: receipt.cleanup_requests,
    policy: 'fail_closed',
  };
}

export async function prepare(
  binding,
  configPath,
  state,
  { maxRequests = 12 } = {},
) {
  if (!validBinding(binding)) throw new RecipeError('invalid_binding');
  if (!Number.isInteger(maxRequests) || maxRequests < 1 || maxRequests > 16)
    throw new RecipeError('invalid_budget');
  const config = await readConfig(configPath);
  if (config.database_id !== binding.database_id)
    throw new RecipeError('database_identity_changed');
  if (!isAbsolute(state) || resolve(state) !== state)
    throw new RecipeError('private_path_required');
  await privatePath(dirname(state), true);
  try {
    await mkdir(state, { mode: 0o700 });
  } catch (error) {
    if (error.code === 'EEXIST') throw new RecipeError('state_already_exists');
    throw error;
  }
  const runId = randomUUID();
  const receipt = {
    version: 1,
    run_id: runId,
    binding,
    endpoint_digest: digest(config.url),
    credential_digest: digest(config.token),
    keys: keysFor(binding, runId),
    max_requests: maxRequests,
    status: 'prepared',
    phase: 'none',
    error: null,
    requests: 0,
    cleanup_status: 'pending',
    cleanup_error: null,
    cleanup_requests: 0,
  };
  receipt.approval = approvalFor(receipt);
  await locked(state, () => save(state, receipt));
  await syncDirectory(dirname(state));
  return safeResult(receipt);
}

export async function inspect(state) {
  return safeResult(await load(state));
}

export async function review(state) {
  const receipt = await load(state);
  return { ...safeResult(receipt), approval: receipt.approval };
}

async function checkedConfig(receipt, configPath, approval) {
  if (approval !== receipt.approval) throw new RecipeError('approval_required');
  const config = await readConfig(configPath);
  if (
    config.database_id !== receipt.binding.database_id ||
    digest(config.url) !== receipt.endpoint_digest ||
    digest(config.token) !== receipt.credential_digest
  )
    throw new RecipeError('database_identity_changed');
  return config;
}

async function defaultClient(config, keys, options) {
  const { createClient } = await import('./client.mjs');
  return createClient(config, keys, options);
}

function failureCode(error) {
  return ERRORS.has(error?.code) ? error.code : 'local_state_error';
}

function assertResult(condition, code = 'verification_failed') {
  if (!condition) throw new RecipeError(code);
}

export async function verify(
  state,
  configPath,
  approval,
  { createClient = defaultClient, sleep = delay, now = Date.now } = {},
) {
  return locked(state, async () => {
    const receipt = await load(state);
    if (approval !== receipt.approval)
      throw new RecipeError('approval_required');
    if (receipt.status === 'passed') return safeResult(receipt);
    if (receipt.status !== 'prepared' || receipt.cleanup_status !== 'pending')
      throw new RecipeError('verification_already_started');
    const config = await checkedConfig(receipt, configPath, approval);
    receipt.status = 'running';
    receipt.phase = 'cache';
    await save(state, receipt);
    let client;
    try {
      client = await createClient(config, receipt.keys, {
        budget: receipt.max_requests,
      });
      assertResult((await client.cacheSet()) === 'OK', 'cache_collision');
      assertResult((await client.cacheGet()) === 'synthetic-cache-value');
      receipt.phase = 'limit';
      await save(state, receipt);
      const results = [];
      for (let index = 0; index < 3; index++)
        results.push(await client.limit());
      const firstReset = Math.min(...results.map((result) => result.reset));
      assertResult(
        Number.isFinite(firstReset) && firstReset > now(),
        'verification_inconclusive',
      );
      assertResult(
        results[0].allowed === true &&
          results[1].allowed === true &&
          results[2].allowed === false,
      );
      receipt.phase = 'expiry';
      await save(state, receipt);
      await sleep(5250);
      assertResult((await client.cacheGet()) === null);
      receipt.phase = 'refill';
      await save(state, receipt);
      const wait = Math.max(0, firstReset - now() + 250);
      assertResult(wait <= 10250, 'verification_inconclusive');
      await sleep(wait);
      assertResult((await client.limit()).allowed === true);
      receipt.status = 'passed';
      receipt.phase = 'complete';
      receipt.error = null;
    } catch (error) {
      const code = failureCode(error);
      receipt.status = [
        'timeout',
        'network_error',
        'provider_error',
        'invalid_response',
      ].includes(code)
        ? 'uncertain'
        : 'failed';
      receipt.error = code;
    } finally {
      if (client) {
        client.close();
        receipt.requests = client.calls;
      }
    }
    await save(state, receipt);
    return safeResult(receipt);
  });
}

export async function cleanup(
  state,
  configPath,
  approval,
  { stopped = false, createClient = defaultClient } = {},
) {
  if (!stopped) throw new RecipeError('original_work_must_be_stopped');
  return locked(state, async () => {
    const receipt = await load(state);
    if (approval !== receipt.approval)
      throw new RecipeError('approval_required');
    if (receipt.cleanup_status === 'cleaned') return safeResult(receipt);
    if (receipt.cleanup_status !== 'pending')
      throw new RecipeError('cleanup_outcome_uncertain');
    const config = await checkedConfig(receipt, configPath, approval);
    receipt.cleanup_status = 'running';
    await save(state, receipt);
    let client;
    try {
      client = await createClient(config, receipt.keys, { budget: 1 });
      const deleted = await client.cleanup();
      assertResult(Number.isInteger(deleted) && deleted >= 0 && deleted <= 2);
      receipt.cleanup_status = 'cleaned';
      receipt.cleanup_error = null;
    } catch (error) {
      receipt.cleanup_status = 'uncertain';
      receipt.cleanup_error = failureCode(error);
    } finally {
      if (client) {
        client.close();
        receipt.cleanup_requests = client.calls;
      }
    }
    await save(state, receipt);
    return safeResult(receipt);
  });
}

async function main() {
  const { values, positionals } = parseArgs({
    allowPositionals: true,
    options: {
      state: { type: 'string' },
      binding: { type: 'string' },
      config: { type: 'string' },
      approve: { type: 'string' },
      'max-requests': { type: 'string', default: '12' },
      'confirm-stopped': { type: 'boolean', default: false },
    },
  });
  const command = positionals[0];
  if (
    positionals.length !== 1 ||
    !['prepare', 'review', 'inspect', 'verify', 'cleanup'].includes(command) ||
    !values.state ||
    (['prepare', 'verify', 'cleanup'].includes(command) && !values.config) ||
    (command === 'prepare' && !values.binding)
  )
    throw new RecipeError('invalid_arguments');
  let result;
  if (command === 'prepare') {
    const text = await readFile(values.binding, 'utf8');
    if (Buffer.byteLength(text) > MAX_FILE)
      throw new RecipeError('invalid_binding');
    result = await prepare(JSON.parse(text), values.config, values.state, {
      maxRequests: Number(values['max-requests']),
    });
  } else if (command === 'verify') {
    result = await verify(values.state, values.config, values.approve);
  } else if (command === 'cleanup') {
    result = await cleanup(values.state, values.config, values.approve, {
      stopped: values['confirm-stopped'],
    });
  } else if (command === 'review') result = await review(values.state);
  else result = await inspect(values.state);
  process.stdout.write(`${JSON.stringify(result)}\n`);
  return command === 'verify'
    ? Number(result.status !== 'passed')
    : command === 'cleanup'
      ? Number(result.cleanup_status !== 'cleaned')
      : 0;
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  main().then(
    (code) => {
      process.exitCode = code;
    },
    (error) => {
      const code =
        error instanceof RecipeError ? error.code : 'local_state_error';
      process.stderr.write(
        `${JSON.stringify({ error: code, policy: 'fail_closed' })}\n`,
      );
      process.exitCode = 1;
    },
  );
}
