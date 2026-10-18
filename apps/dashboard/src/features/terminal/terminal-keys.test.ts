import { describe, expect, it } from 'vitest';
import { isDesktopTerminalKey } from './terminal-keys';

function key(key: string, modifiers: Partial<KeyboardEvent> = {}) {
  return { key, ctrlKey: false, metaKey: false, altKey: false, ...modifiers };
}

describe('terminal keyboard ownership', () => {
  it.each(['a', 'c', 'd', 'l', 'n', 's', 'w', 'z'])(
    'preserves Control-%s for the host shell',
    (letter) => {
      expect(isDesktopTerminalKey(key(letter, { ctrlKey: true }))).toBe(false);
    },
  );

  it('keeps window cycling, workspace switching, workspace menus and Command-W available', () => {
    expect(isDesktopTerminalKey(key('F6', { ctrlKey: true }))).toBe(true);
    expect(
      isDesktopTerminalKey(key('F6', { ctrlKey: true, shiftKey: true })),
    ).toBe(true);
    for (const digit of ['1', '2', '3', '4'])
      expect(isDesktopTerminalKey(key(digit, { altKey: true }))).toBe(true);
    expect(isDesktopTerminalKey(key('F10'))).toBe(true);
    expect(isDesktopTerminalKey(key('w', { metaKey: true }))).toBe(true);
  });

  it('leaves ordinary function, Escape, Tab and unused Alt keys to xterm', () => {
    for (const name of ['Escape', 'Tab', 'F6', 'Enter'])
      expect(isDesktopTerminalKey(key(name))).toBe(false);
    expect(isDesktopTerminalKey(key('5', { altKey: true }))).toBe(false);
    expect(isDesktopTerminalKey(key('1'))).toBe(false);
  });
});
