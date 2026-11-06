import { useCallback } from 'react';
import { Button } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { notificationsAPI } from './api';
import { PushDeliveryDetails } from './PushDeliveryDetails';
import { deliveryExplanation, deliveryLabel, kindLabel } from './presentation';
import { useNotificationResource } from './useNotificationResource';
import type { NotificationReference, NotificationTarget } from './types';

export function TargetIdentity({ target }: { target: NotificationTarget }) {
  return (
    <dl className="info-list notification-identity">
      {Object.entries(target)
        .filter(([, value]) => value !== undefined && value !== '')
        .map(([key, value]) => (
          <div key={key}>
            <dt>
              {(
                {
                  kind: 'Work',
                  storeId: 'Source store',
                  sessionId: 'Session',
                  runId: 'Run',
                  threadId: 'Conversation',
                  turnId: 'Turn',
                  eventId: 'Event',
                  hostId: 'Host',
                  projectId: 'Project',
                  taskId: 'Task',
                  attemptId: 'Attempt',
                } as Record<string, string>
              )[key] || key}
            </dt>
            <dd>{value}</dd>
          </div>
        ))}
    </dl>
  );
}

export function NotificationDetails({
  reference,
  active,
  desktop,
}: {
  reference: NotificationReference;
  active: boolean;
  desktop: DesktopAPI;
}) {
  const { storeId, notificationId } = reference;
  const read = useCallback(
    (signal: AbortSignal) =>
      notificationsAPI.detail({ storeId, notificationId }, signal),
    [storeId, notificationId],
  );
  const resource = useNotificationResource(read, active, 5000);
  const detail = resource.value;
  return (
    <section aria-label="Notification details" className="notification-details">
      <div className="notification-heading">
        <h2>Notification details</h2>
        <Button
          disabled={!active || resource.loading}
          onClick={resource.refresh}
        >
          Refresh details
        </Button>
      </div>
      {resource.error && <p role="alert">{resource.error}</p>}
      {!detail && !resource.error && <p role="status">Loading notification…</p>}
      {detail && (
        <>
          <p>
            <strong>{kindLabel(detail.event.kind)}</strong> ·{' '}
            {detail.event.pushDeliveries?.length ? 'Pocket Alert: ' : ''}
            {deliveryLabel(detail.event)}
          </p>
          <PushDeliveryDetails deliveries={detail.event.pushDeliveries} />
          {deliveryExplanation(detail.event) && (
            <p>{deliveryExplanation(detail.event)}</p>
          )}
          <p>
            {resource.error
              ? 'The original event could not be verified. Its recorded identity is retained.'
              : !detail.resolution.available
                ? 'The original work is unavailable. AXV will keep this recorded identity.'
                : !detail.resolution.current
                  ? 'This event is stale. Open the original work to inspect its current state.'
                  : 'The original event is still current.'}
          </p>
          <details>
            <summary>Recorded identity</summary>
            <TargetIdentity target={detail.event.target} />
          </details>
          <div className="app-actions">
            <Button
              disabled={
                !active || !!resource.error || !detail.resolution.available
              }
              onClick={() =>
                desktop.openApp('notification-work', {
                  storeId: reference.storeId,
                  notificationId: reference.notificationId,
                })
              }
            >
              Open original work
            </Button>
          </div>
        </>
      )}
    </section>
  );
}
