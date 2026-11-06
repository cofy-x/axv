import type { PushDelivery } from './types';

export function pushDeliveryLabel(status: string) {
  return (
    (
      {
        pending: 'Queued',
        sending: 'Sending',
        accepted: 'Accepted by push service',
        uncertain: 'Delivery uncertain',
        failed: 'Delivery failed',
        disabled: 'Disabled before send',
        expired: 'Delivery expired',
        superseded: 'Superseded before send',
      } as Record<string, string>
    )[status] || 'Delivery unavailable'
  );
}

export function PushDeliveryDetails({
  deliveries,
}: {
  deliveries?: PushDelivery[];
}) {
  if (!deliveries?.length) return null;
  return (
    <ul
      aria-label="Web Push delivery history"
      className="notification-push-deliveries"
    >
      {deliveries.map((delivery) => (
        <li key={`${delivery.subscriptionId}:${delivery.keyId}`}>
          <strong>Web Push: {pushDeliveryLabel(delivery.status)}</strong>
          <p className="fine-print">
            Push service acceptance does not confirm that this browser displayed
            or read an alert.
          </p>
          <details>
            <summary>Browser delivery details</summary>
            <dl className="info-list">
              <dt>Registration</dt>
              <dd>
                <code>{delivery.subscriptionId}</code>
              </dd>
              <dt>Sending key</dt>
              <dd>
                <code>{delivery.keyId}</code>
              </dd>
              <dt>Attempts</dt>
              <dd>{delivery.attempts}</dd>
              <dt>Expires</dt>
              <dd>
                <time dateTime={delivery.expiresAt}>
                  {new Date(delivery.expiresAt).toLocaleString()}
                </time>
              </dd>
              {delivery.nextAttemptAt && (
                <>
                  <dt>Next attempt</dt>
                  <dd>
                    <time dateTime={delivery.nextAttemptAt}>
                      {new Date(delivery.nextAttemptAt).toLocaleString()}
                    </time>
                  </dd>
                </>
              )}
            </dl>
            {delivery.status === 'uncertain' && (
              <p>
                A previous request may have been accepted. A scheduled retry can
                display a duplicate; delivery is best-effort.
              </p>
            )}
            {delivery.status === 'expired' && (
              <p>
                {delivery.error === 'subscription_expired'
                  ? 'The push service retired this browser subscription. Unsubscribe and explicitly subscribe again.'
                  : 'The finite delivery window elapsed. AXV will not retry this alert.'}
              </p>
            )}
            {delivery.error && (
              <p>
                Review this browser’s registration and delivery settings.
                Provider response details are not exposed.
              </p>
            )}
          </details>
        </li>
      ))}
    </ul>
  );
}
