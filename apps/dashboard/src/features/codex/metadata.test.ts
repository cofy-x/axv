import { afterEach, describe, expect, it, vi } from 'vitest';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { APIError } from '../../shared/api/http';
import { codexAPI, sessionTitle, sessionWindowTitle } from './api';
import type { CodexSessionRecord } from './api';
import { renameSession, setSessionArchived } from './actions';
import { mergeSessionMetadata, subscribeSessionMetadata } from './metadata';

const session: CodexSessionRecord = {
  id: 'session',
  runId: 'run',
  target: 'host',
  projectPath: '/home/user/project',
  title: '',
  metadataRevision: 1,
  tmuxSession: 'axv-codex-instance-session',
  tmuxSocket: '/tmp/tmux-test/default',
  attachCommand: 'tmux attach-session -t axv-codex-instance-session',
  status: 'running',
  createdAt: '2026-10-16T00:00:00Z',
  connections: 1,
  continuation: { state: 'active', activeRunId: 'run' },
};

afterEach(() => vi.restoreAllMocks());

describe('Codex session metadata', () => {
  it('distinguishes final runs in the Dock while retaining the active conversation title', () => {
    const named = { ...session, title: 'Saved work', runId: 'abcdefgh-1234' };
    expect(sessionWindowTitle(named)).toBe('Codex · Saved work');
    for (const status of ['ended', 'lost', 'failed'] as const) {
      const label = status[0].toUpperCase() + status.slice(1);
      expect(sessionWindowTitle({ ...named, status })).toBe(
        `Codex · Saved work · ${label} (abcdefgh)`,
      );
    }
    expect(
      sessionWindowTitle({ ...named, status: 'ended', runId: 'ijklmnop-5678' }),
    ).toBe('Codex · Saved work · Ended (ijklmnop)');
  });

  it('applies newer metadata without replacing process state or accepting stale responses', () => {
    const newer = {
      ...session,
      title: 'Fix build',
      archivedAt: '2026-10-16T01:00:00Z',
      metadataRevision: 3,
      status: 'starting' as const,
      connections: 0,
    };
    const merged = mergeSessionMetadata(session, newer);
    expect(merged).toMatchObject({
      title: 'Fix build',
      archivedAt: newer.archivedAt,
      metadataRevision: 3,
      status: 'running',
      connections: 1,
      tmuxSession: session.tmuxSession,
    });
    expect(mergeSessionMetadata(merged, session)).toBe(merged);
    expect(mergeSessionMetadata(merged, { ...newer, id: 'other' })).toBe(
      merged,
    );
    expect(sessionTitle(merged)).toBe('Fix build');
    expect(sessionTitle(session)).toBe('project');
  });

  it('keeps a failed rename draft and waits for an explicit second submission after a conflict', async () => {
    const current = {
      ...session,
      title: 'Changed elsewhere',
      metadataRevision: 2,
    };
    const updated = { ...current, title: 'My draft', metadataRevision: 3 };
    const update = vi
      .spyOn(codexAPI, 'update')
      .mockRejectedValueOnce(new APIError('Conflict', 409, 'metadata_conflict'))
      .mockResolvedValueOnce(updated);
    vi.spyOn(codexAPI, 'get').mockResolvedValueOnce(current);
    const dialog = vi
      .fn<DesktopAPI['dialog']>()
      .mockResolvedValueOnce({ button: 'rename', value: 'My draft' })
      .mockImplementationOnce(async (options) => {
        expect(update).toHaveBeenCalledTimes(1);
        expect(options.input?.value).toBe('My draft');
        expect(options.message).toContain('Changed elsewhere');
        return { button: 'rename', value: 'My draft' };
      });
    expect(await renameSession({ dialog }, session)).toBe(updated);
    expect(update.mock.calls).toEqual([
      ['session', { revision: 1, title: 'My draft' }],
      ['session', { revision: 2, title: 'My draft' }],
    ]);
  });

  it('keeps a failed rename draft visible without automatically retrying the write', async () => {
    const update = vi
      .spyOn(codexAPI, 'update')
      .mockRejectedValue(new Error('Storage unavailable'));
    const dialog = vi
      .fn<DesktopAPI['dialog']>()
      .mockResolvedValueOnce({ button: 'rename', value: 'Keep this draft' })
      .mockResolvedValueOnce(null);
    expect(await renameSession({ dialog }, session)).toBeNull();
    expect(update).toHaveBeenCalledOnce();
    expect(dialog.mock.calls[1][0]).toMatchObject({
      input: { value: 'Keep this draft' },
      message: expect.stringContaining('Storage unavailable'),
    });
  });

  it('archives a live session through metadata only and publishes its new revision', async () => {
    const archived = {
      ...session,
      archivedAt: '2026-10-16T02:00:00Z',
      metadataRevision: 2,
    };
    const update = vi.spyOn(codexAPI, 'update').mockResolvedValueOnce(archived);
    const end = vi.spyOn(codexAPI, 'end');
    const listener = vi.fn();
    const unsubscribe = subscribeSessionMetadata(listener);
    try {
      expect(await setSessionArchived(session)).toBe(archived);
      expect(update).toHaveBeenCalledExactlyOnceWith('session', {
        revision: 1,
        archived: true,
      });
      expect(end).not.toHaveBeenCalled();
      expect(listener).toHaveBeenCalledExactlyOnceWith(archived);
    } finally {
      unsubscribe();
    }
  });

  it('refreshes a metadata conflict but never silently retries archive against a newer revision', async () => {
    const update = vi
      .spyOn(codexAPI, 'update')
      .mockRejectedValueOnce(
        new APIError('Conflict', 409, 'metadata_conflict'),
      );
    vi.spyOn(codexAPI, 'get').mockResolvedValueOnce({
      ...session,
      metadataRevision: 2,
    });
    await expect(setSessionArchived(session)).rejects.toThrow(
      'Review its latest details',
    );
    expect(update).toHaveBeenCalledOnce();
  });
});
