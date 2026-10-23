import { codexAPI } from './api';
import type { CodexActivity, CodexSessionRecord } from './api';
import { mergeSessionMetadata } from './metadata';

const labels: Record<CodexActivity['state'], string> = {
  unknown: 'Activity unavailable',
  idle: 'Idle',
  working: 'Working',
  waiting_approval: 'Waiting for approval',
  waiting_input: 'Waiting for input',
  completed: 'Completed',
  failed: 'Failed',
  interrupted: 'Interrupted',
};

export function activityLabel(activity?: CodexActivity) {
  return labels[activity?.state || 'unknown'];
}

export function hasActivityAction(activity?: CodexActivity) {
  return (
    !!activity?.needsAttention &&
    ['waiting_approval', 'waiting_input', 'failed', 'interrupted'].includes(
      activity.state,
    )
  );
}

export function canReviewActivity(activity?: CodexActivity) {
  return (
    !!activity?.eventId &&
    hasActivityAction(activity) &&
    ['failed', 'interrupted'].includes(activity.state)
  );
}

/** A latest-run snapshot can change runs; only conversation metadata crosses that boundary. */
export function mergeSessionSnapshot(
  previous: CodexSessionRecord,
  incoming: CodexSessionRecord,
): CodexSessionRecord {
  if (previous.id !== incoming.id || previous.runId !== incoming.runId)
    return mergeSessionMetadata(incoming, previous);
  const currentObservation = previous.activity?.observation;
  const incomingObservation = incoming.activity?.observation;
  const olderObservation =
    currentObservation &&
    incomingObservation &&
    currentObservation.instanceId === incomingObservation.instanceId &&
    incomingObservation.revision < currentObservation.revision;
  const snapshot = olderObservation
    ? mergeSessionMetadata(previous, incoming)
    : mergeSessionMetadata(incoming, previous);
  return {
    ...snapshot,
    activity: mergeSessionActivity(previous, incoming).activity,
  };
}

/** Activity belongs to an exact run; late reads cannot revive a reviewed event. */
export function mergeSessionActivity(
  session: CodexSessionRecord,
  update: CodexSessionRecord,
): CodexSessionRecord {
  if (
    session.id !== update.id ||
    session.runId !== update.runId ||
    !update.activity
  )
    return session;
  const current = session.activity;
  const incoming = update.activity;
  if (!current) return { ...session, activity: incoming };
  const currentObservation = current.observation;
  const incomingObservation = incoming.observation;
  if (
    currentObservation.instanceId === incomingObservation.instanceId &&
    incomingObservation.revision <= currentObservation.revision
  )
    return session;
  const currentSequence = current.sequence ?? -1;
  const incomingSequence = incoming.sequence ?? -1;
  if (incomingSequence >= 0 && incomingSequence < currentSequence)
    return session;
  if (incoming.state === 'unknown')
    return {
      ...session,
      activity: {
        ...incoming,
        sequence: incoming.sequence ?? current.sequence,
        eventId: incoming.eventId ?? current.eventId,
        turnId: incoming.turnId ?? current.turnId,
        updatedAt: incoming.updatedAt ?? current.updatedAt,
      },
    };
  return { ...session, activity: incoming };
}

type Listener = (session: CodexSessionRecord) => void;
const listeners = new Set<Listener>();

// Views share only their current observations, so an accepted review can reach
// a window whose initial read is still pending. Nothing survives the last view.
interface CachedActivity {
  session: CodexSessionRecord;
  retiredInstances: Set<string>;
}

const observations = new Map<string, CachedActivity>();

export function publishSessionActivity(session: CodexSessionRecord) {
  if (!listeners.size || !session.activity) return session;
  const key = JSON.stringify([session.id, session.runId]);
  const previous = observations.get(key);
  const incoming = session.activity;
  const instance = incoming.observation.instanceId;
  const previousInstance = previous?.session.activity?.observation?.instanceId;
  if (instance && previous?.retiredInstances.has(instance))
    return mergeSessionMetadata(previous.session, session);
  // Merge the original service stamp before activity normalization. Otherwise
  // a delayed list could pair old lifecycle fields with the cached new stamp.
  const snapshot = previous
    ? mergeSessionSnapshot(previous.session, session)
    : session;
  const activity = snapshot.activity;
  const changedInstance =
    instance &&
    previousInstance &&
    instance !== previousInstance &&
    activity?.observation?.instanceId === instance;
  const retiredInstances = previous?.retiredInstances || new Set<string>();
  if (changedInstance) retiredInstances.add(previousInstance);
  observations.set(key, {
    session: snapshot,
    retiredInstances,
  });
  for (const listener of listeners) listener(snapshot);
  return snapshot;
}

export function subscribeSessionActivity(listener: Listener) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
    if (!listeners.size) observations.clear();
  };
}

export async function reviewSessionActivity(session: CodexSessionRecord) {
  if (!canReviewActivity(session.activity)) return;
  const updated = await codexAPI.reviewActivity(
    session.id,
    session.runId,
    session.activity!.eventId!,
  );
  return publishSessionActivity(updated);
}
