import { describe, expect, it } from 'vitest';
import { configDraft, configWrite, savedDraft } from './config-draft';
import {
  allocatedPublicationHostname,
  checkPublicationActionReceipt,
  checkPublicationReceipt,
  publicationName,
} from './publication-receipt';
import { requestedPublication } from './publishing-context';
import { safePublicationURL } from './CloudflarePublishing';
import type {
  CloudflareConfig,
  CloudflarePublication,
  CloudflarePublishRequest,
} from './types';

const config: CloudflareConfig = {
  instanceId: 'instance-1',
  revision: 'revision-1',
  access: {
    enabled: false,
    listen: '127.0.0.1:3002',
    origin: 'https://desktop.example.com',
    issuer: 'https://team.cloudflareaccess.com',
    audience: 'desktop-aud',
    ownerSubject: 'owner-sub',
  },
  account: {
    configured: true,
    hasToken: true,
    accountId: 'account-1',
    zoneId: 'zone-1',
    tunnelId: 'tunnel-1',
    domain: 'example.net',
    listen: '127.0.0.1:3003',
    audience: 'apps-aud',
  },
};
const binding = {
  hostId: 'host-1',
  projectId: 'project-1',
  projectRevision: 1,
  volumeId: 'volume-1',
  volumeRevision: 2,
  machineId: 'machine-1',
  bootId: 'boot-1',
  instanceId: 'instance-1',
};
const submitted: CloudflarePublishRequest = {
  instanceId: 'instance-1',
  requestId: 'request-1',
  binding,
  port: 3000,
  name: 'app',
  domain: 'example.net',
  visibility: 'owner',
};
const receipt: CloudflarePublication = {
  ...submitted,
  id: 'publication-1',
  domain: 'example.net',
  generation: '0123456789abcdef0123456789abcdef',
  hostname: 'app-0123456789abcdef0123456789abcdef.example.net',
  state: 'published',
  phase: 'complete',
  cleanupPending: false,
  createdAt: '2026-10-24T00:00:00Z',
  updatedAt: '2026-10-24T00:00:00Z',
  url: 'https://app-0123456789abcdef0123456789abcdef.example.net',
};

describe('Cloudflare configuration drafts', () => {
  it('saves only an explicit snapshot, preserves its revision and does not send a retained secret', () => {
    const draft = configDraft(config);
    draft.access.origin = 'https://new.example.com';
    draft.token = 'old-in-memory-secret';
    expect(configWrite(config, draft)).toEqual({
      instanceId: config.instanceId,
      expectedRevision: config.revision,
      access: draft.access,
      account: draft.account,
      tokenAction: 'keep',
    });
    expect(
      configWrite(config, { ...draft, tokenAction: 'clear' }),
    ).not.toHaveProperty('token');
    expect(
      configWrite(config, {
        ...draft,
        tokenAction: 'replace',
        token: 'replacement',
      }),
    ).toHaveProperty('token', 'replacement');
    expect(config.access.origin).toBe('https://desktop.example.com');
  });

  it('acknowledges the submitted revision while preserving later typing and token replacement', () => {
    const submitted = configDraft(config);
    const saved = { ...config, revision: 'revision-2' };
    const newer = {
      ...submitted,
      access: {
        ...submitted.access,
        origin: 'https://typed-after-save.example.com',
      },
      tokenAction: 'replace' as const,
      token: 'new-draft-secret',
    };
    expect(savedDraft(newer, submitted, saved)).toBe(newer);
    expect(savedDraft(submitted, submitted, saved)).toEqual(configDraft(saved));
    expect(
      savedDraft(
        { ...submitted, tokenAction: 'replace', token: 'submitted-secret' },
        { ...submitted, tokenAction: 'replace', token: 'submitted-secret' },
        saved,
      ),
    ).toEqual(configDraft(saved));
  });
});

