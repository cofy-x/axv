import { useCallback } from 'react';
import { Select } from '@cofy-x/axv-nextstep';
import { documentsAPI } from '../../filesystem/documents';
import { useDesktopResource as useResource } from '../../shared/api/useDesktopResource';
import { MarkdownPreview, isMarkdownFilename } from '../../shared/markdown';
import { currentSavedVersion, versionInfo } from './useSavedVersion';
import type {
  SavedVersionChoice,
  SavedVersionController,
} from './useSavedVersion';

function VersionPreview({ choice }: { choice: SavedVersionChoice }) {
  const id = choice.kind === 'snapshot' ? choice.snapshot.id : '';
  const load = useCallback(
    () => (id ? documentsAPI.getSnapshot(id) : Promise.resolve(null)),
    [id],
  );
  const result = useResource(id, load);
  const content =
    choice.kind === 'saved' ? choice.content : result.data?.snapshot.content;
  const info = versionInfo(choice);
  return (
    <>
      {result.error && <p role="alert">{result.error}</p>}
      {content === undefined ? (
        !result.error && <p role="status">Loading preview…</p>
      ) : (
        <div className="document-version-preview">
          {isMarkdownFilename(info.name) ? (
            <MarkdownPreview content={content} />
          ) : (
            <pre tabIndex={0} aria-label="Saved version text">
              {content}
            </pre>
          )}
        </div>
      )}
    </>
  );
}

export function SavedVersionPicker({
  version,
  disabled = false,
}: {
  version: SavedVersionController;
  disabled?: boolean;
}) {
  const { data, choice } = version;
  const current = data && currentSavedVersion(data);
  const reviewed =
    choice?.kind === 'saved' &&
    (!current || versionInfo(current).revision !== choice.revision);
  const value =
    choice?.kind === 'snapshot'
      ? choice.snapshot.id
      : choice
        ? reviewed
          ? 'reviewed'
          : 'saved'
        : '';
  const snapshots = data?.snapshots || [];
  const selectedSnapshot =
    choice?.kind === 'snapshot' ? choice.snapshot : undefined;
  const retainedSelection =
    selectedSnapshot &&
    !snapshots.some((item) => item.id === selectedSnapshot.id);
  return (
    <div className="document-version-picker">
      {version.loading && <p role="status">Loading versions…</p>}
      {version.error && <p role="alert">{version.error}</p>}
      {data?.warnings.map((warning) => (
        <p role="alert" key={warning}>
          {warning}
        </p>
      ))}
      {data && (
        <>
          {!current && (
            <p>Source document unavailable. Choose a retained version.</p>
          )}
          <Select
            label="Version"
            value={value}
            disabled={disabled || version.loading}
            onChange={(event) => version.choose(event.target.value)}
          >
            {!choice && <option value="">Choose a version</option>}
            {reviewed && (
              <option value="reviewed">
                Reviewed saved revision {choice.revision}
              </option>
            )}
            {current && (
              <option value="saved">
                Saved revision {versionInfo(current).revision}
              </option>
            )}
            {retainedSelection && (
              <option value={selectedSnapshot.id}>
                Revision {selectedSnapshot.revision} ·{' '}
                {new Date(selectedSnapshot.createdAt).toLocaleString()}
              </option>
            )}
            {snapshots.map((snapshot) => (
              <option value={snapshot.id} key={snapshot.id}>
                Revision {snapshot.revision} ·{' '}
                {new Date(snapshot.createdAt).toLocaleString()}
              </option>
            ))}
          </Select>
          <p className="document-hint">Unsaved edits excluded.</p>
          {reviewed && current && (
            <p role="status">
              Newer saved version available. Choose it to include changes.
            </p>
          )}
          {choice && (
            <details className="document-version-preview-details">
              <summary>Preview</summary>
              <VersionPreview choice={choice} />
            </details>
          )}
        </>
      )}
    </div>
  );
}
