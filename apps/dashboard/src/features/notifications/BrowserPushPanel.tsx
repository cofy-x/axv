import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Checkbox, Field } from '@cofy-x/axv-nextstep';
import { notificationKinds } from './types';
import {
  browserPushSubscription,
  newPushInstallationId,
  pushApplicationKey,
  pushBindingMatches,
  pushEnvironment,
  pushSubscriptionFingerprint,
  pushSubscriptionKey,
  pushWorkerPath,
  pushWorkerScope,
  readBrowserPushBinding,
  webPushAPI,
  webPushError,
  writeBrowserPushBinding,
} from './webpush';
import type { BrowserPushBinding, PushSnapshot } from './webpush';

interface BrowserState {
  snapshot?: PushSnapshot;
  binding?: BrowserPushBinding;
  browserSubscribed: boolean;
  permission: NotificationPermission | 'unsupported';
  fingerprint?: string;
  applicationKey?: string;
}

async function activated(
  registration: ServiceWorkerRegistration,
  signal: AbortSignal,
) {
  if (registration.active) return;
  const worker = registration.installing || registration.waiting;
  if (!worker) throw new Error('Notification worker unavailable.');
  await new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(
      () => done(new Error('Notification worker did not become ready.')),
      15000,
    );
    const changed = () => {
      if (worker.state === 'activated') done();
      else if (worker.state === 'redundant')
        done(new Error('Notification worker became unavailable.'));
    };
    const aborted = () => done(new Error('Notification setup cancelled.'));
    const done = (error?: Error) => {
      clearTimeout(timeout);
      worker.removeEventListener('statechange', changed);
      signal.removeEventListener('abort', aborted);
      if (error) reject(error);
      else resolve();
    };
    worker.addEventListener('statechange', changed);
    signal.addEventListener('abort', aborted, { once: true });
    if (signal.aborted) aborted();
    else changed();
  });
}

