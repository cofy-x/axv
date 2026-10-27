import { APIError } from '../../shared/api/http';

// These responses precede journal admission in the publication manager or its
// desktop authentication guards. Other 4xx responses can describe an existing
// request, so status alone never establishes that publication was rejected.
const preAcceptanceRefusals = new Map<string, readonly number[]>([
  ['invalid_publication_request', [400]],
  ['publication_hostname_forbidden', [400]],
  ['publication_desktop_origin', [400]],
  ['publication_forbidden', [403]],
  ['publication_instance_changed', [409]],
  ['publication_binding_unavailable', [409]],
  ['publication_config_busy', [409]],
  ['publication_not_configured', [409]],
  ['publication_access_not_configured', [409]],
  ['publication_configuration_changed', [409]],
  ['publication_hostname_busy', [409]],
  ['publication_site_retired', [409]],
  ['publication_desktop_site_conflict', [409]],
  ['publication_cookie_site', [400]],
  ['publication_capacity', [429]],
  ['authentication_required', [401]],
  ['cloudflare_forbidden', [403]],
  ['remote_forbidden', [403]],
  ['remote_local_only', [403]],
  ['local_only', [403]],
  ['sandbox_request_forbidden', [403]],
]);

/** A refusal of this attempt cannot resolve a previous unknown submission. */
export function unconfirmedPublicationRequest(
  requestId: string,
  previouslyUnconfirmedId: string,
  error: unknown,
) {
  if (previouslyUnconfirmedId === requestId) return requestId;
  const rejected =
    error instanceof APIError &&
    preAcceptanceRefusals.get(error.code)?.includes(error.status) === true;
  return rejected ? '' : requestId;
}
