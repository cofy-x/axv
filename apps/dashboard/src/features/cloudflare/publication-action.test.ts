import { Fragment, createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { publicationActionReview } from './publication-action';
import type { CloudflarePublication } from './types';

const original: CloudflarePublication = {
  id: 'original-publication',
  requestId: 'original-request',
  name: 'editor',
  generation: '0123456789abcdef0123456789abcdef',
  domain: 'example.net',
  hostname: 'editor-0123456789abcdef0123456789abcdef.example.net',
  port: 3000,
  binding: {
    hostId: 'original-host',
    projectId: 'original-project',
    projectRevision: 1,
    volumeId: 'original-volume',
    volumeRevision: 2,
    machineId: 'original-machine',
    bootId: 'original-boot',
    instanceId: 'original-instance',
  },
  visibility: 'owner',
  state: 'error',
  phase: 'ingress',
  cleanupPending: false,
  createdAt: '2026-10-24T00:00:00Z',
  updatedAt: '2026-10-24T00:00:00Z',
};

describe('publication action consent', () => {
  it.each([
    'verify-target',
    'dns',
    'ingress',
    'configuration',
    'origin',
    'access',
    'retry-original',
    'restore-original',
    'restart-required',
    'persistence',
    'complete',
    'unknown-future-phase',
  ])(
    'requires original target and visibility review for retry of retained phase %s',
    (phase) => {
      for (const visibility of ['owner', 'public'] as const) {
        for (const state of ['error', 'unavailable'] as const) {
          const record = { ...original, phase, visibility, state };
          const review = publicationActionReview(record, 'retry');
          expect(review?.acceptedButton).toBe('resume');
          expect(review?.dialog.title).toBe('Resume publication?');
          expect(review?.dialog.buttons).toEqual([
            { label: 'Cancel', value: 'cancel', cancel: true, default: true },
            { label: 'Resume', value: 'resume' },
          ]);
          const html = renderToStaticMarkup(
            createElement(Fragment, null, review?.dialog.message),
          );
          for (const value of [
            original.hostname,
            original.binding.hostId,
            original.binding.projectId,
            original.binding.machineId,
            original.binding.bootId,
            String(original.port),
          ])
            expect(html).toContain(value);
          expect(html).toContain('hostname and generation stay unchanged');
          expect(html).toContain(
            visibility === 'public'
              ? 'Anyone on the internet can reach this service after it resumes.'
              : 'Owner only — Cloudflare Access required',
          );
        }
      }
    },
  );

  it('keeps permanent removal cleanup separate even when its retained phase is stale', () => {
    for (const phase of ['remove-dns', 'retry-original', 'restart-required'])
      expect(
        publicationActionReview(
          { ...original, phase, cleanupPending: true },
          'retry',
        ),
      ).toBeUndefined();
  });

  it('still reviews withdrawal of the original hostname separately from cleanup', () => {
    const review = publicationActionReview(
      { ...original, cleanupPending: true },
      'unpublish',
    );
    expect(review?.acceptedButton).toBe('unpublish');
    expect(review?.dialog.title).toBe('Unpublish hostname?');
    expect(review?.dialog.message).toContain(original.hostname);
    expect(review?.dialog.message).toContain(
      'The VM and its service keep running.',
    );
    expect(review?.dialog.buttons?.[0]).toMatchObject({
      cancel: true,
      default: true,
    });
  });
});
