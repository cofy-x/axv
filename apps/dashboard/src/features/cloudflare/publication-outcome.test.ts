import { describe, expect, it } from 'vitest';
import { APIError } from '../../shared/api/http';
import { unconfirmedPublicationRequest } from './publication-outcome';

describe('publication acceptance certainty', () => {
  it('does not report possible acceptance for explicit validation, permission or configuration refusals', () => {
    for (const [status, code] of [
      [400, 'invalid_publication_request'],
      [400, 'publication_hostname_forbidden'],
      [400, 'publication_desktop_origin'],
      [403, 'publication_forbidden'],
      [409, 'publication_instance_changed'],
      [409, 'publication_binding_unavailable'],
      [409, 'publication_config_busy'],
      [409, 'publication_not_configured'],
      [409, 'publication_access_not_configured'],
      [409, 'publication_configuration_changed'],
      [409, 'publication_hostname_busy'],
      [409, 'publication_site_retired'],
      [409, 'publication_desktop_site_conflict'],
      [400, 'publication_cookie_site'],
      [429, 'publication_capacity'],
      [401, 'authentication_required'],
      [403, 'cloudflare_forbidden'],
      [403, 'remote_forbidden'],
      [403, 'remote_local_only'],
      [403, 'local_only'],
      [403, 'sandbox_request_forbidden'],
    ] as const)
      expect(
        unconfirmedPublicationRequest(
          'request-1',
          '',
          new APIError('Rejected before admission', status, code),
        ),
      ).toBe('');
  });

  it('keeps timeout, transport, server, unknown 4xx and mismatched receipt outcomes', () => {
    for (const error of [
      new DOMException('Read timed out', 'TimeoutError'),
      new DOMException('View hidden', 'AbortError'),
      new TypeError('Failed to fetch'),
      new Error('A different publication receipt was returned'),
      new SyntaxError('Response JSON was incomplete'),
      new APIError('Write response lost', 500, 'publication_save_failed'),
      new APIError('Server unavailable', 503, 'publication_unavailable'),
      new APIError('Shutdown', 503, 'publication_stopping'),
      new APIError('Conflicting original request', 409, 'idempotency_conflict'),
      new APIError('Unknown validation result', 400, 'http_error'),
      new APIError('Unknown denial', 403, 'http_error'),
      new APIError(
        'Same code with unexpected status',
        503,
        'publication_forbidden',
      ),
    ])
      expect(unconfirmedPublicationRequest('request-1', '', error)).toBe(
        'request-1',
      );
  });

  it('preserves a previous unknown outcome when its retry is rejected before admission', () => {
    const rejection = new APIError(
      'Publishing is no longer configured',
      409,
      'publication_not_configured',
    );
    const first = unconfirmedPublicationRequest(
      'request-1',
      '',
      new TypeError('Connection lost after sending'),
    );
    expect(unconfirmedPublicationRequest('request-1', first, rejection)).toBe(
      'request-1',
    );
    expect(unconfirmedPublicationRequest('new-request', first, rejection)).toBe(
      '',
    );
  });
});
