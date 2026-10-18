import { describe, expect, it, vi } from 'vitest';
import { sanitizeLayout } from '../model';
import type { DesktopLayout, DesktopWindow, WindowStateUpdate } from '../types';
import { updateWindowViewState } from './window-state';

function layoutWithWindow(): DesktopLayout {
  const win: DesktopWindow = {
    id: 'browser:main',
    appId: 'browser',
    title: 'Browser',
    params: {},
    viewState: { location: 'parent', path: ['parent'], scrollLeft: 0 },
    x: 0,
    y: 0,
    width: 400,
    height: 300,
    workspaceId: 'workspace-1',
    sticky: false,
    minimized: false,
    shaded: false,
    zoomed: false,
  };
  return { ...sanitizeLayout(undefined, []), windows: [win] };
}

describe('queued window view updates', () => {
  it('reconciles after navigation and scrolling using the latest state', () => {
    const initial = layoutWithWindow();
    const reconcile: WindowStateUpdate = (current) => ({
      path: ['parent', current.location],
    });
    let layout = updateWindowViewState(initial, 'browser:main', {
      location: 'child',
    });
    layout = updateWindowViewState(layout, 'browser:main', { scrollLeft: 38 });
    layout = updateWindowViewState(layout, 'browser:main', reconcile);
    expect(layout.windows[0]!.viewState).toEqual({
      location: 'child',
      path: ['parent', 'child'],
      scrollLeft: 38,
    });
    expect(initial.windows[0]!.viewState).toEqual({
      location: 'parent',
      path: ['parent'],
      scrollLeft: 0,
    });
    expect(updateWindowViewState(layout, 'browser:main', reconcile)).toBe(
      layout,
    );
  });

  it('does not run deferred updates for a closed window', () => {
    const layout = { ...layoutWithWindow(), windows: [] };
    const update = vi.fn(() => ({ location: 'child' }));
    expect(updateWindowViewState(layout, 'browser:main', update)).toBe(layout);
    expect(update).not.toHaveBeenCalled();
  });
});
