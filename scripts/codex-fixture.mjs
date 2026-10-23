import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, open, realpath, writeFile } from 'node:fs/promises';
import { basename, delimiter, join } from 'node:path';
import { promisify } from 'node:util';

const execute = promisify(execFile);

/** A deterministic terminal program: no credentials, network, or model calls. */
export async function createCodexFixture(dir) {
  if (!['darwin', 'linux'].includes(process.platform)) return null;
  try {
    await execute('tmux', ['-V']);
  } catch {
    throw new Error(
      'Host Codex integration tests require tmux. Install it with Homebrew or your Linux package manager.',
    );
  }
  const bin = join(dir, 'codex-bin');
  const requestedProject = join(dir, "project's 中文");
  await mkdir(bin, { recursive: true });
  await mkdir(join(requestedProject, 'nested'), { recursive: true });
  const projectPath = await realpath(requestedProject);
  const axvHome = join(await realpath(dir), 'axv-home');
  const defaultProjectPath = join(axvHome, 'workspace');
  const historyHome = join(await realpath(dir), 'codex-home');
  await mkdir(historyHome, { mode: 0o700 });
  const proxyKeys = [
    'HTTP_PROXY',
    'HTTPS_PROXY',
    'ALL_PROXY',
    'http_proxy',
    'https_proxy',
    'all_proxy',
  ];
  const proxyURL = 'http://fixture-user:fixture-password@127.0.0.1:17890';
  const proxyEnvironment = Object.fromEntries(
    proxyKeys.map((key) => [key, proxyURL]),
  );
  const networkDigest = createHash('sha256')
    .update(proxyKeys.map((key) => proxyEnvironment[key]).join('\0'))
    .digest('hex');
  const executable = join(bin, 'codex');
  const script = `#!${process.execPath}\n(${codexFixtureMain.toString()})(${JSON.stringify(proxyKeys)}).catch(() => { console.error('Codex fixture failed.'); process.exit(1); });\n`;
  await writeFile(executable, script, { mode: 0o755 });
  const tmuxSocket = `axv-test-${basename(dir)}`;
  const env = {
    AXV_HOME: axvHome,
    AXV_WORKSPACE_DIR: defaultProjectPath,
    AXV_TMUX_SOCKET: tmuxSocket,
    CODEX_HOME: historyHome,
    PATH: `${bin}${delimiter}${process.env.PATH || ''}`,
    ...proxyEnvironment,
    NO_PROXY: 'localhost,127.0.0.1,::1',
    no_proxy: 'localhost,127.0.0.1,::1',
  };
  const tmux = (args, environment = env) =>
    execute('tmux', ['-L', tmuxSocket, '-f', '/dev/null', ...args], {
      env: { ...process.env, ...environment },
      timeout: 5000,
    });
  const diagnosticPath = join(dir, 'codex-pane-diagnostics.log');
  const diagnosticScript = join(bin, 'codex-pane-diagnostics.sh');
  const shellQuote = (value) => `'${value.replaceAll("'", "'\\''")}'`;
  await writeFile(diagnosticPath, '', { mode: 0o600 });
  await writeFile(
    diagnosticScript,
    `#!/bin/sh
case "$1" in axv-codex-*) ;; *) exit 0 ;; esac
umask 077
{
  printf '\\n--- Codex pane exited ---\\n'
  printf 'session=%s pane=%s pid=%s dead_status=%s dead_signal=%s dead_time=%s\\n' "$1" "$2" "$3" "$4" "$5" "$6"
  tmux -L ${shellQuote(tmuxSocket)} capture-pane -p -t "$2" -S -30 2>/dev/null | tail -n 30
  printf '\\n--- End Codex pane output ---\\n'
} >> ${shellQuote(diagnosticPath)}
`,
    { mode: 0o700 },
  );
  const cleanup = async () => {
    try {
      await tmux(['kill-server']);
    } catch (error) {
      if (
        !/no server running|No such file|Connection refused/.test(
          error.stderr || '',
        )
      )
        throw error;
    }
  };
  let initialEnvironment;
  let initialOptions;
  let initialWindowOptions;
  let initialServerOptions;
  try {
    await tmux(
      [
        'new-session',
        '-d',
        '-E',
        '-s',
        'fixture-sentinel',
        '/bin/sleep',
        '3600',
      ],
      {
        ...env,
        PATH: process.env.PATH || '',
        ...Object.fromEntries(
          proxyKeys.map((key) => [key, 'http://sentinel.invalid:17889']),
        ),
      },
    );
    // Install fixture diagnostics before recording the shared-server baseline.
    // Only this disposable server's AXV panes can write to the private log.
    const diagnosticCommand = `/bin/sh ${shellQuote(diagnosticScript)} "#{session_name}" "#{pane_id}" "#{pane_pid}" "#{pane_dead_status}" "#{pane_dead_signal}" "#{pane_dead_time}"`;
    await tmux([
      'set-hook',
      '-g',
      'pane-died',
      `run-shell ${shellQuote(diagnosticCommand)}`,
    ]);
    initialEnvironment = (await tmux(['show-environment', '-g'])).stdout;
    initialOptions = (await tmux(['show-options', '-g'])).stdout;
    initialWindowOptions = (await tmux(['show-window-options', '-g'])).stdout;
    initialServerOptions = (await tmux(['show-options', '-s'])).stdout;
  } catch (error) {
    await cleanup();
    throw error;
  }
  return {
    projectPath,
    defaultProjectPath,
    historyHome,
    configPath: join(axvHome, 'config.json'),
    proxyKeys,
    proxyURL,
    networkDigest,
    tmuxSocket,
    env,
    async diagnostics() {
      const file = await open(diagnosticPath, 'r');
      try {
        const { size } = await file.stat();
        const buffer = Buffer.alloc(Math.min(size, 64 * 1024));
        const { bytesRead } = await file.read(
          buffer,
          0,
          buffer.length,
          Math.max(0, size - buffer.length),
        );
        return (
          buffer.subarray(0, bytesRead).toString('utf8') ||
          'No Codex pane exit diagnostics were captured.'
        );
      } finally {
        await file.close();
      }
    },
    async listTmuxSessions() {
      return (await tmux(['list-sessions', '-F', '#{session_name}'])).stdout
        .trim()
        .split('\n');
    },
    async verifyIsolation() {
      await tmux(['has-session', '-t', '=fixture-sentinel']);
      if (
        (await tmux(['show-environment', '-g'])).stdout !== initialEnvironment
      )
        throw new Error(
          'Host Codex changed the shared tmux global environment.',
        );
      if ((await tmux(['show-options', '-g'])).stdout !== initialOptions)
        throw new Error('Host Codex changed the shared tmux global options.');
      if (
        (await tmux(['show-window-options', '-g'])).stdout !==
        initialWindowOptions
      )
        throw new Error(
          'Host Codex changed the shared tmux global window options.',
        );
      if ((await tmux(['show-options', '-s'])).stdout !== initialServerOptions)
        throw new Error('Host Codex changed the shared tmux server options.');
    },
    cleanup,
  };
}

