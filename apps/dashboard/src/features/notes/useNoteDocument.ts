import { useEffect, useMemo, useRef, useSyncExternalStore } from 'react';
import { filesystemApi } from '../../filesystem/api';
import { useDraftNamespace } from '../../shared/drafts/context';
import { draftStorage } from '../../shared/drafts/storage';
import { createNoteDocument } from './note-document';

export function useNoteDocument(
  entryId: string,
  draftId: string,
  name: string,
  refresh: () => Promise<void>,
  recoveryId?: string,
) {
  const { databaseId, clientId } = useDraftNamespace();
  const latest = useRef({ name, refresh });
  latest.current = { name, refresh };
  const document = useMemo(() => {
    const local = draftStorage({ databaseId, clientId }, draftId);
    return createNoteDocument(
      {
        read: () => filesystemApi.entry(entryId),
        write: (content, revision) =>
          filesystemApi.patch(entryId, { content, revision }),
        refresh: () => latest.current.refresh(),
      },
      {
        read: async () => {
          const own = await local.read();
          if (own || !recoveryId) return own;
          return draftStorage({ databaseId, clientId }, recoveryId).read();
        },
        write: (draft) =>
          local.write({
            ...draft,
            entryId,
            name: latest.current.name,
            updatedAt: Date.now(),
          }),
        remove: local.remove,
      },
    );
  }, [entryId, draftId, databaseId, clientId, recoveryId]);
  const snapshot = useSyncExternalStore(
    document.subscribe,
    document.getSnapshot,
  );
  useEffect(() => {
    void document.load().catch(document.reportLoadError);
    return document.cancelLoad;
  }, [document]);
  return { ...snapshot, ...document };
}
