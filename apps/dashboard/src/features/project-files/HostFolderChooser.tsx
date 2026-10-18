import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Field, PixelIcon } from '@cofy-x/axv-nextstep';
import { projectsAPI } from './api';
import type { HostDirectories } from './api';

export function HostFolderChooser({
  initialPath,
  onChoose,
  onCancel,
}: {
  initialPath: string;
  onChoose: (path: string) => void;
  onCancel: () => void;
}) {
  const [path, setPath] = useState(initialPath);
  const [directories, setDirectories] = useState<HostDirectories | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState('');
  const request = useRef<AbortController | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const browse = useCallback(async (next?: string) => {
    request.current?.abort();
    const current = new AbortController();
    request.current = current;
    setBusy(true);
    setError('');
    try {
      const result = await projectsAPI.directories(next, current.signal);
      if (request.current !== current || current.signal.aborted) return;
      setDirectories(result);
      setPath(result.path);
    } catch (error) {
      if (request.current === current && !current.signal.aborted)
        setError(error instanceof Error ? error.message : String(error));
    } finally {
      if (request.current === current && !current.signal.aborted)
        setBusy(false);
    }
  }, []);

  useEffect(() => {
    heading.current?.focus();
    void browse(initialPath || undefined);
    return () => request.current?.abort();
  }, [browse, initialPath]);

  return (
    <section
      className="project-folder-chooser"
      aria-label="Choose a host folder"
    >
      <h2 ref={heading} tabIndex={-1}>
        Choose a host folder
      </h2>
      <p>
        Selecting a folder fills the project path. Choose Open Project to
        register it.
      </p>
      <form
        className="project-folder-path"
        onSubmit={(event) => {
          event.preventDefault();
          void browse(path.trim() || undefined);
        }}
      >
        <Field
          label="Folder path"
          value={path}
          disabled={busy}
          onChange={(event) => setPath(event.target.value)}
        />
        <div className="project-folder-navigation">
          <Button type="submit" disabled={busy}>
            Go
          </Button>
          <Button disabled={busy} onClick={() => void browse()}>
            Home
          </Button>
          <Button
            disabled={busy || !directories?.parentPath}
            onClick={() => void browse(directories?.parentPath || undefined)}
          >
            Up
          </Button>
        </div>
      </form>
      {error && (
        <p className="app-error" role="alert">
          {error}
        </p>
      )}
      <div
        className="project-host-folders"
        aria-label="Host folders"
        aria-busy={busy}
      >
        {busy ? (
          <p role="status">Reading host directories…</p>
        ) : (
          directories?.directories.map((directory) => (
            <Button
              key={directory.path}
              aria-label={`Open folder ${directory.name}`}
              onClick={() => void browse(directory.path)}
            >
              <PixelIcon name="folder" size={16} />
              <span>{directory.name}</span>
            </Button>
          ))
        )}
        {!busy && !error && directories?.directories.length === 0 && (
          <p>No subfolders. You can use this directory.</p>
        )}
      </div>
      {directories?.truncated && (
        <p>
          Only part of this folder is listed. Enter a full path to navigate
          directly.
        </p>
      )}
      <code className="project-folder-current">
        {directories?.path || 'Choose an accessible directory.'}
      </code>
      <div className="app-actions">
        <Button onClick={onCancel}>Cancel</Button>
        <Button
          primary
          disabled={
            busy || !!error || !directories || path !== directories.path
          }
          onClick={() => {
            if (directories) onChoose(directories.path);
          }}
        >
          Use Folder
        </Button>
      </div>
    </section>
  );
}
