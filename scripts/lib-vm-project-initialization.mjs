import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

// Exercise the same initialization interface as the desktop. The parent root,
// files and resulting Git repository stay on the retained guest volume.
export async function verifyProjectInitialization({
  request,
  project,
  binding,
  directory,
  token,
  recordProject,
}) {
  assert.equal(
    typeof recordProject,
    'function',
    'native acceptance must record initialized roots before registration for owned cleanup',
  );
  const results = [];
  const health = await request(`/machines/${binding.machineId}/guest/health`);
  assert.ok(health.capabilities.includes('project-initialization'));

  async function initialize(
    label,
    newDirectory,
    repository = '',
    expectedStatus = 'succeeded',
  ) {
    const payload = {
      requestId: `initialize-${label}-${randomUUID()}`,
      binding,
      directory: newDirectory,
      repository,
      name: `${label} project`,
    };
    const accepted = await request(
      `/projects/${project.id}/initialize`,
      'POST',
      payload,
    );
    const repeated = await request(
      `/projects/${project.id}/initialize`,
      'POST',
      payload,
    );
    assert.equal(repeated.task.id, accepted.task.id);
    assert.equal(
      repeated.attempt.id,
      accepted.attempt.id,
      'identical initialization started another attempt',
    );
    assert.deepEqual(accepted.attempt.binding, binding);
    assert.deepEqual(accepted.task.spec.argv, []);
    assert.deepEqual(accepted.task.spec.initialization, {
      directory: payload.directory,
      ...(repository ? { repository } : {}),
      name: payload.name,
    });
    await assert.rejects(
      () =>
        request(`/projects/${project.id}/tasks`, 'POST', {
          requestId: `ordinary-forgery-${randomUUID()}`,
          binding,
          spec: accepted.task.spec,
        }),
      'ordinary task admission accepted typed initialization intent',
    );
    let receipt;
    const deadline = Date.now() + 180_000;
    while (Date.now() < deadline) {
      receipt = await request(
        `/projects/${project.id}/task-requests/${encodeURIComponent(payload.requestId)}`,
      );
      assert.equal(receipt.attempt.id, accepted.attempt.id);
      if (
        ['succeeded', 'failed', 'cancelled', 'interrupted'].includes(
          receipt.attempt.status,
        ) &&
        receipt.attempt.cachedBytes === receipt.attempt.logBytes
      )
        break;
      await delay(250);
    }
    const chunks = [];
    let offset = 0;
    while (offset < receipt.attempt.cachedBytes) {
      const chunk = await request(
        `/projects/${project.id}/tasks/${accepted.task.id}/attempts/${accepted.attempt.id}/logs?offset=${offset}&limit=65536`,
      );
      assert.equal(chunk.offset, offset);
      assert.ok(chunk.nextOffset > offset);
      chunks.push(Buffer.from(chunk.data, 'base64'));
      offset = chunk.nextOffset;
    }
    const log = Buffer.concat(chunks).toString('utf8');
    await writeFile(join(directory, `initialization-${label}.log`), log, {
      mode: 0o600,
    });
    assert.equal(
      receipt.attempt.status,
      expectedStatus,
      `${label}: ${JSON.stringify(receipt.attempt)}\n${log}`,
    );
    assert.equal(receipt.attempt.cachedBytes, receipt.attempt.logBytes);
    assert.ok(receipt.attempt.guestFinal);
    const finish = () =>
      request(
        `/projects/${project.id}/initializations/${encodeURIComponent(payload.requestId)}/finish`,
        'POST',
        { binding },
      );
    if (expectedStatus !== 'succeeded') {
      await assert.rejects(finish, 'a failed initialization was registered');
      results.push({
        label,
        taskId: accepted.task.id,
        attemptId: accepted.attempt.id,
        status: receipt.attempt.status,
      });
      return null;
    }
    const newRoot = [project.root, newDirectory].filter(Boolean).join('/');
    assert.equal(receipt.attempt.createdRoot?.root, newRoot);
    assert.ok(receipt.attempt.createdRoot.rootIdentity);
    await recordProject({ volumeId: binding.volumeId, root: newRoot });
    const completed = await finish();
    await recordProject(completed.project);
    const recovered = await finish();
    assert.equal(
      recovered.project.id,
      completed.project.id,
      'finishing the original receipt duplicated a project',
    );
    assert.equal(completed.project.root, newRoot);
    assert.equal(
      completed.project.rootIdentity,
      receipt.attempt.createdRoot.rootIdentity,
    );
    assert.equal(completed.project.volumeId, binding.volumeId);
    const opened = await request(`/projects/${completed.project.id}/binding`);
    assert.deepEqual(opened.binding, {
      ...binding,
      projectId: completed.project.id,
      projectRevision: completed.project.revision,
    });
    results.push({
      label,
      projectId: completed.project.id,
      taskId: accepted.task.id,
      attemptId: accepted.attempt.id,
      status: receipt.attempt.status,
    });
    return opened;
  }

  const emptyDirectory = `init-empty-${randomUUID()}`;
  const empty = await initialize('empty', emptyDirectory);
  const files = await request(
    `/projects/${empty.project.id}/files/list`,
    'POST',
    { binding: empty.binding, path: '' },
  );
  assert.deepEqual(files.entries, []);
  const readme = await request(
    `/projects/${empty.project.id}/files/write`,
    'POST',
    {
      binding: empty.binding,
      path: 'README.md',
      content: `AXV initialized project: ${token}\n`,
      expectedSha256: '',
    },
  );
  assert.ok(readme.sha256);
  await initialize('existing-directory', emptyDirectory, '', 'failed');
  const preserved = await request(
    `/projects/${empty.project.id}/files/read`,
    'POST',
    { binding: empty.binding, path: 'README.md' },
  );
  assert.equal(preserved.content, `AXV initialized project: ${token}\n`);

  const cloned = await initialize(
    'clone',
    `init-clone-${randomUUID()}`,
    'https://github.com/pallets/itsdangerous.git',
  );
  const repository = await request(
    `/projects/${cloned.project.id}/files/list`,
    'POST',
    { binding: cloned.binding, path: '' },
  );
  assert.ok(
    repository.entries.some(
      (entry) => entry.name === '.git' && entry.kind === 'folder',
    ),
  );
  const cloneReadme = await request(
    `/projects/${cloned.project.id}/files/read`,
    'POST',
    { binding: cloned.binding, path: 'README.md' },
  );
  assert.ok(cloneReadme.content.length > 0);
  const head = await request(
    `/projects/${cloned.project.id}/files/read`,
    'POST',
    { binding: cloned.binding, path: '.git/HEAD' },
  );
  let commit = head.content.trim();
  if (commit.startsWith('ref: ')) {
    const ref = await request(
      `/projects/${cloned.project.id}/files/read`,
      'POST',
      { binding: cloned.binding, path: `.git/${commit.slice(5)}` },
    );
    commit = ref.content.trim();
  }
  assert.match(commit, /^[a-f0-9]{40,64}$/u);
  results.find((result) => result.label === 'clone').commit = commit;
  await writeFile(
    join(directory, 'project-initialization-result.json'),
    JSON.stringify({ token, results }, null, 2),
    { mode: 0o600 },
  );
  return results;
}
