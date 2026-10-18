import type { DesktopLayout, WindowStateUpdate } from '../types';

export function updateWindowViewState(
  layout: DesktopLayout,
  id: string,
  update: WindowStateUpdate,
): DesktopLayout {
  const win = layout.windows.find((candidate) => candidate.id === id);
  if (!win) return layout;
  const patch = typeof update === 'function' ? update(win.viewState) : update;
  if (
    Object.entries(patch).every(
      ([key, value]) =>
        JSON.stringify(win.viewState[key]) === JSON.stringify(value),
    )
  )
    return layout;
  return {
    ...layout,
    windows: layout.windows.map((candidate) =>
      candidate.id === id
        ? { ...candidate, viewState: { ...candidate.viewState, ...patch } }
        : candidate,
    ),
  };
}
