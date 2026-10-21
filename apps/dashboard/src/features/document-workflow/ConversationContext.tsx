import { useCallback, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Button, IconButton, Select } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useFilesystemData } from '../../filesystem/context';
import { isInTrash } from '../../filesystem/tree';
import { useDraftNamespace } from '../../shared/drafts/context';
import { RequestIdentity } from '../../shared/api/request-identity';
import { useResource } from '../../shared/api/useResource';
import { codexAPI, projectName, sessionTitle } from '../codex/api';
import { contextAPI, contextStatusLabel } from '../codex/context-api';
import type { ContextBatch } from '../codex/context-api';
import { RelatedDocuments } from './RelatedDocuments';
import { SavedVersionPicker } from './SavedVersionPicker';
import { useSavedVersion } from './useSavedVersion';
import { readyContextRun } from './state';

function AttachmentComposer({
  entryId,
  sessionId,
  runId,
  available,
  refreshKey,
  onBusy,
  onAdded,
  children,
}: {
  entryId: string;
  sessionId: string;
  runId: string;
  available: boolean;
  refreshKey: number;
  onBusy: (busy: boolean) => void;
  onAdded: (batch: ContextBatch, warning: string) => Promise<void>;
  children: ReactNode;
}) {
  const version = useSavedVersion(entryId, refreshKey);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const { databaseId } = useDraftNamespace();
  const identity = useRef(
    new RequestIdentity(`${databaseId}:document-context:${entryId}`),
  );

  async function attach() {
    if (busy || !available) return;
    setBusy(true);
    onBusy(true);
    setError('');
    try {
      const snapshot = await version.resolve();
      const snapshotIds = [snapshot.id];
      const requestId = identity.current.for({ sessionId, runId, snapshotIds });
      const { batch } = await contextAPI.attach(
        sessionId,
        runId,
        requestId,
        snapshotIds,
      );
      const warning =
        batch.status !== 'unknown' && !identity.current.complete(requestId)
          ? 'The attachment was queued, but its retry identity could not be cleared in this browser.'
          : '';
      await onAdded(batch, warning);
    } catch (error) {
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
      onBusy(false);
    }
  }

  return (
    <div className="document-attachment-composer" aria-busy={busy}>
      <SavedVersionPicker version={version} disabled={busy} />
      {error && <p role="alert">{error}</p>}
      <div className="document-attachment-actions">
        <Button
          primary
          disabled={busy || !available || version.loading || !version.choice}
          onClick={() => void attach()}
        >
          {busy ? 'Attaching…' : 'Attach'}
        </Button>
        {children}
      </div>
    </div>
  );
}

function documentLocation(
  id: string,
  entries: ReturnType<typeof useFilesystemData>['entries'],
) {
  const parts: string[] = [];
  const visited = new Set<string>();
  let entry = entries.find((item) => item.id === id);
  while (entry && !visited.has(entry.id)) {
    visited.add(entry.id);
    parts.unshift(entry.name);
    entry = entries.find((item) => item.id === entry?.parentId);
  }
  return parts.join(' / ');
}

