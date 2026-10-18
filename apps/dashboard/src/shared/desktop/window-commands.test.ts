import { describe, expect, it, vi } from 'vitest';
import { createWindowCommands } from './window-commands';

describe('window command scope', () => {
  it('publishes availability changes and never invokes a disabled command', async () => {
    const commands = createWindowCommands();
    const changed = vi.fn();
    const handler = vi.fn(async () => true);
    const unsubscribe = commands.subscribe(changed);
    const initial = commands.getSnapshot();
    const obsolete = commands.register(
      'a',
      'terminal:interrupt',
      handler,
      false,
    );
    expect(commands.canExecute('a', 'terminal:interrupt')).toBe(false);
    await expect(commands.execute('a', 'terminal:interrupt')).resolves.toBe(
      false,
    );
    expect(handler).not.toHaveBeenCalled();
    const unregister = commands.register('a', 'terminal:interrupt', handler);
    expect(commands.getSnapshot()).toBeGreaterThan(initial);
    expect(commands.canExecute('a', 'terminal:interrupt')).toBe(true);
    obsolete();
    // Replacing a registration with the same callback still leaves the new one live.
    expect(commands.canExecute('a', 'terminal:interrupt')).toBe(true);
    await expect(commands.execute('a', 'terminal:interrupt')).resolves.toBe(
      true,
    );
    unregister();
    expect(commands.canExecute('a', 'terminal:interrupt')).toBe(false);
    expect(changed).toHaveBeenCalled();
    unsubscribe();
    changed.mockClear();
    commands.register('b', 'save', handler);
    expect(changed).not.toHaveBeenCalled();
  });

  it('targets only the requested live window and desktop instance', async () => {
    const commands = createWindowCommands();
    const otherDesktop = createWindowCommands();
    const saveA = vi.fn(async () => true);
    const saveB = vi.fn(async () => false);
    const unregister = commands.register('a', 'save', saveA);
    commands.register('b', 'save', saveB);
    await expect(commands.execute('a', 'save')).resolves.toBe(true);
    expect(saveA).toHaveBeenCalledTimes(1);
    expect(saveB).not.toHaveBeenCalled();
    await expect(otherDesktop.execute('a', 'save')).resolves.toBe(false);
    unregister();
    await expect(commands.execute('a', 'save')).resolves.toBe(false);
    await expect(commands.execute('b', 'save')).resolves.toBe(false);
    expect(saveB).toHaveBeenCalledTimes(1);
  });

  it('does not remove a replacement handler when an older registration cleans up', async () => {
    const commands = createWindowCommands();
    const obsolete = commands.register('a', 'save', async () => false);
    const latest = vi.fn(async () => true);
    commands.register('a', 'save', latest);
    obsolete();
    await expect(commands.execute('a', 'save')).resolves.toBe(true);
    expect(latest).toHaveBeenCalledTimes(1);
  });
});
