import { describe, expect, it, vi } from 'vitest';
import { createPreferenceQueue, defaultPreferences } from './preferences';
import type { Preferences } from './preferences';

describe('appearance preference writes', () => {
  it('merges concurrent partial changes with the last committed server value', async () => {
    let finish!: (preferences: Preferences) => void;
    const first = new Promise<Preferences>((resolve) => {
      finish = resolve;
    });
    const write = vi
      .fn<(value: Preferences) => Promise<Preferences>>()
      .mockImplementationOnce(() => first)
      .mockImplementation(async (value) => value);
    const queue = createPreferenceQueue(defaultPreferences, write);
    const wallpaper = queue.save({ wallpaper: 'dither' });
    const strip = queue.save({ clock24Hour: false });
    await Promise.resolve();
    expect(write).toHaveBeenCalledTimes(1);
    finish({ ...defaultPreferences, wallpaper: 'dither' });
    await wallpaper;
    await expect(strip).resolves.toEqual({
      ...defaultPreferences,
      wallpaper: 'dither',
      clock24Hour: false,
    });
    expect(write.mock.calls[1]).toEqual([
      { ...defaultPreferences, wallpaper: 'dither', clock24Hour: false },
    ]);
  });

  it('recovers after a rejected write without treating its patch as committed', async () => {
    const write = vi
      .fn<(value: Preferences) => Promise<Preferences>>()
      .mockRejectedValueOnce(new Error('offline'))
      .mockImplementation(async (value) => value);
    const queue = createPreferenceQueue(defaultPreferences, write);
    const failed = expect(queue.save({ iconSnap: false })).rejects.toThrow(
      'offline',
    );
    const next = queue.save({ clock24Hour: false });
    await failed;
    await expect(next).resolves.toEqual({
      ...defaultPreferences,
      clock24Hour: false,
    });
    expect(write.mock.calls[1]).toEqual([
      { ...defaultPreferences, clock24Hour: false },
    ]);
  });
});
