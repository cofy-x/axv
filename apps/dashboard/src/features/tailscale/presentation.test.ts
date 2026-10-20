import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { tailscaleApp } from './definition';
import { TailscaleDetails } from './TailscalePanel';
import { connectionPresentation } from './presentation';
import type { ConnectionInfo, ConnectionSnapshot } from './types';

const info: ConnectionInfo = {
  local: true,
  remote: {
    enabled: false,
    supported: true,
    state: 'disabled',
    message: 'AXV remote access is disabled in this profile.',
  },
};
const snapshot: ConnectionSnapshot = { info, loading: false };

function render(candidate: ConnectionSnapshot) {
  return renderToStaticMarkup(
    createElement(TailscaleDetails, {
      snapshot: candidate,
      onSetEnabled: vi.fn(),
      onCopy: vi.fn(),
      authorization: {
        url: 'https://login.tailscale.com/a/private-capability',
      },
    }),
  );
}

describe('Tailscale status and controls', () => {
  it('offers embedded access without a system-installation prerequisite', () => {
    expect(connectionPresentation(snapshot)).toMatchObject({
      remote: 'Off',
      remoteTone: 'off',
    });
    const html = render(snapshot);
    expect(html).toContain('<h2 id="tailscale-axv-title">Remote access</h2>');
    expect(html).toContain('Access AXV from your other devices.');
    expect(html).not.toContain(
      'AXV remote access is disabled in this profile.',
    );
    expect(html).toContain('Enabling is saved for future launches.');
    expect(html).toContain('Enable</button>');
    expect(html).toContain('No system installation is needed');
    expect(html).toContain('Connect another device');
    expect(html).toContain('href="https://tailscale.com/download"');
    expect(html).not.toContain('System Tailscale');
    expect(html).not.toContain('private-capability');
  });

  it('keeps HTTPS setup and its disclosure without duplicating the server summary', () => {
    const message =
      'AXV is waiting for tailnet HTTPS availability. Check Tailscale HTTPS settings.';
    const html = render({
      ...snapshot,
      info: {
        ...info,
        remote: {
          ...info.remote,
          enabled: true,
          state: 'needs-https',
          message,
        },
      },
    });
    expect(html).not.toContain(message);
    expect(html).toContain('Enable MagicDNS and HTTPS certificates');
    expect(html).toContain('href="https://login.tailscale.com/admin/dns"');
    expect(html).toContain(
      'The hostname appears in public certificate logs. Access stays private.',
    );
  });

  it('makes stale state unknown and prevents actions or retained login links', () => {
    const candidate = { ...snapshot, error: 'Offline' };
    expect(connectionPresentation(candidate)).toMatchObject({
      remote: 'Unknown',
      remoteTone: 'unknown',
    });
    const html = render(candidate);
    expect(html).toContain('disabled=""');
    expect(html).not.toContain('private-capability');
    expect(connectionPresentation({ loading: true }).remote).toBe('Checking…');
  });

  it('shows a ready HTTPS address and keeps certificate warnings visible', () => {
    const candidate: ConnectionSnapshot = {
      ...snapshot,
      info: {
        ...info,
        remote: {
          ...info.remote,
          enabled: true,
          state: 'ready',
          url: 'https://axv.example.ts.net',
          message: 'Certificate renewal will retry.',
        },
      },
    };
    expect(connectionPresentation(candidate)).toMatchObject({
      remote: 'Ready',
      remoteTone: 'pending',
    });
    const html = render(candidate);
    expect(html).toContain('Certificate renewal will retry.');
    expect(html).toContain('href="https://axv.example.ts.net"');
    expect(html).toContain('Copy HTTPS URL');
    expect(html).toContain('Disable</button>');
    expect(html).not.toContain('Authorize</a>');
  });

  it('limits authorization, retry and enable controls to a local supported server', () => {
    const login: ConnectionInfo = {
      ...info,
      remote: {
        ...info.remote,
        enabled: true,
        state: 'needs-login',
        message: 'Complete Tailscale enrollment on the computer running AXV.',
      },
    };
    const html = render({ ...snapshot, info: login });
    expect(html).toContain('Authorize</a>');
    expect(html).toContain('Sign in with your Tailscale account.');
    expect(html).not.toContain(login.remote.message);
    expect(html).toContain('referrerPolicy="no-referrer"');
    for (const candidate of [
      { ...snapshot, info: { ...login, local: false } },
      { ...snapshot, error: 'Offline', info: login },
    ]) {
      const hidden = render(candidate);
      expect(hidden).not.toContain('private-capability');
      expect(hidden).not.toContain('Authorize</a>');
    }
    const remote = render({ ...snapshot, info: { ...login, local: false } });
    expect(remote).not.toContain('Disable</button>');
    expect(
      remote.match(/Manage remote access on the computer running AXV/g),
    ).toHaveLength(1);
    expect(remote).not.toContain('Sign in with your Tailscale account.');
    expect(remote).not.toContain(login.remote.message);
    const unsupported = render({
      ...snapshot,
      info: { ...info, remote: { ...info.remote, supported: false } },
    });
    expect(unsupported).not.toContain('Enable</button>');
    expect(unsupported).toContain('macOS or Linux');
    const failed = render({
      ...snapshot,
      info: {
        ...info,
        remote: {
          ...info.remote,
          enabled: true,
          state: 'error',
          message:
            'AXV remote access needs attention. Check remote status locally.',
        },
      },
    });
    expect(failed).toContain('Retry</button>');
    expect(failed).toContain('Disable</button>');
    expect(failed).toContain(
      'AXV remote access needs attention. Check remote status locally.',
    );
  });

  it('preserves the singleton utility and existing saved launcher identities', () => {
    expect(tailscaleApp).toMatchObject({
      id: 'remote-access',
      title: 'Tailscale',
      kind: 'panel',
      launchable: true,
      defaultPinned: false,
    });
    expect(tailscaleApp.windowKey!({})).toBe(
      tailscaleApp.windowKey!({ any: 'other' }),
    );
  });
});
