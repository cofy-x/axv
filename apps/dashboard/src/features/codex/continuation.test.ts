import { afterEach, describe, expect, it, vi } from 'vitest';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { APIError } from '../../shared/api/http';
import { desktopDatabase } from '../../shared/api/desktop';
import { codexAPI } from './api';
import type { CodexSessionRecord } from './api';
import { confirmEndSession } from './actions';
import { CodexResumeRequests, continueConversation } from './continuation';
import { vmProjectsAPI } from '../project-files/vm-api';

const previous: CodexSessionRecord = {
  id: 'session',
  runId: 'old-run',
  target: 'host',
  projectPath: '/home/user/project',
  title: 'Saved work',
  metadataRevision: 1,
  tmuxSession: 'axv-codex-old-run',
  tmuxSocket: '/tmp/tmux-test/default',
  attachCommand: 'tmux attach-session -t axv-codex-old-run',
  status: 'ended',
  createdAt: '2026-10-16T00:00:00Z',
  connections: 0,
  conversationId: 'official-thread',
  continuation: { state: 'available' },
};
const running: CodexSessionRecord = {
  ...previous,
  runId: 'new-run',
  tmuxSession: 'axv-codex-new-run',
  status: 'starting',
  continuation: { state: 'active', activeRunId: 'new-run' },
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe('explicit Codex conversation continuation', () => {
  const vmProject = {
    project: {
      id: 'vm-project',
      hostId: 'host',
      kind: 'vm' as const,
      name: 'VM repository',
      volumeId: 'volume',
      root: 'repo',
      rootIdentity: 'root',
      revision: 1,
      createdAt: previous.createdAt,
    },
    binding: {
      hostId: 'host',
      projectId: 'vm-project',
      projectRevision: 1,
      volumeId: 'volume',
      volumeRevision: 1,
      machineId: 'old-vm',
      bootId: 'old-boot',
      instanceId: 'old-instance',
    },
  };

  it('retains the original continuation receipt when replacement is detected before acknowledgement', async () => {
    const saved = new Map<string, string>();
    vi.stubGlobal('sessionStorage', {
      getItem: (key: string) => saved.get(key) ?? null,
      setItem: (key: string, value: string) => void saved.set(key, value),
      removeItem: (key: string) => void saved.delete(key),
    });
    const source = { ...previous, vmProject };
    vi.spyOn(codexAPI, 'resume').mockResolvedValue({ ...running, vmProject });
    vi.spyOn(desktopDatabase, 'assertUnchanged').mockImplementation(() => {
      throw new APIError('Changed', 409, 'database_changed');
    });
    const requests = new CodexResumeRequests('original-scope', 'original-db');
    await expect(requests.resume(source, vmProject.binding)).rejects.toThrow(
      'Changed',
    );
    expect(saved.size).toBe(1);
    expect(requests.hasPending(source.id)).toBe(true);
    expect(requests.pendingRequestId(source.id)).toBeTruthy();
  });

  it('reviews a fresh VM binding and confirms it explicitly while preserving the old run', async () => {
    const source = { ...previous, vmProject };
    const fresh = {
      ...vmProject,
      binding: {
        ...vmProject.binding,
        machineId: 'replacement-vm',
        bootId: 'new-boot',
      },
    };
    vi.spyOn(codexAPI, 'get').mockResolvedValue(source);
    const binding = vi.spyOn(vmProjectsAPI, 'binding').mockResolvedValue(fresh);
    const resume = vi
      .spyOn(codexAPI, 'resume')
      .mockResolvedValue({ ...running, vmProject: fresh });
    const dialog = vi
      .fn<DesktopAPI['dialog']>()
      .mockResolvedValue({ button: 'continue', value: '' });
    await continueConversation({ dialog }, source, new CodexResumeRequests());
    expect(binding).toHaveBeenCalledExactlyOnceWith('vm-project', 'host');
    expect(resume).toHaveBeenCalledExactlyOnceWith(
      'session',
      expect.any(String),
      'old-run',
      { binding: fresh.binding },
    );
    expect(source.vmProject.binding.machineId).toBe('old-vm');
  });

  it('recovers an unknown VM continuation across reload without selecting a newer environment', async () => {
    const saved = new Map<string, string>();
    vi.stubGlobal('sessionStorage', {
      getItem: (key: string) => saved.get(key) ?? null,
      setItem: (key: string, value: string) => void saved.set(key, value),
      removeItem: (key: string) => void saved.delete(key),
    });
    const source = { ...previous, vmProject };
    const resume = vi
      .spyOn(codexAPI, 'resume')
      .mockRejectedValueOnce(new TypeError('Response lost'))
      .mockResolvedValueOnce({ ...running, vmProject });
    const first = new CodexResumeRequests('test-scope', 'original-desktop-db');
    await expect(first.resume(source, vmProject.binding)).rejects.toThrow(
      'Response lost',
    );
    const reloaded = new CodexResumeRequests(
      'test-scope',
      'original-desktop-db',
    );
    reloaded.restore(source);
    vi.spyOn(codexAPI, 'get').mockResolvedValue({ ...running, vmProject });
    const binding = vi.spyOn(vmProjectsAPI, 'binding');
    const dialog = vi
      .fn<DesktopAPI['dialog']>()
      .mockResolvedValue({ button: 'continue', value: '' });
    await continueConversation({ dialog }, source, reloaded);
    expect(binding).not.toHaveBeenCalled();
    expect(resume.mock.calls[1]).toEqual(resume.mock.calls[0]);
    expect(resume.mock.calls[1][4]).toBe('original-desktop-db');
    expect(saved.size).toBe(0);
  });

  it('removes a cached VM continuation after another mounted view resolves it', async () => {
    const saved = new Map<string, string>();
    vi.stubGlobal('sessionStorage', {
      getItem: (key: string) => saved.get(key) ?? null,
      setItem: (key: string, value: string) => void saved.set(key, value),
      removeItem: (key: string) => void saved.delete(key),
    });
    const source = { ...previous, vmProject };
    const resumed = { ...running, vmProject };
    const resume = vi
      .spyOn(codexAPI, 'resume')
      .mockRejectedValueOnce(new TypeError('Response lost'))
      .mockResolvedValueOnce(resumed)
      .mockResolvedValueOnce({ ...resumed, runId: 'third-run' });
    const terminal = new CodexResumeRequests('shared-scope', 'original-db');
    const manager = new CodexResumeRequests('shared-scope', 'original-db');
    await expect(terminal.resume(source, vmProject.binding)).rejects.toThrow(
      'Response lost',
    );
    expect(manager.restore(source)).toBeDefined();
    await terminal.resume(source, vmProject.binding);
    expect(saved.size).toBe(0);
    expect(manager.restore(resumed)).toBeUndefined();
    expect(manager.hasPending(source.id)).toBe(false);

    await manager.resume(
      { ...resumed, status: 'ended', continuation: { state: 'available' } },
      { ...vmProject.binding, machineId: 'replacement-vm' },
    );
    expect(resume.mock.calls[1]).toEqual(resume.mock.calls[0]);
    expect(resume.mock.calls[2][1]).not.toBe(resume.mock.calls[0][1]);
    expect(resume.mock.calls[2][2]).toBe(resumed.runId);
    expect(resume.mock.calls[2][3]?.binding.machineId).toBe('replacement-vm');
  });

  it('retains this view’s in-flight VM request while another view resolves its shared receipt', async () => {
    const saved = new Map<string, string>();
    vi.stubGlobal('sessionStorage', {
      getItem: (key: string) => saved.get(key) ?? null,
      setItem: (key: string, value: string) => void saved.set(key, value),
      removeItem: (key: string) => void saved.delete(key),
    });
    const source = { ...previous, vmProject };
    const resumed = { ...running, vmProject };
    let finish: (result: CodexSessionRecord) => void = () => undefined;
    const response = new Promise<CodexSessionRecord>((resolve) => {
      finish = resolve;
    });
    const resume = vi
      .spyOn(codexAPI, 'resume')
      .mockReturnValueOnce(response)
      .mockResolvedValueOnce(resumed);
    const terminal = new CodexResumeRequests('shared-scope', 'original-db');
    const manager = new CodexResumeRequests('shared-scope', 'original-db');
    const active = terminal.resume(source, vmProject.binding);
    const original = manager.restore(source);
    await manager.resume(source, vmProject.binding);
    expect(resume.mock.calls[1]).toEqual(resume.mock.calls[0]);
    expect(saved.size).toBe(0);
    expect(terminal.restore(source)).toEqual(original);
    expect(terminal.resume(source, vmProject.binding)).toBe(active);
    finish(resumed);
    await active;
    expect(terminal.restore(resumed)).toBeUndefined();
    expect(terminal.hasPending(source.id)).toBe(false);
  });

  it('does not clear another view’s newer pending request when an older in-flight response arrives', async () => {
    const saved = new Map<string, string>();
    vi.stubGlobal('sessionStorage', {
      getItem: (key: string) => saved.get(key) ?? null,
      setItem: (key: string, value: string) => void saved.set(key, value),
      removeItem: (key: string) => void saved.delete(key),
    });
    const source = { ...previous, vmProject };
    const resumed = { ...running, vmProject };
    let finish: (result: CodexSessionRecord) => void = () => undefined;
    const response = new Promise<CodexSessionRecord>((resolve) => {
      finish = resolve;
    });
    const resume = vi
      .spyOn(codexAPI, 'resume')
      .mockReturnValueOnce(response)
      .mockResolvedValueOnce(resumed)
      .mockRejectedValueOnce(new TypeError('New response lost'));
    const terminal = new CodexResumeRequests('shared-scope', 'original-db');
    const manager = new CodexResumeRequests('shared-scope', 'original-db');
    const older = terminal.resume(source, vmProject.binding);
    await manager.resume(source, vmProject.binding);
    await expect(
      manager.resume(
        { ...resumed, status: 'ended', continuation: { state: 'available' } },
        { ...vmProject.binding, machineId: 'replacement-vm' },
      ),
    ).rejects.toThrow('New response lost');
    const newer = terminal.restore(resumed);
    expect(newer?.requestId).not.toBe(resume.mock.calls[0][1]);
    expect(newer?.expectedRunId).toBe(resumed.runId);
    finish(resumed);
    await older;
    expect(terminal.hasPending(source.id)).toBe(true);
    expect(terminal.restore(resumed)).toEqual(newer);
    expect(manager.restore(resumed)).toEqual(newer);
    expect(saved.size).toBe(1);
  });

  it.each(['before-action', 'during-confirmation'] as const)(
    'does not turn an already resolved recovery into a fresh continuation %s',
    async (completion) => {
      const saved = new Map<string, string>();
      vi.stubGlobal('sessionStorage', {
        getItem: (key: string) => saved.get(key) ?? null,
        setItem: (key: string, value: string) => void saved.set(key, value),
        removeItem: (key: string) => void saved.delete(key),
      });
      const source = { ...previous, vmProject };
      const resumed = { ...running, vmProject };
      const resume = vi
        .spyOn(codexAPI, 'resume')
        .mockRejectedValueOnce(new TypeError('Response lost'))
        .mockResolvedValueOnce(resumed);
      const terminal = new CodexResumeRequests('shared-scope', 'original-db');
      const manager = new CodexResumeRequests('shared-scope', 'original-db');
      await expect(terminal.resume(source, vmProject.binding)).rejects.toThrow(
        'Response lost',
      );
      manager.restore(source);
      vi.spyOn(codexAPI, 'get').mockResolvedValue({
        ...resumed,
        status: 'ended',
        continuation: { state: 'available' },
      });
      const binding = vi.spyOn(vmProjectsAPI, 'binding');
      const dialog = vi
        .fn<DesktopAPI['dialog']>()
        .mockImplementation(async () => {
          await terminal.resume(source, vmProject.binding);
          return { button: 'continue', value: '' };
        });
      if (completion === 'before-action')
        await terminal.resume(source, vmProject.binding);
      await expect(
        continueConversation({ dialog }, source, manager),
      ).rejects.toThrow('continuation request changed in another window');
      expect(binding).not.toHaveBeenCalled();
      expect(dialog).toHaveBeenCalledTimes(
        completion === 'before-action' ? 0 : 1,
      );
      expect(resume).toHaveBeenCalledTimes(2);
      expect(resume.mock.calls[1]).toEqual(resume.mock.calls[0]);
      expect(saved.size).toBe(0);
    },
  );

  it('does not accept a different pending request behind a rendered fresh continuation action', async () => {
    const saved = new Map<string, string>();
    vi.stubGlobal('sessionStorage', {
      getItem: (key: string) => saved.get(key) ?? null,
      setItem: (key: string, value: string) => void saved.set(key, value),
      removeItem: (key: string) => void saved.delete(key),
    });
    const source = { ...previous, vmProject };
    const resume = vi
      .spyOn(codexAPI, 'resume')
      .mockRejectedValueOnce(new TypeError('Response lost'));
    const terminal = new CodexResumeRequests('shared-scope', 'original-db');
    await expect(terminal.resume(source, vmProject.binding)).rejects.toThrow(
      'Response lost',
    );
    vi.spyOn(codexAPI, 'get').mockResolvedValue(source);
    const binding = vi.spyOn(vmProjectsAPI, 'binding');
    const dialog = vi.fn<DesktopAPI['dialog']>();
    const manager = new CodexResumeRequests('shared-scope', 'original-db');
    await expect(
      continueConversation({ dialog }, source, manager, null),
    ).rejects.toThrow('continuation request changed in another window');
    expect(binding).not.toHaveBeenCalled();
    expect(dialog).not.toHaveBeenCalled();
    expect(resume).toHaveBeenCalledTimes(1);
    expect(saved.size).toBe(1);
  });

  it.each([
    [503, 'server_stopping'],
    [409, 'history_identity_conflict'],
  ])(
    'retains an existing VM continuation after HTTP %s / %s and reload',
    async (status, code) => {
      const saved = new Map<string, string>();
      vi.stubGlobal('sessionStorage', {
        getItem: (key: string) => saved.get(key) ?? null,
        setItem: (key: string, value: string) => void saved.set(key, value),
        removeItem: (key: string) => void saved.delete(key),
      });
      const source = { ...previous, vmProject };
      const resume = vi
        .spyOn(codexAPI, 'resume')
        .mockRejectedValueOnce(
          new APIError(
            'The reserved run could not be reconciled',
            status,
            code,
          ),
        )
        .mockResolvedValueOnce({ ...running, vmProject });
      const first = new CodexResumeRequests('original-scope', 'original-db');
      await expect(first.resume(source, vmProject.binding)).rejects.toThrow(
        'could not be reconciled',
      );
      expect(first.hasPending(source.id)).toBe(true);
      expect(saved.size).toBe(1);
      const reloaded = new CodexResumeRequests('original-scope', 'original-db');
      await reloaded.resume(
        { ...source, runId: 'newer-observation' },
        { ...vmProject.binding, machineId: 'replacement-vm' },
      );
      expect(resume.mock.calls[1]).toEqual(resume.mock.calls[0]);
      expect(resume.mock.calls[1][2]).toBe('old-run');
      expect(resume.mock.calls[1][3]).toEqual({ binding: vmProject.binding });
      expect(saved.size).toBe(0);
    },
  );
  it.each(['ended', 'failed', 'lost'] as const)(
    'opens the exact %s run when resolving an earlier unknown continuation',
    async (status) => {
      const source = { ...previous, vmProject };
      const recovered = {
        ...running,
        vmProject,
        status,
        continuation: { state: 'available' as const },
      };
      const resume = vi
        .spyOn(codexAPI, 'resume')
        .mockRejectedValueOnce(new TypeError('Response lost'))
        .mockResolvedValueOnce(recovered);
      const requests = new CodexResumeRequests();
      await expect(requests.resume(source, vmProject.binding)).rejects.toThrow(
        'Response lost',
      );
      vi.spyOn(codexAPI, 'get').mockResolvedValue(recovered);
      const binding = vi.spyOn(vmProjectsAPI, 'binding');
      const dialog = vi
        .fn<DesktopAPI['dialog']>()
        .mockResolvedValue({ button: 'continue', value: '' });
      expect(await continueConversation({ dialog }, source, requests)).toBe(
        recovered,
      );
      expect(resume.mock.calls[1]).toEqual(resume.mock.calls[0]);
      expect(binding).not.toHaveBeenCalled();
      expect(requests.hasPending(source.id)).toBe(false);
    },
  );

  it('keeps a fresh failed continuation visible as a startup error', async () => {
    vi.spyOn(codexAPI, 'get').mockResolvedValue(previous);
    vi.spyOn(codexAPI, 'resume').mockResolvedValue({
      ...running,
      status: 'failed',
      reason: 'The provider could not start.',
      continuation: { state: 'available' },
    });
    const dialog = vi
      .fn<DesktopAPI['dialog']>()
      .mockResolvedValue({ button: 'continue', value: '' });
    await expect(
      continueConversation({ dialog }, previous, new CodexResumeRequests()),
    ).rejects.toThrow('The provider could not start.');
  });

  it('checks the latest run for confirmation but creates nothing when cancelled', async () => {
    vi.spyOn(codexAPI, 'get').mockResolvedValue(previous);
    const resume = vi.spyOn(codexAPI, 'resume');
    const dialog = vi.fn<DesktopAPI['dialog']>().mockResolvedValue(null);
    expect(
      await continueConversation(
        { dialog },
        previous,
        new CodexResumeRequests(),
      ),
    ).toBeNull();
    expect(dialog).toHaveBeenCalledOnce();
    expect(resume).not.toHaveBeenCalled();
  });

  it('opens an existing active run without confirming or creating another process', async () => {
    vi.spyOn(codexAPI, 'get').mockResolvedValue(running);
    const resume = vi.spyOn(codexAPI, 'resume');
    const dialog = vi.fn<DesktopAPI['dialog']>();
    expect(
      await continueConversation(
        { dialog },
        previous,
        new CodexResumeRequests(),
      ),
    ).toBe(running);
    expect(dialog).not.toHaveBeenCalled();
    expect(resume).not.toHaveBeenCalled();
  });

  it('uses the explicitly checked latest ended run and preserves the old window record', async () => {
    const latest = {
      ...previous,
      runId: 'later-ended-run',
      archivedAt: '2026-10-16T01:00:00Z',
    };
    vi.spyOn(codexAPI, 'get').mockResolvedValue(latest);
    const resume = vi.spyOn(codexAPI, 'resume').mockResolvedValue(running);
    const dialog = vi
      .fn<DesktopAPI['dialog']>()
      .mockResolvedValue({ button: 'continue', value: '' });
    expect(
      await continueConversation(
        { dialog },
        previous,
        new CodexResumeRequests(),
      ),
    ).toBe(running);
    expect(resume).toHaveBeenCalledExactlyOnceWith(
      'session',
      expect.any(String),
      'later-ended-run',
    );
    expect(previous.runId).toBe('old-run');
    expect(previous.status).toBe('ended');
  });

  it('retains an unknown request outcome and its expected run across an explicit retry', async () => {
    const resume = vi
      .spyOn(codexAPI, 'resume')
      .mockRejectedValueOnce(new TypeError('Network connection lost'))
      .mockResolvedValueOnce(running);
    const requests = new CodexResumeRequests();
    await expect(requests.resume(previous)).rejects.toThrow(
      'Network connection lost',
    );
    expect(requests.hasPending(previous.id)).toBe(true);
    expect(
      await requests.resume({ ...previous, runId: 'a-newer-observation' }),
    ).toBe(running);
    expect(resume.mock.calls[1]).toEqual(resume.mock.calls[0]);
    expect(requests.hasPending(previous.id)).toBe(false);
  });

  it('allows a new explicit attempt after a definite pre-launch refusal or failed run', async () => {
    const failed = {
      ...running,
      status: 'failed' as const,
      continuation: { state: 'available' as const },
    };
    const resume = vi
      .spyOn(codexAPI, 'resume')
      .mockRejectedValueOnce(
        new APIError('All slots are in use.', 503, 'codex_limit'),
      )
      .mockResolvedValueOnce(failed)
      .mockResolvedValueOnce({ ...running, runId: 'retry-run' });
    const requests = new CodexResumeRequests();
    await expect(requests.resume(previous)).rejects.toThrow('All slots');
    expect(requests.hasPending(previous.id)).toBe(false);
    expect(await requests.resume(previous)).toBe(failed);
    expect(requests.hasPending(previous.id)).toBe(false);
    await requests.resume(failed);
    expect(new Set(resume.mock.calls.map((call) => call[1])).size).toBe(3);
    expect(resume.mock.calls[2][2]).toBe(failed.runId);
  });

  it('reports unavailable history without silently starting a new conversation', async () => {
    vi.spyOn(codexAPI, 'get').mockResolvedValue({
      ...previous,
      conversationId: undefined,
      continuation: {
        state: 'unavailable',
        reason: 'Saved history was not found.',
      },
    });
    const resume = vi.spyOn(codexAPI, 'resume');
    const create = vi.spyOn(codexAPI, 'create');
    const dialog = vi.fn<DesktopAPI['dialog']>();
    await expect(
      continueConversation({ dialog }, previous, new CodexResumeRequests()),
    ).rejects.toThrow('Saved history was not found.');
    expect(dialog).not.toHaveBeenCalled();
    expect(resume).not.toHaveBeenCalled();
    expect(create).not.toHaveBeenCalled();
  });

  it('keeps End Session bound to the old run even after a new run exists', async () => {
    const end = vi.spyOn(codexAPI, 'end').mockResolvedValue(previous);
    const get = vi.spyOn(codexAPI, 'get');
    const dialog = vi
      .fn<DesktopAPI['dialog']>()
      .mockResolvedValue({ button: 'end', value: '' });
    await confirmEndSession(
      { dialog },
      { ...previous, continuation: running.continuation },
    );
    expect(end).toHaveBeenCalledExactlyOnceWith('session', 'old-run');
    expect(get).not.toHaveBeenCalled();
  });
});
