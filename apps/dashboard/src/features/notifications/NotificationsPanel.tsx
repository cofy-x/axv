import { useEffect, useRef, useState } from 'react';
import { Button, Checkbox, Field, StatusBar, Tabs } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { notificationError, notificationsAPI } from './api';
import { NotificationDetails } from './NotificationDetails';
import { BrowserPushPanel } from './BrowserPushPanel';
import { PushDeliveryDetails } from './PushDeliveryDetails';
import { deliveryExplanation, deliveryLabel, kindLabel } from './presentation';
import { useNotificationResource } from './useNotificationResource';
import { notificationKinds } from './types';
import type {
  NotificationConfig,
  NotificationReference,
  NotificationSnapshot,
} from './types';

function sameConfig(left: NotificationConfig, right: NotificationConfig) {
  return (
    left.enabled === right.enabled &&
    left.applicationId === right.applicationId &&
    left.deviceId === right.deviceId &&
    left.returnOrigin === right.returnOrigin &&
    [...left.eventTypes].sort().join(',') ===
      [...right.eventTypes].sort().join(',')
  );
}

function referenceValue(value: unknown): NotificationReference | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const candidate = value as NotificationReference;
  return typeof candidate.storeId === 'string' &&
    typeof candidate.notificationId === 'string'
    ? candidate
    : undefined;
}