describe('Cloudflare publication identity', () => {
  it('accepts bounded canonical names and rejects hostnames, Unicode and invalid DNS labels', () => {
    for (const name of ['a', 'editor', 'app-2', 'a'.repeat(24)])
      expect(publicationName(name)).toBe(true);
    for (const name of [
      '',
      '-app',
      'app-',
      'App',
      'app.example.net',
      'xn--label',
      'é',
      'a'.repeat(25),
    ])
      expect(publicationName(name)).toBe(false);
  });

  it('accepts only the receipt for the original target, visibility and request', () => {
    expect(checkPublicationReceipt(receipt, submitted, 'example.net')).toBe(
      receipt,
    );
    for (const changed of [
      { requestId: 'another-request' },
      { name: 'another' },
      { hostname: 'another.example.net' },
      { domain: 'example.org' },
      { generation: '0123456789abcdef0123456789abcdeF' },
      { generation: '0123456789abcdef0123456789abcdef00' },
      { generation: '' },
      { visibility: 'public' as const },
      { port: 3001 },
      { binding: { ...binding, bootId: 'new-boot' } },
      { binding: { ...binding, projectRevision: 2 } },
      { binding: { ...binding, hostId: 'new-host' } },
    ])
      expect(() =>
        checkPublicationReceipt(
          { ...receipt, ...changed },
          submitted,
          'example.net',
        ),
      ).toThrow(/original draft and request ID are kept/);
  });

  it('rejects an internally consistent allocation on a domain that was not reviewed', () => {
    const another = {
      ...receipt,
      domain: 'example.org',
      hostname: `${receipt.name}-${receipt.generation}.example.org`,
    };
    expect(allocatedPublicationHostname(another)).toBe(another.hostname);
    expect(() =>
      checkPublicationReceipt(another, submitted, 'example.net'),
    ).toThrow(/original draft/);
    expect(() => checkPublicationReceipt(receipt, submitted, '')).toThrow(
      /original draft/,
    );
  });

  it('accepts direct children of public suffixes without changing the generated-hostname contract', () => {
    const another = {
      ...receipt,
      domain: 'github.io',
      hostname: `${receipt.name}-${receipt.generation}.github.io`,
    };
    expect(
      checkPublicationReceipt(
        another,
        { ...submitted, domain: 'github.io' },
        'github.io',
      ),
    ).toBe(another);
    expect(
      allocatedPublicationHostname({
        ...another,
        hostname: `nested.${another.hostname}`,
      }),
    ).toBeUndefined();
  });

  it('keeps retry, resume and removal receipts bound to the original generation and identity', () => {
    expect(
      checkPublicationActionReceipt(
        { ...receipt, state: 'provisioning' },
        receipt,
      ),
    ).toMatchObject({ generation: receipt.generation });
    for (const change of [
      { id: 'another-publication' },
      {
        generation: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
        hostname: 'app-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.example.net',
      },
      { binding: { ...binding, bootId: 'replacement-boot' } },
    ])
      expect(() =>
        checkPublicationActionReceipt({ ...receipt, ...change }, receipt),
      ).toThrow(/original/);
  });

  it('rejects incomplete restored targets and copies the captured binding', () => {
    const context = { id: 'intent-1', binding, port: 3000 };
    const restored = requestedPublication(context);
    expect(restored).toEqual(context);
    expect(restored!.binding).not.toBe(binding);
    for (const invalid of [
      null,
      {},
      { ...context, port: 80 },
      { ...context, port: 65536 },
      { ...context, binding: { ...binding, bootId: '' } },
      { ...context, binding: { ...binding, volumeRevision: 1.5 } },
    ])
      expect(requestedPublication(invalid)).toBeUndefined();
  });

  it('exposes addresses only for published receipts with a matching clean HTTPS hostname', () => {
    expect(safePublicationURL(receipt)).toBe(
      'https://app-0123456789abcdef0123456789abcdef.example.net',
    );
    for (const state of [
      'provisioning',
      'unavailable',
      'removing',
      'removed',
      'error',
    ] as const)
      expect(safePublicationURL({ ...receipt, state })).toBeUndefined();
    for (const url of [
      'javascript:alert(1)',
      'http://app.apps.example.com',
      'https://other.example.com',
      'https://owner:secret@app.apps.example.com',
      'https://app-0123456789abcdef0123456789abcdef.example.net/?token=secret',
      'https://app-0123456789abcdef0123456789abcdef.example.net/path',
    ])
      expect(safePublicationURL({ ...receipt, url })).toBeUndefined();
  });
});
