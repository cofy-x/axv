import { APIError } from '../../shared/api/http';
import type {
  DraftRemoval,
  DraftRemovalGuard,
} from '../../shared/drafts/storage';

export interface NoteSnapshot {
  text: string;
  saved: string;
  revision: number;
  loaded: boolean;
  blocked: boolean;
  status: string;
  error: string;
  conflict: boolean;
  dirty: boolean;
  saving: boolean;
  recovered: boolean;
  recoveryAvailable: boolean;
  resolvingRecovery: boolean;
  localStatus: string;
}

interface NoteStorage {
  assertCurrent?: () => void;
  subscribeInvalidation?: DraftRemovalGuard['subscribe'];
  read: () => Promise<{ entry: { revision: number }; content?: string }>;
  write: (
    content: string,
    revision: number,
  ) => Promise<{ entry: { revision: number } }>;
  refresh: () => Promise<void>;
}

export interface LocalDraft {
  text: string;
  saved: string;
  baseRevision: number;
}

interface DraftStorage {
  read: () => Promise<LocalDraft | undefined>;
  write: (draft: LocalDraft) => Promise<unknown>;
  remove: (guard?: DraftRemovalGuard) => Promise<DraftRemoval | void>;
}

type SavingNote = {
  phase: 'saving';
  content: string;
  revision: number;
  promise: Promise<boolean>;
};

type NoteSaveState = { phase: 'idle' } | SavingNote;
type LocalWriteState = { phase: 'idle' | 'writing'; promise: Promise<void> };

