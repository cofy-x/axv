import { useLayoutEffect, useRef } from 'react';
import { IconButton } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { canTransferWindowFocus } from '../../shared/desktop/focus-transfer';
import { NotificationDetails } from './NotificationDetails';
import { deliveryLabel, kindLabel, pushDeliveryLabel } from './presentation';
import type { NotificationReference, NotificationSnapshot } from './types';

function readReference(value: unknown): NotificationReference | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const { storeId, notificationId } = value as NotificationReference;
  if (typeof storeId === 'string' && typeof notificationId === 'string')
    return value as NotificationReference;
}

function sameReference(
  reference: NotificationReference | undefined,
  other: NotificationReference,
) {
  return (
    reference?.storeId === other.storeId &&
    reference.notificationId === other.notificationId
  );
}

type FocusRequest = {
  source: HTMLButtonElement;
} & (
  | { view: 'details'; reference: NotificationReference }
  | { view: 'history'; row?: HTMLButtonElement }
);

export function NotificationHistory({
  window,
  desktop,
  active,
  visible,
  resource,
}: Pick<AppWindowProps, 'window' | 'desktop'> & {
  active: boolean;
  visible: boolean;
  resource: {
    value?: NotificationSnapshot;
    error: string;
    loading: boolean;
    refresh: () => void;
  };
}) {
  const snapshot = resource.value;
  const reference = readReference(window.viewState.notificationReference);
  const historyHeading = useRef<HTMLHeadingElement>(null);
  const detailHeading = useRef<HTMLHeadingElement>(null);
  const openedFrom = useRef<{
    reference: NotificationReference;
    row: HTMLButtonElement;
  }>(null);
  const focusRequest = useRef<FocusRequest>(null);

  useLayoutEffect(() => {
    const request = focusRequest.current;
    focusRequest.current = null;
    if (!active || !request) return;
    const target =
      request.view === 'details'
        ? sameReference(reference, request.reference)
          ? detailHeading.current
          : null
        : !reference
          ? request.row?.isConnected
            ? request.row
            : historyHeading.current
          : null;
    if (canTransferWindowFocus(target, request.source)) target?.focus();
  }, [active, reference]);

  function showEvent(notificationId: string, row: HTMLButtonElement) {
    if (!active || !snapshot) return;
    row.focus({ preventScroll: true });
    const selected = { storeId: snapshot.storeId, notificationId };
    openedFrom.current = { reference: selected, row };
    focusRequest.current = {
      view: 'details',
      reference: selected,
      source: row,
    };
    desktop.updateWindowState(window.id, (state) => ({
      ...state,
      notificationReference: selected,
    }));
  }

  function showHistory(button: HTMLButtonElement) {
    if (!active || !reference) return;
    button.focus({ preventScroll: true });
    const origin = openedFrom.current;
    openedFrom.current = null;
    focusRequest.current = {
      view: 'history',
      source: button,
      row:
        origin && sameReference(origin.reference, reference)
          ? origin.row
          : undefined,
    };
    desktop.updateWindowState(window.id, (state) =>
      sameReference(readReference(state.notificationReference), reference)
        ? { ...state, notificationReference: undefined }
        : state,
    );
  }

  return (
    <div
      className="notification-history-pane"
      hidden={!visible}
      inert={!active}
    >
      <div
        className="app-pad notifications-content"
        hidden={!!reference}
        inert={!!reference}
      >
        <div className="notification-heading">
          <h2 ref={historyHeading} tabIndex={-1}>
            Delivery history
          </h2>
          <IconButton
            icon="refresh"
            label="Refresh history"
            disabled={!active || resource.loading}
            onClick={resource.refresh}
          />
        </div>
        {snapshot?.canConfigure === false && (
          <p className="notification-note">
            Manage Pocket Alert settings on the computer running AXV.
          </p>
        )}
        {snapshot?.nextSendAt && (
          <p className="notification-error" role="status">
            Pocket Alert quota cooldown until{' '}
            <time dateTime={snapshot.nextSendAt}>
              {new Date(snapshot.nextSendAt).toLocaleString()}
            </time>
            . All queued deliveries wait until this time.
          </p>
        )}
        {snapshot?.intake.gap && (
          <p className="notification-error" role="alert">
            Source history has a gap. Some events are missing.
          </p>
        )}
        {snapshot?.intake.error && (
          <p className="notification-error" role="alert">
            Notification intake is unavailable. Retained history may be
            incomplete.
          </p>
        )}
        {!snapshot && !resource.error && <p role="status">Loading history…</p>}
        {snapshot?.events.length === 0 && <p>No notifications recorded.</p>}
        {!!snapshot?.events.length && (
          <ul
            className="notification-history"
            role="list"
            aria-label="Delivery events"
          >
            {snapshot.events.map((event) => {
              const pushStatuses = new Set(
                event.pushDeliveries?.map((delivery) =>
                  pushDeliveryLabel(delivery.status),
                ),
              );
              return (
                <li key={`${snapshot.storeId}:${event.id}`}>
                  <button
                    type="button"
                    className="notification-history-item"
                    onClick={(click) =>
                      showEvent(event.id, click.currentTarget)
                    }
                  >
                    <strong>{kindLabel(event.kind)}</strong>
                    <time dateTime={event.createdAt}>
                      {new Date(event.createdAt).toLocaleString()}
                    </time>
                    <span className="notification-event-status">
                      <span>{deliveryLabel(event)}</span>
                      {[...pushStatuses].map((label) => (
                        <span key={label}>Web Push: {label}</span>
                      ))}
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </div>
      {reference && (
        <div
          className="app-pad notifications-content"
          key={`${reference.storeId}:${reference.notificationId}`}
        >
          <NotificationDetails
            reference={reference}
            active={active}
            desktop={desktop}
            headingRef={detailHeading}
            onBack={showHistory}
          />
        </div>
      )}
    </div>
  );
}