export function BrowserPushPanel({ active }: { active: boolean }) {
  const [state, setState] = useState<BrowserState>({
    browserSubscribed: false,
    permission: 'unsupported',
  });
  const [loading, setLoading] = useState(true);
  const [serverReady, setServerReady] = useState(false);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [contact, setContact] = useState('');
  const [label, setLabel] = useState('Personal browser');
  const [eventTypes, setEventTypes] = useState([
    'agent_waiting_approval',
    'agent_waiting_input',
    'agent_failed',
    'agent_interrupted',
    'task_failed',
    'task_interrupted',
    'task_unknown',
  ]);
  const pending = useRef<AbortController | null>(null);
  const generation = useRef(0);
  const environment = pushEnvironment();

  const refresh = useCallback(async () => {
    if (pending.current) return;
    const controller = new AbortController();
    pending.current = controller;
    const current = ++generation.current;
    setLoading(true);
    try {
      const signal = AbortSignal.any([
        controller.signal,
        AbortSignal.timeout(15000),
      ]);
      const server = webPushAPI.snapshot(signal).then(
        (value) => ({ ok: true as const, value }),
        (cause) => ({ ok: false as const, cause: cause as unknown }),
      );
      let binding = !('indexedDB' in globalThis)
        ? undefined
        : await readBrowserPushBinding();
      const browser =
        'serviceWorker' in navigator && 'PushManager' in globalThis
          ? await browserPushSubscription()
          : { subscription: null };
      const fingerprint = browser.subscription
        ? await pushSubscriptionFingerprint(browser.subscription)
        : undefined;
      const applicationKey = browser.subscription
        ? pushSubscriptionKey(browser.subscription)
        : undefined;
      const result = await server;
      if (current !== generation.current || controller.signal.aborted) return;
      const local = {
        binding,
        fingerprint,
        applicationKey,
        browserSubscribed: !!browser.subscription,
        permission:
          'Notification' in globalThis
            ? Notification.permission
            : ('unsupported' as const),
      };
      if (!result.ok) {
        setState((previous) => ({ ...previous, ...local }));
        setServerReady(false);
        setError(webPushError(result.cause));
        return;
      }
      const snapshot = result.value;
      if (
        !snapshot.storeId ||
        !Number.isSafeInteger(snapshot.revision) ||
        snapshot.revision < 0 ||
        !Array.isArray(snapshot.subscriptions)
      )
        throw new Error('Invalid push configuration.');
      const observedBinding = binding;
      const registered = observedBinding
        ? snapshot.subscriptions.find(
            (value) =>
              value.installationId === observedBinding.installationId &&
              value.origin === observedBinding.origin &&
              value.id === observedBinding.subscriptionId,
          )
        : undefined;
      const nativeMatches =
        !!browser.subscription &&
        !!registered?.fingerprint &&
        registered.fingerprint === fingerprint &&
        !!snapshot.publicKey &&
        snapshot.publicKey === applicationKey;
      if (
        binding &&
        !binding.needsReview &&
        (!pushBindingMatches(binding, snapshot, location.origin) ||
          !nativeMatches)
      ) {
        const invalidated = { ...binding, needsReview: true };
        await writeBrowserPushBinding(binding, invalidated);
        binding = invalidated;
      }
      if (current !== generation.current || controller.signal.aborted) return;
      setState({ ...local, binding, snapshot });
      setServerReady(true);
      setError('');
    } catch (cause) {
      if (current === generation.current && !controller.signal.aborted)
        setError(webPushError(cause));
    } finally {
      if (current === generation.current) {
        pending.current = null;
        setLoading(false);
      }
    }
  }, []);

  const cancel = useCallback(() => {
    generation.current++;
    pending.current?.abort();
    pending.current = null;
    setBusy('');
  }, []);

  useEffect(() => {
    if (!active) return;
    void refresh();
    const focused = () => {
      void refresh();
    };
    globalThis.addEventListener('focus', focused);
    return () => {
      cancel();
      globalThis.removeEventListener('focus', focused);
    };
  }, [active, refresh, cancel]);

  const { snapshot, binding, browserSubscribed, permission } = state;
  const sameScope =
    !!snapshot &&
    !!binding &&
    binding.origin === location.origin &&
    binding.storeId === snapshot.storeId &&
    binding.keyId === snapshot.keyId;
  const matches =
    !!snapshot && pushBindingMatches(binding, snapshot, location.origin);
  const own =
    sameScope && binding
      ? snapshot?.subscriptions.find(
          (value) =>
            value.installationId === binding.installationId &&
            value.origin === binding.origin &&
            (!binding.subscriptionId || value.id === binding.subscriptionId),
        )
      : undefined;
  const confirmed =
    matches &&
    !!own &&
    !!binding?.subscriptionId &&
    own.id === binding.subscriptionId &&
    !!own.fingerprint &&
    own.fingerprint === state.fingerprint &&
    !!snapshot?.publicKey &&
    snapshot.publicKey === state.applicationKey;
  const editable = !binding && !browserSubscribed;

  async function change(
    action: 'subscribe' | 'test' | 'disable' | 'unsubscribe',
  ) {
    if (
      !active ||
      pending.current ||
      (action !== 'unsubscribe' && (!snapshot || !serverReady))
    )
      return;
    const controller = new AbortController();
    pending.current = controller;
    const current = ++generation.current;
    const signal = AbortSignal.any([
      controller.signal,
      AbortSignal.timeout(30000),
    ]);
    const original = { snapshot, binding, own };
    setBusy(action);
    setError('');
    setMessage('');
    try {
      if (action === 'subscribe') {
        if (environment || binding || browserSubscribed || !original.snapshot)
          return;
        const originalSnapshot = original.snapshot;
        // Request permission before the first await, directly in the click handler.
        const permitted = await Notification.requestPermission();
        signal.throwIfAborted();
        if (permitted !== 'granted') {
          setMessage(
            permitted === 'denied'
              ? 'Browser permission denied. Change it in browser or OS settings before subscribing.'
              : 'Browser permission was not granted. Nothing was subscribed.',
          );
          return;
        }
        if (!navigator.locks)
          throw new Error('Browser notification locking unavailable.');
        await navigator.locks.request(
          'axv-web-push-binding',
          { signal },
          async () => {
            if (
              (await readBrowserPushBinding()) ||
              (await browserPushSubscription()).subscription
            )
              throw new Error(
                'An existing browser subscription requires explicit removal.',
              );
            let reviewed = originalSnapshot;
            if (!reviewed.keyId || !reviewed.publicKey)
              reviewed = await webPushAPI.prepare(
                {
                  storeId: reviewed.storeId,
                  revision: reviewed.revision,
                  contact,
                },
                signal,
              );
            signal.throwIfAborted();
            if (
              reviewed.storeId !== originalSnapshot.storeId ||
              !reviewed.keyId ||
              !reviewed.publicKey
            )
              throw new Error('Notification store changed.');
            const installationId = newPushInstallationId();
            const provisional: BrowserPushBinding = {
              version: 1,
              installationId,
              origin: location.origin,
              storeId: reviewed.storeId,
              keyId: reviewed.keyId,
              subscriptionId: '',
            };
            await writeBrowserPushBinding(undefined, provisional);
            const registration = await navigator.serviceWorker.register(
              pushWorkerPath,
              { scope: pushWorkerScope, updateViaCache: 'none' },
            );
            await activated(registration, signal);
            signal.throwIfAborted();
            const subscription = await registration.pushManager.subscribe({
              userVisibleOnly: true,
              applicationServerKey: pushApplicationKey(reviewed.publicKey),
            });
            const json = subscription.toJSON();
            if (!json.endpoint || !json.keys?.p256dh || !json.keys.auth)
              throw new Error('Browser returned an incomplete subscription.');
            const saved = await webPushAPI.subscribe(
              {
                storeId: reviewed.storeId,
                revision: reviewed.revision,
                keyId: reviewed.keyId,
                installationId,
                label,
                eventTypes,
                subscription: {
                  endpoint: json.endpoint,
                  keys: { p256dh: json.keys.p256dh, auth: json.keys.auth },
                },
              },
              signal,
            );
            signal.throwIfAborted();
            const registered = saved.subscriptions.find(
              (value) =>
                value.installationId === installationId &&
                value.origin === provisional.origin,
            );
            if (
              saved.storeId !== provisional.storeId ||
              saved.keyId !== provisional.keyId ||
              !registered?.enabled ||
              !/^[a-f0-9]{32}$/.test(registered.id)
            )
              throw new Error(
                'Server did not confirm the reviewed registration.',
              );
            await writeBrowserPushBinding(provisional, {
              ...provisional,
              subscriptionId: registered.id,
            });
            if (current === generation.current)
              setMessage(
                'This browser is subscribed. Use Test to inspect a synthetic delivery outcome.',
              );
          },
        );
      } else if (action === 'unsubscribe') {
        if (!navigator.locks)
          throw new Error('Browser notification locking unavailable.');
        await navigator.locks.request(
          'axv-web-push-binding',
          { signal },
          async () => {
            if (
              JSON.stringify(await readBrowserPushBinding()) !==
              JSON.stringify(binding)
            )
              throw new Error(
                'Browser registration changed. Refresh before removing it.',
              );
            // Browser removal must succeed before its server record is retired.
            const browser = await browserPushSubscription();
            if (
              browser.subscription &&
              !(await browser.subscription.unsubscribe())
            )
              throw new Error('Browser did not confirm unsubscribe.');
            signal.throwIfAborted();
            if (!serverReady || !snapshot) {
              if (current === generation.current)
                setMessage(
                  'Browser subscription removed. The server registration could not be checked; reconnect and use Unsubscribe again to retire its saved record. Browser permission is unchanged.',
                );
              return;
            }
            if (sameScope && binding && own) {
              const saved = await webPushAPI.unsubscribe(
                {
                  storeId: snapshot.storeId,
                  revision: snapshot.revision,
                  keyId: binding.keyId,
                  installationId: binding.installationId,
                  subscriptionId: own.id,
                },
                signal,
              );
              if (
                saved.storeId !== snapshot.storeId ||
                saved.keyId !== binding.keyId ||
                saved.subscriptions.some((value) => value.id === own.id)
              )
                throw new Error(
                  'Server did not confirm removal of the original registration.',
                );
            }
            if (binding) await writeBrowserPushBinding(binding, undefined);
            if (current === generation.current)
              setMessage(
                'Browser subscription removed. Browser or OS notification permission is unchanged; previously queued alerts may still arrive.',
              );
          },
        );
      } else {
        if (!snapshot || !matches || !binding || !confirmed || !own) return;
        if (action === 'test') {
          const currentBinding = await readBrowserPushBinding();
          if (JSON.stringify(currentBinding) !== JSON.stringify(binding))
            throw new Error('Browser registration changed.');
          const browser = await browserPushSubscription();
          if (
            !browser.subscription ||
            (await pushSubscriptionFingerprint(browser.subscription)) !==
              own.fingerprint ||
            pushSubscriptionKey(browser.subscription) !== snapshot.publicKey
          ) {
            await writeBrowserPushBinding(binding, {
              ...binding,
              needsReview: true,
            });
            throw new Error('Browser subscription changed.');
          }
          signal.throwIfAborted();
        }
        const saved = await webPushAPI[action](
          {
            storeId: snapshot.storeId,
            revision: snapshot.revision,
            keyId: binding.keyId,
            installationId: binding.installationId,
            subscriptionId: own.id,
          },
          signal,
        );
        const originalRegistration = saved.subscriptions.find(
          (value) =>
            value.id === own.id &&
            value.installationId === binding.installationId &&
            value.origin === binding.origin,
        );
        if (
          saved.storeId !== snapshot.storeId ||
          saved.keyId !== binding.keyId ||
          !originalRegistration ||
          (action === 'disable' && originalRegistration.enabled)
        )
          throw new Error('Server did not confirm the original registration.');
        if (current === generation.current)
          setMessage(
            action === 'disable'
              ? 'Server delivery disabled for this browser. Browser permission and subscription remain; previously queued alerts cannot be recalled.'
              : 'Synthetic test recorded. Review History; provider acceptance does not confirm device display.',
          );
      }
    } catch (cause) {
      if (current === generation.current && !controller.signal.aborted)
        setError(webPushError(cause));
    } finally {
      if (current === generation.current) {
        pending.current = null;
        setBusy('');
        void refresh();
      }
    }
  }

  const disabled = !active || loading || !!busy;
  const serverDisabled = disabled || !snapshot || !serverReady;
  return (
    <section aria-label="This browser notifications">
      <div className="notification-heading">
        <h2>This browser</h2>
        <Button
          disabled={!active || loading || !!busy}
          onClick={() => void refresh()}
        >
          Refresh browser state
        </Button>
      </div>
      <p>
        Optional standard Web Push without a Pocket Alert account. Subscribe
        only in the browser or installed AXV app that should receive alerts.
      </p>
      {environment && <p role="status">{environment}</p>}
      <dl className="info-list">
        <dt>Browser permission</dt>
        <dd>{permission}</dd>
        <dt>Browser subscription</dt>
        <dd>
          {browserSubscribed
            ? 'Present in this browser context'
            : 'Not subscribed'}
        </dd>
        <dt>Server delivery</dt>
        <dd>
          {own?.status === 'expired'
            ? 'Subscription expired'
            : confirmed
              ? own?.enabled
                ? 'Enabled'
                : 'Disabled'
              : 'No confirmed matching registration'}
        </dd>
      </dl>
      {permission === 'denied' && (
        <p>
          Browser permission denied. AXV cannot change this permission; review
          browser or OS settings.
        </p>
      )}
      {binding && !matches && (
        <p role="alert">
          The notification store, origin, sending key or browser subscription
          changed. AXV has not adopted this browser subscription. Unsubscribe
          this browser, then explicitly subscribe again.
        </p>
      )}
      {(browserSubscribed || binding) && !confirmed && matches && (
        <p role="status">
          The browser and server registration do not match. Remove this browser
          subscription before enrolling again.
        </p>
      )}
      {browserSubscribed && !binding && (
        <p role="alert">
          This browser has an unrecognized subscription. Unsubscribe it
          explicitly before enrolling.
        </p>
      )}
      {own?.status === 'expired' && (
        <p>
          The push service retired this browser registration. Unsubscribe this
          browser, then explicitly subscribe again.
        </p>
      )}
      {snapshot?.error && (
        <p role="alert">
          Web Push delivery needs attention. Review this browser registration
          and History.
        </p>
      )}
      {error && (
        <p className="notification-error" role="alert">
          {error}
        </p>
      )}
      {message && <p role="status">{message}</p>}
      {loading && <p role="status">Reading browser notification state…</p>}
      <fieldset disabled={serverDisabled || !editable}>
        <legend>Subscribe this browser</legend>
        <Field
          label="Device label"
          value={label}
          autoComplete="off"
          onChange={(event) => setLabel(event.target.value)}
        />
        <Field
          label="Push service contact"
          value={snapshot?.contact || contact}
          disabled={!!snapshot?.publicKey}
          placeholder="mailto:you@example.com"
          autoComplete="off"
          onChange={(event) => setContact(event.target.value)}
        />
        <p className="fine-print">
          The sending key uses this owner-reviewed public email address or HTTPS
          contact URL. Push vendors receive it in the authentication token; it
          is not notification content. Changing an existing contact requires
          local sending-key rotation and explicit re-subscription.
        </p>
        <fieldset className="notification-event-options">
          <legend>Notify this browser about</legend>
          {notificationKinds.map(([kind, text]) => (
            <Checkbox
              key={kind}
              label={text}
              checked={eventTypes.includes(kind)}
              onChange={(event) =>
                setEventTypes((values) =>
                  event.target.checked
                    ? [...values, kind]
                    : values.filter((value) => value !== kind),
                )
              }
            />
          ))}
        </fieldset>
        <Button
          primary
          disabled={
            !!environment ||
            permission === 'denied' ||
            !eventTypes.length ||
            (!snapshot?.publicKey && !contact.trim())
          }
          onClick={() => void change('subscribe')}
        >
          {busy === 'subscribe' ? 'Subscribing…' : 'Subscribe this browser'}
        </Button>
      </fieldset>
      <div className="app-actions">
        <Button
          disabled={
            serverDisabled ||
            !confirmed ||
            !own?.enabled ||
            !browserSubscribed ||
            permission !== 'granted'
          }
          onClick={() => void change('test')}
        >
          Test this browser
        </Button>
        <Button
          disabled={serverDisabled || !confirmed || !own?.enabled}
          onClick={() => void change('disable')}
        >
          Disable server delivery
        </Button>
        <Button
          disabled={disabled || (!binding && !browserSubscribed)}
          onClick={() => void change('unsubscribe')}
        >
          Unsubscribe this browser
        </Button>
      </div>
      {confirmed && !own?.enabled && (
        <p>
          To enable delivery again, unsubscribe this browser and explicitly
          subscribe again.
        </p>
      )}
      <p className="fine-print">
        Alerts contain only minimal status and an opaque original-event
        reference. Receiving an alert does not require reaching AXV, but
        reopening private work requires owner authorization and connectivity.
        Delivery and display are best-effort with finite expiry; stopping AXV
        postpones sends. Server disable does not revoke browser permission or
        retract queued alerts.
      </p>
    </section>
  );
}
