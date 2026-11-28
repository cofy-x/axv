import { describe, expect, it } from 'vitest';
import { describeBuiltin, groupBuiltins, readPackage } from './package-utils';
import type { BuiltinApplication } from './types';

function app(
  id: string,
  title: string,
  launchable = true,
  kind = 'application',
): BuiltinApplication {
  return { id, title, icon: 'document', launchable, kind };
}

describe('groupBuiltins', () => {
  it('separates workstation, system and non-launchable internal panels cleanly', () => {
    const grouped = groupBuiltins([
      app('about', 'About AXV', false),
      app('gallery', 'Component Gallery'),
      app('terminal', 'Terminal'),
      app('codex', 'Codex'),
      app('machine-inspector', 'VM Inspector', false, 'panel'),
      app('cloudflare', 'Cloudflare', true, 'panel'),
      app('remote-access', 'Tailscale', true, 'panel'),
      app('machines', 'Virtual Machines'),
    ]);

    expect(grouped.workstation.map((item) => item.id)).toEqual([
      'codex',
      'machines',
      'terminal',
    ]);
    expect(grouped.system.map((item) => item.id)).toEqual([
      'remote-access',
      'cloudflare',
      'gallery',
    ]);
    expect(grouped.internal.map((item) => item.id)).toEqual([
      'about',
      'machine-inspector',
    ]);
    expect(describeBuiltin(grouped.internal[0]!)).toBe(
      'Opened from its owning application',
    );
    expect(describeBuiltin(grouped.workstation[0]!)).toMatch(/coding/i);
  });
});

describe('readPackage', () => {
  it('rejects packages without API 1 storage manifest and HTML', () => {
    expect(() => readPackage(null)).toThrow(/JSON format/i);
    expect(() =>
      readPackage({
        manifest: {
          schemaVersion: 1,
          apiVersion: 1,
          id: 'local.test',
          title: 'Test',
          version: '1.0.0',
          capabilities: [],
        },
        html: '<html></html>',
      }),
    ).toThrow(/storage capability/i);
  });
});