export function NotificationsPanel({
  window,
  desktop,
  interactive = true,
}: AppWindowProps) {
  const resource = useNotificationResource(
    notificationsAPI.snapshot,
    interactive,
    5000,
  );
  const snapshot = resource.value;
  const [baseline, setBaseline] = useState<NotificationSnapshot>();
  const [draft, setDraft] = useState<NotificationConfig>();
  const [token, setToken] = useState('');
  const [clearToken, setClearToken] = useState(false);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const operation = useRef<AbortController | null>(null);
  const allowed = snapshot?.canConfigure === true;
  const tab =
    window.viewState.notificationTab === 'browser'
      ? 'browser'
      : window.viewState.notificationTab === 'history' || !allowed
        ? 'history'
        : 'settings';
  const reference = referenceValue(window.viewState.notificationReference);
  const dirty =
    !!token ||
    clearToken ||
    !!(draft && baseline && !sameConfig(draft, baseline.config));
  useEffect(() => {
    if (snapshot && !baseline) {
      setBaseline(snapshot);
      setDraft({
        ...snapshot.config,
        eventTypes: [...snapshot.config.eventTypes],
      });
    }
  }, [snapshot, baseline]);
  useEffect(() => () => operation.current?.abort(), []);
  useEffect(() => {
    if (snapshot && !snapshot.canConfigure) {
      setToken('');
      setClearToken(false);
    }
  }, [snapshot]);
  useEffect(
    () =>
      desktop.beforeClose(window.id, async () => {
        if (!dirty) return true;
        const answer = await desktop.dialog({
          title: 'Discard notification settings?',
          message:
            'Unsaved notification settings will be lost. Saved settings and delivery history are kept.',
          buttons: [
            { label: 'Cancel', value: 'cancel', cancel: true, default: true },
            { label: 'Discard settings', value: 'discard' },
          ],
        });
        return answer?.button === 'discard';
      }),
    [desktop, window.id, dirty],
  );

  function edit(patch: Partial<NotificationConfig>) {
    if (!draft) return;
    setDraft({ ...draft, ...patch });
    setError('');
    setMessage('');
  }

  async function change(action: 'save' | 'test' | 'disable') {
    if (
      !interactive ||
      !allowed ||
      !snapshot ||
      !baseline ||
      !draft ||
      operation.current ||
      resource.error
    )
      return;
    const controller = new AbortController();
    operation.current = controller;
    setBusy(action);
    setError('');
    setMessage('');
    const signal = AbortSignal.any([
      controller.signal,
      AbortSignal.timeout(30000),
    ]);
    try {
      const identity = {
        storeId: baseline.storeId,
        revision: baseline.revision,
      };
      const values =
        action === 'save'
          ? await notificationsAPI.save(
              {
                ...identity,
                enabled: draft.enabled,
                applicationId: draft.applicationId,
                deviceId: draft.deviceId,
                returnOrigin: draft.returnOrigin,
                eventTypes: draft.eventTypes,
                tokenAction: clearToken ? 'clear' : token ? 'replace' : 'keep',
                ...(token ? { token } : {}),
              },
              signal,
            )
          : action === 'test'
            ? await notificationsAPI.test(identity, signal)
            : await notificationsAPI.disable(identity, signal);
      signal.throwIfAborted();
      resource.accept(values);
      setBaseline(values);
      if (action === 'save' || !dirty)
        setDraft({
          ...values.config,
          eventTypes: [...values.config.eventTypes],
        });
      else if (action === 'disable')
        setDraft((current) =>
          current ? { ...current, enabled: false } : current,
        );
      setClearToken(false);
      setMessage(
        action === 'save'
          ? 'Settings saved.'
          : action === 'disable'
            ? 'Notifications disabled. Previously accepted deliveries cannot be recalled.'
            : 'Test recorded. Check History for its delivery outcome.',
      );
    } catch (cause) {
      if (!controller.signal.aborted) setError(notificationError(cause));
      resource.refresh();
    } finally {
      if (!controller.signal.aborted) {
        setToken('');
        setBusy('');
      }
      operation.current = null;
    }
  }

  const selectTab = (next: string) =>
    desktop.updateWindowState(window.id, (state) => ({
      ...state,
      notificationTab: next,
    }));
  const selectEvent = (notificationId: string) =>
    snapshot &&
    desktop.updateWindowState(window.id, (state) => ({
      ...state,
      notificationReference: { storeId: snapshot.storeId, notificationId },
    }));

  async function reloadSavedSettings() {
    if (!snapshot) return;
    const answer =
      !dirty ||
      (
        await desktop.dialog({
          title: 'Reload saved notification settings?',
          message:
            'This replaces your unsaved notification settings with the saved configuration.',
          buttons: [
            { label: 'Cancel', value: 'cancel', cancel: true, default: true },
            { label: 'Reload settings', value: 'reload' },
          ],
        })
      )?.button === 'reload';
    if (!answer) return;
    setBaseline(snapshot);
    setDraft({
      ...snapshot.config,
      eventTypes: [...snapshot.config.eventTypes],
    });
    setToken('');
    setClearToken(false);
    setError('');
  }

  const currentStore =
    !snapshot || !baseline || baseline.storeId === snapshot.storeId;
  return (
    <div className="notifications-app">
      <Tabs
        label="Notification sections"
        value={tab}
        onChange={selectTab}
        tabs={[
          ...(allowed
            ? [{ id: 'settings', label: 'Settings', disabled: !interactive }]
            : []),
          { id: 'browser', label: 'This browser', disabled: !interactive },
          { id: 'history', label: 'History', disabled: !interactive },
        ]}
      >
        {resource.error && (
          <p className="app-error" role="alert">
            {resource.error} Retained history may be stale.
          </p>
        )}
        {!currentStore && (
          <p className="app-error" role="alert">
            Notification storage changed. Close this window and reopen it to
            review the new store. The unsaved settings are retained here.
          </p>
        )}
        {snapshot?.error && (
          <p className="app-error" role="alert">
            Notification delivery needs attention. Review the saved settings and
            delivery history.
          </p>
        )}
        {allowed && (
          <div
            className="app-pad notifications-content"
            hidden={tab !== 'settings'}
            inert={!interactive || tab !== 'settings'}
          >
            <div className="notification-heading">
              <h2>Pocket Alert</h2>
              <Button
                disabled={!interactive || !!busy || resource.loading}
                onClick={resource.refresh}
              >
                Refresh
              </Button>
            </div>
            <p className="fine-print">
              Optional notifications for agent requests and completed work.
              Delivery history stays in AXV.
            </p>
            {!draft ? (
              <p role="status">Loading settings…</p>
            ) : (
              <fieldset
                disabled={
                  !interactive ||
                  !allowed ||
                  !!busy ||
                  !currentStore ||
                  !!resource.error
                }
              >
                <legend>Delivery settings</legend>
                <Checkbox
                  label="Enable Pocket Alert notifications"
                  checked={draft.enabled}
                  onChange={(event) => edit({ enabled: event.target.checked })}
                />
                <Field
                  label="Application ID"
                  value={draft.applicationId}
                  autoComplete="off"
                  onChange={(event) =>
                    edit({ applicationId: event.target.value })
                  }
                />
                <Field
                  label="Device ID"
                  value={draft.deviceId}
                  autoComplete="off"
                  onChange={(event) => edit({ deviceId: event.target.value })}
                />
                <Field
                  label={
                    baseline?.config.tokenConfigured
                      ? 'Replacement API token'
                      : 'API token'
                  }
                  type="password"
                  value={token}
                  autoComplete="new-password"
                  spellCheck={false}
                  disabled={clearToken}
                  onChange={(event) => {
                    setToken(event.target.value);
                    setError('');
                    setMessage('');
                  }}
                />
                <p className="fine-print">
                  {baseline?.config.tokenConfigured
                    ? 'A token is saved on the server. Leave this field empty to keep it.'
                    : 'No token is saved. Enter a Pocket Alert API token before enabling delivery.'}{' '}
                  Tokens are never shown after saving.
                </p>
                {baseline?.config.tokenConfigured && (
                  <Checkbox
                    label="Remove saved token on Save"
                    checked={clearToken}
                    onChange={(event) => {
                      setClearToken(event.target.checked);
                      setToken('');
                    }}
                  />
                )}
                <Field
                  label="Return-link origin"
                  type="url"
                  value={draft.returnOrigin}
                  placeholder="https://your-axv.example"
                  autoComplete="off"
                  onChange={(event) =>
                    edit({ returnOrigin: event.target.value })
                  }
                />
                <p className="fine-print">
                  Use the AXV address reachable from your notification device.
                  Opening a link only inspects the original event.
                </p>
                <fieldset className="notification-event-options">
                  <legend>Notify me about</legend>
                  {notificationKinds.map(([kind, label]) => (
                    <Checkbox
                      key={kind}
                      label={label}
                      checked={draft.eventTypes.includes(kind)}
                      onChange={(event) =>
                        edit({
                          eventTypes: event.target.checked
                            ? [...draft.eventTypes, kind]
                            : draft.eventTypes.filter(
                                (value) => value !== kind,
                              ),
                        })
                      }
                    />
                  ))}
                </fieldset>
                <div className="app-actions">
                  <Button
                    primary
                    disabled={!dirty}
                    onClick={() => void change('save')}
                  >
                    {busy === 'save' ? 'Saving…' : 'Save'}
                  </Button>
                  <Button
                    disabled={
                      dirty ||
                      !snapshot?.config.enabled ||
                      !snapshot.config.tokenConfigured
                    }
                    onClick={() => void change('test')}
                  >
                    {busy === 'test' ? 'Testing…' : 'Test'}
                  </Button>
                  <Button
                    disabled={!snapshot?.config.enabled}
                    onClick={() => void change('disable')}
                  >
                    {busy === 'disable' ? 'Disabling…' : 'Disable'}
                  </Button>
                </div>
                {dirty && (
                  <p className="fine-print">
                    Save settings before sending a test.
                  </p>
                )}
                <p className="fine-print">
                  Changing credentials, destination, return-link origin or
                  enabled state disables deliveries queued under the previous
                  settings. Changing event types only removes deselected types
                  from the queue. Already accepted deliveries cannot be
                  recalled.
                </p>
              </fieldset>
            )}
            {error && (
              <p className="notification-error" role="alert">
                {error}
              </p>
            )}
            {message && <p role="status">{message}</p>}
            {baseline &&
              snapshot &&
              (baseline.revision !== snapshot.revision || !currentStore) && (
                <div className="notification-error">
                  <p>
                    The saved configuration changed. Your draft is preserved.
                  </p>
                  <Button
                    disabled={!interactive || !!busy || !currentStore}
                    onClick={() => void reloadSavedSettings()}
                  >
                    Reload saved settings
                  </Button>
                </div>
              )}
          </div>
        )}
        <div
          className="app-pad notifications-content"
          hidden={tab !== 'browser'}
          inert={!interactive || tab !== 'browser'}
        >
          <BrowserPushPanel active={interactive && tab === 'browser'} />
        </div>
        <div
          className="app-pad notifications-content"
          hidden={tab !== 'history'}
          inert={!interactive || tab !== 'history'}
        >
          <div className="notification-heading">
            <h2>Delivery history</h2>
            <Button
              disabled={!interactive || resource.loading}
              onClick={resource.refresh}
            >
              Refresh
            </Button>
          </div>
          {!allowed && (
            <p className="fine-print">
              Manage Pocket Alert settings on the computer running AXV.
            </p>
          )}
          {snapshot?.nextSendAt && (
            <p className="notification-error" role="status">
              Pocket Alert quota cooldown until{' '}
              <time dateTime={snapshot.nextSendAt}>
                {new Date(snapshot.nextSendAt).toLocaleString()}
              </time>
              . All queued deliveries wait until this time, including events
              without an individual retry time.
            </p>
          )}
          {snapshot?.intake.gap && (
            <p className="notification-error" role="alert">
              Source history has a gap. Some events could not be recovered; AXV
              will not invent missing outcomes.
            </p>
          )}
          {snapshot?.intake.error && (
            <p className="notification-error" role="alert">
              Notification intake is unavailable. The retained history may be
              incomplete.
            </p>
          )}
          {!snapshot && !resource.error && (
            <p role="status">Loading history…</p>
          )}
          {snapshot && snapshot.events.length === 0 && (
            <p>No notifications recorded.</p>
          )}
          <ul className="notification-history">
            {snapshot?.events.map((event) => (
              <li key={event.id}>
                <button
                  className="notification-history-item"
                  aria-pressed={
                    reference?.notificationId === event.id &&
                    reference.storeId === snapshot.storeId
                  }
                  onClick={() => selectEvent(event.id)}
                >
                  <strong>{kindLabel(event.kind)}</strong>
                  <span>
                    {event.pushDeliveries?.length ? 'Pocket Alert: ' : ''}
                    <span>{deliveryLabel(event)}</span>
                  </span>
                  <time dateTime={event.createdAt}>
                    {new Date(event.createdAt).toLocaleString()}
                  </time>
                </button>
                <PushDeliveryDetails deliveries={event.pushDeliveries} />
                {deliveryExplanation(event) && (
                  <p className="fine-print">{deliveryExplanation(event)}</p>
                )}
                <p className="fine-print">
                  Delivery attempts: {event.attempts}
                  {event.nextAttemptAt && (
                    <>
                      {' '}
                      · Next check:{' '}
                      <time dateTime={event.nextAttemptAt}>
                        {new Date(event.nextAttemptAt).toLocaleString()}
                      </time>
                    </>
                  )}
                </p>
              </li>
            ))}
          </ul>
          {reference && (
            <NotificationDetails
              key={`${reference.storeId}:${reference.notificationId}`}
              reference={reference}
              active={interactive && tab === 'history'}
              desktop={desktop}
            />
          )}
        </div>
      </Tabs>
      <StatusBar>
        {busy
          ? busy === 'test'
            ? 'Recording test…'
            : busy === 'disable'
              ? 'Disabling notifications…'
              : 'Saving settings…'
          : resource.error
            ? 'History may be stale.'
            : dirty
              ? 'Unsaved settings.'
              : snapshot?.nextSendAt
                ? 'Pocket Alert quota cooldown.'
                : snapshot?.config.enabled
                  ? 'Pocket Alert enabled.'
                  : 'Pocket Alert disabled.'}
      </StatusBar>
    </div>
  );
}
