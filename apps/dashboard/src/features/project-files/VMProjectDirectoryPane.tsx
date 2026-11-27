import type { RefObject } from 'react';
import { Button, PixelIcon } from '@cofy-x/axv-nextstep';
import type { VMProjectBinding, vmProjectsAPI } from './vm-api';

type VMProjectFileListing = Awaited<ReturnType<typeof vmProjectsAPI.files>>;

export interface VMProjectDirectoryPaneProps {
  directoryRef: RefObject<HTMLElement | null>;
  binding: VMProjectBinding | null;
  pendingRecovery: boolean;
  listingData?: VMProjectFileListing | null;
  listingError?: string;
  folder: string;
  file: string;
  cursor: string;
  interactive: boolean;
  saving: boolean;
  composing: boolean;
  onNavigate: (path: string, nextFile?: string) => void;
  onCursorChange: (nextCursor: string) => void;
}

export function VMProjectDirectoryPane({
  directoryRef,
  binding,
  pendingRecovery,
  listingData,
  listingError,
  folder,
  file,
  cursor,
  interactive,
  saving,
  composing,
  onNavigate,
  onCursorChange,
}: VMProjectDirectoryPaneProps) {
  return (
    <nav
      className="project-directory"
      aria-label="VM project directory"
      ref={directoryRef}
      tabIndex={-1}
    >
      <ul>
        {listingData?.entries.map((entry) => (
          <li key={entry.path}>
            <button
              disabled={
                !interactive || saving || composing || entry.kind === 'symlink'
              }
              className={entry.path === file ? 'is-selected' : ''}
              aria-current={entry.path === file ? 'true' : undefined}
              onClick={(event) => {
                event.currentTarget.focus({ preventScroll: true });
                onNavigate(
                  entry.kind === 'folder' ? entry.path : folder,
                  entry.kind === 'file' ? entry.path : '',
                );
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
      {binding && !pendingRecovery && !listingData && !listingError && (
        <p role="status">Loading directory…</p>
      )}
      {listingData?.entries.length === 0 && <p>This directory is empty.</p>}
      <div className="project-pagination">
        {cursor && (
          <Button disabled={!interactive} onClick={() => onCursorChange('')}>
            First page
          </Button>
        )}
        {listingData?.nextCursor && (
          <Button
            disabled={!interactive}
            onClick={() => onCursorChange(listingData.nextCursor!)}
          >
            Next page
          </Button>
        )}
        {listingData?.truncated && !listingData.nextCursor && (
          <p>Directory limit reached.</p>
        )}
      </div>
    </nav>
  );
}
