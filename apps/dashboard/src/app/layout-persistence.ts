import type { DesktopLayout } from '@cofy-x/axv-nextstep';

type LayoutWriteState =
  | { phase: 'idle' }
  | { phase: 'pending' | 'failed'; layout: DesktopLayout }
  | {
      phase: 'saving';
      pending: DesktopLayout | null;
      completion: Promise<void>;
    };

/** Coalesce movement while serializing writes; failed writes retain the newest layout. */
export function createLayoutPersistence(
  write: (layout: DesktopLayout) => Promise<unknown>,
  onSaved: () => void,
  onError: (error: unknown) => void,
  assertCurrent?: () => void,
) {
  let state: LayoutWriteState = { phase: 'idle' };

  async function drain(saving: Extract<LayoutWriteState, { phase: 'saving' }>) {
    while (saving.pending) {
      const next = saving.pending;
      saving.pending = null;
      try {
        assertCurrent?.();
        await write(next);
        assertCurrent?.();
        onSaved();
      } catch (error) {
        state = { phase: 'failed', layout: saving.pending ?? next };
        onError(error);
        return;
      }
    }
    state = { phase: 'idle' };
  }

  return {
    enqueue(layout: DesktopLayout) {
      if (state.phase === 'saving') state.pending = layout;
      else state = { phase: 'pending', layout };
    },
    flush(): Promise<void> {
      if (state.phase === 'saving') return state.completion;
      if (state.phase === 'idle') return Promise.resolve();
      let resolve!: () => void;
      let reject!: (error: unknown) => void;
      const completion = new Promise<void>((done, fail) => {
        resolve = done;
        reject = fail;
      });
      const saving: Extract<LayoutWriteState, { phase: 'saving' }> = {
        phase: 'saving',
        pending: state.layout,
        completion,
      };
      state = saving;
      void drain(saving).then(resolve, reject);
      return completion;
    },
  };
}
