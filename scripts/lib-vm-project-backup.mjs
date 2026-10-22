import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';

// Invoke after the source VM has explicitly stopped and task reconciliation has
// finished. The caller records every receipt before its own native cleanup.
export async function verifyProjectBackupWorkflow({
  request,
  operation,
  create,
  action,
  ready,
  exec,
  restartHost,
  source,
  project,
  originalBinding,
  recordMutation,
  recordVolume,
  recordProject,
}) {
  const machine = await request(`/machines/${source.id}`);
  assert.equal(machine.status, 'stopped');
  const { volumes } = await request('/volumes');
  const volume = volumes.find((v) => v.id === project.volumeId);
  assert.equal(originalBinding.projectId, project.id);
  assert.equal(originalBinding.volumeId, project.volumeId);

  async function copy(path, values, kind) {
    const mutation = { requestId: `backup-native-${randomUUID()}`, kind };
    await recordMutation(mutation);
    const body = { requestId: mutation.requestId, ...values };
    const receipt = await request(path, 'POST', body);
    mutation.id = receipt.operation.id;
    mutation.volumeId = receipt.volume.id;
    await recordVolume(receipt.volume);
    await recordMutation(mutation);
    // Recovery must retain the accepted target even if the host exits while
    // copying or after the provider committed but before host publication.
    await restartHost();
    const replay = await request(path, 'POST', body);
    assert.equal(replay.operation.id, receipt.operation.id);
    assert.equal(replay.volume.id, receipt.volume.id);
    await operation(receipt.operation.id);
    return (await request('/volumes')).volumes.find(
      (v) => v.id === receipt.volume.id,
    );
  }

  const backup = await copy(
    `/projects/${project.id}/backups`,
    {
      projectRevision: project.revision,
      volumeRevision: volume.revision,
      machineId: machine.id,
      machineRevision: machine.revision,
      name: 'Native offline project backup',
    },
    'project-backup',
  );
  assert.equal(backup.status, 'ready');
  assert.equal(backup.backup.sourceVolumeId, volume.id);
  assert.ok(backup.backup.projects.some((p) => p.id === project.id));
  await assert.rejects(
    request(`/volumes/${backup.id}/actions`, 'POST', {
      requestId: `backup-mutate-${randomUUID()}`,
      revision: backup.revision,
      action: 'retain',
      retain: false,
    }),
    (error) => [409, 503].includes(error.statusCode),
  );

  const restored = await copy(
    `/project-backups/${backup.id}/restore`,
    {
      revision: backup.revision,
      name: 'Native independent restored project',
    },
    'project-restore',
  );
  assert.notEqual(restored.id, volume.id);
  assert.notEqual(restored.id, backup.id);
  assert.equal(restored.restoredFrom, backup.id);
  const recovered = await create('AXV native backup recovery', {
    retain: true,
    volumeId: restored.id,
  });
  await ready(recovered.id);
  const restoredEnvironment = await request(`/machines/${recovered.id}`);
  assert.ok(
    restoredEnvironment.guestCapabilities.includes('project-restore'),
    'restored binding requires the independent project-restore capability',
  );
  const current = (await request('/volumes')).volumes.find(
    (v) => v.id === restored.id,
  );
  const bind = {
    requestId: `bind-native-${randomUUID()}`,
    sourceProjectId: project.id,
    volumeId: restored.id,
    volumeRevision: current.revision,
    machineId: recovered.id,
    name: 'Native restored project',
  };
  await recordProject({ volumeId: restored.id, root: project.root });
  const { project: next } = await request(
    `/project-backups/${backup.id}/bind`,
    'POST',
    bind,
  );
  await recordProject(next);
  assert.notEqual(next.id, project.id);
  assert.equal(next.volumeId, restored.id);
  assert.equal(next.root, project.root);
  assert.equal(next.rootIdentity, project.rootIdentity);
  const replay = await request(
    `/project-backups/${backup.id}/bind`,
    'POST',
    bind,
  );
  assert.equal(replay.project.id, next.id);
  const bound = await request(`/projects/${next.id}/binding`);
  assert.equal(bound.binding.machineId, recovered.id);
  assert.notEqual(bound.binding.volumeId, originalBinding.volumeId);
  assert.equal(
    (await request(`/projects/${project.id}`)).project.volumeId,
    volume.id,
  );
  await assert.rejects(
    request(`/projects/${next.id}/files/list`, 'POST', {
      binding: originalBinding,
      path: '',
    }),
    (error) => error.statusCode === 409,
  );
  const git = await exec(recovered.id, [
    'git',
    '-C',
    project.root,
    'status',
    '--porcelain=v1',
  ]);
  const worktrees = await exec(recovered.id, [
    'git',
    '-C',
    project.root,
    'worktree',
    'list',
    '--porcelain',
  ]);
  assert.ok(worktrees.includes('worktree /projects/'));
  await action(recovered, 'stop');
  return { backup, restored, project: next, git, worktrees };
}
