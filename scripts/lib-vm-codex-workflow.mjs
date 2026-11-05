import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { createCodexFixture } from './codex-fixture.mjs';
import { launchServer, stop } from './lib.mjs';

// tmux can render one protocol line across several rows and frames. Wait for
// the complete JSON array instead of assuming field order or a newline boundary.
export function codexTurnInput(output) {
  const cleaned = output
    .replace(
      new RegExp(`${String.fromCharCode(27)}\\[[0-?]*[ -/]*[@-~]`, 'g'),
      '',
    )
    .replace(/[\r\n]/g, '');
  const prefix = 'TURN_INPUT ';
  const start = cleaned.lastIndexOf(prefix);
  const end = cleaned.lastIndexOf(']');
  if (start < 0 || end < start) return undefined;
  try {
    const value = JSON.parse(cleaned.slice(start + prefix.length, end + 1));
    return Array.isArray(value) ? value : undefined;
  } catch {
    return undefined;
  }
}

// A controlled provider exercises the real Codex runner and protocol bridge;
// every project file operation and persistent task reaches the real guest.
export async function verifyVMCodexWorkflow({
  executable,
  directory,
  profileHome,
  socket,
  env,
  project,
  binding,
  run,
  signal,
}) {
  const fixtureDirectory = await mkdtemp(join(directory, 'codex-workflow-'));
  const dataDir = join(fixtureDirectory, 'data');
  const fixture = await createCodexFixture(fixtureDirectory);
  let desktop;
  let databaseId;
  let active;
  let closed = false;
  const terminals = new Set();
  const results = [];

  async function until(check, description, timeout = 30_000) {
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      signal?.throwIfAborted();
      const value = await check();
      if (value) return value;
      await delay(100, undefined, { signal });
    }
    throw new Error(`Timed out waiting for ${description}.`);
  }

  async function api(path, method = 'GET', body) {
    const response = await fetch(`${desktop.base}/api${path}`, {
      method,
      headers: {
        Origin: desktop.base,
        'Content-Type': 'application/json',
        ...(databaseId && path !== '/host/codex/info'
          ? { 'X-AXV-Database-ID': databaseId }
          : {}),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.any(
        [closed ? undefined : signal, AbortSignal.timeout(15_000)].filter(
          Boolean,
        ),
      ),
    });
    const value = await response.json();
    assert.ok(response.ok, `${method} ${path}: ${JSON.stringify(value)}`);
    return value;
  }

  const runPath = (session) =>
    `/host/codex/sessions/${session.id}/runs/${session.runId}`;
  const contextPath = (session) =>
    join(dataDir, 'codex-runs', session.runId, 'vm-project.json');

  async function bound(session, args, allowFailure = false) {
    const path = contextPath(session);
    const context = JSON.parse(await readFile(path, 'utf8'));
    assert.equal(context.sessionId, session.id);
    assert.equal(context.runId, session.runId);
    return run(context.executable, ['internal-vm-project', path, ...args], {
      cwd: fixtureDirectory,
      env: { ...env, ...fixture.env, AXV_HOME: profileHome },
      allowFailure,
      timeout: 45_000,
    });
  }

  const boundJSON = async (session, args) =>
    JSON.parse((await bound(session, args)).stdout);

  async function startDesktop() {
    desktop = await launchServer({
      executable,
      cwd: fixtureDirectory,
      profileHome,
      dataDir,
      args: ['serve', '--addr', '127.0.0.1:0', '--data-dir', dataDir],
      env: {
        ...env,
        ...fixture.env,
        AXV_COMPUTE_SOCKET: socket,
        DATA_DIR: dataDir,
        ADDR: '127.0.0.1:0',
      },
    });
    if (!databaseId) {
      databaseId = (await api('/bootstrap?clientId=vm-codex-workflow'))
        .databaseId;
      assert.equal(typeof databaseId, 'string');
      assert.ok(databaseId);
    }
  }

  async function running(session) {
    return until(async () => {
      const current = (await api(runPath(session))).session;
      assert.ok(
        !['failed', 'ended', 'lost'].includes(current.status),
        JSON.stringify(current),
      );
      return current.status === 'running' && current;
    }, 'the original Codex conversation');
  }

  async function verifySkill(session) {
    const url = new URL(
      `/api${runPath(session)}/terminal?cols=500&rows=24`,
      desktop.base,
    );
    url.protocol = 'ws:';
    url.searchParams.set('databaseId', databaseId);
    const terminal = new WebSocket(url, {
      headers: { Origin: desktop.base },
    });
    terminals.add(terminal);
    terminal.binaryType = 'arraybuffer';
    let ready = false;
    let failure;
    let output = '';
    const decoder = new TextDecoder();
    terminal.addEventListener('message', ({ data }) => {
      if (typeof data === 'string') {
        const control = JSON.parse(data);
        ready ||= control.ready === true;
        if (control.error) failure = new Error(control.error);
      } else {
        output += decoder.decode(data, { stream: true });
        if (output.length > 4 << 20)
          failure = new Error('Codex acceptance output exceeded 4 MiB.');
      }
    });
    terminal.addEventListener('error', () => {
      failure = new Error('Codex acceptance terminal failed.');
    });
    terminal.addEventListener('close', () => terminals.delete(terminal));
    const wait = (check, description) =>
      until(() => {
        if (failure) throw failure;
        return check();
      }, description);
    await wait(() => ready, 'Codex terminal attachment');
    const marker = `native-workflow-${randomUUID()}`;
    terminal.send(Buffer.from(`message ${marker}\r`));
    const input = await wait(
      () => codexTurnInput(output),
      'official Skill and VM context protocol input',
    );
    assert.ok(
      input.some((item) => item.type === 'text' && item.text === marker),
    );
    assert.ok(
      input.some(
        (item) => item.type === 'skill' && item.name === 'axv-vm-project',
      ),
      JSON.stringify(input),
    );
    assert.ok(
      input.some(
        (item) =>
          item.type === 'text' &&
          item.text.includes(`session ${session.id}, run ${session.runId}`),
      ),
    );
    assert.ok(
      input.some(
        (item) =>
          item.type === 'text' &&
          item.text.includes(session.vmProject.binding.instanceId),
      ),
    );
    terminal.close();
    results.push({
      check: 'provider-skill-and-context',
      runId: session.runId,
      providerThreadId: session.conversationId,
    });
  }

  async function end(session) {
    const ended = (await api(`${runPath(session)}/end`, 'POST')).session;
    assert.equal(ended.status, 'ended');
  }

  async function close() {
    if (closed) return;
    closed = true;
    const errors = [];
    for (const terminal of terminals) terminal.close();
    if (desktop && active) {
      try {
        await end(active);
      } catch (error) {
        errors.push(error);
      }
    }
    if (desktop) {
      try {
        await stop(desktop.child);
      } catch (error) {
        errors.push(error);
      }
    }
    try {
      await fixture.verifyIsolation();
    } catch (error) {
      errors.push(error);
    }
    try {
      await fixture.cleanup();
    } catch (error) {
      errors.push(error);
    }
    if (errors.length)
      throw new AggregateError(errors, 'VM Codex acceptance cleanup failed.');
  }

  try {
    await startDesktop();
    const createRequest = {
      requestId: `native-codex-${randomUUID()}`,
      vmProject: { binding },
    };
    active = (await api('/host/codex/sessions', 'POST', createRequest)).session;
    active = await running(active);
    const first = active;
    assert.equal(first.target, 'host');
    assert.deepEqual(first.vmProject.binding, binding);
    assert.equal(first.vmProject.project.id, project.id);
    await verifySkill(first);
    await boundJSON(first, ['mkdir', 'codex-workflow']);
    const filePath = 'codex-workflow/agent.txt';
    const content = `Original bound VM project ${randomUUID()}\n`;
    const contentFile = join(fixtureDirectory, 'agent-content.txt');
    await writeFile(contentFile, content, { mode: 0o600 });
    const written = await boundJSON(first, [
      'write',
      filePath,
      '--create',
      '--file',
      contentFile,
    ]);
    assert.equal((await boundJSON(first, ['read', filePath])).content, content);
    const taskArgs = [
      'task',
      'submit',
      '--request-id',
      `native-agent-task-${randomUUID()}`,
      '--timeout',
      '30s',
      '--memory',
      '128',
      '--',
      'python3',
      '-c',
      `from pathlib import Path; import time; assert Path(${JSON.stringify(filePath)}).read_text()==${JSON.stringify(content)}; Path("codex-workflow/once.txt").open("a").write("once\\n"); print("AXV_AGENT_GUEST_OK",flush=True); time.sleep(3)`,
    ];
    const accepted = await boundJSON(first, taskArgs);
    assert.equal(accepted.attempt.binding.machineId, binding.machineId);
    await stop(desktop.child);
    desktop = undefined;
    await startDesktop();
    const recovered = (await api(runPath(first))).session;
    assert.equal(recovered.runId, first.runId);
    assert.equal(recovered.conversationId, first.conversationId);
    assert.deepEqual(recovered.vmProject, first.vmProject);
    const completed = await until(async () => {
      const result = await boundJSON(first, [
        'task',
        'inspect',
        accepted.task.id,
      ]);
      assert.equal(result.attempt.id, accepted.attempt.id);
      if (
        ['failed', 'cancelled', 'interrupted'].includes(result.attempt.status)
      )
        throw new Error(JSON.stringify(result));
      // A final execution receipt can precede recovery of its cached log tail.
      // Keep inspecting the original attempt; never resubmit to recover output.
      return (
        result.attempt.status === 'succeeded' &&
        result.attempt.cachedBytes === result.attempt.logBytes &&
        !result.attempt.syncError &&
        result
      );
    }, 'original guest task and complete cached logs after desktop restart');
    assert.equal(completed.attempt.exitCode, 0);
    const logs = await boundJSON(first, [
      'task',
      'logs',
      accepted.task.id,
      accepted.attempt.id,
    ]);
    assert.match(
      Buffer.from(logs.data, 'base64').toString(),
      /AXV_AGENT_GUEST_OK/,
    );
    assert.equal(
      (await boundJSON(first, ['read', 'codex-workflow/once.txt'])).content,
      'once\n',
    );
    assert.equal(
      (await boundJSON(first, taskArgs)).attempt.id,
      accepted.attempt.id,
    );
    assert.equal(
      (await api('/host/codex/projects')).projects.length,
      0,
      'The empty host workspace must not enter recent host projects.',
    );
    await end(first);
    const resumed = {
      requestId: `native-resume-${randomUUID()}`,
      expectedRunId: first.runId,
      vmProject: { binding },
    };
    active = (
      await api(`/host/codex/sessions/${first.id}/resume`, 'POST', resumed)
    ).session;
    active = await running(active);
    assert.equal(active.conversationId, first.conversationId);
    assert.equal(active.projectPath, first.projectPath);
    assert.notEqual(active.runId, first.runId);
    await verifySkill(active);
    const originalContext = await readFile(contextPath(first), 'utf8');
    results.push({
      check: 'guest-file-task-and-desktop-recovery',
      projectId: project.id,
      taskId: accepted.task.id,
      attemptId: accepted.attempt.id,
      runId: active.runId,
    });
    console.log(
      'VM project → real host Codex runner/controlled provider Skill → bound guest file/task, desktop restart and exact conversation continuation passed.',
    );

    return {
      close,
      async verifyReplacement(replacement) {
        assert.equal(replacement.projectId, binding.projectId);
        assert.notEqual(replacement.machineId, binding.machineId);
        for (const args of [
          ['read', filePath],
          [
            'write',
            filePath,
            '--expected-sha256',
            written.sha256,
            '--file',
            contentFile,
          ],
          ['mkdir', 'codex-workflow/old-command'],
          [
            ...taskArgs.slice(0, 3),
            `stale-${randomUUID()}`,
            ...taskArgs.slice(4),
          ],
        ]) {
          const rejected = await bound(first, args, true);
          assert.notEqual(
            rejected.code,
            0,
            'An old command reached the replacement VM.',
          );
          assert.match(
            rejected.stderr,
            /binding|boot|instance|environment|changed/i,
          );
        }
        // Accepted task receipts still belong to the old attempt even after the
        // VM has gone; recovering the response must never dispatch it again.
        assert.equal(
          (await boundJSON(first, taskArgs)).attempt.id,
          accepted.attempt.id,
        );
        await end(active);
        active = (
          await api(`/host/codex/sessions/${first.id}/resume`, 'POST', {
            requestId: `native-replacement-resume-${randomUUID()}`,
            expectedRunId: active.runId,
            vmProject: { binding: replacement },
          })
        ).session;
        active = await running(active);
        assert.equal(active.conversationId, first.conversationId);
        assert.deepEqual(active.vmProject.binding, replacement);
        assert.equal(
          (await boundJSON(active, ['read', filePath])).content,
          content,
        );
        await verifySkill(active);
        assert.equal(
          await readFile(contextPath(first), 'utf8'),
          originalContext,
        );
        assert.deepEqual(
          (await api(runPath(first))).session.vmProject,
          first.vmProject,
        );
        await end(first);
        assert.equal((await api(runPath(active))).session.status, 'running');
        await fixture.verifyIsolation();
        results.push({
          check: 'replacement-explicit-rebind-and-old-run-isolation',
          originalMachineId: binding.machineId,
          replacementMachineId: replacement.machineId,
          runId: active.runId,
        });
        await writeFile(
          join(directory, 'codex-workflow-result.json'),
          JSON.stringify(
            { provider: 'deterministic-fixture', results },
            null,
            2,
          ) + '\n',
          { mode: 0o600 },
        );
        console.log(
          'VM replacement: stale bound commands rejected, original receipt recovered without replay, explicit same-conversation rebind and old-run isolation passed.',
        );
        console.log(
          `Native Codex workflow results: ${JSON.stringify(results)}`,
        );
      },
    };
  } catch (error) {
    try {
      await close();
    } catch (cleanupError) {
      throw new AggregateError(
        [error, cleanupError],
        'VM Codex acceptance and cleanup failed.',
        { cause: cleanupError },
      );
    }
    throw error;
  }
}
