import { createHash } from 'node:crypto';
import { Ratelimit } from '@upstash/ratelimit';
import { Redis } from '@upstash/redis';

export const CACHE_TTL_SECONDS = 5;
export const LIMIT_INTERVAL_MS = 10_000;
export const SYNTHETIC_VALUE = 'synthetic-cache-value';
// The reviewed @upstash/ratelimit 2.2.0 single-region token-bucket script.
const TOKEN_BUCKET_HASH = 'd60be5bd3d83482ae229309f1f7425424f891204';

export class ClientError extends Error {
  constructor(code) {
    super(code);
    this.name = 'ClientError';
    this.code = code;
  }
}

function classify(error) {
  if (error instanceof ClientError) return error;
  const name = typeof error?.name === 'string' ? error.name : '';
  // SDK errors can include command arguments. Inspect only in memory, then discard.
  const message =
    typeof error?.message === 'string'
      ? error.message.split(', command was:')[0]
      : '';
  let code = 'provider_error';
  if (/TimeoutError|AbortError/.test(name)) code = 'timeout';
  else if (
    /unauthori[sz]ed|forbidden|invalid.?token|invalid.?password|wrongpass|noauth|noperm|authentication/i.test(
      message,
    )
  )
    code = 'auth_error';
  else if (
    /quota|commands?[\s_-]*limit|requests?[\s_-]*limit|too many|rate.?limit|daily.?request|monthly.?request/i.test(
      message,
    )
  )
    code = 'quota_error';
  else if (
    /fetch failed|network|ENOTFOUND|EAI_AGAIN|ECONNRESET|ECONNREFUSED|ETIMEDOUT|connection|socket/i.test(
      message,
    )
  )
    code = 'network_error';
  return new ClientError(code);
}

function validateInputs(config, keys, options) {
  let url;
  try {
    url = new URL(config?.url);
  } catch {
    throw new ClientError('invalid_response');
  }
  if (
    url.protocol !== 'https:' ||
    !url.hostname.endsWith('.upstash.io') ||
    url.username ||
    url.password ||
    url.port ||
    url.search ||
    url.hash ||
    url.pathname !== '/' ||
    ![url.origin, `${url.origin}/`].includes(config.url) ||
    typeof config.token !== 'string' ||
    !/^[\x21-\x7e]{8,4096}$/.test(config.token)
  )
    throw new ClientError('invalid_response');
  for (const field of ['cacheKey', 'ratePrefix', 'rateKey', 'identifier']) {
    if (
      typeof keys?.[field] !== 'string' ||
      !/^[A-Za-z0-9:_-]{1,512}$/.test(keys[field])
    )
      throw new ClientError('invalid_response');
  }
  if (
    keys.rateKey !== `${keys.ratePrefix}:${keys.identifier}` ||
    keys.cacheKey === keys.rateKey ||
    !Number.isInteger(options.budget) ||
    options.budget < 0 ||
    options.budget > 16 ||
    [options.requestTimeoutMs, options.limiterTimeoutMs].some(
      (value) => !Number.isInteger(value) || value < 1 || value > 10_000,
    )
  )
    throw new ClientError('invalid_response');
}

function validateCommand(request, keys) {
  const body = request.body;
  if (
    !Array.isArray(body) ||
    typeof body[0] !== 'string' ||
    (request.path !== undefined && request.path.length !== 0)
  )
    throw new ClientError('invalid_response');
  const command = body[0].toLowerCase();
  if (
    command === 'set' &&
    body.length === 6 &&
    body[1] === keys.cacheKey &&
    body[2] === SYNTHETIC_VALUE &&
    body[3] === 'nx' &&
    body[4] === 'ex' &&
    body[5] === CACHE_TTL_SECONDS
  )
    return;
  if (command === 'get' && body.length === 2 && body[1] === keys.cacheKey)
    return;
  if (
    command === 'del' &&
    body.length === 3 &&
    body[1] === keys.cacheKey &&
    body[2] === keys.rateKey
  )
    return;
  if (
    (command === 'evalsha' || command === 'eval') &&
    body.length === 9 &&
    typeof body[1] === 'string' &&
    body[2] === 1 &&
    body[3] === keys.rateKey &&
    body[4] === 2 &&
    body[5] === LIMIT_INTERVAL_MS &&
    body[6] === 2 &&
    Number.isSafeInteger(body[7]) &&
    body[7] >= 0 &&
    body[8] === 1
  ) {
    const hash =
      command === 'evalsha'
        ? body[1]
        : createHash('sha1').update(body[1]).digest('hex');
    if (hash === TOKEN_BUCKET_HASH) return;
  }
  throw new ClientError('invalid_response');
}

