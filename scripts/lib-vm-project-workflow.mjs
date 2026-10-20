import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

// Only AXV's project interfaces touch this repository. Authoritative source,
// installed dependencies and build outputs all remain on the guest volume.
export async function verifyProjectWorkflow({
  cliJSON,
  request,
  project,
  bindingPath,
  directory,
  token,
}) {
  const binding = JSON.parse(await readFile(bindingPath, 'utf8')).binding;
  const results = [];

  async function task(label, command, cwd = '') {
    const accepted = await cliJSON([
      'project',
      'task',
      'submit',
      project.id,
      '--binding-file',
      bindingPath,
      '--request-id',
      `workflow-${label}-${randomUUID()}`,
      '--directory',
      cwd,
      '--memory',
      '256',
      '--timeout',
      '8m',
      '--max-log-bytes',
      String(2 << 20),
      '--',
      '/bin/sh',
      '-ec',
      command,
    ]);
    const taskID = accepted.task.id;
    const attemptID = accepted.attempt.id;
    const deadline = Date.now() + 510_000;
    let inspected;
    while (Date.now() < deadline) {
      inspected = await request(`/projects/${project.id}/tasks/${taskID}`);
      assert.equal(inspected.attempt.id, attemptID);
      if (
        ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(
          inspected.attempt.status,
        ) &&
        inspected.attempt.cachedBytes === inspected.attempt.logBytes
      )
        break;
      await delay(300);
    }
    assert.equal(
      inspected.attempt.cachedBytes,
      inspected.attempt.logBytes,
      `${label}: the original task log tail was not fully recovered: ${JSON.stringify(inspected.attempt)}`,
    );
    const chunks = [];
    let offset = 0;
    while (offset < inspected.attempt.cachedBytes) {
      const chunk = await request(
        `/projects/${project.id}/tasks/${taskID}/attempts/${attemptID}/logs?offset=${offset}&limit=65536`,
      );
      assert.equal(chunk.taskId, taskID);
      assert.equal(chunk.attemptId, attemptID);
      const data = Buffer.from(chunk.data, 'base64');
      assert.equal(chunk.offset, offset);
      assert.ok(chunk.nextOffset > offset);
      assert.equal(chunk.nextOffset, offset + data.length);
      assert.ok(chunk.nextOffset <= inspected.attempt.cachedBytes);
      chunks.push(data);
      offset = chunk.nextOffset;
    }
    const log = Buffer.concat(chunks).toString('utf8');
    await writeFile(join(directory, `workflow-${label}.log`), log, {
      mode: 0o600,
    });
    assert.equal(
      inspected.attempt.status,
      'succeeded',
      `${label}: ${JSON.stringify(inspected.attempt)}\n${log}`,
    );
    assert.equal(inspected.attempt.exitCode, 0);
    results.push({
      label,
      taskID,
      attemptID,
      status: inspected.attempt.status,
    });
    return log;
  }

  await task(
    'clone',
    [
      'git clone --depth 1 --branch 2.2.0 https://github.com/pallets/itsdangerous.git upstream',
      'test "$(git -C upstream rev-parse HEAD)" = 096c8d42545d3b68ea21a4f890fb2b2d8979c0bd',
    ].join('\n'),
  );

  const readme = await cliJSON([
    'project',
    'read',
    project.id,
    'upstream/README.md',
    '--binding-file',
    bindingPath,
  ]);
  const readmeContent = join(directory, 'workflow-readme.txt');
  await writeFile(
    readmeContent,
    `${readme.content}\nAXV workspace acceptance: ${token}\n`,
  );
  await cliJSON([
    'project',
    'write',
    project.id,
    'upstream/README.md',
    '--binding-file',
    bindingPath,
    '--expected-sha256',
    readme.sha256,
    '--file',
    readmeContent,
  ]);

  const module = [
    '"""A small original extension used by AXV workspace acceptance."""',
    'from itsdangerous import URLSafeSerializer',
    '',
    'def workspace_roundtrip(value: str) -> str:',
    '    serializer = URLSafeSerializer("axv-acceptance-key")',
    '    return serializer.loads(serializer.dumps(value))',
    '',
  ].join('\n');
  const test = [
    'from itsdangerous.axv_workspace import workspace_roundtrip',
    '',
    'def test_axv_workspace_roundtrip():',
    `    assert workspace_roundtrip(${JSON.stringify(token)}) == ${JSON.stringify(token)}`,
    '    assert workspace_roundtrip("项目工作区") == "项目工作区"',
    '',
  ].join('\n');
  for (const [path, content] of [
    ['upstream/src/itsdangerous/axv_workspace.py', module],
    ['upstream/tests/test_axv_workspace.py', test],
  ]) {
    const source = join(
      directory,
      path.endsWith('test_axv_workspace.py')
        ? 'workflow-test.py'
        : 'workflow-module.py',
    );
    await writeFile(source, content);
    await cliJSON([
      'project',
      'write',
      project.id,
      path,
      '--binding-file',
      bindingPath,
      '--create',
      '--file',
      source,
    ]);
    const actual = await request(`/projects/${project.id}/files/read`, 'POST', {
      binding,
      path,
    });
    assert.equal(actual.content, content);
  }
  await task(
    'install',
    'python3 -m venv .venv\n.venv/bin/python -m pip install --disable-pip-version-check -e . -r requirements/tests.txt build',
    'upstream',
  );
  await task(
    'build',
    '.venv/bin/python -m build --wheel\ntest -f dist/itsdangerous-2.2.0-py3-none-any.whl',
    'upstream',
  );
  const tests = await task('test', '.venv/bin/python -m pytest -q', 'upstream');
  assert.match(tests, /\d+ passed/);
  const git = await task(
    'git-state',
    'git status --porcelain=v1\ntest -f .venv/bin/python\nls dist/*.whl',
    'upstream',
  );
  assert.match(git, / M README\.md/);
  assert.match(git, /\?\? src\/itsdangerous\/axv_workspace\.py/);
  assert.match(git, /\?\? tests\/test_axv_workspace\.py/);
  await writeFile(
    join(directory, 'workflow-result.json'),
    JSON.stringify(
      {
        repository: 'https://github.com/pallets/itsdangerous.git',
        commit: '096c8d42545d3b68ea21a4f890fb2b2d8979c0bd',
        projectID: project.id,
        volumeID: project.volumeId,
        binding,
        results,
      },
      null,
      2,
    ) + '\n',
    { mode: 0o600 },
  );
  console.log(
    'Real upstream repository: AXV project read/write, persistent dependency installation, wheel build, complete pytest suite and Git state passed inside the VM.',
  );
}
