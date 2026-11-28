import { Button } from '@cofy-x/axv-nextstep';
import { targetLabel } from './CloudflarePublicationForm';
import type { CloudflarePublication } from './types';

const stateLabels: Record<CloudflarePublication['state'], string> = {
  provisioning: 'Publishing…',
  published: 'Published',
  unavailable: 'Target unavailable',
  removing: 'Removing…',
  removed: 'Removed',
  error: 'Needs attention',
};

function stateTone(hasError: boolean, state: CloudflarePublication['state']) {
  if (hasError) return 'unknown';
  if (state === 'published') return 'healthy';
  if (state === 'provisioning' || state === 'removing') return 'pending';
  if (state === 'error' || state === 'unavailable') return 'error';
  return 'off';
}

interface CloudflarePublicationCardProps {
  record: CloudflarePublication;
  listError: boolean;
  disabled: boolean;
  phase?: string;
  url?: string;
  onAction: (
    record: CloudflarePublication,
    action: 'unpublish' | 'retry',
  ) => void;
}

export function CloudflarePublicationCard({
  record,
  listError,
  disabled,
  phase,
  url,
  onAction,
}: CloudflarePublicationCardProps) {
  const tone = stateTone(listError, record.state);
  return (
    <article className="cloudflare-card cloudflare-publication">
      <div className="cloudflare-section-heading">
        <div className="cloudflare-publication-title">
          <h3>{record.hostname}</h3>
          <span
            className={`cloudflare-visibility-badge is-${record.visibility}`}
          >
            {record.visibility === 'public' ? 'Public' : 'Owner only'}
          </span>
        </div>
        <strong className={`cloudflare-pill is-${tone}`}>
          {listError ? 'Unknown' : stateLabels[record.state]}
        </strong>
      </div>
      <p className="cloudflare-publication-meta">{targetLabel(record)}</p>
      {phase && <p className="fine-print">{phase}</p>}
      {record.error && <p className="app-error">{record.error}</p>}
      {record.cleanupPending && (
        <p className="cloudflare-notice">
          Cloudflare cleanup is pending. Retry cleanup removes only this
          publication’s cloud resources; AXV access stays closed.
        </p>
      )}
      {!listError && record.state === 'published' && url && (
        <div className="cloudflare-url-card">
          <a href={url} target="_blank" rel="noopener noreferrer">
            Open {record.hostname}
          </a>
        </div>
      )}
      <div className="cloudflare-actions">
        {record.state !== 'removed' && record.state !== 'removing' && (
          <Button
            disabled={disabled}
            onClick={() => onAction(record, 'unpublish')}
          >
            Unpublish
          </Button>
        )}
        {(record.state === 'error' ||
          record.state === 'unavailable' ||
          record.cleanupPending) && (
          <Button disabled={disabled} onClick={() => onAction(record, 'retry')}>
            {record.cleanupPending
              ? 'Retry cleanup'
              : record.phase === 'restart-required'
                ? 'Resume'
                : 'Retry'}
          </Button>
        )}
      </div>
      <details className="cloudflare-disclosure is-nested">
        <summary>Details</summary>
        <div className="cloudflare-disclosure-body">
          <dl className="info-list">
            <dt>Name</dt>
            <dd>{record.name}</dd>
            <dt>Allocation domain</dt>
            <dd>{record.domain}</dd>
            <dt>Request ID</dt>
            <dd>{record.requestId}</dd>
            <dt>Host</dt>
            <dd>{record.binding.hostId}</dd>
            <dt>Boot</dt>
            <dd>{record.binding.bootId}</dd>
          </dl>
        </div>
      </details>
    </article>
  );
}
