import { desktopDatabase } from '../../shared/api/desktop';
import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, IconButton, PixelIcon, StatusBar } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { useFilesystemData } from '../../filesystem/context';
import { useDraftNamespace } from '../../shared/drafts/context';
import { canTransferWindowFocus } from '../../shared/desktop/focus-transfer';
import { useDesktopResource as useResource } from '../../shared/api/useDesktopResource';
import { RequestIdentity } from '../../shared/api/request-identity';
import { MarkdownPreview, isMarkdownFilename } from '../../shared/markdown';
import { CodeEditor } from '../../shared/code-editor';
import { RelatedDocuments } from '../document-workflow/RelatedDocuments';
import { projectsAPI } from './api';
import { projectFileLocation } from './navigation';

export function HostProjectFiles({
  window,
  desktop,
  interactive,
}: AppWindowProps) {
  const id = window.params.projectId;
  const initialLocation = projectFileLocation(window.params.file);
  const folder =
    typeof window.viewState.folder === 'string'
      ? window.viewState.folder
      : initialLocation.folder;
  const file =
    typeof window.viewState.file === 'string'
      ? window.viewState.file
      : initialLocation.file;
  const cursor =
    typeof window.viewState.cursor === 'string' ? window.viewState.cursor : '';
  const reading =
    typeof window.viewState.reading === 'boolean'
      ? window.viewState.reading
      : !!file;
  const directory = useRef<HTMLElement>(null);
  const preview = useRef<HTMLElement>(null);
  const previewFocusLocation = useRef<string | null>(null);
  const returnToDirectory = useRef(false);
  const mounted = useRef(false);
  const downloadPending = useRef(false);
  const [refresh, setRefresh] = useState(0);
  const [raw, setRaw] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState('');
  const [error, setError] = useState('');
  const { databaseId } = useDraftNamespace();
  const transfer = useRef(
    new RequestIdentity(`${databaseId}:project-import:${id}`),
  );
  const filesystem = useFilesystemData();
  const load = useCallback(
    () => projectsAPI.files(id, folder, cursor),
    [id, folder, cursor],
  );
  const listing = useResource(`${id}:${folder}:${cursor}:${refresh}`, load);
  const read = useCallback(
    () => (file ? projectsAPI.read(id, file) : Promise.resolve(null)),
    [id, file],
  );
  const content = useResource(`${id}:${file}:${refresh}`, read);
  const navigate = (path: string) =>
    desktop.updateWindowState(window.id, {
      folder: path,
      file: '',
      cursor: '',
      reading: false,
    });
  const selected = content.data;

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  useEffect(() => {
    if (!reading) previewFocusLocation.current = null;
    if (!interactive) return;
    if (
      reading &&
      previewFocusLocation.current !== file &&
      directory.current?.getClientRects().length === 0 &&
      canTransferWindowFocus(preview.current, directory.current)
    ) {
      previewFocusLocation.current = file;
      preview.current?.focus({ preventScroll: true });
    } else if (!reading && returnToDirectory.current) {
      const selected = directory.current?.querySelector<HTMLButtonElement>(
        '[aria-current="true"]',
      );
      const destination = selected || directory.current;
      if (canTransferWindowFocus(destination, preview.current)) {
        returnToDirectory.current = false;
        destination?.focus({ preventScroll: true });
      }
    }
  }, [reading, file, interactive]);

  useEffect(() => {
    const surface = directory.current?.parentElement;
    if (!interactive || !surface) return;
    let compact = directory.current?.getClientRects().length === 0;
    const observer = new ResizeObserver(() => {
      const next = directory.current?.getClientRects().length === 0;
      if (
        next &&
        !compact &&
        reading &&
        canTransferWindowFocus(preview.current, directory.current)
      ) {
        previewFocusLocation.current = file;
        preview.current?.focus({ preventScroll: true });
      }
      compact = next;
    });
    observer.observe(surface);
    return () => observer.disconnect();
  }, [interactive, reading, file]);

  async function copyToDocuments() {
    if (!selected || busy) return;
    setBusy(true);
    setError('');
    setFeedback('');
    try {
      const payload = {
        path: selected.path,
        sha256: selected.sha256,
        parentId: 'documents',
      };
      const requestId = transfer.current.for({ id, ...payload });
      const { entry } = await projectsAPI.import(id, {
        ...payload,
        requestId,
      });
      desktopDatabase.assertUnchanged();
      const cleared = transfer.current.complete(requestId);
      if (mounted.current) {
        setFeedback(`Copied ${entry.name} to Documents.`);
        if (!cleared)
          setError(
            'The copy was saved, but its retry identity could not be cleared in this browser.',
          );
      }
      try {
        await filesystem.refresh();
        desktopDatabase.assertUnchanged();
        if (mounted.current)
          desktop.openApp(
            'notes',
            { entryId: entry.id },
            { title: entry.name },
          );
      } catch {
        if (mounted.current)
          setError(
            'The copy was saved, but the document list could not be refreshed. Refresh Documents before opening it.',
          );
      }
    } catch (error) {
      if (mounted.current)
        setError(error instanceof Error ? error.message : String(error));
    } finally {
      if (mounted.current) setBusy(false);
    }
  }

  async function download(path: string) {
    if (!mounted.current || !path || downloadPending.current) return;
    downloadPending.current = true;
    setDownloading(true);
    setError('');
    try {
      await projectsAPI.download(id, path);
    } catch (error) {
      if (mounted.current)
        setError(error instanceof Error ? error.message : String(error));
    } finally {
      downloadPending.current = false;
      if (mounted.current) setDownloading(false);
    }
  }

  return (
    <div className="project-browser">
      <div className="app-toolbar project-browser-toolbar">
        <div
          className="project-browser-navigation"
          role="group"
          aria-label="Project navigation"
        >
          <IconButton
            icon="up"
            label="Up"
            disabled={!folder}
            onClick={() => navigate(folder.split('/').slice(0, -1).join('/'))}
          />
          <IconButton
            icon="refresh"
            label="Refresh"
            onClick={() => {
              setRefresh((value) => value + 1);
              setFeedback('');
            }}
          />
        </div>
        <div
          className="project-browser-actions"
          role="group"
          aria-label="Project actions"
        >
          <Button
            disabled={!selected || busy}
            onClick={() => void copyToDocuments()}
          >
            {busy ? 'Copying…' : 'Copy to Documents'}
          </Button>
          <IconButton
            icon="more"
            label="More project actions"
            aria-haspopup="menu"
            onClick={(event) => {
              const rect = event.currentTarget.getBoundingClientRect();
              const path = file;
              desktop.contextMenu(
                [
                  ...(path
                    ? [
                        {
                          label: downloading ? 'Downloading…' : 'Download',
                          disabled: downloading,
                          action: () => void download(path),
                        },
                        { separator: true },
                      ]
                    : []),
                  {
                    label: 'Projects',
                    action: () => {
                      if (mounted.current) desktop.openApp('project-files');
                    },
                  },
                ],
                { x: rect.left, y: rect.bottom },
                { title: path.split('/').pop() || 'Project actions' },
              );
            }}
          />
        </div>
      </div>
      <div className="project-location">
        <div className="project-location-summary">
          <strong>Host · {listing.data?.project.name || window.title}</strong>
          <span>{folder || 'Project root'}</span>
        </div>
        <details>
          <summary>Details</summary>
          <dl className="info-list">
            <dt>Project directory</dt>
            <dd>
              <code>
                {listing.data?.project.rootPath || 'Loading project…'}
              </code>
            </dd>
            <dt>Current folder</dt>
            <dd>
              <code>{folder || 'Project root'}</code>
            </dd>
            <dt>Registered project</dt>
            <dd>
              <code>{id}</code>
            </dd>
          </dl>
        </details>
      </div>
      {(error || listing.error) && (
        <p className="project-error" role="alert">
          {error || listing.error}
        </p>
      )}
      {feedback && (
        <p className="project-feedback" role="status">
          {feedback}
        </p>
      )}
      {downloading && (
        <p className="project-feedback" role="status">
          Downloading…
        </p>
      )}
      <div className={`project-content${reading ? ' is-reading' : ''}`}>
        <nav
          className="project-directory"
          aria-label="Project directory"
          ref={directory}
          tabIndex={-1}
        >
          <ul>
            {listing.data?.entries.map((entry) => (
              <li key={entry.path}>
                <button
                  className={entry.path === file ? 'is-selected' : ''}
                  aria-current={entry.path === file ? 'true' : undefined}
                  disabled={entry.kind === 'symlink'}
                  onClick={(event) => {
                    event.currentTarget.focus({ preventScroll: true });
                    setError('');
                    setFeedback('');
                    if (entry.kind === 'folder') navigate(entry.path);
                    else
                      desktop.updateWindowState(window.id, {
                        file: entry.path,
                        reading: true,
                      });
                  }}
                >
                  <PixelIcon
                    size={16}
                    name={entry.kind === 'folder' ? 'folder' : 'document'}
                  />
                  <span>
                    {entry.name}
                    {entry.kind === 'symlink' && ' (symlink)'}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          {!listing.data && !listing.error && (
            <p role="status">Loading directory…</p>
          )}
          {listing.data?.entries.length === 0 && (
            <p>This directory is empty.</p>
          )}
          <div className="project-pagination">
            {cursor && (
              <Button
                onClick={() =>
                  desktop.updateWindowState(window.id, { cursor: '' })
                }
              >
                First page
              </Button>
            )}
            {listing.data?.nextCursor && (
              <Button
                onClick={() =>
                  desktop.updateWindowState(window.id, {
                    cursor: listing.data!.nextCursor!,
                  })
                }
              >
                Next page
              </Button>
            )}
            {listing.data?.truncated && !listing.data.nextCursor && (
              <p>Directory limit reached.</p>
            )}
          </div>
        </nav>
        <section
          className={`project-preview${selected && (!isMarkdownFilename(file) || raw) ? ' project-code-preview' : ''}`}
          aria-label="Project file preview"
          ref={preview}
          tabIndex={0}
        >
          <Button
            className="project-back"
            onClick={(event) => {
              event.currentTarget.focus({ preventScroll: true });
              returnToDirectory.current = true;
              desktop.updateWindowState(window.id, { reading: false });
            }}
          >
            Back
          </Button>
          {!file ? (
            <p>Select a file to preview. Originals stay in the host folder.</p>
          ) : (
            <>
              <div className="project-preview-heading">
                <code>{file}</code>
                {isMarkdownFilename(file) && (
                  <Button onClick={() => setRaw((value) => !value)}>
                    {raw ? 'Preview' : 'Source'}
                  </Button>
                )}
              </div>
              {content.error ? (
                <>
                  <p role="alert">{content.error}</p>
                  <Button
                    disabled={downloading}
                    onClick={() => void download(file)}
                  >
                    {downloading ? 'Downloading…' : 'Download'}
                  </Button>
                </>
              ) : !selected ? (
                <p role="status">Loading file…</p>
              ) : isMarkdownFilename(file) && !raw ? (
                <MarkdownPreview content={selected.content} />
              ) : (
                <CodeEditor
                  documentId={`${id}:${file}`}
                  filename={file}
                  label="Project file source"
                  value={selected.content}
                  readOnly
                  disabled={!interactive}
                />
              )}
            </>
          )}
        </section>
      </div>
      <RelatedDocuments target={{ projectId: id }} desktop={desktop} />
      <StatusBar>
        {file || folder || 'Project root'}
        {selected && ` · ${selected.size.toLocaleString()} bytes`}
      </StatusBar>
    </div>
  );
}
