import { describe, expect, it } from 'vitest';
import { initialDesktopLayout } from './useDesktopController';
import { sanitizeLayout } from '../model';
import type { AppDefinition } from '../types';

const app: AppDefinition = {
  id: 'example',
  title: 'Example',
  icon: 'document',
  component: () => null,
  defaultDesktop: true,
};

describe('initial desktop visibility', () => {
  it('orders only fresh pinned applications and preserves saved order and pins', () => {
    const apps = ['one', 'two', 'three'].map((id) => ({ ...app, id }));
    const props = {
      apps,
      icons: [],
      initialDockOrder: ['two', 'unknown', 'two', 'one'],
    };
    const initial = initialDesktopLayout(props);
    expect(initial.dock.map((entry) => entry.appId)).toEqual([
      'two',
      'one',
      'three',
    ]);
    const saved = {
      ...initial,
      dock: [
        { appId: 'three', pinned: true },
        { appId: 'one', pinned: false },
      ],
    };
    expect(
      initialDesktopLayout({ ...props, initialLayout: saved }).dock,
    ).toEqual(saved.dock);
  });
  it('keeps initially running utilities in the Dock without pinning them', () => {
    const utility = { ...app, defaultPinned: false };
    const initial = initialDesktopLayout({
      apps: [utility],
      icons: [],
      initialWindows: [{ appId: utility.id }],
    });
    expect(initial.dock).toEqual([{ appId: utility.id, pinned: false }]);
    const saved = { ...initial, windows: [], dock: [] };
    expect(
      initialDesktopLayout({
        apps: [utility],
        icons: [],
        initialWindows: [{ appId: utility.id }],
        initialLayout: saved,
      }).windows,
    ).toEqual([]);
  });
  it('seeds explicit launcher choices only for a new desktop', () => {
    const defaults = { [app.id]: false };
    const props = {
      apps: [app],
      icons: [],
      initialDesktopApplications: defaults,
    };
    const initial = initialDesktopLayout(props);
    expect(initial.desktopApplications).toEqual(defaults);
    expect(initial.desktopApplications).not.toBe(defaults);
    const saved = { ...initial, desktopApplications: { [app.id]: true } };
    expect(
      initialDesktopLayout({ ...props, initialLayout: saved })
        .desktopApplications,
    ).toEqual({ [app.id]: true });
    const inherited = sanitizeLayout(undefined, [app]);
    expect(
      initialDesktopLayout({ ...props, initialLayout: inherited })
        .desktopApplications,
    ).toEqual(inherited.desktopApplications);
  });
});