export function ConversationContext({
  window,
  desktop,
  interactive = true,
}: AppWindowProps) {
  const { sessionId, runId } = window.params;
  const [batches, setBatches] = useState<ContextBatch[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState('');
  const [warning, setWarning] = useState('');
  const [lastAttachment, setLastAttachment] = useState<ContextBatch | null>(
    null,
  );
  const [busy, setBusy] = useState('');
  const [cancelError, setCancelError] = useState<{
    id: string;
    message: string;
  } | null>(null);
  const [documentId, setDocumentId] = useState('');
  const [composing, setComposing] = useState(false);
  const [composingBusy, setComposingBusy] = useState(false);
  const [metadataRevision, setMetadataRevision] = useState(0);
  const files = useFilesystemData();
  const addButton = useRef<HTMLDivElement>(null);
  const focusAfterClose = useRef(false);
  const request = useRef<AbortController | null>(null);
  const getRun = useCallback(
    () => codexAPI.getRun(sessionId, runId),
    [sessionId, runId],
  );
  const run = useResource(`${sessionId}:${runId}:${metadataRevision}`, getRun);
  const available =
    !!run.data &&
    run.data.id === sessionId &&
    run.data.runId === runId &&
    readyContextRun(run.data);
  const refresh = useCallback(
    async (replace = true) => {
      if (request.current && !replace) return;
      request.current?.abort();
      const current = new AbortController();
      request.current = current;
      try {
        const result = await contextAPI.list(sessionId, runId, current.signal);
        if (request.current !== current || current.signal.aborted) return;
        setBatches(result.batches);
        setLoaded(true);
        setError('');
      } catch (error) {
        if (request.current === current && !current.signal.aborted)
          setError(error instanceof Error ? error.message : String(error));
      } finally {
        if (request.current === current) request.current = null;
      }
    },
    [sessionId, runId],
  );
  useEffect(() => {
    let current = true;
    let timer: ReturnType<typeof setTimeout>;

    async function poll() {
      await refresh(false);
      if (current) timer = setTimeout(() => void poll(), 2500);
    }

    void poll();
    return () => {
      current = false;
      clearTimeout(timer);
      request.current?.abort();
      request.current = null;
    };
  }, [refresh]);
  useEffect(() => {
    if (!composing && interactive && focusAfterClose.current) {
      addButton.current?.querySelector('button')?.focus();
      focusAfterClose.current = false;
    }
  }, [composing, interactive]);

  async function cancel(id: string) {
    if (busy) return;
    setBusy(id);
    setCancelError(null);
    try {
      await contextAPI.cancel(sessionId, runId, id);
      await refresh();
    } catch (error) {
      setCancelError({
        id,
        message: error instanceof Error ? error.message : String(error),
      });
    } finally {
      setBusy('');
    }
  }

  function closeComposer() {
    focusAfterClose.current = true;
    setComposing(false);
    setDocumentId('');
  }

  function receipt(batch: ContextBatch) {
    return (
      <article className="document-delivery" key={batch.id}>
        <strong>{contextStatusLabel[batch.status]}</strong>
        {batch.status === 'unknown' && (
          <p role="alert">
            Not resent automatically. Check Codex before attaching again.
          </p>
        )}
        <ul>
          {batch.snapshots.map((snapshot) => (
            <li key={snapshot.id}>
              <Button
                onClick={() =>
                  desktop.openApp(
                    'document-snapshot',
                    { snapshotId: snapshot.id },
                    {
                      title: `${snapshot.name} · revision ${snapshot.revision}`,
                    },
                  )
                }
              >
                {snapshot.name} · revision {snapshot.revision}
              </Button>
            </li>
          ))}
        </ul>
        {batch.error && <p role="alert">{batch.error}</p>}
        {cancelError?.id === batch.id && (
          <p role="alert">{cancelError.message}</p>
        )}
        {batch.status === 'pending' && (
          <Button
            aria-label="Cancel Attachment"
            disabled={!!busy}
            onClick={() => void cancel(batch.id)}
          >
            {busy === batch.id ? 'Cancelling…' : 'Cancel'}
          </Button>
        )}
        {batch.providerTurnId && (
          <details>
            <summary>Delivery details</summary>
            <small>
              Turn <code>{batch.providerTurnId}</code>
            </small>
          </details>
        )}
      </article>
    );
  }

  const active = batches.filter(
    (batch) => batch.status !== 'accepted' && batch.status !== 'cancelled',
  );
  const history = batches.filter(
    (batch) => batch.status === 'accepted' || batch.status === 'cancelled',
  );
  const latestReceipt =
    batches.find((batch) => batch.id === lastAttachment?.id) || lastAttachment;
  const documents = files.entries.filter(
    (entry) => entry.editable && !isInTrash(entry, files.entries),
  );
  const cancelComposer = (
    <Button disabled={composingBusy} onClick={closeComposer}>
      Cancel
    </Button>
  );
  return (
    <div className="document-workflow document-context app-pad">
      <div className="document-context-header">
        <div className="document-context-target">
          <strong className="document-context-title">
            {run.data ? sessionTitle(run.data) : 'Codex attachments'}
          </strong>
          <p className="document-target-summary">
            {run.data && <>Host · {projectName(run.data.projectPath)} · </>}
            <span className="document-context-run">
              run {runId.slice(0, 8)}
            </span>
          </p>
        </div>
        <div className="document-context-actions">
          <Button
            onClick={() => desktop.openApp('codex', { sessionId, runId })}
          >
            Open Codex
          </Button>
          <IconButton
            icon="refresh"
            label="Refresh"
            onClick={() => {
              void refresh();
              setMetadataRevision((value) => value + 1);
            }}
          />
        </div>
      </div>
      {(error || run.error) && <p role="alert">{error || run.error}</p>}
      {warning && <p role="alert">{warning}</p>}
      {latestReceipt && (
        <p role="status">
          Attachment: {contextStatusLabel[latestReceipt.status]}
        </p>
      )}
      {latestReceipt?.status === 'unknown' &&
        !active.some((batch) => batch.id === latestReceipt.id) && (
          <p role="alert">
            Not resent automatically. Check Codex before attaching again.
          </p>
        )}
      {run.data && !available && (
        <p role="status">
          This run is not ready for attachments. Attachments stay with this run;
          a continuation needs a new selection.
        </p>
      )}
      <section
        className="document-context-documents"
        aria-label="Context deliveries"
      >
        <div className="document-context-documents-heading">
          <h2>Documents</h2>
          <div ref={addButton} hidden={composing} inert={composing}>
            <Button
              primary
              aria-label="Add document"
              disabled={!available}
              onClick={() => {
                setWarning('');
                setComposing(true);
              }}
            >
              Add
            </Button>
          </div>
        </div>
        <p className="document-hint">For your next message in this run.</p>
        {composing && (
          <section className="document-add-section" aria-label="Add document">
            <Select
              label="Document"
              autoFocus
              value={documentId}
              disabled={composingBusy}
              onChange={(event) => setDocumentId(event.target.value)}
            >
              <option value="">Choose a document</option>
              {documents.map((entry) => (
                <option value={entry.id} key={entry.id}>
                  {entry.name}
                  {documents.some(
                    (other) =>
                      other.id !== entry.id && other.name === entry.name,
                  )
                    ? ` · ${documentLocation(entry.id, files.entries)}`
                    : ''}
                </option>
              ))}
            </Select>
            {!documents.length && (
              <p>
                No saved documents. Create and save a document in Text Editor
                first.
              </p>
            )}
            {documentId ? (
              <AttachmentComposer
                key={documentId}
                entryId={documentId}
                sessionId={sessionId}
                runId={runId}
                available={available}
                refreshKey={metadataRevision}
                onBusy={setComposingBusy}
                onAdded={async (batch, message) => {
                  setLastAttachment(batch);
                  setWarning(message);
                  await refresh();
                  closeComposer();
                }}
              >
                {cancelComposer}
              </AttachmentComposer>
            ) : (
              cancelComposer
            )}
          </section>
        )}
        {!loaded && !error && <p role="status">Loading attachments…</p>}
        {loaded && !batches.length && (
          <p className="document-context-empty">No documents attached.</p>
        )}
        {active.map(receipt)}
        {!!history.length && (
          <details className="document-history">
            <summary>History ({history.length})</summary>
            {history.map(receipt)}
          </details>
        )}
      </section>
      <RelatedDocuments
        target={{ conversationId: sessionId }}
        desktop={desktop}
      />
      <details className="document-detail-group">
        <summary>Run details</summary>
        {run.data && (
          <p>
            <code>{run.data.projectPath}</code>
          </p>
        )}
        <code className="document-run-id">Run {runId}</code>
      </details>
    </div>
  );
}
