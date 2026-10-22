import { describe, expect, it } from 'vitest';
import {
  repositoryURLAllowed,
  validPendingInitialization,
} from './vm-init-api';

describe('VM project repository URLs', () => {
  it('accepts guest Git HTTPS and SSH identities', () => {
    for (const url of [
      '',
      'https://github.com/cofy-x/axv.git',
      'ssh://git@github.com/cofy-x/axv.git',
      'git@github.com:cofy-x/axv.git',
    ])
      expect(repositoryURLAllowed(url)).toBe(true);
  });

  it('rejects credentials and executable/local Git transports before browser recovery is saved', () => {
    for (const url of [
      'https://token@host/repo',
      'https://@host/repo',
      'https://host',
      'https://user:secret@host/repo',
      'ssh://git:secret@host/repo',
      'https://host/repo?token=secret',
      'https://host/repo?',
      'https://host/repo#secret',
      'file:///projects/repo',
      'ext::sh command',
      '--upload-pack=command',
      'https://host/repo\ncommand',
    ])
      expect(repositoryURLAllowed(url)).toBe(false);
  });
});

// Recovery may resume only the captured guest environment and typed intent.
describe('VM initialization recovery identity', () => {
  const binding = {
    hostId: 'host-original',
    projectId: 'volume-workspace',
    projectRevision: 1,
    volumeId: 'volume-original',
    volumeRevision: 3,
    machineId: 'vm-original',
    bootId: 'boot-original',
    instanceId: 'guest-original',
  };
  const pending = {
    version: 1 as const,
    hostId: binding.hostId,
    volumeId: binding.volumeId,
    volumeRevision: binding.volumeRevision,
    machineId: binding.machineId,
    bootId: binding.bootId,
    instanceId: binding.instanceId,
    requestId: 'request-original',
    rootRequestId: 'workspace-original',
    directory: 'new-project',
    repository: '',
    name: '',
    submission: {
      requestId: 'request-original',
      binding,
      directory: 'new-project',
      repository: '',
      name: '',
    },
  };
  it('rejects changed environment and payloads rather than reconstructing submissions', () => {
    expect(validPendingInitialization(pending)).toBe(true);
    for (const submission of [
      { ...pending.submission, binding: { ...binding, bootId: 'new-boot' } },
      { ...pending.submission, directory: 'new-directory' },
      { ...pending.submission, requestId: 'another-request' },
      { ...pending.submission, binding: { ...binding, projectRevision: 0 } },
    ])
      expect(validPendingInitialization({ ...pending, submission })).toBe(
        false,
      );
    expect(
      validPendingInitialization({
        ...pending,
        repository: 'https://secret@host/repo',
      }),
    ).toBe(false);
    expect(validPendingInitialization({ ...pending, name: ' padded ' })).toBe(
      false,
    );
  });
});
