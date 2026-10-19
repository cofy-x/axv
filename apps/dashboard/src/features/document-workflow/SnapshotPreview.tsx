import { useCallback } from 'react';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { Button } from '@cofy-x/axv-nextstep';
import { documentsAPI } from '../../filesystem/documents';
import { useResource } from '../../shared/api/useResource';
import { MarkdownPreview, isMarkdownFilename } from '../../shared/markdown';

export function SnapshotPreview({ window, desktop }: AppWindowProps) {
  const id = window.params.snapshotId;
  const load = useCallback(() => documentsAPI.getSnapshot(id), [id]);
  const { data, error } = useResource(id, load);
  const snapshot = data?.snapshot;
  return (
    <div className="document-workflow app-pad">
      {error && <p role="alert">{error}</p>}
      {!snapshot && !error && <p role="status">Loading snapshot…</p>}
      {snapshot && (
        <>
          <strong>
            {snapshot.name} · revision {snapshot.revision}
          </strong>
          <Button
            onClick={() => {
              const id = desktop.openApp('document-workflow', {
                entryId: snapshot.entryId,
                task: 'details',
              });
              desktop.updateWindowState(id, { task: 'details' });
            }}
          >
            Document Details
          </Button>
          <p>
            Captured {new Date(snapshot.createdAt).toLocaleString()}. This copy
            retains its original content.
          </p>
          <details>
            <summary>Source identity</summary>
            <dl className="info-list">
              <dt>Database</dt>
              <dd>
                <code>{snapshot.databaseId}</code>
              </dd>
              <dt>Document</dt>
              <dd>
                <code>{snapshot.entryId}</code>
              </dd>
              <dt>SHA-256</dt>
              <dd>
                <code>{snapshot.sha256}</code>
              </dd>
            </dl>
          </details>
          {isMarkdownFilename(snapshot.name) ? (
            <MarkdownPreview content={snapshot.content} />
          ) : (
            <pre
              className="project-source"
              tabIndex={0}
              aria-label="Snapshot text"
            >
              {snapshot.content}
            </pre>
          )}
        </>
      )}
    </div>
  );
}
