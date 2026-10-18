import { useState } from 'react';
import { StatusBar } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { filesystemApi } from '../../filesystem/api';
import { isInTrash } from '../../filesystem/tree';
import { readableSize } from '../../filesystem/format';
import { useFilesystemData } from '../../filesystem/context';

export function ImageViewer({ window }: AppWindowProps) {
  const data = useFilesystemData();
  const entry = data.entries.find(
    (entry) => entry.id === window.params.entryId,
  );
  const [error, setError] = useState(false);
  return (
    <div className="image-app">
      {entry && !isInTrash(entry, data.entries) && !error ? (
        <img
          src={filesystemApi.contentURL(entry.id)}
          alt={entry.name}
          onError={() => setError(true)}
        />
      ) : (
        <p>This image is unavailable.</p>
      )}
      <StatusBar>
        {entry
          ? `${entry.name} · ${readableSize(entry.size)}`
          : 'Image unavailable'}
      </StatusBar>
    </div>
  );
}
