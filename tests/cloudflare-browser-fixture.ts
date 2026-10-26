import { execFile, spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { mkdir, mkdtemp, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { createInterface } from 'node:readline';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import { test as base } from '@playwright/test';
import type { BrowserContext, Request, Route } from '@playwright/test';
import type { CloudflarePublication } from '../apps/dashboard/src/features/cloudflare/types';

interface RPCReply {
  id: number;
  result?: unknown;
  error?: string;
  code?: string;
}

type SurfaceResponse =
  | {
      status: number;
      headers: Record<string, string>;
      body: string;
    }
  | { networkError: 'connectionrefused' };

export interface EdgeRequest {
  url: string;
  method: string;
  headers: Record<string, string>;
  status: number;
  body?: string;
  representativeMetadata?: string[];
}

export interface PublishingBrowserFixture {
  command<T>(op: string, value: Record<string, unknown>): Promise<T>;
  publish(
    domain: string,
    name: string,
    target?: string,
  ): Promise<CloudflarePublication>;
  route(
    context: BrowserContext,
    publications: CloudflarePublication[],
    options?: { bootstrapBarrier?: number },
  ): Promise<EdgeRequest[]>;
}

const root = fileURLToPath(new URL('../', import.meta.url));

function representativeEdgeMetadata(
  request: Request,
  headers: Record<string, string>,
) {
  // Chromium interception can occur before the network stack adds Fetch
  // Metadata. These fixture values only drive the real bootstrap handler;
  // native header authorization is covered by the separate Go network tests.
  const navigation = request.isNavigationRequest();
  const origin = new URL(request.url()).origin;
  let frameURL = '';
  try {
    frameURL = request.frame().url();
  } catch {
    /* Service workers do not own a page frame. */
  }
  const source = headers.origin || headers.referer || frameURL;
  let sourceOrigin = '';
  try {
    sourceOrigin = new URL(source).origin;
  } catch {
    /* A new tab has no source origin. */
  }
  const site =
    sourceOrigin === origin
      ? 'same-origin'
      : navigation
        ? 'none'
        : 'cross-site';
  const destination = navigation
    ? 'document'
    : ['script', 'stylesheet', 'image', 'font'].includes(request.resourceType())
      ? request.resourceType()
      : 'empty';
  const values = {
    'sec-fetch-site': site,
    'sec-fetch-mode': navigation
      ? 'navigate'
      : headers.origin
        ? 'cors'
        : 'no-cors',
    'sec-fetch-dest': destination,
  };
  const filled: string[] = [];
  for (const [name, value] of Object.entries(values))
    if (headers[name] === undefined) {
      headers[name] = value;
      filled.push(name);
    }
  return filled;
}

async function launchFixture(): Promise<{
  fixture: PublishingBrowserFixture;
  close: () => Promise<void>;
}> {
  const testRoot = process.env.AXV_TEST_TMPDIR
    ? resolve(process.env.AXV_TEST_TMPDIR)
    : join(root, '.data/test');
  await mkdir(testRoot, { recursive: true, mode: 0o700 });
  const directory = await mkdtemp(join(testRoot, 'cf-browser-'));
  const executable = join(directory, 'browser-fixture');
  const environment = {
    ...process.env,
    TMPDIR: directory,
    GOTMPDIR: directory,
  };
  try {
    await promisify(execFile)(
      process.env.GO || 'go',
      [
        'build',
        '-o',
        executable,
        './apps/server/internal/cloudflare/publish/testdata/browserfixture',
      ],
      { cwd: root, env: environment, timeout: 120_000, maxBuffer: 1024 * 1024 },
    );
  } catch (error) {
    await rm(directory, { recursive: true, force: true });
    throw error;
  }
  const child = spawn(executable, [directory], {
    cwd: root,
    env: environment,
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  const pending = new Map<
    number,
    {
      resolve: (value: unknown) => void;
      reject: (error: Error) => void;
      timer: ReturnType<typeof setTimeout>;
    }
  >();
  let nextID = 0;
  let diagnostics = '';
  let failure: Error | undefined;
  child.stderr.on('data', (chunk: Buffer) => {
    diagnostics = (diagnostics + chunk.toString()).slice(-16384);
  });
  const rejectAll = (error: Error) => {
    failure = error;
    for (const item of pending.values()) {
      clearTimeout(item.timer);
      item.reject(error);
    }
    pending.clear();
  };
  child.once('error', rejectAll);
  const exited = new Promise<void>((resolveExit) =>
    child.once('exit', (code, signal) => {
      rejectAll(
        new Error(
          `Cloudflare browser fixture exited (${signal || code}). ${diagnostics}`,
        ),
      );
      resolveExit();
    }),
  );
  const lines = createInterface({ input: child.stdout });
  lines.on('line', (line) => {
    const response = JSON.parse(line) as RPCReply;
    const item = pending.get(response.id);
    if (!item) return;
    pending.delete(response.id);
    clearTimeout(item.timer);
    if (response.error)
      item.reject(
        Object.assign(new Error(response.error), { code: response.code }),
      );
    else item.resolve(response.result);
  });

  function command<T>(op: string, value: Record<string, unknown>): Promise<T> {
    if (failure) return Promise.reject(failure);
    const id = ++nextID;
    return new Promise<T>((resolveCommand, reject) => {
      const timer = setTimeout(() => {
        pending.delete(id);
        reject(
          new Error(
            `Cloudflare fixture command timed out: ${op}. ${diagnostics}`,
          ),
        );
      }, 20_000);
      pending.set(id, {
        resolve: (result) => resolveCommand(result as T),
        reject,
        timer,
      });
      child.stdin.write(JSON.stringify({ ...value, id, op }) + '\n');
    });
  }

  const fixture: PublishingBrowserFixture = {
    command,
    publish: (domain, name, target) =>
      command('publish', { domain, name, target, requestId: randomUUID() }),
    async route(context, publications, options) {
      const requests: EdgeRequest[] = [];
      let arrivals = 0;
      let release = () => {};
      const barrier = new Promise<void>((resolve) => {
        release = resolve;
      });
      await context.route('**/*', async (route: Route) => {
        const request = route.request();
        const url = new URL(request.url());
        const domain = publications.find(
          (publication) => publication.hostname === url.hostname,
        )?.domain;
        if (url.protocol !== 'https:' || !domain || url.port) {
          await route.abort('blockedbyclient');
          return;
        }
        const headers = await request.allHeaders();
        const representativeMetadata = representativeEdgeMetadata(
          request,
          headers,
        );
        const response = await command<SurfaceResponse>('fetch', {
          domain,
          url: request.url(),
          method: request.method(),
          headers,
          body: request.postDataBuffer()?.toString('base64') || '',
        });
        if ('networkError' in response) {
          requests.push({
            url: request.url(),
            method: request.method(),
            headers,
            status: 0,
            representativeMetadata,
          });
          await route.abort(response.networkError);
          return;
        }
        requests.push({
          url: request.url(),
          method: request.method(),
          headers,
          status: response.status,
          body: request.postData() || undefined,
          representativeMetadata,
        });
        const body = Buffer.from(response.body, 'base64');
        if (
          options?.bootstrapBarrier &&
          request.isNavigationRequest() &&
          body.includes('<title>AXV browser isolation</title>') &&
          arrivals < options.bootstrapBarrier
        ) {
          arrivals++;
          if (arrivals === options.bootstrapBarrier) release();
          const timer = setTimeout(release, 5000);
          try {
            await barrier;
          } finally {
            clearTimeout(timer);
          }
          if (arrivals !== options.bootstrapBarrier)
            throw new Error(
              'Concurrent bootstrap fixture did not receive both navigations.',
            );
        }
        await route.fulfill({
          status: response.status,
          headers: response.headers,
          body,
        });
      });
      return requests;
    },
  };
  return {
    fixture,
    close: async () => {
      try {
        if (!failure) await command('shutdown', {});
      } finally {
        child.stdin.end();
        const timer = setTimeout(() => child.kill('SIGKILL'), 5000);
        try {
          await exited;
        } finally {
          clearTimeout(timer);
          lines.close();
          await rm(directory, { recursive: true, force: true });
        }
      }
    },
  };
}

export const isolationTest = base.extend<
  object,
  { publishingFixture: PublishingBrowserFixture }
>({
  publishingFixture: [
    async ({ browserName: _browserName }, use) => {
      const { fixture, close } = await launchFixture();
      try {
        await use(fixture);
      } finally {
        await close();
      }
    },
    { scope: 'worker' },
  ],
});
