import type { CloudflareStatus } from './types';

export function CloudflareDiagnostics({
  status,
  error,
  unavailable = false,
}: {
  status?: CloudflareStatus;
  error?: string;
  unavailable?: boolean;
}) {
  if (unavailable)
    return <p>Refresh access status before loading diagnostics.</p>;
  return (
    <>
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      {error && status && (
        <p>Showing previous diagnostics. Refresh to check the current state.</p>
      )}
      {status ? (
        <>
          {status.error && (
            <p className="app-error" role="alert">
              {status.error}
            </p>
          )}
          <dl className="info-list">
            <dt>Origin address</dt>
            <dd>
              <span>{status.listen || 'Not configured'}</span>
              <span className="cloudflare-origin-note fine-print">
                {error
                  ? 'Previous address; current state is unknown.'
                  : status.state === 'listening'
                    ? 'Listening on this computer.'
                    : status.state === 'disabled'
                      ? 'Configured address; the entrypoint is off.'
                      : 'Configured address; listening is not confirmed.'}
              </span>
            </dd>
            <dt>Instance</dt>
            <dd>{status.instanceId}</dd>
          </dl>
        </>
      ) : (
        !error && <p role="status">Loading diagnostics…</p>
      )}
    </>
  );
}
