import { describe, expect, it, vi } from 'vitest';
import {
  arrangeDesktopIconPositions,
  desktopApplicationIcons,
  fitDesktopIcons,
  iconWorkArea,
  isApplicationOnDesktop,
  sanitizeLayout,
  showDesktopApplication,
} from './model';
import type { AppDefinition, DesktopIcon } from './types';

const app: AppDefinition = {
  id: 'example.tool',
  title: 'Example Tool',
  icon: 'document',
  component: () => null,
};
const launcher: DesktopIcon = {
  id: 'system:example',
  appId: app.id,
  label: app.title,
  icon: app.icon,
  applicationLauncher: true,
};
const resource: DesktopIcon = {
  id: 'resource:original',
  appId: app.id,
  params: { resourceId: 'original' },
  label: 'Original resource',
  icon: 'document',
  trashable: true,
};

describe('desktop application shortcuts', () => {
  it('inherits host launchers and explicit application defaults without duplicating them', () => {
    const layout = sanitizeLayout(undefined, [app]);
    const defaultApp = { ...app, defaultDesktop: true };
    const launchApp = vi.fn();
    expect(isApplicationOnDesktop(layout, [app], [], app.id)).toBe(false);
    expect(isApplicationOnDesktop(layout, [app], [resource], app.id)).toBe(
      false,
    );
    expect(isApplicationOnDesktop(layout, [app], [launcher], app.id)).toBe(
      true,
    );
    const host = desktopApplicationIcons(
      layout,
      [defaultApp],
      [launcher, resource],
      launchApp,
    );
    expect(host.map((icon) => icon.id)).toEqual([launcher.id, resource.id]);
    expect(host[0].trashable).toBe(false);
    host[0].onOpen?.();
    expect(launchApp).toHaveBeenCalledExactlyOnceWith(app.id);
    const generated = desktopApplicationIcons(
      layout,
      [defaultApp],
      [resource],
      launchApp,
    );
    expect(generated.map((icon) => icon.id)).toEqual([
      resource.id,
      `application:${app.id}`,
    ]);
    expect(generated[1]).toMatchObject({
      appId: app.id,
      applicationLauncher: true,
      trashable: false,
    });
  });

  it('keeps an explicit hidden choice across missing and later default-visible definitions', () => {
    const hidden = showDesktopApplication(
      sanitizeLayout(undefined, []),
      app.id,
      false,
    );
    const missing = sanitizeLayout(JSON.parse(JSON.stringify(hidden)), []);
    const late = { ...app, defaultDesktop: true };
    expect(missing.desktopApplications).toEqual({ [app.id]: false });
    expect(isApplicationOnDesktop(missing, [late], [launcher], app.id)).toBe(
      false,
    );
    expect(
      desktopApplicationIcons(missing, [late], [launcher, resource], vi.fn()),
    ).toEqual([resource]);
    expect(sanitizeLayout(missing, [late]).desktopApplications).toEqual({
      [app.id]: false,
    });
  });

  it('retains unknown shown applications and restores metadata under the same shortcut identity', () => {
    const shown = showDesktopApplication(
      sanitizeLayout(undefined, []),
      app.id,
      true,
    );
    const restored = sanitizeLayout(JSON.parse(JSON.stringify(shown)), []);
    const launch = vi.fn();
    const missing = desktopApplicationIcons(restored, [], [], launch)[0];
    expect(missing).toMatchObject({
      id: `application:${app.id}`,
      appId: app.id,
      label: app.id,
      applicationLauncher: true,
      trashable: false,
      status: { label: 'Unavailable', tone: 'warning' },
    });
    missing.onOpen?.();
    expect(launch).toHaveBeenCalledExactlyOnceWith(app.id);
    const available = desktopApplicationIcons(restored, [app], [], launch)[0];
    expect(available.id).toBe(missing.id);
    expect(available.label).toBe(app.title);
    expect(available.status).toBeUndefined();
  });

  it('preserves host actions and resource identities when application visibility changes', () => {
    const onOpen = vi.fn();
    const hostLauncher = { ...launcher, onOpen };
    const launch = vi.fn();
    const initial = sanitizeLayout(undefined, [app]);
    const shown = desktopApplicationIcons(
      initial,
      [app],
      [hostLauncher, resource],
      launch,
    );
    expect(shown[0].id).toBe(launcher.id);
    expect(shown[0].onOpen).toBe(onOpen);
    shown[0].onOpen?.();
    expect(onOpen).toHaveBeenCalledOnce();
    expect(launch).not.toHaveBeenCalled();
    expect(shown[1]).toBe(resource);
    const hidden = showDesktopApplication(initial, app.id, false);
    expect(
      desktopApplicationIcons(hidden, [app], [hostLauncher, resource], launch),
    ).toEqual([resource]);
    expect(hostLauncher).not.toHaveProperty('trashable');
  });

  it('preserves both geometry maps and unrelated user state through toggles and arrangement', () => {
    const initial = {
      ...sanitizeLayout(undefined, [app]),
      icons: {
        [launcher.id]: { x: 100, y: 200 },
        retained: { x: 400, y: 500 },
      },
      compactIcons: { [launcher.id]: { x: 20, y: 100 } },
    };
    const hidden = showDesktopApplication(initial, app.id, false);
    expect(hidden.icons).toBe(initial.icons);
    expect(hidden.compactIcons).toBe(initial.compactIcons);
    expect(hidden.windows).toBe(initial.windows);
    expect(hidden.dock).toBe(initial.dock);
    expect(hidden.workspaces).toBe(initial.workspaces);
    const visible = desktopApplicationIcons(
      hidden,
      [app],
      [launcher, resource],
      vi.fn(),
    );
    const area = iconWorkArea({ width: 1440, height: 900 }, false);
    const arranged = arrangeDesktopIconPositions(hidden.icons, visible, area);
    expect(arranged[launcher.id]).toEqual(initial.icons[launcher.id]);
    expect(arranged.retained).toEqual(initial.icons.retained);
    expect(arranged[resource.id]).toBeDefined();
    const shown = showDesktopApplication(
      { ...hidden, icons: arranged },
      app.id,
      true,
    );
    expect(shown.icons[launcher.id]).toEqual(initial.icons[launcher.id]);
    const projected = desktopApplicationIcons(
      shown,
      [app],
      [launcher, resource],
      vi.fn(),
    );
    const fit = fitDesktopIcons(
      projected.map((icon) => icon.id),
      shown.icons,
      area,
    );
    expect(
      projected.every((icon) => fit.positions[icon.id] !== undefined),
    ).toBe(true);
  });

  it('validates persisted overrides without removing missing application references', () => {
    const result = sanitizeLayout(
      {
        version: 2,
        windows: [],
        desktopApplications: {
          'missing.visible': true,
          'missing.hidden': false,
          'wrong.value': 'true',
          '../invalid': true,
          '': false,
          ['x'.repeat(129)]: true,
          constructor: true,
        },
      },
      [],
    );
    expect(result.desktopApplications).toEqual({
      'missing.visible': true,
      'missing.hidden': false,
      constructor: true,
    });
    expect(
      desktopApplicationIcons(result, [], [], vi.fn()).map(
        (icon) => icon.appId,
      ),
    ).toEqual(['missing.visible', 'constructor']);
    expect(showDesktopApplication(result, '../invalid', true)).toBe(result);
    expect(showDesktopApplication(result, 'missing.visible', true)).toBe(
      result,
    );
    const queued = showDesktopApplication(
      showDesktopApplication(result, 'another.tool', true),
      'missing.hidden',
      true,
    );
    expect(queued.desktopApplications).toEqual({
      ...result.desktopApplications,
      'another.tool': true,
      'missing.hidden': true,
    });
  });

  it('keeps unavailable applications visible without executing their components', () => {
    const component = vi.fn(() => null);
    const unavailable = {
      ...app,
      defaultDesktop: true,
      unavailable: 'Disabled',
      component,
    };
    const projected = desktopApplicationIcons({}, [unavailable], [], vi.fn());
    expect(projected).toHaveLength(1);
    expect(projected[0].status).toEqual({
      label: 'Unavailable',
      tone: 'warning',
    });
    expect(component).not.toHaveBeenCalled();
    const contextual = { ...app, launchable: false, defaultDesktop: true };
    expect(
      desktopApplicationIcons({}, [contextual], [], vi.fn())[0].status,
    ).toEqual({ label: 'Unavailable', tone: 'warning' });
    expect(
      desktopApplicationIcons({}, [contextual], [launcher], vi.fn())[0].status,
    ).toEqual({ label: 'Unavailable', tone: 'warning' });
    expect(sanitizeLayout(undefined, []).desktopApplications).toBeUndefined();
  });
});
