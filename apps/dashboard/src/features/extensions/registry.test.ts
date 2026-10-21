import { describe, expect, it, vi } from 'vitest';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { ExtensionWindow, createExtensionDefinitions } from './ExtensionWindow';
import { composeApplicationDefinitions } from './registry';
import type { ExtensionRecord } from './types';

const app: ExtensionRecord = {
  id: 'example.tool',
  title: 'Example Tool',
  version: '1.0.0',
  apiVersion: 1,
  capabilities: ['storage'],
  digest: 'a'.repeat(64),
  generation: 1,
  installed: true,
  enabled: true,
};

describe('extension desktop definitions', () => {
  it('rejects duplicate identities within and across definition groups', () => {
    const definition = createExtensionDefinitions([app])[0];
    expect(() =>
      composeApplicationDefinitions([definition, definition]),
    ).toThrow('Duplicate application ID: extension.example.tool');
    expect(() =>
      composeApplicationDefinitions(
        [definition],
        [{ ...definition, title: 'Different label' }],
      ),
    ).toThrow('Duplicate application ID: extension.example.tool');
    const other = { ...definition, id: 'another.tool' };
    expect(composeApplicationDefinitions([definition], [other])).toEqual([
      definition,
      other,
    ]);
  });

  it('keeps the host component and window identity stable across catalog changes', () => {
    const available = createExtensionDefinitions([app])[0];
    for (const change of [
      { enabled: false, generation: 2 },
      { installed: false, enabled: false, generation: 3 },
      {
        title: 'Updated Label',
        digest: 'b'.repeat(64),
        version: '2.0.0',
        generation: 4,
      },
    ]) {
      const next = createExtensionDefinitions([{ ...app, ...change }])[0];
      expect(next.id).toBe(available.id);
      expect(next.windowKey?.({})).toBe(available.windowKey?.({}));
      expect(next.component).toBe(ExtensionWindow);
      expect(next.component).toBe(available.component);
      // The feature host remains mounted to protect unsaved data and explain its state.
      expect(next.unavailable).toBeUndefined();
    }
  });

  it('opens an explicit namespaced identity without pinning new installations automatically', async () => {
    const [definition] = createExtensionDefinitions([app]);
    const openApp = vi.fn();
    await definition.launch?.({ openApp } as unknown as DesktopAPI);
    expect(openApp).toHaveBeenCalledExactlyOnceWith('extension.example.tool');
    expect(definition.defaultPinned).toBe(false);
    expect(definition.defaultDesktop).toBe(false);
    expect(definition.launchable).toBe(true);
    expect(
      createExtensionDefinitions([{ ...app, installed: false }])[0].launchable,
    ).toBe(false);
  });

  it('uses controlled package artwork with a document fallback', () => {
    expect(createExtensionDefinitions([app])[0].icon).toBe('document');
    expect(
      createExtensionDefinitions([{ ...app, icon: 'checklist' }])[0].icon,
    ).toBe('checklist');
    expect(
      createExtensionDefinitions([
        { ...app, installed: false, icon: 'checklist' },
      ])[0].icon,
    ).toBe('checklist');
  });
});
