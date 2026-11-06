import {
  mkdtemp,
  readFile,
  open,
  rm,
  writeFile,
  chmod,
  readdir,
} from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  afterAll,
  afterEach,
  beforeAll,
  describe,
  expect,
  it,
  vi,
} from 'vitest';
import { createClient, ClientError } from '../recipes/upstash/client.mjs';
import { configureTestTemp } from './test-temp.mjs';

const token = 'UPSTASH_TEST_ONLY_SECRET_DO_NOT_USE';
const config = { url: 'https://synthetic-database.upstash.io', token };
const keys = {
  cacheKey: 'axv:development:fixture-namespace:cache',
  ratePrefix: 'axv:development:fixture-namespace:rate',
  identifier: 'synthetic-client',
  rateKey: 'axv:development:fixture-namespace:rate:synthetic-client',
};

const clients = [];
let temporary;
let sample;

beforeAll(async () => {
  temporary = configureTestTemp(
    { ...process.env },
    fileURLToPath(new URL('../', import.meta.url)),
  );
  sample = await import('../recipes/upstash/sample.mjs');
});

async function privateFixture() {
  const root = await mkdtemp(join(temporary.directory, 'upstash-state-'));
  await chmod(root, 0o700);
  const state = join(root, 'verification');
  const configPath = join(root, 'configuration.json');
  const configuration = {
    ...config,
    database_id: 'reviewed-development-database',
  };
  const binding = {
    database_id: configuration.database_id,
    region: 'reviewed-development-region',
    application_id: 'synthetic-development-application',
  };
  await writeFile(configPath, JSON.stringify(configuration), { mode: 0o600 });
  await sample.prepare(binding, configPath, state);
  const reviewed = await sample.review(state);
  return { root, state, configPath, configuration, binding, reviewed };
}

function syntheticClient({ onStart, failure } = {}) {
  let current = 1_800_000_000_000;
  let calls = 0;
  let gets = 0;
  let limits = 0;
  const connection = {
    get calls() {
      return calls;
    },
    cacheSet: vi.fn(async () => {
      await onStart?.();
      calls += 1;
      if (failure) throw failure;
      return 'OK';
    }),
    cacheGet: vi.fn(async () => {
      calls += 1;
      gets += 1;
      return gets === 1 ? 'synthetic-cache-value' : null;
    }),
    limit: vi.fn(async () => {
      calls += 1;
      limits += 1;
      return {
        allowed: limits !== 3,
        reason: limits === 3 ? 'rate_limited' : 'allowed',
        remaining: limits === 1 || limits === 4 ? 1 : 0,
        reset: current + 10_000,
      };
    }),
    cleanup: vi.fn(async () => {
      calls += 1;
      return 2;
    }),
    close: vi.fn(),
  };
  return {
    connection,
    createClient: vi.fn(() => connection),
    now: () => current,
    sleep: vi.fn(async (milliseconds) => {
      current += milliseconds;
    }),
  };
}

