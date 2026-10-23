import { afterEach, describe, expect, it, vi } from 'vitest';
import { codexAPI } from './api';
import type { CodexActivity, CodexSessionRecord } from './api';
import {
  activityLabel,
  canReviewActivity,
  hasActivityAction,
  mergeSessionActivity,
  mergeSessionSnapshot,
  publishSessionActivity,
  reviewSessionActivity,
  subscribeSessionActivity,
} from './activity';

const session: CodexSessionRecord = {
  id: 'conversation',
  runId: 'original-run',
  target: 'host',
  projectPath: '/project',
  title: 'Current title',
  metadataRevision: 3,
  tmuxSession: 'owned-session',
  tmuxSocket: 'owned-socket',
  attachCommand: 'tmux attach',
  status: 'running',
  createdAt: '2026-10-23T00:00:00Z',
  connections: 1,
  continuation: { state: 'active', activeRunId: 'original-run' },
};

const failed: CodexActivity = {
  observation: { instanceId: 'service', revision: 1 },
  state: 'failed',
  sequence: 2,
  eventId: 'first-failure',
  turnId: 'first-turn',
  updatedAt: '2026-10-23T00:01:00Z',
  needsAttention: true,
};

function observed(
  activity: Omit<CodexActivity, 'observation'>,
  revision: number,
  instanceId = 'service',
): CodexSessionRecord {
  return {
    ...session,
    activity: { ...activity, observation: { instanceId, revision } },
  };
}

afterEach(() => vi.restoreAllMocks());

