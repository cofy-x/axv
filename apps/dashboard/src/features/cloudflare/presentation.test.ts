import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { CloudflareDetails } from './CloudflarePanel';
import { CloudflareDiagnostics } from './CloudflareDiagnostics';
import { SetupGuide } from './SetupGuide';
import { cloudflareApp } from './definition';
import { cloudflarePresentation } from './presentation';
import type { CloudflareSnapshot } from './types';

const snapshot: CloudflareSnapshot = {
  loading: false,
  info: {
    local: true,
    cloudflare: {
      enabled: true,
      configured: true,
      state: 'listening',
      url: 'https://workstation.example.com',
    },
  },
};

function render(value = snapshot, interactive = true) {
  return renderToStaticMarkup(
    createElement(CloudflareDetails, {
      snapshot: value,
      interactive,
      onCopy: () => undefined,
      onSetEnabled: () => undefined,
      onSetupGuide: () => undefined,
      onViewDiagnostics: () => undefined,
      onRefresh: () => undefined,
    }),
  );
}

describe('Cloudflare utility presentation', () => {
  it('distinguishes a local listening socket from public tunnel connectivity', () => {
    expect(cloudflarePresentation(snapshot)).toEqual({
      label: 'Listening locally',
      tone: 'pending',
    });
    const html = render();
    expect(html).toContain('Public access has not been verified.');
    expect(html).toContain('href="https://workstation.example.com"');
    expect(html).not.toMatch(/is-ready|>Connected<|>Ready</);
  });

  it('keeps refresh available when the first status read fails', () => {
    const html = render({ loading: false, error: 'Status unavailable.' });
    expect(html).toContain('Status unavailable. Refresh to check.');
    expect(html).toContain('aria-label="Refresh"');
    expect(html).not.toContain('disabled=""');
  });

  it('marks retained status unknown and removes addresses and enabled controls after a failed read', () => {
    const stale = { ...snapshot, error: 'Status unavailable.' };
    expect(cloudflarePresentation(stale)).toEqual({
      label: 'Unknown',
      tone: 'unknown',
    });
    const html = render(stale);
    expect(html).not.toContain('href=');
    expect(html).toContain('disabled=""');
  });

  it('limits controls to local clients and removes interactive addresses when inactive', () => {
    const remote = render({
      ...snapshot,
      info: { ...snapshot.info!, local: false },
    });
    expect(remote).toContain('Manage Cloudflare on the computer running AXV.');
    expect(remote).not.toContain('Disable</button>');
    expect(remote).toContain('Copy Desktop URL');
    expect(remote).not.toContain('Setup guide</button>');
    expect(remote).not.toContain('View diagnostics</button>');
    expect(render(snapshot, false)).not.toContain('href=');
  });

  it('explains missing setup and disables enabling until configured', () => {
    const html = render({
      ...snapshot,
      info: {
        local: true,
        cloudflare: { enabled: false, configured: false, state: 'disabled' },
      },
    });
    expect(html).toContain('Setup required');
    expect(html).toContain('Open setup</button>');
    expect(html).toContain('aria-controls="cloudflare-setup-page"');
    expect(html).toContain('disabled=""');
    expect(html).toContain('Enable</button>');
    expect(html).not.toContain('Enabling is saved for future launches.');
  });

  it('keeps entrypoint errors ahead of incomplete setup and offers local diagnostics', () => {
    const failed: CloudflareSnapshot = {
      loading: false,
      info: {
        local: true,
        cloudflare: { enabled: true, configured: false, state: 'error' },
      },
    };
    expect(cloudflarePresentation(failed)).toEqual({
      label: 'Needs attention',
      tone: 'error',
    });
    expect(render(failed)).toContain('View diagnostics</button>');
    expect(render(failed)).not.toContain('Setup required');
    expect(
      render({ ...failed, info: { ...failed.info!, local: false } }),
    ).not.toContain('View diagnostics</button>');
  });

  it('never describes a configured inactive origin as a listening socket', () => {
    const html = renderToStaticMarkup(
      createElement(CloudflareDiagnostics, {
        status: {
          instanceId: 'inactive-instance',
          enabled: false,
          configured: true,
          state: 'disabled',
          listen: '127.0.0.1:3002',
        },
      }),
    );
    expect(html).toContain('127.0.0.1:3002');
    expect(html).toContain('the entrypoint is off.');
    expect(html).not.toContain('Listening on this computer.');
  });

  it('marks retained diagnostic addresses unknown after a failed update', () => {
    const html = renderToStaticMarkup(
      createElement(CloudflareDiagnostics, {
        error: 'Diagnostics unavailable.',
        status: {
          instanceId: 'previous-instance',
          enabled: true,
          configured: true,
          state: 'listening',
          listen: '127.0.0.1:3002',
        },
      }),
    );
    expect(html).toContain('Showing previous diagnostics.');
    expect(html).toContain('current state is unknown.');
    expect(html).not.toContain('Listening on this computer.');
  });

  it('supplies a complete local setup example without credential fields or profile replacement', () => {
    const html = renderToStaticMarkup(
      createElement(SetupGuide, { onClose: () => undefined }),
    );
    const example = html.match(
      /<pre aria-label="Example AXV profile configuration"><code>(.*?)<\/code><\/pre>/s,
    );
    expect(example).not.toBeNull();
    const parsed = JSON.parse(example![1]!.replaceAll('&quot;', '"'));
    expect(parsed.cloudflare).toMatchObject({
      enabled: false,
      listen: '127.0.0.1:3002',
      origin: 'https://axv.example.com',
    });
    expect(Object.keys(parsed.cloudflare).sort()).toEqual([
      'audience',
      'enabled',
      'issuer',
      'listen',
      'origin',
      'owner_subject',
    ]);
    expect(html).toContain('Preserve its other settings');
    expect(html).toContain('AXV_HOME/config.json');
    expect(html).toContain('httpHostHeader: axv.example.com');
    expect(html).toContain('Preserve the public Host and Origin.');
  });

  it('has one stable unpinned utility identity', () => {
    expect(cloudflareApp).toMatchObject({
      id: 'cloudflare',
      title: 'Cloudflare',
      kind: 'panel',
      icon: 'cloud-tunnel',
      defaultPinned: false,
      defaultDesktop: true,
    });
    expect(cloudflareApp.windowKey!({})).toBe('main');
    expect(cloudflareApp.windowKey!({ other: 'ignored' })).toBe('main');
  });
});
