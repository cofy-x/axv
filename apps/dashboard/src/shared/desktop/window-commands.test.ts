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
    commands.register('b', 'terminal:copy', handler);
    expect(changed).not.toHaveBeenCalled();
  });

  it('targets only the requested live window and desktop instance', async () => {
    const commands = createWindowCommands();
    const otherDesktop = createWindowCommands();
    const copyA = vi.fn(async () => true);
    const copyB = vi.fn(async () => false);
    const unregister = commands.register('a', 'terminal:copy', copyA);
    commands.register('b', 'terminal:copy', copyB);
    await expect(commands.execute('a', 'terminal:copy')).resolves.toBe(true);
    expect(copyA).toHaveBeenCalledTimes(1);
    expect(copyB).not.toHaveBeenCalled();
    await expect(otherDesktop.execute('a', 'terminal:copy')).resolves.toBe(
      false,
    );
    unregister();
    await expect(commands.execute('a', 'terminal:copy')).resolves.toBe(false);
    await expect(commands.execute('b', 'terminal:copy')).resolves.toBe(false);
    expect(copyB).toHaveBeenCalledTimes(1);
  });

  it('does not remove a replacement handler when an older registration cleans up', async () => {
    const commands = createWindowCommands();
    const obsolete = commands.register('a', 'terminal:copy', async () => false);
    const latest = vi.fn(async () => true);
    commands.register('a', 'terminal:copy', latest);
    obsolete();
    await expect(commands.execute('a', 'terminal:copy')).resolves.toBe(true);
    expect(latest).toHaveBeenCalledTimes(1);
  });
});
