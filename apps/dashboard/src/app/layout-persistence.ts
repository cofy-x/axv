import type { DesktopLayout } from '@cofy-x/axv-nextstep';

/** Coalesce movement while serializing writes; failed writes retain the newest layout. */
export function createLayoutPersistence(
  write: (layout: DesktopLayout) => Promise<unknown>,
  onSaved: () => void,
  onError: (error: unknown) => void,
) {
  let pending: DesktopLayout | null = null;
  let saving = false;
  return {
    enqueue(layout: DesktopLayout) {
      pending = layout;
    },
    async flush(): Promise<void> {
      if (saving || !pending) return;
      saving = true;
      try {
        while (pending) {
          const next = pending;
          pending = null;
          try {
            await write(next);
            onSaved();
          } catch (error) {
            pending ??= next;
            onError(error);
            break;
          }
        }
      } finally {
        saving = false;
      }
    },
  };
}
