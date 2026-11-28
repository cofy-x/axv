import { Button } from '@cofy-x/axv-nextstep';
import type { CloudflareHealth } from './types';

interface CloudflareTunnelCardProps {
  interactive: boolean;
  canPublish: boolean;
  canHealth: boolean;
  healthValue?: CloudflareHealth;
  healthError?: string;
  desktopListening: boolean;
  onPublish: () => void;
}

const healthLabels: Record<CloudflareHealth['state'], string> = {
  unconfigured: 'Not configured',
  healthy: 'Healthy',
  degraded: 'Degraded',
  error: 'Needs attention',
};

function healthTone(
  canHealth: boolean,
  healthError: string | undefined,
  state?: CloudflareHealth['state'],
) {
  if (healthError || !canHealth) return 'unknown';
  if (!state) return 'pending';
  if (state === 'healthy') return 'healthy';
  if (state === 'degraded') return 'pending';
  if (state === 'error') return 'error';
  return 'off';
}

export function CloudflareTunnelCard({
  interactive,
  canPublish,
  canHealth,
  healthValue,
  healthError,
  desktopListening,
  onPublish,
}: CloudflareTunnelCardProps) {
  const tone = healthTone(canHealth, healthError, healthValue?.state);
  const label =
    healthError || !canHealth
      ? 'Unknown'
      : healthValue
        ? healthLabels[healthValue.state]
        : 'Checking…';

  return (
    <section
      className="cloudflare-card cloudflare-cloud-status"
      aria-labelledby="cloudflare-tunnel-title"
    >
      <div className="cloudflare-section-heading">
        <h2 id="cloudflare-tunnel-title">Tunnel</h2>
        <strong className={`cloudflare-pill is-${tone}`}>{label}</strong>
      </div>
      {healthError && (
        <p className="app-error" role="alert">
          {healthError}
        </p>
      )}
      {canHealth && healthValue && (
        <div className="cloudflare-card-body">
          <p>
            {healthValue.state === 'healthy'
              ? 'Token and DNS read access checked.'
              : healthValue.message}
          </p>
          {healthValue.tunnelName && (
            <p className="cloudflare-tunnel-meta">
              Tunnel: <strong>{healthValue.tunnelName}</strong>
              {healthValue.tunnelStatus && healthValue.state !== 'healthy'
                ? ` · ${healthValue.tunnelStatus}`
                : ''}
            </p>
          )}
          <p className="fine-print">
            Checked{' '}
            <time
              dateTime={healthValue.checkedAt}
              title={new Date(healthValue.checkedAt).toLocaleString()}
            >
              {new Date(healthValue.checkedAt).toLocaleString(undefined, {
                dateStyle: 'medium',
                timeStyle: 'short',
              })}
            </time>
          </p>
          {healthValue.state === 'healthy' && !desktopListening && (
            <p className="fine-print">Public access has not been verified.</p>
          )}
        </div>
      )}
      <div className="cloudflare-card-footer">
        <div className="cloudflare-actions">
          <Button disabled={!interactive || !canPublish} onClick={onPublish}>
            Publish a service
          </Button>
        </div>
      </div>
    </section>
  );
}
