import type { CodexSessionRecord } from './api';

type Listener = (session: CodexSessionRecord) => void;

const listeners = new Set<Listener>();

/** Metadata updates never overwrite process or connection state. */
export function mergeSessionMetadata(
  session: CodexSessionRecord,
  metadata: CodexSessionRecord,
): CodexSessionRecord {
  if (
    session.id !== metadata.id ||
    metadata.metadataRevision <= session.metadataRevision
  )
    return session;
  return {
    ...session,
    title: metadata.title,
    archivedAt: metadata.archivedAt,
    metadataRevision: metadata.metadataRevision,
  };
}

export function publishSessionMetadata(session: CodexSessionRecord) {
  for (const listener of listeners) listener(session);
}

export function subscribeSessionMetadata(listener: Listener) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}