afterEach(async () => {
  for (const client of clients.splice(0)) client.close();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

afterAll(() => temporary.cleanup());

function client(options) {
  const value = createClient(config, keys, options);
  clients.push(value);
  return value;
}

function response(result, status = 200) {
  return new Response(JSON.stringify({ result }), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function redisFixture({ noScript = false, reviewedKeys = keys } = {}) {
  const commands = [];
  const values = new Map();
  let now = 0;
  let remaining = 2;
  let missingScript = noScript;
  let refilledAt;
  const fetch = vi.fn(async (url, options) => {
    expect(url).toBe(config.url);
    expect(options.method).toBe('POST');
    expect(new Headers(options.headers).get('authorization')).toBe(
      `Bearer ${token}`,
    );
    const command = JSON.parse(options.body);
    commands.push(command);
    switch (command[0].toLowerCase()) {
      case 'set': {
        const expiration = command.findIndex(
          (value) => String(value).toLowerCase() === 'ex',
        );
        expect(command).toContain('nx');
        expect(command[expiration + 1]).toBe(5);
        if (values.has(command[1])) return response(null);
        values.set(command[1], { value: command[2], expires: now + 5000 });
        return response('OK');
      }
      case 'get': {
        const value = values.get(command[1]);
        return response(value && now < value.expires ? value.value : null);
      }
      case 'evalsha':
        if (missingScript) {
          missingScript = false;
          return new Response(
            JSON.stringify({ error: 'NOSCRIPT No matching script' }),
          );
        }
      // Fall through: both SDK operations execute the same bounded token bucket.
      case 'eval': {
        expect(Number(command[2])).toBe(1);
        expect(command[3]).toBe(reviewedKeys.rateKey);
        const timestamp = Number(command[7]);
        if (refilledAt === undefined) refilledAt = timestamp;
        if (timestamp >= refilledAt + 10_000) {
          remaining = 2;
          refilledAt += Math.floor((timestamp - refilledAt) / 10_000) * 10_000;
        }
        const result = remaining > 0 ? --remaining : -1;
        return response([result, refilledAt + 10_000, 2]);
      }
      case 'del': {
        let deleted = 0;
        for (const key of command.slice(1)) {
          if (values.delete(key)) deleted += 1;
        }
        return response(deleted);
      }
      default:
        throw new Error(`Unexpected fixture command ${command[0]}`);
    }
  });
  vi.stubGlobal('fetch', fetch);
  return {
    fetch,
    commands,
    advance(milliseconds) {
      now += milliseconds;
    },
  };
}

function pendingFetch() {
  const requests = [];
  const fetch = vi.fn(
    (_url, options) =>
      new Promise((_resolve, reject) => {
        requests.push(options);
        options.signal.addEventListener(
          'abort',
          () => reject(options.signal.reason),
          { once: true },
        );
      }),
  );
  vi.stubGlobal('fetch', fetch);
  return { fetch, requests };
}

describe('Upstash recipe pinned SDK transport', () => {
  it('uses one reviewed database for a short cache TTL and exact key cleanup', async () => {
    const fixture = redisFixture();
    const connection = client();
    expect(await connection.cacheSet()).toBe('OK');
    expect(await connection.cacheGet()).toBe('synthetic-cache-value');
    fixture.advance(5250);
    expect(await connection.cacheGet()).toBeNull();
    await connection.cleanup();
    const deleted = fixture.commands.find(
      (command) => command[0].toLowerCase() === 'del',
    );
    expect(deleted.slice(1)).toEqual([keys.cacheKey, keys.rateKey]);
    expect(
      fixture.commands.some((command) => /scan|keys|flush/i.test(command[0])),
    ).toBe(false);
    expect(connection.calls).toBe(4);
  });

  it('denies exhausted tokens through Redis on every request without cached decisions', async () => {
    const fixture = redisFixture();
    const connection = client();
    expect(await connection.limit()).toMatchObject({
      allowed: true,
      reason: 'allowed',
      remaining: 1,
    });
    expect(await connection.limit()).toMatchObject({
      allowed: true,
      reason: 'allowed',
      remaining: 0,
    });
    expect(await connection.limit()).toMatchObject({
      allowed: false,
      reason: 'rate_limited',
      remaining: 0,
    });
    expect(await connection.limit()).toMatchObject({
      allowed: false,
      reason: 'rate_limited',
      remaining: 0,
    });
    expect(fixture.fetch).toHaveBeenCalledTimes(4);
    expect(
      fixture.commands.every(
        (command) => command[0].toLowerCase() === 'evalsha',
      ),
    ).toBe(true);
    expect(connection.calls).toBe(4);
  });

  it('counts both script-cache miss and EVAL fallback against the request budget', async () => {
    const fixture = redisFixture({ noScript: true });
    const connection = client({ budget: 2 });
    expect(await connection.limit()).toMatchObject({ allowed: true });
    expect(fixture.commands.map((command) => command[0].toLowerCase())).toEqual(
      ['evalsha', 'eval'],
    );
    expect(connection.calls).toBe(2);
    await expect(connection.cacheGet()).rejects.toMatchObject({
      code: 'command_budget_exhausted',
    });
    expect(fixture.fetch).toHaveBeenCalledTimes(2);
  });

  it('stops before a fallback request when its budget is already spent', async () => {
    const fixture = redisFixture({ noScript: true });
    const connection = client({ budget: 1 });
    await expect(connection.limit()).rejects.toMatchObject({
      code: 'command_budget_exhausted',
    });
    expect(fixture.fetch).toHaveBeenCalledTimes(1);
    expect(connection.calls).toBe(1);
  });

  it.each([
    [401, 'auth_error', 'Unauthorized'],
    [429, 'quota_error', 'Monthly command limit exceeded'],
    [500, 'provider_error', 'Internal provider failure'],
  ])(
    'surfaces HTTP %s safely and freezes subsequent operations',
    async (status, code, message) => {
      const fetch = vi.fn(
        async () =>
          new Response(JSON.stringify({ error: `${message} ${token}` }), {
            status,
          }),
      );
      vi.stubGlobal('fetch', fetch);
      const connection = client();
      const error = await connection.cacheGet().catch((value) => value);
      expect(error).toBeInstanceOf(ClientError);
      expect(error).toMatchObject({ code });
      expect(String(error)).not.toContain(token);
      await expect(connection.limit()).rejects.toMatchObject({
        code: 'closed',
      });
      expect(fetch).toHaveBeenCalledTimes(1);
    },
  );

  it('does not retry a lost network response or silently switch endpoints', async () => {
    const fetch = vi.fn(async () => {
      throw new Error(`network failure ${token}`);
    });
    vi.stubGlobal('fetch', fetch);
    const connection = client();
    const error = await connection.cacheSet().catch((value) => value);
    expect(error).toMatchObject({ code: 'network_error' });
    expect(String(error)).not.toContain(token);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0]).toBe(config.url);
  });

  it('classifies the documented monthly command-limit response and stops further requests', async () => {
    const fetch = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            error: 'ERR max requests limit exceeded.',
          }),
        ),
    );
    vi.stubGlobal('fetch', fetch);
    const connection = client();
    const error = await connection.limit().catch((failure) => failure);
    expect(error).toBeInstanceOf(ClientError);
    expect(error).toMatchObject({
      code: 'quota_error',
      message: 'quota_error',
    });
    expect(String(error)).not.toContain(token);
    expect(String(error)).not.toContain(keys.rateKey);
    await expect(connection.cacheGet()).rejects.toMatchObject({
      code: 'closed',
    });
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(connection.calls).toBe(1);
  });

  it('fails closed when the actual rate-limit SDK returns its timeout success', async () => {
    vi.useFakeTimers();
    const fixture = pendingFetch();
    const connection = client({ requestTimeoutMs: 1000, limiterTimeoutMs: 5 });
    const assertion = expect(connection.limit()).rejects.toMatchObject({
      code: 'timeout',
    });
    await vi.advanceTimersByTimeAsync(6);
    await assertion;
    expect(fixture.fetch).toHaveBeenCalledTimes(1);
    expect(fixture.requests[0].signal.aborted).toBe(true);
    await expect(connection.limit()).rejects.toMatchObject({ code: 'closed' });
    expect(fixture.fetch).toHaveBeenCalledTimes(1);
  });

  it('aborts a stalled request without issuing another request', async () => {
    const fixture = pendingFetch();
    const connection = client({ requestTimeoutMs: 5, limiterTimeoutMs: 1000 });
    await expect(connection.cacheGet()).rejects.toMatchObject({
      code: 'timeout',
    });
    expect(fixture.fetch).toHaveBeenCalledTimes(1);
    expect(fixture.requests[0].signal.aborted).toBe(true);
  });

  it('refuses an invalid limiter response rather than permitting a request', async () => {
    const fetch = vi.fn(async () => response([]));
    vi.stubGlobal('fetch', fetch);
    const connection = client();
    await expect(connection.limit()).rejects.toMatchObject({
      code: 'invalid_response',
    });
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('close revokes only this client and prevents later operations', async () => {
    const fixture = redisFixture();
    const connection = client();
    connection.close();
    await expect(connection.cacheGet()).rejects.toMatchObject({
      code: 'closed',
    });
    expect(fixture.fetch).not.toHaveBeenCalled();
  });

  it.each([
    'http://synthetic-database.upstash.io',
    'https://synthetic-database.upstash.io/another-database',
    'https://user:password@synthetic-database.upstash.io',
    'https://synthetic-database.upstash.io?token=secret',
    'https://another-provider.example.com',
  ])('rejects unreviewed endpoint shapes before network access (%s)', (url) => {
    const fixture = redisFixture();
    expect(() => createClient({ ...config, url }, keys)).toThrow(ClientError);
    expect(fixture.fetch).not.toHaveBeenCalled();
  });

  it('captures exact cleanup keys so caller mutation cannot retarget deletion', async () => {
    const fixture = redisFixture();
    const reviewed = { ...keys };
    const connection = createClient(config, reviewed);
    clients.push(connection);
    reviewed.cacheKey = 'unrelated:cache';
    reviewed.rateKey = 'unrelated:rate';
    await connection.cleanup();
    expect(fixture.commands).toEqual([['del', keys.cacheKey, keys.rateKey]]);
  });
});

