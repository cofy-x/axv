import { useCallback, useState } from 'react';
import { Button, IconButton } from '@cofy-x/axv-nextstep';
import type { DesktopAPI } from '@cofy-x/axv-nextstep';
import { documentsAPI } from '../../filesystem/documents';
import { useFilesystemData } from '../../filesystem/context';
import { isInTrash } from '../../filesystem/tree';
import { useDesktopResource as useResource } from '../../shared/api/useDesktopResource';

export function RelatedDocuments({
  target,
  desktop,
}: {
  target: Record<string, string>;
  desktop: DesktopAPI;
}) {
  const encoded = new URLSearchParams(target).toString();
  const [revision, refresh] = useState(0);
  const load = useCallback(
    () =>
      documentsAPI.related(Object.fromEntries(new URLSearchParams(encoded))),
    [encoded],
  );
  const { data, error } = useResource(`${encoded}:${revision}`, load);
  const files = useFilesystemData();
  return (
    <details className="document-related">
      <summary>Related documents ({data?.links.length ?? '…'})</summary>
      {error && <p role="alert">{error}</p>}
      <IconButton
        icon="refresh"
        label="Refresh documents"
        onClick={() => refresh((value) => value + 1)}
      />
      {data?.links.length === 0 && <p>No documents linked to this target.</p>}
      {data?.links.map((link) => {
        const entry = files.entries.find((item) => item.id === link.entryId);
        const unavailable = !entry || isInTrash(entry, files.entries);
        return (
          <div className="document-related-item" key={link.id}>
            <Button
              disabled={unavailable}
              onClick={() =>
                desktop.openApp(
                  'notes',
                  { entryId: link.entryId },
                  { title: entry?.name },
                )
              }
            >
              {entry?.name || 'Document unavailable'}
            </Button>
            {unavailable && <span>Unavailable</span>}
          </div>
        );
      })}
    </details>
  );
}
