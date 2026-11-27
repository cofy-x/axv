import { describe, expect, it } from 'vitest';
import { applyCtrlModifier } from './MobileTerminalKeybar';

describe('applyCtrlModifier', () => {
  it('maps lowercase and uppercase ASCII letters to control bytes', () => {
    expect(applyCtrlModifier('c')).toBe('\x03');
    expect(applyCtrlModifier('C')).toBe('\x03');
    expect(applyCtrlModifier('a')).toBe('\x01');
    expect(applyCtrlModifier('z')).toBe('\x1a');
    expect(applyCtrlModifier('l')).toBe('\x0c');
  });

  it('maps control punctuation and rejects multi-char or non-ctrl input', () => {
    expect(applyCtrlModifier('[')).toBe('\x1b');
    expect(applyCtrlModifier('@')).toBe('\x00');
    expect(applyCtrlModifier('ab')).toBeNull();
    expect(applyCtrlModifier('1')).toBeNull();
    expect(applyCtrlModifier('')).toBeNull();
  });
});
