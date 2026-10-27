import { afterEach, describe, expect, it, vi } from 'vitest';
import { desktopLayoutMode, windowWorkArea } from '../model';
import { viewportSize } from './useViewport';

afterEach(() => vi.unstubAllGlobals());

describe('visual desktop viewport', () => {
  it('uses the keyboard-reduced visual height without moving a narrow Dock to the side', () => {
    vi.stubGlobal('innerWidth', 390);
    vi.stubGlobal('innerHeight', 844);
    vi.stubGlobal('visualViewport', { height: 320 });
    const viewport = viewportSize();
    expect(viewport).toEqual({ width: 390, height: 320 });
    expect(desktopLayoutMode(viewport)).toEqual({
      mobile: true,
      short: true,
      sideDock: false,
    });
    expect(windowWorkArea(viewport, false)).toEqual({
      x: 4,
      y: 64,
      width: 382,
      height: 192,
    });
  });

  it('returns to the ordinary mobile work area when the visual viewport grows again', () => {
    vi.stubGlobal('innerWidth', 390);
    vi.stubGlobal('innerHeight', 844);
    vi.stubGlobal('visualViewport', { height: 844 });
    expect(desktopLayoutMode(viewportSize()).short).toBe(false);
    expect(windowWorkArea(viewportSize(), false)).toEqual({
      x: 4,
      y: 90,
      width: 382,
      height: 690,
    });
  });
});