describe.skipIf(process.platform === 'win32')(
  'Upstash recipe durable identity',
  () => {
    it('prepares offline and reviews a fresh namespace without exposing connection details', async () => {
      const fetch = vi.fn(() => {
        throw new Error('unexpected network');
      });
      vi.stubGlobal('fetch', fetch);
      const fixture = await privateFixture();
      const result = await sample.inspect(fixture.state);
      expect(result).toMatchObject({
        status: 'prepared',
        policy: 'fail_closed',
        requests: 0,
      });
      expect(result.binding).toEqual(fixture.binding);
      expect(fixture.reviewed.approval).toMatch(/^[0-9a-f]{64}$/);
      expect(JSON.stringify(fixture.reviewed)).not.toContain(token);
      expect(JSON.stringify(fixture.reviewed)).not.toContain(config.url);
      expect(result.keys.cacheKey).toContain(fixture.binding.application_id);
      expect(result.keys.cacheKey).toContain(result.run_id);
      expect(result.keys.rateKey).toBe(
        `${result.keys.ratePrefix}:${result.keys.identifier}`,
      );
      expect(fetch).not.toHaveBeenCalled();
    });

    it('never overwrites existing state or silently assigns it a new namespace', async () => {
      const fixture = await privateFixture();
      await expect(
        sample.prepare(fixture.binding, fixture.configPath, fixture.state),
      ).rejects.toMatchObject({ code: 'state_already_exists' });
      expect(await sample.review(fixture.state)).toEqual(fixture.reviewed);
    });

    it('commits running state before dispatch and checks expiry, denial and refill', async () => {
      const fixture = await privateFixture();
      const fake = syntheticClient({
        onStart: async () => {
          expect(await sample.inspect(fixture.state)).toMatchObject({
            status: 'running',
            phase: 'cache',
          });
        },
      });
      const result = await sample.verify(
        fixture.state,
        fixture.configPath,
        fixture.reviewed.approval,
        fake,
      );
      expect(result).toMatchObject({
        status: 'passed',
        phase: 'complete',
        error: null,
        policy: 'fail_closed',
      });
      expect(fake.createClient).toHaveBeenCalledWith(
        fixture.configuration,
        fixture.reviewed.keys,
        { budget: 12 },
      );
      expect(fake.connection.limit).toHaveBeenCalledTimes(4);
      expect(fake.connection.cacheGet).toHaveBeenCalledTimes(2);
      expect(
        fake.sleep.mock.calls.map(([milliseconds]) => milliseconds),
      ).toEqual([5250, 5000]);
      expect(fake.connection.close).toHaveBeenCalledOnce();
      expect(result.requests).toBe(7);
      expect(JSON.stringify(result)).not.toContain(token);
    });

    it('does not construct the SDK client if the running receipt cannot be durably saved', async () => {
      const fixture = await privateFixture();
      const handle = await open(fixture.configPath, 'r');
      const prototype = Object.getPrototypeOf(handle);
      await handle.close();
      const originalWrite = prototype.writeFile;
      const originalSync = prototype.sync;
      const runningWrites = new WeakSet();
      vi.spyOn(prototype, 'writeFile').mockImplementation(function (
        contents,
        ...options
      ) {
        if (typeof contents === 'string' && contents.startsWith('{')) {
          const pending = JSON.parse(contents);
          if (
            pending.run_id === fixture.reviewed.run_id &&
            pending.status === 'running'
          )
            runningWrites.add(this);
        }
        return originalWrite.call(this, contents, ...options);
      });
      vi.spyOn(prototype, 'sync').mockImplementation(function () {
        if (runningWrites.has(this))
          throw new Error('simulated receipt fsync failure');
        return originalSync.call(this);
      });
      const fake = syntheticClient();
      await expect(
        sample.verify(
          fixture.state,
          fixture.configPath,
          fixture.reviewed.approval,
          fake,
        ),
      ).rejects.toThrow('simulated receipt fsync failure');
      expect(fake.createClient).not.toHaveBeenCalled();
      expect(await sample.inspect(fixture.state)).toMatchObject({
        status: 'prepared',
        requests: 0,
      });
    });

    it('returns known success without reading credentials or dispatching again', async () => {
      const fixture = await privateFixture();
      const fake = syntheticClient();
      const before = await sample.verify(
        fixture.state,
        fixture.configPath,
        fixture.reviewed.approval,
        fake,
      );
      await rm(fixture.configPath);
      const unavailable = vi.fn(() => {
        throw new Error('must not create client');
      });
      const result = await sample.verify(
        fixture.state,
        fixture.configPath,
        fixture.reviewed.approval,
        { createClient: unavailable },
      );
      expect(result).toEqual(before);
      expect(unavailable).not.toHaveBeenCalled();
    });

    it('completes the reviewed task through the pinned SDK and HTTP fixtures', async () => {
      const fixture = await privateFixture();
      const provider = redisFixture({ reviewedKeys: fixture.reviewed.keys });
      let clock = 1_800_000_000_000;
      vi.spyOn(Date, 'now').mockImplementation(() => clock);
      const result = await sample.verify(
        fixture.state,
        fixture.configPath,
        fixture.reviewed.approval,
        {
          createClient,
          now: () => clock,
          sleep: async (milliseconds) => {
            clock += milliseconds;
            provider.advance(milliseconds);
          },
        },
      );
      expect(result).toMatchObject({
        status: 'passed',
        error: null,
        requests: 7,
      });
      expect(provider.fetch).toHaveBeenCalledTimes(7);
      expect(
        provider.commands.map((command) => command[0].toLowerCase()),
      ).toEqual([
        'set',
        'get',
        'evalsha',
        'evalsha',
        'evalsha',
        'get',
        'evalsha',
      ]);
    });

    it.each([
      ['database_id', 'another-development-database'],
      ['url', 'https://another-development-database.upstash.io'],
      ['token', 'ANOTHER_TEST_ONLY_CREDENTIAL'],
    ])('refuses changed %s before dispatch', async (field, value) => {
      const fixture = await privateFixture();
      await writeFile(
        fixture.configPath,
        JSON.stringify({ ...fixture.configuration, [field]: value }),
      );
      const fake = syntheticClient();
      await expect(
        sample.verify(
          fixture.state,
          fixture.configPath,
          fixture.reviewed.approval,
          fake,
        ),
      ).rejects.toMatchObject({ code: 'database_identity_changed' });
      expect(fake.createClient).not.toHaveBeenCalled();
      expect(await sample.inspect(fixture.state)).toMatchObject({
        status: 'prepared',
        requests: 0,
      });
    });

    it('requires the exact review and private configuration before dispatch', async () => {
      const fixture = await privateFixture();
      const fake = syntheticClient();
      await expect(
        sample.verify(fixture.state, fixture.configPath, '0'.repeat(64), fake),
      ).rejects.toMatchObject({ code: 'approval_required' });
      await chmod(fixture.configPath, 0o644);
      await expect(
        sample.verify(
          fixture.state,
          fixture.configPath,
          fixture.reviewed.approval,
          fake,
        ),
      ).rejects.toMatchObject({ code: 'private_path_required' });
      expect(fake.createClient).not.toHaveBeenCalled();
    });

    it('preserves uncertain outcomes and refuses automatic verification replay', async () => {
      const fixture = await privateFixture();
      const fake = syntheticClient({
        failure: Object.assign(new Error(token), { code: 'network_error' }),
      });
      const result = await sample.verify(
        fixture.state,
        fixture.configPath,
        fixture.reviewed.approval,
        fake,
      );
      expect(result).toMatchObject({
        status: 'uncertain',
        error: 'network_error',
        policy: 'fail_closed',
      });
      expect(JSON.stringify(result)).not.toContain(token);
      const retry = syntheticClient();
      await expect(
        sample.verify(
          fixture.state,
          fixture.configPath,
          fixture.reviewed.approval,
          retry,
        ),
      ).rejects.toMatchObject({ code: 'verification_already_started' });
      expect(retry.createClient).not.toHaveBeenCalled();
      expect((await sample.review(fixture.state)).keys).toEqual(
        fixture.reviewed.keys,
      );
    });

    it('refuses malformed or modified established state rather than reseeding', async () => {
      const fixture = await privateFixture();
      const receiptPath = join(fixture.state, 'receipt.json');
      expect(await readdir(fixture.state)).toEqual(['receipt.json']);
      const original = await readFile(receiptPath, 'utf8');
      for (const contents of [
        '{',
        '{}',
        JSON.stringify({ ...JSON.parse(original), max_requests: 16 }),
      ]) {
        await writeFile(receiptPath, contents);
        const fake = syntheticClient();
        await expect(
          sample.verify(
            fixture.state,
            fixture.configPath,
            fixture.reviewed.approval,
            fake,
          ),
        ).rejects.toThrow();
        expect(fake.createClient).not.toHaveBeenCalled();
        expect(await readFile(receiptPath, 'utf8')).toBe(contents);
      }
    });

    it('retains a stale lock until explicit original-attempt inspection', async () => {
      const fixture = await privateFixture();
      await writeFile(join(fixture.state, '.busy'), '999999\n', {
        mode: 0o600,
      });
      const fake = syntheticClient();
      await expect(
        sample.verify(
          fixture.state,
          fixture.configPath,
          fixture.reviewed.approval,
          fake,
        ),
      ).rejects.toMatchObject({ code: 'recipe_busy' });
      expect(await sample.inspect(fixture.state)).toMatchObject({
        status: 'prepared',
      });
      expect(fake.createClient).not.toHaveBeenCalled();
      expect(await readFile(join(fixture.state, '.busy'), 'utf8')).toBe(
        '999999\n',
      );
    });

    it('requires explicit stopped-work attestation and cleans only the original keys', async () => {
      const fixture = await privateFixture();
      const fake = syntheticClient();
      await expect(
        sample.cleanup(
          fixture.state,
          fixture.configPath,
          fixture.reviewed.approval,
          fake,
        ),
      ).rejects.toMatchObject({ code: 'original_work_must_be_stopped' });
      expect(fake.createClient).not.toHaveBeenCalled();
      const result = await sample.cleanup(
        fixture.state,
        fixture.configPath,
        fixture.reviewed.approval,
        { ...fake, stopped: true },
      );
      expect(result).toMatchObject({
        status: 'prepared',
        cleanup_status: 'cleaned',
      });
      expect(fake.createClient).toHaveBeenCalledWith(
        fixture.configuration,
        fixture.reviewed.keys,
        { budget: 1 },
      );
      expect(fake.connection.cleanup).toHaveBeenCalledOnce();
      expect(fake.connection.cacheSet).not.toHaveBeenCalled();
      expect(fake.connection.close).toHaveBeenCalledOnce();
      await sample.cleanup(
        fixture.state,
        fixture.configPath,
        fixture.reviewed.approval,
        { ...fake, stopped: true },
      );
      expect(fake.connection.cleanup).toHaveBeenCalledOnce();
    });

    it('keeps known verification success when cleanup outcome becomes uncertain', async () => {
      const fixture = await privateFixture();
      await sample.verify(
        fixture.state,
        fixture.configPath,
        fixture.reviewed.approval,
        syntheticClient(),
      );
      const fake = syntheticClient();
      fake.connection.cleanup.mockRejectedValue(
        Object.assign(new Error(token), { code: 'network_error' }),
      );
      const result = await sample.cleanup(
        fixture.state,
        fixture.configPath,
        fixture.reviewed.approval,
        { ...fake, stopped: true },
      );
      expect(result).toMatchObject({
        status: 'passed',
        error: null,
        cleanup_status: 'uncertain',
        cleanup_error: 'network_error',
      });
      expect(JSON.stringify(result)).not.toContain(token);
      expect(await sample.inspect(fixture.state)).toMatchObject({
        status: 'passed',
        error: null,
      });
      await expect(
        sample.cleanup(
          fixture.state,
          fixture.configPath,
          fixture.reviewed.approval,
          { ...fake, stopped: true },
        ),
      ).rejects.toMatchObject({ code: 'cleanup_outcome_uncertain' });
      expect(fake.connection.cleanup).toHaveBeenCalledOnce();
    });
  },
);
