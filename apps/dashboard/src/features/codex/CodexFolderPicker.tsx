import { Button, Field, IconButton, PixelIcon } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import type { CodexProject, HostDirectories } from './api';

export interface CodexFolderPickerProps {
  desktop: DesktopAPI;
  windowId: string;
  path: string;
  directories: HostDirectories | null;
  directoryError: string;
  projectError: string;
  recentProjects: CodexProject[];
  browsing: boolean;
  creating: 'default' | 'folder' | null;
  canStart: boolean;
  onChangePath: (value: string) => void;
  onBrowse: (next?: string) => void;
  onClose: (restoreFocus?: boolean) => void;
  onStart: (directory: string) => void;
}

export function CodexFolderPicker({
  desktop,
  windowId,
  path,
  directories,
  directoryError,
  projectError,
  recentProjects,
  browsing,
  creating,
  canStart,
  onChangePath,
  onBrowse,
  onClose,
  onStart,
}: CodexFolderPickerProps) {
  return (
    <section
      id="codex-folder-picker"
      className="codex-section codex-folder-picker"
      aria-label="New Codex session"
      onKeyDown={(event) => {
        if (
          event.key === 'Escape' &&
          !event.nativeEvent.isComposing &&
          creating === null
        ) {
          event.preventDefault();
          event.stopPropagation();
          onClose(true);
        }
      }}
    >
      <form
        className="codex-path-form"
        onSubmit={(event) => {
          event.preventDefault();
          onBrowse(path);
        }}
      >
        <Field
          label="Project directory"
          value={path}
          disabled={creating !== null}
          onChange={(event) => onChangePath(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
              event.preventDefault();
              if (!browsing && creating === null) onBrowse(path);
            }
          }}
        />
        <div className="codex-directory-actions">
          <Button type="submit" disabled={browsing || creating !== null}>
            Go
          </Button>
          <Button
            disabled={browsing || creating !== null}
            onClick={() => onBrowse()}
          >
            Home
          </Button>
          <IconButton
            icon="up"
            label="Up"
            disabled={
              browsing ||
              creating !== null ||
              !directories?.parentPath ||
              !!directoryError
            }
            onClick={() => onBrowse(directories?.parentPath || undefined)}
          />
          {(recentProjects.length > 0 || projectError) && (
            <section className="codex-recent" aria-label="Recent projects">
              <Button
                aria-haspopup="menu"
                disabled={creating !== null || !recentProjects.length}
                onKeyDown={(event) => {
                  if (
                    event.key === 'ArrowDown' &&
                    !event.altKey &&
                    !event.ctrlKey &&
                    !event.metaKey &&
                    !event.nativeEvent.isComposing
                  ) {
                    event.preventDefault();
                    event.currentTarget.click();
                  }
                }}
                onClick={(event) => {
                  const rect = event.currentTarget.getBoundingClientRect();
                  desktop.contextMenu(
                    recentProjects.map((project) => {
                      // Host projects use canonical POSIX paths; a
                      // backslash can be part of a directory name.
                      const separator = project.path.lastIndexOf('/');
                      return {
                        label:
                          project.path.slice(separator + 1) || project.path,
                        description:
                          project.path === '/'
                            ? undefined
                            : project.path.slice(0, separator) || '/',
                        action: () => {
                          onChangePath(project.path);
                          onBrowse(project.path);
                        },
                      };
                    }),
                    { x: rect.right, y: rect.bottom },
                    {
                      title: 'Recent Projects',
                      ownerWindowId: windowId,
                      align: 'end',
                      width: 'wide',
                    },
                  );
                }}
              >
                Recent Projects{projectError && ' · Outdated'}
                <span aria-hidden="true">▾</span>
              </Button>
            </section>
          )}
        </div>
      </form>
      {projectError && (
        <p className="app-error" role="alert">
          {projectError}
        </p>
      )}
      {directoryError && (
        <p className="app-error" role="alert">
          {directoryError}
        </p>
      )}
      {!browsing && directories && path !== directories.path && (
        <p
          className="codex-note codex-directory-context"
          data-testid="codex-directory-context"
        >
          Showing: <code>{directories.path}</code>
        </p>
      )}
      <div
        className="codex-directory-list"
        aria-label="Host folders"
        aria-busy={browsing}
      >
        {!browsing && directoryError && directories && (
          <p className="codex-note">Previous folder contents · Outdated</p>
        )}
        {browsing ? (
          <p role="status">Loading folders…</p>
        ) : (
          directories?.directories.map((directory) => (
            <button
              type="button"
              key={directory.path}
              disabled={creating !== null || !!directoryError}
              aria-label={`Open folder ${directory.name}`}
              onClick={() => onBrowse(directory.path)}
            >
              <PixelIcon name="folder" size={16} />
              <span>{directory.name}</span>
            </button>
          ))
        )}
        {!browsing && directories?.directories.length === 0 && (
          <p>No subfolders.</p>
        )}
      </div>
      {directories?.truncated && (
        <p className="codex-note">
          Some folders are not shown. Enter a full path to browse.
        </p>
      )}
      <div className="codex-project-choice">
        <Button disabled={creating !== null} onClick={() => onClose(true)}>
          Cancel
        </Button>
        <Button
          primary
          disabled={
            !canStart ||
            !directories ||
            browsing ||
            !!directoryError ||
            creating !== null ||
            path !== directories.path
          }
          onClick={() => {
            if (directories) onStart(directories.path);
          }}
        >
          {creating === 'folder' ? 'Starting…' : 'Start Here'}
        </Button>
      </div>
    </section>
  );
}
