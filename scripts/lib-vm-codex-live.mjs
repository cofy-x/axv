import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { mkdtemp, readFile, readdir, stat, writeFile } from 'node:fs/promises';
import { join, relative, isAbsolute } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { captureCommand, launchServer, stop } from './lib.mjs';

const hash = (content) => createHash('sha256').update(content).digest('hex');
const final = (status) =>
  ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(status);

// Opt-in native acceptance. The official TUI owns all provider turns; this
// helper retains the account's actual authentication and permission settings.
export async function verifyVMCodexLive({
  executable,
  directory,
  profileHome,
  socket,
  env,
  project,
  binding,
  run,
  signal,
  registerCleanup,
}) {
  const owned = await mkdtemp(join(directory, 'codex-live-'));
  const dataDir = join(owned, 'data');
  const token = randomUUID().replaceAll('-', '');
  const workDirectory = `axv-live-${token.slice(0, 12)}`;
  const tmuxSocket = join(profileHome, 'tmux.sock');
  assert.ok(
    Buffer.byteLength(tmuxSocket) <= 100,
    'Use a short private profile for the real Codex socket.',
  );
  const progressPath = join(owned, 'progress.json');
  const progress = { provider: 'real-codex', projectId: project.id };
  let desktop;
  let active;
  let terminal;
  let terminalFailure;
  let output = '';
  let terminalBytes = 0;
  let terminalReady = false;
  let cleaning = false;
  let cleaned = false;
  let desktopStopped = false;
  const modelTasks = [];

  async function update(phase, fields = {}) {
    Object.assign(progress, fields, {
      phase,
      updatedAt: new Date().toISOString(),
    });
    await writeFile(progressPath, JSON.stringify(progress, null, 2) + '\n', {
      mode: 0o600,
    });
    console.log(`Real Codex VM acceptance: ${phase}.`);
  }

  async function until(check, description, timeout = 30_000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      if (!cleaning) signal?.throwIfAborted();
      if (terminalFailure) throw terminalFailure;
      const value = await check();
      if (value) return value;
      await delay(250, undefined, cleaning ? undefined : { signal });
    }
    throw new Error(`Timed out waiting for ${description}.`);
  }

  async function api(path, method = 'GET', body) {
    const response = await fetch(`${desktop.base}/api${path}`, {
      method,
      headers: { Origin: desktop.base, 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.any(
        [cleaning ? undefined : signal, AbortSignal.timeout(30_000)].filter(
          Boolean,
        ),
      ),
    });
    const value = await response.json();
    assert.ok(
      response.ok,
      `${method} ${path}: ${value.code || response.status}`,
    );
    return value;
  }

  const runPath = () =>
    `/host/codex/sessions/${active.id}/runs/${active.runId}`;
  const contextPath = () =>
    join(dataDir, 'codex-runs', active.runId, 'vm-project.json');

  async function bound(args) {
    const context = JSON.parse(await readFile(contextPath(), 'utf8'));
    assert.equal(context.sessionId, active.id);
    assert.equal(context.runId, active.runId);
    assert.ok(isAbsolute(context.executable));
    assert.equal(context.project.project.id, project.id);
    assert.deepEqual(context.project.binding, binding);
    return JSON.parse(
      (
        await run(
          context.executable,
          ['internal-vm-project', contextPath(), ...args],
          {
            cwd: owned,
            env: { ...env, AXV_HOME: profileHome },
            timeout: 45_000,
          },
        )
      ).stdout,
    );
  }

  async function writeSeed(path, content) {
    const staging = join(owned, `${randomUUID()}.txt`);
    await writeFile(staging, content, { mode: 0o600 });
    await bound(['write', path, '--create', '--file', staging]);
    const actual = await bound(['read', path]);
    assert.equal(actual.content, content);
    assert.equal(actual.sha256, hash(content));
    return actual.sha256;
  }

  async function inspectTask(taskId, attemptId) {
    return until(
      async () => {
        const result = await bound(['task', 'inspect', taskId]);
        const attempt = result.attempts.find((item) => item.id === attemptId);
        assert.ok(
          attempt,
          'The exact submitted attempt must remain inspectable.',
        );
        assert.deepEqual(attempt.binding, binding);
        return (
          final(attempt.status) &&
          attempt.cachedBytes === attempt.logBytes &&
          !attempt.syncError && { ...result, attempt }
        );
      },
      'the original guest attempt and its complete cached logs',
      90_000,
    );
  }

  async function taskLog(result) {
    const { task, attempt } = result;
    assert.ok(!attempt.logLimited, 'Task output must not be truncated.');
    assert.ok(
      attempt.logBytes <= 1 << 20,
      'Acceptance task output exceeded 1 MiB.',
    );
    const chunks = [];
    let offset = 0;
    while (offset < attempt.cachedBytes) {
      const chunk = await bound([
        'task',
        'logs',
        task.id,
        attempt.id,
        '--offset',
        String(offset),
      ]);
      const bytes = Buffer.from(chunk.data, 'base64');
      assert.equal(chunk.taskId, task.id);
      assert.equal(chunk.attemptId, attempt.id);
      assert.equal(chunk.offset, offset);
      assert.equal(chunk.nextOffset, offset + bytes.length);
      assert.ok(
        chunk.nextOffset > offset && chunk.nextOffset <= attempt.cachedBytes,
      );
      chunks.push(bytes);
      offset = chunk.nextOffset;
    }
    return Buffer.concat(chunks).toString('utf8');
  }

  async function observerTask(label, argv) {
    const accepted = await bound([
      'task',
      'submit',
      '--request-id',
      `observer-${label}-${token}`,
      '--directory',
      workDirectory,
      '--timeout',
      '60s',
      '--memory',
      '128',
      '--',
      ...argv,
    ]);
    const result = await inspectTask(accepted.task.id, accepted.attempt.id);
    return { result, log: await taskLog(result) };
  }

  function cleanTerminal(text) {
    return text.replace(
      new RegExp(`${String.fromCharCode(27)}\\[[0-?]*[ -/]*[@-~]`, 'g'),
      '',
    );
  }

  async function attachTerminal() {
    const url = new URL(
      `/api${runPath()}/terminal?cols=180&rows=50`,
      desktop.base,
    );
    url.protocol = 'ws:';
    terminal = new WebSocket(url, { headers: { Origin: desktop.base } });
    terminal.binaryType = 'arraybuffer';
    const decoder = new TextDecoder();
    terminal.addEventListener('message', ({ data }) => {
      if (typeof data === 'string') {
        const control = JSON.parse(data);
        terminalReady ||= control.ready === true;
        if (control.error)
          terminalFailure = new Error(
            'The real Codex terminal reported an attachment error.',
          );
      } else {
        terminalBytes += data.byteLength;
        // Terminal redraws repeat the viewport while a model works. Retain a
        // bounded tail instead of treating cumulative render traffic as logs.
        output = (output + decoder.decode(data, { stream: true })).slice(
          -262144,
        );
      }
    });
    terminal.addEventListener('error', () => {
      terminalFailure = new Error('The real Codex terminal connection failed.');
    });
    terminal.addEventListener('close', () => {
      if (!cleaning)
        terminalFailure = new Error(
          'The real Codex terminal connection closed before verification finished.',
        );
    });
    await until(() => terminalReady, 'official Codex TUI attachment');
    await until(
      async () => {
        const current = (await api(runPath())).session;
        if (['failed', 'ended', 'lost'].includes(current.status))
          throw new Error(
            `The actual Codex runner stopped with status ${current.status}.`,
          );
        if (current.status === 'running' && current.conversationId) {
          active = current;
          return /Codex/i.test(cleanTerminal(output));
        }
        if (
          /Do you trust|trust (?:this|the) (?:folder|directory)/i.test(
            cleanTerminal(output),
          )
        ) {
          if (progress.phase !== 'awaiting-project-trust')
            await update('awaiting-project-trust', {
              desktopBase: desktop.base,
              controlWorkspace: current.projectPath,
            });
          // The operator can review and answer the official TUI prompt. Never
          // configure trust or change approval/sandbox policy on their behalf.
          return false;
        }
        if (/Sign in with ChatGPT/i.test(cleanTerminal(output)))
          throw new Error(
            'Sign in with the intended Codex account before real-model acceptance.',
          );
        return false;
      },
      'the real provider conversation and TUI readiness',
      120_000,
    );
  }

  async function typePrompt(prompt) {
    // Small distinct key events avoid the official TUI's large-paste guard.
    for (const character of prompt) {
      signal?.throwIfAborted();
      terminal.send(Buffer.from(character));
      await delay(6, undefined, { signal });
    }
    await delay(500, undefined, { signal });
    terminal.send(Buffer.from('\r'));
  }

  async function providerMetadata() {
    // Inspect only the exact new conversation, never arbitrary provider history.
    const databases = (await readdir(active.historyHome))
      .filter((name) => /^state_\d+\.sqlite$/.test(name))
      .sort((a, b) => Number(b.match(/\d+/)[0]) - Number(a.match(/\d+/)[0]));
    if (!databases.length) return {};
    const script = [
      'import json, sqlite3, sys',
      'from pathlib import Path',
      'db=sqlite3.connect(Path(sys.argv[1]).as_uri()+"?mode=ro", uri=True)',
      'db.row_factory=sqlite3.Row',
      'row=db.execute("SELECT * FROM threads WHERE id=?", (sys.argv[2],)).fetchone()',
      'print(json.dumps({key:row[key] for key in ("rollout_path", "model", "model_provider") if row is not None and key in row.keys()}))',
      'db.close()',
    ].join('\n');
    const metadata = JSON.parse(
      (
        await run(
          'python3',
          [
            '-c',
            script,
            join(active.historyHome, databases[0]),
            active.conversationId,
          ],
          { cwd: owned, timeout: 10_000 },
        )
      ).stdout,
    );
    if (metadata.rollout_path) {
      const rel = relative(active.historyHome, metadata.rollout_path);
      assert.ok(
        rel && !rel.startsWith('..') && !isAbsolute(rel),
        'Provider rollout must remain inside its recorded history home.',
      );
      const info = await stat(metadata.rollout_path);
      assert.ok(
        info.isFile() && info.size <= 16 << 20,
        'The exact live verification history exceeded its read limit.',
      );
      const content = await readFile(metadata.rollout_path, 'utf8');
      // The exact provider writer can still be appending its final JSON record.
      const lines = content.slice(0, content.lastIndexOf('\n')).split('\n');
      const tools = new Set();
      let boundCommandReferences = 0;
      for (const line of lines) {
        if (!line) continue;
        const item = JSON.parse(line);
        if (
          item.type === 'turn_context' &&
          typeof item.payload?.model === 'string'
        )
          metadata.model = item.payload.model;
        const payload = item.payload;
        if (
          item.type === 'response_item' &&
          ['function_call', 'custom_tool_call'].includes(payload?.type)
        ) {
          if (typeof payload.name === 'string') tools.add(payload.name);
          const arguments_ =
            typeof payload.arguments === 'string'
              ? payload.arguments
              : typeof payload.input === 'string'
                ? payload.input
                : '';
          if (
            arguments_.includes('internal-vm-project') &&
            arguments_.includes(contextPath())
          ) {
            boundCommandReferences += 1;
          }
        }
      }
      metadata.toolNames = [...tools].sort();
      metadata.boundCommandReferences = boundCommandReferences;
    }
    return {
      model: metadata.model,
      modelProvider: metadata.model_provider,
      toolNames: metadata.toolNames,
      boundCommandReferences: metadata.boundCommandReferences,
    };
  }

  async function close() {
    if (cleaned) return;
    cleaning = true;
    const errors = [];
    if (terminal) {
      try {
        terminal.close();
      } catch (error) {
        errors.push(error);
      }
    }
    if (desktop && !desktopStopped && active) {
      try {
        const ended = (await api(`${runPath()}/end`, 'POST')).session;
        assert.equal(ended.status, 'ended');
      } catch {
        // The API response can be lost. Private tmux absence and the AXV-owned
        // history-writer locks below independently verify termination.
        progress.endResponseUnconfirmed = true;
      }
    }
    if (desktop && !desktopStopped) {
      try {
        await stop(desktop.child);
        desktopStopped = true;
      } catch (error) {
        errors.push(error);
      }
    }
    const listed = await captureCommand(
      'tmux',
      ['-S', tmuxSocket, 'list-sessions', '-F', '#{session_name}'],
      { cwd: owned, allowFailure: true, timeout: 10_000 },
    );
    const missing = (result) =>
      result.code === 1 &&
      /no server running|No such file or directory|can't find session/.test(
        result.stderr,
      );
    if (!missing(listed)) {
      assert.equal(
        listed.code,
        0,
        'The private live verification socket could not be inspected.',
      );
      for (const name of listed.stdout.split('\n').filter(Boolean)) {
        assert.ok(
          name.startsWith('axv-codex-'),
          'An unexpected session blocks private-socket cleanup.',
        );
        const removed = await captureCommand(
          'tmux',
          ['-S', tmuxSocket, 'kill-session', '-t', `=${name}`],
          { cwd: owned, allowFailure: true, timeout: 10_000 },
        );
        assert.ok(
          removed.code === 0 || missing(removed),
          'An owned live Codex session could not be stopped.',
        );
      }
      const remaining = await captureCommand(
        'tmux',
        ['-S', tmuxSocket, 'list-sessions'],
        {
          cwd: owned,
          allowFailure: true,
          timeout: 10_000,
        },
      );
      assert.ok(
        missing(remaining),
        'A real verification runtime remains alive.',
      );
    }
    // A missing tmux session alone cannot prove the runner reaped its provider
    // child. These are AXV-owned locks, never provider-owned history locks.
    let runDirectories = [];
    try {
      runDirectories = await readdir(join(dataDir, 'codex-runs'));
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    for (const runDirectory of runDirectories) {
      const manifest = JSON.parse(
        await readFile(
          join(dataDir, 'codex-runs', runDirectory, 'manifest.json'),
          'utf8',
        ),
      );
      const lock = manifest.ownershipPath;
      const rel = relative(join(dataDir, 'codex-owners'), lock);
      assert.ok(
        rel && !rel.startsWith('..') && !isAbsolute(rel),
        'Cleanup requires an AXV-owned conversation lock.',
      );
      await captureCommand(
        'python3',
        [
          '-c',
          [
            'import fcntl, sys, time',
            'with open(sys.argv[1], "rb") as lock:',
            '    deadline=time.monotonic()+10',
            '    while True:',
            '        try:',
            '            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)',
            '            break',
            '        except BlockingIOError:',
            '            if time.monotonic() >= deadline: raise',
            '            time.sleep(0.1)',
          ].join('\n'),
          lock,
        ],
        { cwd: owned, timeout: 12_000 },
      );
    }
    if (errors.length)
      throw new AggregateError(
        errors,
        'Real Codex verification cleanup failed.',
      );
    cleaned = true;
  }

  registerCleanup?.(close);
  let report;
  let failure;
  try {
    await update('starting', { workDirectory });
    desktop = await launchServer({
      executable,
      cwd: owned,
      profileHome,
      dataDir,
      args: ['serve', '--addr', '127.0.0.1:0', '--data-dir', dataDir],
      env: {
        ...env,
        AXV_COMPUTE_SOCKET: socket,
        AXV_TMUX_SOCKET: tmuxSocket,
        DATA_DIR: dataDir,
        ADDR: '127.0.0.1:0',
      },
    });
    const info = await api('/host/codex/info');
    assert.ok(
      info.ready,
      'The installed real Codex CLI and tmux must be available.',
    );
    active = (
      await api('/host/codex/sessions', 'POST', {
        requestId: `real-codex-${token}`,
        vmProject: { binding },
      })
    ).session;
    assert.deepEqual(active.vmProject.binding, binding);
    assert.equal(active.tmuxSocket, tmuxSocket);
    await update('seeding-isolated-guest-project', {
      sessionId: active.id,
      runId: active.runId,
    });
    for (const path of [
      workDirectory,
      `${workDirectory}/src`,
      `${workDirectory}/tests`,
    ])
      await bound(['mkdir', path]);
    const instructions = [
      '# Isolated AXV real-model acceptance project',
      '',
      'Modify only src/calculator.py through the supplied AXV bound file command.',
      'Do not change AGENTS.md, tests, __main__.py or other project directories.',
      'total_positive(values) sums strictly positive integers, returns zero for empty input,',
      'and raises TypeError for any non-integer item (including bool).',
      'Run project commands only as persistent bound tasks with this project subdirectory.',
      'Test: python3 -m unittest discover -s tests -v',
      'Build: mkdir -p dist && python3 -m zipapp src -o dist/axv-live.pyz',
      'Artifact smoke: python3 dist/axv-live.pyz "[8, -2, 5, 0]" (prints 13)',
      'No network, package installation or host project execution is needed.',
      '',
    ].join('\n');
    const tests = [
      'import sys',
      'import unittest',
      'from pathlib import Path',
      'sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))',
      'from calculator import total_positive',
      '',
      'class PositiveTotalTests(unittest.TestCase):',
      '    def test_positive_values(self):',
      '        self.assertEqual(total_positive([8, 5]), 13)',
      '    def test_ignore_negative(self):',
      '        self.assertEqual(total_positive([8, -2, 5, 0]), 13)',
      '    def test_empty(self):',
      '        self.assertEqual(total_positive([]), 0)',
      '    def test_no_positive(self):',
      '        self.assertEqual(total_positive([-5, 0, -1]), 0)',
      '    def test_reject_non_integer(self):',
      '        for value in (True, 1.5, "2", None):',
      '            with self.subTest(value=value), self.assertRaises(TypeError):',
      '                total_positive([1, value])',
      '',
    ].join('\n');
    const immutable = {};
    for (const [path, content] of [
      ['AGENTS.md', instructions],
      ['tests/test_calculator.py', tests],
      [
        'src/__main__.py',
        'import json\nimport sys\nfrom calculator import total_positive\nprint(total_positive(json.loads(sys.argv[1])))\n',
      ],
    ])
      immutable[path] = await writeSeed(`${workDirectory}/${path}`, content);
    const originalHash = await writeSeed(
      `${workDirectory}/src/calculator.py`,
      'def total_positive(values):\n    return sum(values)\n',
    );
    const baseline = await observerTask('baseline', [
      'python3',
      '-m',
      'unittest',
      'discover',
      '-s',
      'tests',
      '-v',
    ]);
    assert.equal(
      baseline.result.attempt.status,
      'failed',
      'The seeded bug must cause an actual guest test failure.',
    );
    assert.match(baseline.log, /FAILED/);
    const priorTaskIds = new Set(
      (await bound(['task', 'list'])).tasks.map(({ task }) => task.id),
    );
    await attachTerminal();
    await update('real-model-working', {
      conversationId: active.conversationId,
      baselineTaskId: baseline.result.task.id,
      baselineAttemptId: baseline.result.attempt.id,
    });
    output = '';
    const prompt = `Use the injected axv-vm-project Skill and its exact bound command. Work only in VM project subdirectory ${workDirectory}. Read its AGENTS.md, src/calculator.py and tests/test_calculator.py through bound reads. Fix only src/calculator.py to satisfy AGENTS.md; preserve all seeded instruction and test files. Use reviewed SHA256 writes. Submit separate persistent test, build and artifact-smoke tasks with --directory ${workDirectory}, --timeout 60s and unique request IDs starting real-model-${token}-. Inspect each exact attempt and complete logs before reporting success. Keep all project execution inside the VM; host staging text is allowed. No network or installation is needed. Report Task and Attempt IDs. After all three tasks succeed, reply with AXV_VM_LIVE_DONE_ followed immediately by verification token ${token}.`;
    await typePrompt(prompt);
    const marker = `AXV_VM_LIVE_DONE_${token}`;
    const modelDeadline = Date.now() + 600_000;
    while (Date.now() < modelDeadline) {
      signal?.throwIfAborted();
      if (terminalFailure) throw terminalFailure;
      const current = (await api(runPath())).session;
      assert.equal(
        current.status,
        'running',
        'The real Codex run must remain alive until completion.',
      );
      const snapshot = cleanTerminal(output);
      if (
        /Would you like to run the following command|Do you want to proceed|requires approval|Do you trust/i.test(
          snapshot.slice(-6000),
        )
      )
        throw new Error(
          'The real Codex model requires an interactive approval; no approval or sandbox setting was changed.',
        );
      if (snapshot.replace(/[\r\n]/g, '').includes(marker)) break;
      await delay(1500, undefined, { signal });
    }
    assert.ok(
      cleanTerminal(output)
        .replace(/[\r\n]/g, '')
        .includes(marker),
      'The real model did not complete the requested VM workflow within ten minutes.',
    );
    const newTasks = (await bound(['task', 'list'])).tasks
      .map(({ task }) => task)
      .filter((task) => !priorTaskIds.has(task.id));
    assert.ok(
      newTasks.length >= 3,
      'The actual model must submit separate test, build and artifact tasks.',
    );
    for (const task of newTasks) {
      assert.ok(
        task.requestId.startsWith(`real-model-${token}-`),
        'An unexpected model task request identity was found.',
      );
      assert.equal(task.spec.directory, workDirectory);
      const result = await inspectTask(task.id, task.currentAttemptId);
      assert.ok(
        ['succeeded', 'failed'].includes(result.attempt.status),
        'A model task must have a reconciled execution outcome.',
      );
      const receipt = await bound([
        'task',
        'receipt',
        result.attempt.requestId,
      ]);
      assert.equal(receipt.task.id, task.id);
      assert.equal(receipt.attempt.id, result.attempt.id);
      const log = await taskLog(result);
      modelTasks.push({
        taskId: task.id,
        attemptId: result.attempt.id,
        requestId: task.requestId,
        attemptRequestId: result.attempt.requestId,
        argv: task.spec.argv,
        status: result.attempt.status,
        exitCode: result.attempt.exitCode,
        logBytes: result.attempt.logBytes,
        cachedBytes: result.attempt.cachedBytes,
        logLimited: result.attempt.logLimited,
        logSHA256: hash(log),
        testsPassed: /Ran 5 tests[\s\S]*\bOK\b/.test(log),
        artifactRan: /(?:^|\n)13\r?(?:\n|$)/.test(log),
      });
    }
    const commands = modelTasks
      .filter((task) => task.status === 'succeeded' && task.exitCode === 0)
      .map((task) => task.argv.join(' '));
    assert.ok(
      commands.some(
        (command) =>
          command.includes('unittest') && command.includes('discover'),
      ),
      'The model did not submit the required tests.',
    );
    assert.ok(
      commands.some((command) => command.includes('zipapp')),
      'The model did not build the required artifact.',
    );
    assert.ok(
      commands.some((command) => command.includes('python3 dist/axv-live.pyz')),
      'The model did not run the built artifact.',
    );
    assert.ok(
      modelTasks.some(
        (task) => task.status === 'succeeded' && task.testsPassed,
      ),
      'The model test task did not contain the complete passing suite result.',
    );
    assert.ok(
      modelTasks.some(
        (task) => task.status === 'succeeded' && task.artifactRan,
      ),
      'The model artifact task did not return the required calculation.',
    );
    await update('independently-verifying-guest-results', {
      modelTaskIds: modelTasks.map((task) => task.taskId),
    });
    for (const [path, expected] of Object.entries(immutable))
      assert.equal(
        (await bound(['read', `${workDirectory}/${path}`])).sha256,
        expected,
        `The model changed immutable acceptance file ${path}.`,
      );
    const modified = await bound([
      'read',
      `${workDirectory}/src/calculator.py`,
    ]);
    assert.notEqual(
      modified.sha256,
      originalHash,
      'The model must actually modify the VM source.',
    );
    assert.equal(modified.sha256, hash(modified.content));
    const independent = await observerTask('independent', [
      'sh',
      '-ec',
      'python3 -m unittest discover -s tests -v; test -s dist/axv-live.pyz; test "$(python3 dist/axv-live.pyz \'[8, -2, 5, 0]\')" = 13; sha256sum dist/axv-live.pyz; echo AXV_INDEPENDENT_GUEST_OK',
    ]);
    assert.equal(independent.result.attempt.status, 'succeeded');
    assert.equal(independent.result.attempt.exitCode, 0);
    assert.match(independent.log, /Ran 5 tests/);
    assert.match(independent.log, /AXV_INDEPENDENT_GUEST_OK/);
    const artifactSHA256 = /^([a-f0-9]{64}) {2}dist\/axv-live\.pyz$/m.exec(
      independent.log,
    )?.[1];
    assert.ok(
      artifactSHA256,
      'The built guest artifact must have an independently observed hash.',
    );
    assert.equal(
      (await api('/host/codex/projects')).projects.length,
      0,
      'The VM control workspace must not enter recent host projects.',
    );
    const metadata = await providerMetadata();
    assert.ok(
      metadata.model,
      'The actual provider model could not be identified from this conversation metadata.',
    );
    // A model can store and reuse the bound command between tool calls. Text
    // matching cannot audit each invocation; guest hashes and durable receipts
    // above are the authoritative execution evidence.
    assert.ok(
      metadata.boundCommandReferences > 0,
      "The model did not reference this run's original bound command.",
    );
    report = {
      provider: 'real-codex',
      verifiedAt: new Date().toISOString(),
      codexVersion: info.codexVersion,
      terminalBytes,
      ...metadata,
      sessionId: active.id,
      runId: active.runId,
      conversationId: active.conversationId,
      projectId: project.id,
      machineId: binding.machineId,
      volumeId: binding.volumeId,
      workDirectory,
      changedFile: `${workDirectory}/src/calculator.py`,
      originalSHA256: originalHash,
      modifiedSHA256: modified.sha256,
      artifactSHA256,
      immutableFilesSHA256: immutable,
      baseline: {
        taskId: baseline.result.task.id,
        attemptId: baseline.result.attempt.id,
        status: baseline.result.attempt.status,
      },
      modelTasks,
      independent: {
        taskId: independent.result.task.id,
        attemptId: independent.result.attempt.id,
        status: independent.result.attempt.status,
        logSHA256: hash(independent.log),
      },
      checks: [
        'actual model through official AXV TUI',
        'in-guest source modification',
        'immutable tests and instructions preserved',
        'model-submitted persistent tests/build/artifact tasks',
        'exact receipts and complete cached logs',
        'independent in-guest tests and artifact execution',
        'no host recent-project pollution',
      ],
    };
  } catch (error) {
    failure = error;
    await update('failed', { error: error.message });
  } finally {
    try {
      await close();
    } catch (error) {
      failure = failure
        ? new AggregateError(
            [failure, error],
            'Real Codex acceptance and cleanup failed.',
          )
        : error;
    }
  }
  if (failure) throw failure;
  report.runtimeCleaned = true;
  await update('completed', { model: report.model, runtimeCleaned: true });
  await writeFile(
    join(directory, 'codex-live-result.json'),
    JSON.stringify(report, null, 2) + '\n',
    { mode: 0o600 },
  );
  console.log(`AXV_REAL_CODEX_RESULT ${JSON.stringify(report)}`);
  return report;
}
