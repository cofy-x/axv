export interface Preferences {
  wallpaper: 'slate' | 'dither' | 'solid';
  iconSnap: boolean;
  clock24Hour: boolean;
}

export const defaultPreferences: Preferences = {
  wallpaper: 'slate',
  iconSnap: true,
  clock24Hour: true,
};

/** Merge each patch with the last committed preferences, including after failure. */
export function createPreferenceQueue(
  initial: Preferences,
  write: (preferences: Preferences) => Promise<Preferences>,
) {
  let current = initial;
  let queue: Promise<unknown> = Promise.resolve();
  return {
    reset(value: Preferences) {
      current = value;
    },
    save(patch: Partial<Preferences>): Promise<Preferences> {
      const pending = queue.then(async () => {
        const saved = await write({ ...current, ...patch });
        current = saved;
        return saved;
      });
      queue = pending.catch(() => {});
      return pending;
    },
  };
}