export function createClient(
  config,
  keys,
  { budget = 12, requestTimeoutMs = 1500, limiterTimeoutMs = 1000 } = {},
) {
  validateInputs(config, keys, { budget, requestTimeoutMs, limiterTimeoutMs });
  // Capture the reviewed key set; caller mutation cannot retarget an operation.
  keys = Object.freeze({ ...keys });
  const lifecycle = new AbortController();
  let closed = false;
  let calls = 0;
  const redis = new Redis({
    url: config.url,
    token: config.token,
    retry: { retries: 0 },
    enableAutoPipelining: false,
    enableTelemetry: false,
    latencyLogging: false,
    automaticDeserialization: false,
    signal: () =>
      AbortSignal.any([
        lifecycle.signal,
        AbortSignal.timeout(requestTimeoutMs),
      ]),
  });
  redis.use(async (request, next) => {
    if (closed) throw new ClientError('closed');
    validateCommand(request, keys);
    if (calls >= budget) throw new ClientError('command_budget_exhausted');
    calls += 1;
    // Leave NOSCRIPT visible to the SDK's reviewed EVAL fallback.
    return next(request);
  });
  const limiter = new Ratelimit({
    redis,
    limiter: Ratelimit.tokenBucket(2, '10 s', 2),
    prefix: keys.ratePrefix,
    timeout: limiterTimeoutMs,
    analytics: false,
    ephemeralCache: false,
    enableProtection: false,
    dynamicLimits: false,
    enableTelemetry: false,
  });

  function close() {
    closed = true;
    lifecycle.abort(new ClientError('closed'));
  }

  async function operation(action) {
    if (closed) throw new ClientError('closed');
    try {
      const result = await action();
      if (closed) throw new ClientError('closed');
      return result;
    } catch (error) {
      const failure = classify(error);
      close();
      throw failure;
    }
  }

  return Object.freeze({
    get calls() {
      return calls;
    },
    cacheSet: () =>
      operation(async () => {
        const result = await redis.set(keys.cacheKey, SYNTHETIC_VALUE, {
          nx: true,
          ex: CACHE_TTL_SECONDS,
        });
        if (result !== 'OK' && result !== null)
          throw new ClientError('invalid_response');
        return result;
      }),
    cacheGet: () =>
      operation(async () => {
        const result = await redis.get(keys.cacheKey);
        if (result !== SYNTHETIC_VALUE && result !== null)
          throw new ClientError('invalid_response');
        return result;
      }),
    limit: () =>
      operation(async () => {
        const result = await limiter.limit(keys.identifier);
        if (result?.reason === 'timeout') throw new ClientError('timeout');
        if (
          result?.reason !== undefined ||
          typeof result?.success !== 'boolean' ||
          result.limit !== 2 ||
          !Number.isInteger(result.remaining) ||
          result.remaining < 0 ||
          result.remaining > 1 ||
          (!result.success && result.remaining !== 0) ||
          !Number.isSafeInteger(result.reset) ||
          result.reset < 1 ||
          typeof result.pending?.then !== 'function'
        )
          throw new ClientError('invalid_response');
        await result.pending;
        return {
          allowed: result.success,
          reason: result.success ? 'allowed' : 'rate_limited',
          remaining: result.remaining,
          reset: result.reset,
        };
      }),
    cleanup: () =>
      operation(async () => {
        const result = await redis.del(keys.cacheKey, keys.rateKey);
        if (!Number.isInteger(result) || result < 0 || result > 2)
          throw new ClientError('invalid_response');
        return result;
      }),
    close,
  });
}
