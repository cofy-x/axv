import { afterEach, describe, expect, it, vi } from 'vitest';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { APIError } from '../../shared/api/http';
import { codexAPI } from './api';
import type { CodexSessionRecord } from './api';
import { confirmEndSession } from './actions';
import { CodexResumeRequests, continueConversation } from './continuation';

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

afterEach(() => vi.restoreAllMocks());

describe('explicit Codex conversation continuation', () => {
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
