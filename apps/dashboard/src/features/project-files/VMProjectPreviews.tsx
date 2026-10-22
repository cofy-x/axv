import { useEffect, useRef, useState } from 'react';
import { Button, Field } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { useDraftNamespace } from '../../shared/drafts/context';
import type { VMProjectBinding } from './vm-api';
import {
  validPendingPreview,
  verifyPreviewReceipt,
  vmPreviewsAPI,
} from './vm-preview-api';
import type {
  PendingPreviewRegistration,
  VMPreviewSnapshot,
  VMProjectPreview,
} from './vm-preview-api';
import { replaceVMRecoveryRecord } from './vm-workflow-recovery';

const message = (error: unknown) =>
  error instanceof Error ? error.message : String(error);

export function VMProjectPreviews({
  projectId,
  hostId,
  binding,
  active,
  interactive,
  desktop,
  onNoticeChange,
}: {
  projectId: string;
  hostId: string;
  binding: VMProjectBinding | null;
  active: boolean;
  interactive: boolean;
  desktop: DesktopAPI;
  onNoticeChange: (notice: '' | 'recovery' | 'error' | 'working') => void;
}) {
  const namespace = useDraftNamespace();
  const recoveryKey = JSON.stringify([
    'axv-project-preview',
    namespace.databaseId,
    namespace.clientId,
    hostId,
    projectId,
  ]);
  const [recovery] = useState(() => {
    let raw: string | null = null;
    try {
      raw = localStorage.getItem(recoveryKey);
      const value: unknown = raw === null ? null : JSON.parse(raw);
      if (raw !== null && !validPendingPreview(value, hostId, projectId))
        throw new Error(
          'The saved preview registration could not be read. Its original record was preserved.',
        );
      return {
        raw,
        value: value as PendingPreviewRegistration | null,
        error: '',
      };
    } catch (error) {
      return { raw, value: null, error: message(error) };
    }
  });
  const retainedRaw = useRef(recovery.raw);
  const [pending, setPending] = useState(recovery.value);
  const [recoveryError, setRecoveryError] = useState(recovery.error);
  const [port, setPort] = useState(
    String(recovery.value?.registration.port || 5173),
  );
  const [snapshot, setSnapshot] = useState<VMPreviewSnapshot | null>(null);
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  const generation = useRef(0);
  const interactiveRef = useRef(interactive);
  interactiveRef.current = interactive;
  const mounted = useRef(true);
  const [error, setError] = useState('');
  const [refreshError, setRefreshError] = useState('');
  const notice =
    pending || recoveryError
      ? busy
        ? 'working'
        : 'recovery'
      : error ||
          refreshError ||
          snapshot?.previews.some((preview) => preview.error)
        ? 'error'
        : '';
  useEffect(() => {
    // Clearing a hidden view's local status must not erase its unread notice.
    if (active || notice) onNoticeChange(notice);
  }, [active, notice, onNoticeChange]);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  async function persist(value: PendingPreviewRegistration | null) {
    const next = value === null ? null : JSON.stringify(value);
    await replaceVMRecoveryRecord(recoveryKey, retainedRaw.current, next);
    retainedRaw.current = next;
    if (mounted.current) setPending(value);
  }

  useEffect(() => {
    if (!active || !interactive) return;
    const controller = new AbortController();
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;

    async function refresh() {
      const current = generation.current;
      try {
        const next = await vmPreviewsAPI.list(
          projectId,
          hostId,
          controller.signal,
        );
        if (!stopped && generation.current === current && !busyRef.current) {
          setSnapshot(next);
          setRefreshError('');
        }
      } catch (error) {
        if (!stopped && generation.current === current && !busyRef.current)
          setRefreshError(message(error));
      }
      if (!stopped) timer = setTimeout(() => void refresh(), 5000);
    }

    void refresh();
    return () => {
      stopped = true;
      controller.abort();
      clearTimeout(timer);
    };
  }, [active, interactive, projectId, hostId]);

  function apply(preview: VMProjectPreview) {
    if (!mounted.current) return;
    setSnapshot(
      (current) =>
        current && {
          ...current,
          previews: [
            ...current.previews.filter((item) => item.id !== preview.id),
            preview,
          ],
        },
    );
  }

  async function register(saved?: PendingPreviewRegistration) {
    if (busyRef.current || !interactiveRef.current) return;
    busyRef.current = true;
    generation.current++;
    setBusy(true);
    setError('');
    try {
      let operation = saved;
      if (!operation) {
        if (
          !binding ||
          !snapshot?.supported ||
          !snapshot.instanceId ||
          pending ||
          recoveryError
        )
          throw new Error('Choose an available preview environment first.');
        const number = Number(port);
        if (!Number.isInteger(number) || number < 1024 || number > 65535)
          throw new Error('Choose a guest TCP port from 1024 to 65535.');
        operation = {
          version: 1,
          registration: {
            instanceId: snapshot.instanceId,
            requestId: crypto.randomUUID(),
            binding: { ...binding },
            port: number,
          },
        };
        await persist(operation);
      } else if (localStorage.getItem(recoveryKey) !== retainedRaw.current) {
        throw new Error(
          'The original preview recovery record changed. Reopen this project before recovering it.',
        );
      }
      if (!mounted.current || !interactiveRef.current) return;
      const preview = verifyPreviewReceipt(
        await vmPreviewsAPI.register(operation.registration),
        operation.registration,
      );
      apply(preview);
      await persist(null);
    } catch (error) {
      if (mounted.current) setError(message(error));
    } finally {
      generation.current++;
      busyRef.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  async function stop(preview: VMProjectPreview) {
    if (busyRef.current || !interactiveRef.current) return;
    busyRef.current = true;
    generation.current++;
    setBusy(true);
    setError('');
    try {
      const stopped = await vmPreviewsAPI.stop(preview);
      if (stopped.id !== preview.id || stopped.state !== 'stopped')
        throw new Error('The stop receipt does not match this preview.');
      apply(stopped);
    } catch (error) {
      if (mounted.current) setError(message(error));
    } finally {
      generation.current++;
      busyRef.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  async function dismiss() {
    if (busyRef.current || !interactiveRef.current) return;
    const answer = await desktop.dialog({
      title: 'Dismiss preview recovery',
      message:
        'This removes only the browser recovery record. Any accepted preview remains in the list; inspect and stop it before registering again.',
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Dismiss', value: 'dismiss' },
      ],
    });
    if (
      answer?.button !== 'dismiss' ||
      busyRef.current ||
      !interactiveRef.current
    )
      return;
    try {
      await persist(null);
      setRecoveryError('');
      setError('');
    } catch (error) {
      setError(message(error));
    }
  }

  return (
    <section className="vm-project-previews" aria-label="Project previews">
      <h2 className="vm-project-workflow-title">Previews</h2>
      {snapshot && !snapshot.supported && (
        <p role="status">
          {snapshot.reason ||
            'Previews are available only from the local desktop.'}
        </p>
      )}
      <form
        className="vm-preview-register"
        onSubmit={(event) => {
          event.preventDefault();
          void register();
        }}
      >
        <Field
          label="Port"
          type="number"
          min={1024}
          max={65535}
          value={port}
          onChange={(event) => setPort(event.target.value)}
          disabled={
            busy ||
            !interactive ||
            !!pending ||
            !!recoveryError ||
            !snapshot?.supported
          }
          required
        />
        <Button
          type="submit"
          disabled={
            !binding ||
            busy ||
            !interactive ||
            !!pending ||
            !!recoveryError ||
            !snapshot?.supported
          }
        >
          {busy ? 'Working…' : 'Add'}
        </Button>
      </form>
      {binding && <p className="vm-preview-target">VM {binding.machineId}</p>}
      {[...new Set([error, refreshError, recoveryError].filter(Boolean))].map(
        (reason) => (
          <p role="alert" key={reason}>
            {reason}
          </p>
        ),
      )}
      {pending && (
        <>
          <p>
            Original registration: port {pending.registration.port} · VM{' '}
            {pending.registration.binding.machineId}
          </p>
          <div className="vm-preview-actions">
            <Button
              disabled={busy || !interactive}
              onClick={() => void register(pending)}
            >
              Recover
            </Button>
            <Button
              disabled={busy || !interactive}
              onClick={() => void dismiss()}
            >
              Dismiss
            </Button>
          </div>
        </>
      )}
      {!pending && recoveryError && (
        <Button disabled={busy || !interactive} onClick={() => void dismiss()}>
          Dismiss
        </Button>
      )}
      {snapshot?.previews.length ? (
        <>
          <ul className="vm-project-preview-list">
            {snapshot.previews.map((preview) => (
              <li
                key={preview.id}
                aria-label={`Preview on port ${preview.port}`}
              >
                <div className="vm-preview-entry">
                  <strong>Port {preview.port}</strong>
                  <span>{preview.state}</span>
                </div>
                <small className="vm-preview-target">
                  VM {preview.binding.machineId}
                </small>
                {preview.error && <p role="alert">{preview.error}</p>}
                <div className="vm-preview-actions">
                  {preview.state === 'ready' && preview.url && interactive && (
                    <a
                      href={preview.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="ns-button"
                    >
                      Open
                    </a>
                  )}
                  {preview.state !== 'stopped' && (
                    <Button
                      disabled={busy || !interactive}
                      onClick={() => void stop(preview)}
                    >
                      Stop
                    </Button>
                  )}
                </div>
              </li>
            ))}
          </ul>
          <p className="vm-project-workflow-help">
            Stop closes preview access; the server keeps running.
          </p>
        </>
      ) : snapshot?.supported ? (
        <p className="vm-project-workflow-help">
          No previews yet. Start a server in this VM, then add its HTTP port.
        </p>
      ) : !snapshot && !refreshError ? (
        <p role="status">Loading previews…</p>
      ) : null}
      <details className="vm-preview-access">
        <summary>Access and recovery</summary>
        <p>
          Previews open in a separate tab with isolated cookies. Stop closes
          access and live connections. After restarting AXV, dismiss any old
          recovery and register again. Recovery always uses the original AXV
          instance and VM.
        </p>
      </details>
    </section>
  );
}
