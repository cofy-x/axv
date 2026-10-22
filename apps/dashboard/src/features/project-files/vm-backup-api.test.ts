import { expect, test } from 'vitest';
import { readBackupRecovery } from './vm-backup-api';

const receipt = {
  version: 1,
  action: {
    kind: 'backup',
    hostId: 'host-a',
    projectId: 'project-a',
    values: {
      requestId: 'retained-request',
      projectRevision: 1,
      volumeRevision: 2,
      machineId: 'original-vm',
      machineRevision: 3,
      name: 'Snapshot',
    },
  },
  operationId: 'original-operation',
};

test('recovery preserves the accepted operation and exact reviewed payload', () => {
  expect(
    readBackupRecovery(JSON.stringify(receipt), 'host-a', 'project-a'),
  ).toEqual(receipt);
  expect(() =>
    readBackupRecovery(JSON.stringify(receipt), 'host-a', 'other-project'),
  ).toThrow();
  expect(() =>
    readBackupRecovery(JSON.stringify(receipt), 'other-host', 'project-a'),
  ).toThrow();
});

test('recovery rejects free routes and a bind request for another source project', () => {
  expect(() =>
    readBackupRecovery(
      JSON.stringify({
        ...receipt,
        action: { ...receipt.action, path: 'projects/other-project/backups' },
      }),
      'host-a',
      'project-a',
    ),
  ).toThrow();
  const action = {
    kind: 'bind',
    hostId: 'host-a',
    projectId: 'project-a',
    backupId: 'original-backup',
    values: {
      requestId: 'retained-request',
      sourceProjectId: 'other-project',
      volumeId: 'restored-volume',
      volumeRevision: 2,
      machineId: 'restored-vm',
      name: 'Recovered',
    },
  };
  expect(() =>
    readBackupRecovery(
      JSON.stringify({ version: 1, action }),
      'host-a',
      'project-a',
    ),
  ).toThrow();
});