describe('Codex activity and attention', () => {
  it('distinguishes provider activity from process liveness and request dismissal', () => {
    expect(activityLabel()).toBe('Activity unavailable');
    expect(
      activityLabel({ ...failed, state: 'idle', needsAttention: false }),
    ).toBe('Idle');
    for (const state of [
      'waiting_approval',
      'waiting_input',
      'unknown',
      'completed',
    ] as const)
      expect(canReviewActivity({ ...failed, state })).toBe(false);
    for (const state of ['failed', 'interrupted'] as const)
      expect(canReviewActivity({ ...failed, state })).toBe(true);
    expect(canReviewActivity({ ...failed, needsAttention: false })).toBe(false);
    expect(canReviewActivity({ ...failed, eventId: undefined })).toBe(false);
    for (const state of [
      'waiting_approval',
      'waiting_input',
      'failed',
      'interrupted',
    ] as const) {
      expect(hasActivityAction({ ...failed, state })).toBe(true);
      expect(
        hasActivityAction({ ...failed, state, needsAttention: false }),
      ).toBe(false);
    }
    for (const state of ['completed', 'idle', 'working', 'unknown'] as const)
      expect(hasActivityAction({ ...failed, state })).toBe(false);
  });

  it('keeps a newer turn when a review response arrives late and never replaces metadata or process state', () => {
    const working = observed(
      {
        ...failed,
        state: 'working',
        sequence: 3,
        eventId: 'second-turn',
        needsAttention: false,
      },
      3,
    );
    const lateReview = {
      ...observed({ ...failed, needsAttention: false }, 2),
      title: 'Old title',
      metadataRevision: 1,
      status: 'starting' as const,
    };
    expect(mergeSessionActivity(working, lateReview)).toBe(working);
    expect(mergeSessionActivity(observed(failed, 1), lateReview)).toMatchObject(
      {
        title: 'Current title',
        metadataRevision: 3,
        status: 'running',
        connections: 1,
        activity: { needsAttention: false },
      },
    );
  });

  it('rejects stale observations in either direction without using provider transition timestamps', () => {
    const unread = observed(failed, 1);
    const reviewed = observed({ ...failed, needsAttention: false }, 2);
    expect(mergeSessionActivity(reviewed, unread)).toBe(reviewed);
    expect(
      mergeSessionActivity(unread, reviewed).activity?.needsAttention,
    ).toBe(false);
    expect(mergeSessionActivity(reviewed, observed(failed, 2))).toBe(reviewed);
    // A newer authoritative snapshot can report changed review validity for the same event.
    expect(
      mergeSessionActivity(reviewed, observed(failed, 3)).activity
        ?.needsAttention,
    ).toBe(true);
  });

  it('never merges activity into a different conversation or run and fences lower provider sequences', () => {
    const current = observed(failed, 1);
    expect(
      mergeSessionActivity(current, {
        ...observed(failed, 10),
        runId: 'later-run',
      }),
    ).toBe(current);
    expect(
      mergeSessionActivity(current, {
        ...observed(failed, 10),
        id: 'another-conversation',
      }),
    ).toBe(current);
    expect(
      mergeSessionActivity(current, observed({ ...failed, sequence: 1 }, 10)),
    ).toBe(current);
  });

  it('replaces the latest run without copying predecessor activity or losing newer conversation metadata', () => {
    const previous = observed(failed, 1);
    const next = {
      ...observed(
        {
          state: 'idle',
          sequence: 1,
          eventId: 'next-run-idle',
          needsAttention: false,
        },
        2,
      ),
      runId: 'next-run',
      title: 'Stale title',
      metadataRevision: 1,
    };
    expect(mergeSessionSnapshot(previous, next)).toMatchObject({
      runId: 'next-run',
      title: 'Current title',
      metadataRevision: 3,
      activity: next.activity,
    });
    expect(
      mergeSessionSnapshot(previous, { ...next, activity: undefined }).activity,
    ).toBeUndefined();
  });

  it('keeps exact-run lifecycle and continuation from the newer observation while merging metadata independently', () => {
    const ended = {
      ...observed(
        {
          state: 'unknown',
          sequence: 3,
          needsAttention: false,
          reason: 'This run is no longer running.',
        },
        5,
      ),
      status: 'ended' as const,
      reason: 'Session ended.',
      connections: 0,
      continuation: { state: 'available' as const },
    };
    const delayedRunning = {
      ...observed(failed, 4),
      title: 'Newer conversation title',
      metadataRevision: 4,
    };
    expect(mergeSessionSnapshot(ended, delayedRunning)).toMatchObject({
      status: 'ended',
      reason: 'Session ended.',
      connections: 0,
      continuation: { state: 'available' },
      title: 'Newer conversation title',
      metadataRevision: 4,
      activity: ended.activity,
    });
    // Continuation can update locally after a confirmed launch, using the
    // same source activity stamp without representing an older service read.
    expect(
      mergeSessionSnapshot(ended, {
        ...ended,
        continuation: { state: 'active', activeRunId: 'new-run' },
      }).continuation,
    ).toEqual({ state: 'active', activeRunId: 'new-run' });
  });

  it('normalizes late list reads against the cached lifecycle before publishing their activity', () => {
    const unsubscribe = subscribeSessionActivity(vi.fn());
    try {
      const ended = {
        ...observed(
          {
            state: 'unknown',
            sequence: 2,
            eventId: failed.eventId,
            needsAttention: false,
          },
          5,
        ),
        status: 'ended' as const,
        reason: 'Session ended.',
        continuation: { state: 'available' as const },
      };
      publishSessionActivity(ended);
      const delayedList = publishSessionActivity(observed(failed, 4));
      // The manager merges this already-published result into its current row.
      expect(mergeSessionSnapshot(ended, delayedList)).toMatchObject({
        status: 'ended',
        reason: 'Session ended.',
        continuation: { state: 'available' },
        activity: ended.activity,
      });
      expect(delayedList.status).toBe('ended');
      expect(delayedList.activity?.needsAttention).toBe(false);
      // A genuinely later snapshot can update lifecycle at the same provider
      // event while preserving exact-run and independently versioned metadata.
      const current = publishSessionActivity({
        ...observed({ ...failed, needsAttention: false, reason: undefined }, 6),
        status: 'ended',
        continuation: { state: 'active', activeRunId: 'new-run' },
      });
      expect(current.continuation).toEqual({
        state: 'active',
        activeRunId: 'new-run',
      });
    } finally {
      unsubscribe();
    }
  });

  it('orders unknown observations, review diagnostics and recovery independently of provider sequence', () => {
    const reviewed = observed({ ...failed, needsAttention: false }, 2);
    const damaged = observed(
      {
        ...failed,
        reason: 'The activity review record could not be verified.',
      },
      3,
    );
    const uncertain = mergeSessionActivity(reviewed, damaged);
    expect(uncertain.activity).toMatchObject({
      needsAttention: true,
      reason: damaged.activity?.reason,
    });
    const recovered = mergeSessionActivity(
      uncertain,
      observed({ ...failed, needsAttention: false }, 4),
    );
    expect(recovered.activity?.reason).toBeUndefined();
    expect(recovered.activity?.needsAttention).toBe(false);
    expect(mergeSessionActivity(recovered, damaged)).toBe(recovered);
    const unknown = mergeSessionActivity(
      recovered,
      observed(
        {
          state: 'unknown',
          needsAttention: false,
          reason: 'Activity unavailable.',
        },
        5,
      ),
    );
    expect(unknown.activity).toMatchObject({ state: 'unknown', sequence: 2 });
    expect(mergeSessionActivity(unknown, recovered)).toBe(unknown);
    const freshReview = observed({ ...failed, needsAttention: false }, 6);
    expect(mergeSessionActivity(unknown, freshReview).activity).toEqual(
      freshReview.activity,
    );
  });

  it('sends only the exact reviewed event and publishes the authoritative server result', async () => {
    const source = observed(failed, 1);
    const result = observed({ ...failed, needsAttention: false }, 2);
    const review = vi
      .spyOn(codexAPI, 'reviewActivity')
      .mockResolvedValue(result);
    const listener = vi.fn();
    const unsubscribe = subscribeSessionActivity(listener);
    try {
      expect(await reviewSessionActivity(source)).toEqual(result);
      expect(review).toHaveBeenCalledExactlyOnceWith(
        'conversation',
        'original-run',
        'first-failure',
      );
      expect(listener).toHaveBeenCalledExactlyOnceWith(result);
      await reviewSessionActivity(
        observed({ ...failed, state: 'waiting_approval' }, 3),
      );
      await reviewSessionActivity(
        observed({ ...failed, state: 'completed' }, 4),
      );
      expect(review).toHaveBeenCalledOnce();
    } finally {
      unsubscribe();
    }
  });

  it('preserves attention after an unconfirmed review and never publishes success', async () => {
    const source = observed(failed, 1);
    vi.spyOn(codexAPI, 'reviewActivity').mockRejectedValue(
      new Error('Connection lost'),
    );
    const listener = vi.fn();
    const unsubscribe = subscribeSessionActivity(listener);
    try {
      await expect(reviewSessionActivity(source)).rejects.toThrow(
        'Connection lost',
      );
      expect(listener).not.toHaveBeenCalled();
      expect(source.activity?.needsAttention).toBe(true);
    } finally {
      unsubscribe();
    }
  });

  it('replays accepted reviews to initial late reads and retains authoritative recovery through unknown', () => {
    const unsubscribe = subscribeSessionActivity(vi.fn());
    try {
      publishSessionActivity(observed({ ...failed, needsAttention: false }, 2));
      expect(
        publishSessionActivity(observed(failed, 1)).activity?.needsAttention,
      ).toBe(false);
      const unknown = publishSessionActivity(
        observed(
          {
            state: 'unknown',
            needsAttention: false,
            reason: 'Activity unavailable.',
          },
          3,
        ),
      );
      expect(unknown.activity?.state).toBe('unknown');
      expect(
        publishSessionActivity(
          observed({ ...failed, needsAttention: false }, 4),
        ).activity,
      ).toMatchObject({ state: 'failed', needsAttention: false });
      const damaged = publishSessionActivity(
        observed({ ...failed, reason: 'Review record is damaged.' }, 5),
      );
      expect(damaged.activity).toMatchObject({
        needsAttention: true,
        reason: expect.any(String),
      });
      expect(
        publishSessionActivity(
          observed({ ...failed, needsAttention: false }, 4),
        ).activity,
      ).toEqual(damaged.activity);
      // The service can recover to an unreviewed record; cached reviews cannot mask that fact.
      expect(
        publishSessionActivity(observed(failed, 6)).activity?.needsAttention,
      ).toBe(true);
    } finally {
      unsubscribe();
    }
  });

  it('resynchronizes after service restart and fences delayed reviews or diagnostics from retired instances', () => {
    const unsubscribe = subscribeSessionActivity(vi.fn());
    try {
      publishSessionActivity(
        observed({ ...failed, needsAttention: false }, 9, 'before-restart'),
      );
      const current = publishSessionActivity({
        ...observed(failed, 1, 'after-restart'),
        status: 'ended',
        continuation: { state: 'available' },
      });
      expect(current.activity?.needsAttention).toBe(true);
      for (const activity of [
        {
          state: 'unknown' as const,
          needsAttention: false,
          reason: 'Old diagnostic.',
        },
        { ...failed, needsAttention: false },
      ])
        expect(
          publishSessionActivity(observed(activity, 10, 'before-restart')),
        ).toMatchObject({
          status: 'ended',
          continuation: { state: 'available' },
          activity: current.activity,
        });
    } finally {
      unsubscribe();
    }
  });

  it('releases cached observations when the last mounted subscriber leaves', () => {
    const first = subscribeSessionActivity(vi.fn());
    publishSessionActivity(observed({ ...failed, needsAttention: false }, 2));
    first();
    const next = subscribeSessionActivity(vi.fn());
    try {
      expect(
        publishSessionActivity(observed(failed, 1)).activity?.needsAttention,
      ).toBe(true);
    } finally {
      next();
    }
  });
});
