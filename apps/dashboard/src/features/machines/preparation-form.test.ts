import { describe, expect, it } from 'vitest';
import {
  preparationDraft,
  preparationDraftError,
  preparationRequest,
} from './preparation-form';

const defaults = {
  imageBundle: '/images/axv/manifest.json',
  storageRoot: '/profile/VM storage',
  limaHome: '/profile/lima',
  cpus: 8,
  memoryMiB: 8192,
  diskGiB: 100,
  machines: 4,
};

describe('VM preparation inputs', () => {
  it('requires explicit valid budgets without substituting values while typing', () => {
    const draft = preparationDraft(defaults);
    expect(preparationDraftError(draft, 'darwin', false)).toBe('');
    draft.memoryMiB = '';
    expect(preparationDraftError(draft, 'darwin', false)).toContain('memory');
    draft.memoryMiB = '256.5';
    expect(preparationDraftError(draft, 'darwin', false)).toContain('whole');
    draft.memoryMiB = '1024';
    draft.storageRoot = 'relative/path';
    expect(preparationDraftError(draft, 'darwin', false)).toContain('absolute');
    draft.storageRoot = '/profile/VM storage';
    expect(preparationRequest(draft, 'darwin', false, defaults)).toEqual({
      ...defaults,
      imageSource: 'bundle',
      memoryMiB: 1024,
    });
  });

  it('requires a bundle path only for advanced custom images', () => {
    const draft = preparationDraft(defaults);
    draft.imageBundle = '';
    expect(preparationDraftError(draft, 'darwin', false)).toContain(
      'image bundle',
    );
  });

  it('defaults to the Ubuntu transition source without requiring a local bundle', () => {
    const draft = preparationDraft({ ...defaults, imageBundle: undefined });
    expect(draft.imageSource).toBe('ubuntu-base');
    expect(preparationDraftError(draft, 'darwin', false)).toBe('');
    expect(
      preparationRequest(draft, 'darwin', false, defaults).imageBundle,
    ).toBeUndefined();
  });

  it('sends only the chosen local Linux connection and never macOS budgets', () => {
    const draft = preparationDraft(defaults);
    expect(preparationRequest(draft, 'linux', false, defaults)).toEqual({
      hostConfig: '/etc/axv/host.json',
    });
    draft.connection = 'socket';
    draft.socket = '/run/custom/control.sock';
    expect(preparationRequest(draft, 'linux', false, defaults)).toEqual({
      socket: '/run/custom/control.sock',
    });
    draft.socket = 'ssh://other-host';
    expect(preparationDraftError(draft, 'linux', false)).toContain('absolute');
  });

  it('preserves existing configuration when reviewing service activation', () => {
    const draft = preparationDraft({});
    draft.storageRoot = '/unrelated/draft';
    expect(preparationDraftError(draft, 'darwin', true)).toBe('');
    const request = preparationRequest(draft, 'darwin', true, defaults);
    expect(request).toEqual(defaults);
    expect(request).not.toBe(defaults);
  });
});