// Serialized into a disposable executable. The fixture speaks the same
// identity protocol as the CLI and stores only deterministic test values.
async function codexFixtureMain(proxyKeys) {
  const { createHash, randomBytes, randomUUID } = await import('node:crypto');
  const { mkdir, readFile, rename, unlink, writeFile } =
    await import('node:fs/promises');
  const { join } = await import('node:path');
  const { createInterface } = await import('node:readline');
  const { createConnection } = await import('node:net');
  const args = process.argv.slice(2);
  if (args.includes('--version')) {
    console.log('codex-cli 0.162.0-axv-test');
    return;
  }
  if (args.includes('--help')) {
    console.log(
      'AXV terminal test fixture: app-server resume --remote --no-daemon --no-alt-screen',
    );
    return;
  }
  if (!process.env.CODEX_HOME)
    throw new Error('The fixture requires its private CODEX_HOME.');
  const history = join(process.env.CODEX_HOME, 'fixture-threads');
  await mkdir(history, { recursive: true, mode: 0o700 });
  const pathFor = (id) => {
    if (!/^[0-9a-f-]{36}$/.test(id)) throw new Error('Invalid fixture id.');
    return join(history, `${id}.json`);
  };
  const readThread = async (id) => JSON.parse(await readFile(pathFor(id)));
  const saveThread = async (thread) => {
    const path = pathFor(thread.id);
    const temporary = `${path}.${process.pid}.tmp`;
    await writeFile(temporary, JSON.stringify(thread), { mode: 0o600 });
    await rename(temporary, path);
  };
  if (args[0] === 'app-server') {
    const threads = new Map();
    const input = createInterface({ input: process.stdin });
    const notify = (method, params) =>
      process.stdout.write(`${JSON.stringify({ method, params })}\n`);
    const reply = (id, result) =>
      process.stdout.write(`${JSON.stringify({ id, result })}\n`);
    const load = async (id) => {
      if (!threads.has(id)) threads.set(id, await readThread(id));
      return threads.get(id);
    };
    let queue = Promise.resolve();
    input.on('line', (line) => {
      queue = queue.then(async () => {
        const message = JSON.parse(line);
        const { id, method, params = {} } = message;
        if (id === undefined) return;
        try {
          if (method === 'initialize') {
            reply(id, { userAgent: 'axv-codex-fixture' });
          } else if (method === 'thread/start') {
            const thread = {
              id: randomUUID(),
              name: null,
              value: 'initial',
              cwd: params.cwd || process.cwd(),
              status: { type: 'idle' },
            };
            threads.set(thread.id, thread);
            reply(id, { thread });
            process.stdout.write(
              `${JSON.stringify({ method: 'thread/started', params: { thread } })}\n`,
            );
          } else if (method === 'thread/name/set') {
            const thread = await load(params.threadId);
            thread.name = params.name;
            await saveThread(thread);
            reply(id, {});
          } else if (method === 'thread/read' || method === 'thread/resume') {
            reply(id, { thread: await load(params.threadId) });
          } else if (method === 'turn/start') {
            const thread = await load(params.threadId);
            if (thread.activeTurnId)
              throw new Error('The fixture already has an active turn.');
            if (!Array.isArray(params.input) || params.input.length === 0)
              throw new Error('Fixture user input is required.');
            thread.activeTurnId = randomUUID();
            thread.status = { type: 'active', activeFlags: [] };
            reply(id, {
              turn: {
                id: thread.activeTurnId,
                status: 'inProgress',
                items: [],
                error: null,
              },
              fixtureInput: params.input,
            });
            notify('turn/started', {
              threadId: thread.id,
              turn: {
                id: thread.activeTurnId,
                status: 'inProgress',
                items: [],
                error: null,
              },
            });
          } else if (method === 'turn/steer') {
            const thread = await load(params.threadId);
            if (
              !thread.activeTurnId ||
              params.expectedTurnId !== thread.activeTurnId ||
              !Array.isArray(params.input) ||
              params.input.length === 0
            )
              throw new Error(
                'Fixture expected active turn ID does not match.',
              );
            reply(id, {
              turnId: thread.activeTurnId,
              fixtureInput: params.input,
            });
          } else if (method === 'fixture/state') {
            const thread = await load(params.threadId);
            thread.value = params.value;
            await saveThread(thread);
            reply(id, {});
          } else if (method === 'fixture/activity') {
            const thread = await load(params.threadId);
            if (!thread.activeTurnId)
              throw new Error(
                'Start a fixture message before activity events.',
              );
            const threadId = thread.id;
            const turnId = thread.activeTurnId;
            if (['approval', 'input'].includes(params.state)) {
              thread.pendingRequestId = `fixture-request-${randomUUID()}`;
              thread.status = {
                type: 'active',
                activeFlags: [
                  params.state === 'approval'
                    ? 'waitingOnApproval'
                    : 'waitingOnUserInput',
                ],
              };
              process.stdout.write(
                `${JSON.stringify({
                  id: thread.pendingRequestId,
                  method:
                    params.state === 'approval'
                      ? 'item/commandExecution/requestApproval'
                      : 'item/tool/requestUserInput',
                  params:
                    params.state === 'approval'
                      ? {
                          threadId,
                          turnId,
                          itemId: 'fixture-item',
                          reason:
                            'Review the fixture request in the official terminal.',
                          command: 'echo fixture',
                          cwd: thread.cwd,
                        }
                      : {
                          threadId,
                          turnId,
                          itemId: 'fixture-item',
                          questions: [
                            {
                              id: 'next-step',
                              header: 'Next step',
                              question: 'Choose the next step.',
                              isOther: false,
                              isSecret: false,
                              options: [
                                {
                                  label: 'Continue',
                                  description: 'Continue the fixture.',
                                },
                              ],
                            },
                          ],
                        },
                })}\n`,
              );
            } else if (params.state === 'resolve') {
              if (!thread.pendingRequestId)
                throw new Error('The fixture has no pending request.');
              notify('serverRequest/resolved', {
                threadId,
                requestId: thread.pendingRequestId,
              });
              delete thread.pendingRequestId;
              thread.status = { type: 'active', activeFlags: [] };
            } else if (['unknown', 'working'].includes(params.state)) {
              thread.status =
                params.state === 'unknown'
                  ? { type: 'systemError' }
                  : { type: 'active', activeFlags: [] };
              notify('thread/status/changed', {
                threadId,
                status: thread.status,
              });
            } else {
              const status = {
                complete: 'completed',
                'complete-later': 'completed',
                fail: 'failed',
                interrupt: 'interrupted',
              }[params.state];
              if (!status) throw new Error('Unknown fixture activity.');
              const complete = async () => {
                // Final turns cannot receive another steer or be completed again.
                // Save the final status before emitting the provider notification.
                delete thread.activeTurnId;
                delete thread.pendingRequestId;
                thread.status = { type: 'idle' };
                const turn = {
                  id: turnId,
                  status,
                  items: [],
                  error:
                    status === 'failed'
                      ? { message: 'The fixture turn failed.' }
                      : null,
                };
                thread.turns = [turn];
                await saveThread(thread);
                notify('turn/completed', {
                  threadId,
                  turn,
                });
              };
              if (params.state === 'complete-later')
                setTimeout(() => {
                  queue = queue.then(complete);
                }, 1000);
              else await complete();
            }
            reply(id, {});
          } else if (method === 'fixture/forget') {
            await unlink(pathFor(params.threadId));
            threads.delete(params.threadId);
            reply(id, {});
          } else if (method === 'thread/fork') {
            throw new Error('The runner should reject fixture forks.');
          } else {
            reply(id, {});
          }
        } catch {
          process.stdout.write(
            `${JSON.stringify({ id, error: { code: -32600, message: 'Fixture history is unavailable.' } })}\n`,
          );
        }
      });
    });
    input.on('close', () => {
      void queue.finally(() => process.exit(0));
    });
    return;
  }
  const remote = args[args.indexOf('--remote') + 1];
  let threadID;
  let activeTurnID;
  let value = 'initial';
  let rpc;
  let socket;
  if (args.includes('--remote')) {
    if (!remote.startsWith('unix://')) throw new Error('Unix socket required.');
    socket = createConnection({ path: remote.slice('unix://'.length) });
    const pending = new Map();
    let nextID = 1;
    let buffer = Buffer.alloc(0);
    let upgraded = false;
    let readyResolve;
    let readyReject;
    const ready = new Promise((resolve, reject) => {
      readyResolve = resolve;
      readyReject = reject;
    });
    const sendFrame = (data, opcode = 1) => {
      const raw = Buffer.isBuffer(data) ? data : Buffer.from(data);
      const length = raw.length;
      const header = Buffer.alloc(length < 126 ? 2 : length < 65536 ? 4 : 10);
      header[0] = 0x80 | opcode;
      if (length < 126) header[1] = 0x80 | length;
      else if (length < 65536) {
        header[1] = 0x80 | 126;
        header.writeUInt16BE(length, 2);
      } else {
        header[1] = 0x80 | 127;
        header.writeBigUInt64BE(BigInt(length), 2);
      }
      const mask = randomBytes(4);
      const payload = Buffer.from(raw);
      for (let i = 0; i < payload.length; i++) payload[i] ^= mask[i % 4];
      socket.write(Buffer.concat([header, mask, payload]));
    };
    socket.on('connect', () => {
      const key = randomBytes(16).toString('base64');
      socket.write(
        `GET /rpc HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: ${key}\r\nSec-WebSocket-Version: 13\r\n\r\n`,
      );
    });
    socket.on('error', (error) => readyReject(error));
    socket.on('close', () => {
      for (const entry of pending.values())
        entry.reject(new Error('Fixture protocol disconnected.'));
      pending.clear();
    });
    socket.on('data', (chunk) => {
      buffer = Buffer.concat([buffer, chunk]);
      if (!upgraded) {
        const end = buffer.indexOf('\r\n\r\n');
        if (end < 0) return;
        if (!buffer.subarray(0, end).toString().startsWith('HTTP/1.1 101')) {
          readyReject(new Error('Fixture WebSocket handshake failed.'));
          return;
        }
        buffer = buffer.subarray(end + 4);
        upgraded = true;
        readyResolve();
      }
      while (buffer.length >= 2) {
        const opcode = buffer[0] & 15;
        let length = buffer[1] & 127;
        let offset = 2;
        if (length === 126) {
          if (buffer.length < 4) return;
          length = buffer.readUInt16BE(2);
          offset = 4;
        } else if (length === 127) {
          if (buffer.length < 10) return;
          length = Number(buffer.readBigUInt64BE(2));
          offset = 10;
        }
        if (buffer.length < offset + length) return;
        const data = buffer.subarray(offset, offset + length);
        buffer = buffer.subarray(offset + length);
        if (opcode === 9) sendFrame(data, 10);
        if (opcode === 8) {
          socket.end();
          continue;
        }
        if (opcode !== 1) continue;
        const message = JSON.parse(data.toString());
        const entry = pending.get(message.id);
        if (!entry) continue;
        pending.delete(message.id);
        if (message.error) entry.reject(new Error(message.error.message));
        else entry.resolve(message.result);
      }
    });
    await ready;
    rpc = (method, params = {}) =>
      new Promise((resolve, reject) => {
        const id = nextID++;
        pending.set(id, { resolve, reject });
        sendFrame(JSON.stringify({ id, method, params }));
      });
    await rpc('initialize', {
      clientInfo: { name: 'axv_fixture_tui', version: '0' },
    });
    sendFrame(JSON.stringify({ method: 'initialized', params: {} }));
    const resumeIndex = args.indexOf('resume');
    const result =
      resumeIndex >= 0
        ? await rpc('thread/resume', { threadId: args[resumeIndex + 1] })
        : await rpc('thread/start', { cwd: process.cwd() });
    threadID = result.thread.id;
    value = result.thread.value;
  }
  const input = createInterface({
    input: process.stdin,
    output: process.stdout,
    terminal: true,
  });
  const prompt = () => {
    input.setPrompt('codex-test> ');
    input.prompt(true);
  };
  console.log('AXV deterministic Codex fixture');
  console.log('PID ' + process.pid);
  console.log('PROJECT ' + process.cwd());
  let commands = Promise.resolve();
  let queuedCommands = 0;
  input.on('line', (line) => {
    queuedCommands++;
    commands = commands.then(async () => {
      try {
        if (line.startsWith('state ')) {
          value = line.slice(6);
          if (rpc) await rpc('fixture/state', { threadId: threadID, value });
          console.log('STORED ' + value);
        } else if (line.startsWith('message ') && rpc) {
          const result = await rpc('turn/start', {
            threadId: threadID,
            input: [{ type: 'text', text: line.slice(8), text_elements: [] }],
          });
          activeTurnID = result.turn.id;
          console.log('TURN_INPUT ' + JSON.stringify(result.fixtureInput));
        } else if (line.startsWith('steer ') && rpc) {
          if (!activeTurnID)
            throw new Error('Start a fixture message before steering.');
          const result = await rpc('turn/steer', {
            threadId: threadID,
            expectedTurnId: activeTurnID,
            input: [{ type: 'text', text: line.slice(6), text_elements: [] }],
          });
          console.log('TURN_INPUT ' + JSON.stringify(result.fixtureInput));
        } else if (line.startsWith('activity ') && rpc) {
          const state = line.slice(9);
          await rpc('fixture/activity', { threadId: threadID, state });
          console.log('ACTIVITY ' + state);
        } else if (line.startsWith('inspect ')) {
          console.log(
            'STATE ' +
              line.slice(8) +
              ' pid=' +
              process.pid +
              ' value=' +
              value,
          );
        } else if (line.startsWith('echo ')) {
          console.log('HEX ' + Buffer.from(line.slice(5)).toString('hex'));
        } else if (line === 'lines') {
          for (let i = 0; i < 100; i++)
            console.log('HISTORY ' + String(i).padStart(3, '0'));
        } else if (line === 'size') {
          console.log(
            'SIZE ' + process.stdout.columns + 'x' + process.stdout.rows,
          );
        } else if (line === 'network') {
          const values = proxyKeys
            .map((key) => process.env[key] || '')
            .join('\0');
          console.log(
            'NETWORK ' + createHash('sha256').update(values).digest('hex'),
          );
        } else if (line === 'forget-history' && rpc) {
          await rpc('fixture/forget', { threadId: threadID });
          console.log('HISTORY_REMOVED');
          input.close();
          return;
        } else if ((line === '/new' || line === '/clear') && rpc) {
          await rpc('thread/start', { cwd: process.cwd() });
          console.log('UNEXPECTED_NEW_THREAD');
        } else if (line === '/fork' && rpc) {
          await rpc('thread/fork', { threadId: threadID });
          console.log('UNEXPECTED_FORK');
        } else if (line.startsWith('/resume ') && rpc) {
          await rpc('thread/resume', { threadId: line.slice(8) });
          console.log('RESUMED');
        } else if (line === 'exit') {
          console.log('FIXTURE EXIT');
          input.close();
          return;
        } else console.log('UNKNOWN ' + line);
      } catch (error) {
        console.log('ERROR ' + error.message);
      }
      queuedCommands--;
      if (queuedCommands === 0) {
        prompt();
      }
    });
  });
  input.on('SIGINT', () => {
    console.log('INTERRUPTED');
    prompt();
  });
  input.on('close', () => {
    process.exit(0);
  });
  prompt();
}
