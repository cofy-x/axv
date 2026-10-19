import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { tailscaleApp } from './definition';
import { TailscaleDetails } from './TailscalePanel';
import { connectionPresentation } from './presentation';
import type { ConnectionInfo, ConnectionSnapshot } from './types';

const info: ConnectionInfo = {
  local: true,
  remote: { state: 'disabled' },
  host: {
    installed: true,
    state: 'connected',
    hostname: 'system-node',
    ips: ['100.65.1.2'],
  },
};
const snapshot: ConnectionSnapshot = { info, loading: false };

describe('remote status presentation', () => {
  it('keeps AXV off even when the system Tailscale daemon is connected', () => {
    expect(connectionPresentation(snapshot)).toMatchObject({
      remote: 'Off',
      remoteTone: 'off',
      host: 'Connected',
      hostTone: 'ready',
    });
    const html = renderToStaticMarkup(
      createElement(TailscaleDetails, { snapshot, onCopy: vi.fn() }),
    );
    expect(html).toContain('AXV Remote Access');
    expect(html).toContain('System Tailscale');
    expect(html).toContain('Off</p>');
    expect(html).toContain('Connected</p>');
    expect(html).toContain('type="button"');
  });

  it('reports stale connection states as unknown without claiming an unavailable daemon is off', () => {
    expect(
      connectionPresentation({ ...snapshot, error: 'Offline' }),
    ).toMatchObject({ remote: 'Unknown', host: 'Unknown' });
    expect(
      connectionPresentation({
        ...snapshot,
        info: { ...info, host: { installed: true, state: 'unavailable' } },
      }),
    ).toMatchObject({ remote: 'Off', host: 'Unavailable' });
    expect(connectionPresentation({ loading: true }).remote).toBe('Checking…');
    expect(
      connectionPresentation({
        ...snapshot,
        info: {
          ...info,
          remote: {
            state: 'ready',
            message: 'Certificate renewal will retry.',
          },
        },
      }),
    ).toMatchObject({ remote: 'Ready', remoteTone: 'pending' });
  });

  it('shows distinct ready HTTPS and host IP details with named copy controls', () => {
    const html = renderToStaticMarkup(
      createElement(TailscaleDetails, {
        snapshot: {
          ...snapshot,
          info: {
            ...info,
            remote: { state: 'ready', url: 'https://axv.example.ts.net' },
          },
        },
        onCopy: vi.fn(),
      }),
    );
    expect(html).toContain('AXV Remote Access');
    expect(html).toContain('System Tailscale');
    expect(html).toContain('href="https://axv.example.ts.net"');
    expect(html).toContain('Copy HTTPS URL');
    expect(html).toContain('100.65.1.2');
    expect(html).toContain('Copy IP address');
    expect(html).not.toContain('Authorize</a>');
  });

  it('never renders a retained enrollment capability to a remote viewer or stale summary', () => {
    for (const candidate of [
      {
        ...snapshot,
        info: {
          ...info,
          local: false,
          remote: { state: 'needs-login' as const },
        },
      },
      {
        ...snapshot,
        error: 'Offline',
        info: { ...info, remote: { state: 'needs-login' as const } },
      },
    ]) {
      const html = renderToStaticMarkup(
        createElement(TailscaleDetails, {
          snapshot: candidate,
          authorization: {
            url: 'https://login.tailscale.com/a/private-capability',
          },
          onCopy: vi.fn(),
        }),
      );
      expect(html).not.toContain('private-capability');
      expect(html).not.toContain('Authorize</a>');
    }
    const local = renderToStaticMarkup(
      createElement(TailscaleDetails, {
        snapshot: {
          ...snapshot,
          info: { ...info, remote: { state: 'needs-login' } },
        },
        authorization: {
          url: 'https://login.tailscale.com/a/private-capability',
        },
        onCopy: vi.fn(),
      }),
    );
    expect(local).toContain('Authorize</a>');
    expect(local).toContain('referrerPolicy="no-referrer"');
  });

  it('uses one non-modal utility window available through existing launchers', () => {
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