/** Server saves commit one explicit snapshot; recovery keeps the draft's original revision. */
export function createNoteDocument(
  storage: NoteStorage,
  drafts?: DraftStorage,
) {
  let snapshot: NoteSnapshot = {
    text: '',
    saved: '',
    revision: 0,
    loaded: false,
    blocked: false,
    status: 'Loading…',
    error: '',
    conflict: false,
    dirty: false,
    saving: false,
    recovered: false,
    recoveryAvailable: false,
    resolvingRecovery: false,
    localStatus: '',
  };
  let saveState: NoteSaveState = { phase: 'idle' };
  let localWrites: LocalWriteState = {
    phase: 'idle',
    promise: Promise.resolve(),
  };
  let pendingLocal: LocalDraft | undefined;
  let recovery: LocalDraft | undefined;
  let loadGeneration = 0;
  let editGeneration = 0;
  const listeners = new Set<() => void>();
  const removalGuard = storage.assertCurrent
    ? {
        assertCurrent: storage.assertCurrent,
        subscribe: storage.subscribeInvalidation,
      }
    : undefined;

  function publish(patch: Partial<Omit<NoteSnapshot, 'saving'>>) {
    snapshot = { ...snapshot, ...patch, saving: saveState.phase === 'saving' };
    for (const listener of listeners) listener();
  }

  function flushLocal() {
    if (!drafts || localWrites.phase === 'writing' || !pendingLocal)
      return localWrites.promise;
    const promise = Promise.resolve()
      .then(async () => {
        while (pendingLocal) {
          const draft = pendingLocal;
          pendingLocal = undefined;
          let removal: DraftRemoval | void = undefined;
          try {
            if (draft.text === draft.saved) {
              storage.assertCurrent?.();
              removal = await drafts.remove(removalGuard);
              storage.assertCurrent?.();
            } else await drafts.write(draft);
            if (!pendingLocal)
              publish({ localStatus: 'Local recovery copy up to date' });
          } catch {
            await removal?.restore().catch(() => {});
            // Editing may replace the queue while the storage operation waits.
            const queued = pendingLocal as LocalDraft | undefined;
            const newerDraft =
              draft.text === draft.saved &&
              queued &&
              queued.text !== queued.saved;
            pendingLocal ??= draft;
            publish({
              localStatus:
                'Local recovery is unavailable. Keep this window open or save your document.',
            });
            if (newerDraft) continue;
            break;
          }
        }
      })
      .finally(() => {
        localWrites = { phase: 'idle', promise };
      });
    localWrites = { phase: 'writing', promise };
    return promise;
  }

  function persistDraft() {
    if (
      !drafts ||
      !snapshot.loaded ||
      snapshot.recoveryAvailable ||
      snapshot.resolvingRecovery
    )
      return;
    pendingLocal = {
      text: snapshot.text,
      saved: snapshot.saved,
      baseRevision: snapshot.revision,
    };
    publish({ localStatus: 'Keeping a local recovery copy…' });
    void flushLocal();
  }

  async function removeLocal(requireCurrent = false) {
    if (requireCurrent) storage.assertCurrent?.();
    const previousPending = pendingLocal;
    let removal: DraftRemoval | void = undefined;
    publish({ resolvingRecovery: true });
    pendingLocal = undefined;
    try {
      await localWrites.promise;
      if (requireCurrent) storage.assertCurrent?.();
      pendingLocal = undefined;
      if (drafts)
        removal = await drafts.remove(
          requireCurrent ? removalGuard : undefined,
        );
      if (requireCurrent) storage.assertCurrent?.();
      publish({ localStatus: '' });
      return removal;
    } catch (error) {
      await removal?.restore().catch(() => {});
      pendingLocal ??= previousPending;
      if (snapshot.dirty && !snapshot.recoveryAvailable)
        pendingLocal ??= {
          text: snapshot.text,
          saved: snapshot.saved,
          baseRevision: snapshot.revision,
        };
      publish({
        error:
          'The local recovery copy could not be removed safely. Keep this window open or download your draft before closing.',
        localStatus: 'Local recovery storage is unavailable.',
      });
      throw error;
    } finally {
      publish({ resolvingRecovery: false });
    }
  }

  async function load(recover = true): Promise<void> {
    const generation = ++loadGeneration;
    const startingEdit = editGeneration;
    const [remote, local] = await Promise.allSettled([
      storage.read(),
      recover && drafts ? drafts.read() : Promise.resolve(undefined),
    ]);
    if (generation !== loadGeneration) return;
    if (startingEdit !== editGeneration) {
      publish({
        error:
          'Loading was cancelled because you continued editing. Your draft is unchanged.',
      });
      return;
    }
    const draft = local.status === 'fulfilled' ? local.value : undefined;
    if (remote.status === 'rejected' && (!draft || draft.text === draft.saved))
      throw remote.reason;
    if (remote.status === 'fulfilled') storage.assertCurrent?.();
    const removal = !recover ? await removeLocal(true) : undefined;
    const restoreRemoved = async () => {
      try {
        await removal?.restore();
      } catch (error) {
        if (snapshot.dirty) persistDraft();
        publish({
          localStatus:
            'Local recovery is unavailable. Keep this window open or download your draft before closing.',
        });
        throw error;
      }
    };
    if (generation !== loadGeneration || startingEdit !== editGeneration) {
      await restoreRemoved();
      return;
    }
    try {
      if (remote.status === 'fulfilled') storage.assertCurrent?.();
      const text =
        remote.status === 'fulfilled' ? (remote.value.content ?? '') : '';
      recovery = draft && draft.text !== draft.saved ? draft : undefined;
      publish({
        text,
        saved: text,
        revision:
          remote.status === 'fulfilled' ? remote.value.entry.revision : 0,
        loaded: remote.status === 'fulfilled',
        blocked: false,
        dirty: false,
        error: '',
        conflict: false,
        status: recovery ? 'Local recovery copy available' : 'Saved',
        recovered: false,
        recoveryAvailable: Boolean(recovery),
        localStatus:
          local.status === 'rejected'
            ? 'Local recovery is unavailable. Save your document before leaving.'
            : '',
      });
    } catch (error) {
      await restoreRemoved();
      throw error;
    }
  }

  function save(): Promise<boolean> {
    if (saveState.phase === 'saving') return saveState.promise;
    if (
      !snapshot.loaded ||
      snapshot.blocked ||
      snapshot.recoveryAvailable ||
      snapshot.resolvingRecovery
    )
      return Promise.resolve(false);
    if (!snapshot.dirty) return Promise.resolve(true);
    let complete!: (result: boolean | PromiseLike<boolean>) => void;
    const promise = new Promise<boolean>((resolve) => {
      complete = resolve;
    });
    const saving: SavingNote = {
      phase: 'saving',
      content: snapshot.text,
      revision: snapshot.revision,
      promise,
    };
    saveState = saving;
    publish({ status: 'Saving…', error: '' });
    complete(writeSnapshot(saving));
    return promise;
  }

  async function writeSnapshot(saving: SavingNote): Promise<boolean> {
    const { content, revision } = saving;
    try {
      const result = await storage.write(content, revision);
      storage.assertCurrent?.();
      publish({
        saved: content,
        revision: result.entry.revision,
        dirty: snapshot.text !== content,
        recovered: false,
        status: snapshot.text === content ? 'Saved' : 'Unsaved changes',
      });
      persistDraft();
      try {
        await storage.refresh();
      } catch {
        // Metadata availability does not change the acknowledged content write.
      }
      storage.assertCurrent?.();
      return snapshot.text === snapshot.saved;
    } catch (error) {
      const conflict =
        error instanceof APIError && error.code === 'revision_conflict';
      publish({
        blocked: conflict,
        conflict,
        error: error instanceof Error ? error.message : String(error),
        status: conflict
          ? 'Another window changed this document'
          : 'Not saved — your draft is still here',
      });
      persistDraft();
      return false;
    } finally {
      if (saveState === saving) {
        saveState = { phase: 'idle' };
        publish({});
      }
    }
  }

  return {
    getSnapshot: () => snapshot,
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    load,
    reload: () => load(false),
    cancelLoad() {
      ++loadGeneration;
    },
    save,
    restoreDraft() {
      if (!recovery) return;
      const changed =
        snapshot.loaded && snapshot.revision !== recovery.baseRevision;
      publish({
        text: recovery.text,
        saved: recovery.saved,
        revision: recovery.baseRevision,
        loaded: true,
        dirty: true,
        recovered: true,
        recoveryAvailable: false,
        conflict: changed,
        blocked: changed,
        status: changed
          ? 'Recovered draft — server changed'
          : 'Recovered draft — not saved',
        error: changed
          ? 'The saved document changed after this draft began. Reload it or save a copy to keep both versions.'
          : '',
        localStatus: 'Recovered from this browser',
      });
      recovery = undefined;
      persistDraft();
    },
    async keepSaved() {
      await removeLocal();
      recovery = undefined;
      publish({
        recoveryAvailable: false,
        recovered: false,
        error: '',
        status: 'Saved',
      });
    },
    edit(text: string) {
      if (snapshot.recoveryAvailable || snapshot.resolvingRecovery) return;
      ++editGeneration;
      publish({
        text,
        dirty: text !== snapshot.saved,
        status: text !== snapshot.saved ? 'Unsaved changes' : 'Saved',
      });
      persistDraft();
    },
    metadataChanged(previousRevision: number, revision: number) {
      if (snapshot.loaded && snapshot.revision === previousRevision) {
        publish({ revision });
        persistDraft();
      }
    },
    async discard() {
      await removeLocal();
      recovery = undefined;
      publish({
        text: snapshot.saved,
        dirty: false,
        recovered: false,
        recoveryAvailable: false,
        status: 'Saved',
        error: '',
      });
    },
    flushDraft: () => flushLocal(),
    setError(error: unknown) {
      publish({
        error: error instanceof Error ? error.message : String(error),
      });
    },
    reportLoadError(error: unknown) {
      publish({
        error: error instanceof Error ? error.message : String(error),
        status: 'Unable to load',
      });
    },
  };
}
