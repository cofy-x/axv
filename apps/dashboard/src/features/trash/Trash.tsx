import { useEffect, useRef, useState } from 'react';
import { Button, IconButton, PixelIcon, StatusBar } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { APIError } from '../../shared/api/http';
import { filesystemApi } from '../../filesystem/api';
import { readableSize } from '../../filesystem/format';
import { entryIcon } from '../../filesystem/presentation';
import { useFilesystemData } from '../../filesystem/context';
import { runCommand } from '../../shared/desktop/dialogs';

export function Trash({ desktop }: AppWindowProps) {
  const data = useFilesystemData();
  const [selected, setSelected] = useState<string[]>([]);
  // Display trash roots, including items trashed independently before their parent.
  const entries = data.entries.filter(
    (entry) => entry.trashedAt && !entry.system,
  );
  const [busy, setBusy] = useState(false);
  const mounted = useRef(false);
  const current = useRef({ busy, entries });
  current.current = { busy, entries };

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  async function mutate(permanent: boolean, all = false, ids = selected) {
    if (permanent) {
      const answer = await desktop.dialog({
        title: all ? 'Empty the Recycler?' : 'Delete permanently?',
        message:
          'These files will be permanently removed. This action cannot be undone.',
        buttons: [
          { label: 'Cancel', value: 'cancel', cancel: true },
          { label: 'Delete', value: 'delete', default: true },
        ],
      });
      if (answer?.button !== 'delete') return;
    }
    setBusy(true);
    try {
      if (all) await filesystemApi.emptyTrash();
      else
        for (const id of ids) {
          // A preceding parent operation may have already restored/deleted this
          // descendant; separately trashed descendants still need their own action.
          const current = (await filesystemApi.entries()).entries.find(
            (entry) => entry.id === id,
          );
          if (!current?.trashedAt) continue;
          try {
            if (permanent) await filesystemApi.remove(id);
            else await filesystemApi.action(id, 'restore');
          } catch (error) {
            if (!(error instanceof APIError && error.status === 404))
              throw error;
          }
        }
      setSelected([]);
    } finally {
      try {
        await data.refresh();
      } finally {
        setBusy(false);
      }
    }
  }

  return (
    <div className="trash-app">
      <div className="app-toolbar">
        <Button
          disabled={busy || !selected.length}
          onClick={() => void runCommand(desktop, () => mutate(false))}
        >
          Restore
        </Button>
        <IconButton
          icon="more"
          label="More recycler actions"
          aria-haspopup="menu"
          disabled={busy || !entries.length}
          onClick={(event) => {
            const ids = [...selected];
            const invoke = (all: boolean) => () => {
              if (!mounted.current || current.current.busy) return;
              if (all && !current.current.entries.length) return;
              void runCommand(desktop, () => mutate(true, all, ids));
            };
            const rect = event.currentTarget.getBoundingClientRect();
            desktop.contextMenu(
              [
                {
                  label: 'Delete',
                  disabled: !ids.length,
                  action: invoke(false),
                },
                { separator: true },
                { label: 'Empty Recycler', action: invoke(true) },
              ],
              { x: rect.left, y: rect.bottom },
              { title: 'Recycler' },
            );
          }}
        />
      </div>
      <div
        className="trash-list"
        role="listbox"
        aria-label="Trashed items"
        aria-multiselectable="true"
      >
        {entries.map((entry) => (
          <button
            key={entry.id}
            role="option"
            aria-selected={selected.includes(entry.id)}
            className={`trash-row ${selected.includes(entry.id) ? 'selected' : ''}`}
            onClick={(event) =>
              setSelected((previous) =>
                event.metaKey || event.ctrlKey || event.shiftKey
                  ? previous.includes(entry.id)
                    ? previous.filter((id) => id !== entry.id)
                    : [...previous, entry.id]
                  : [entry.id],
              )
            }
          >
            <PixelIcon name={entryIcon(entry)} size={16} />
            <span>{entry.name}</span>
            <small>
              {entry.kind === 'folder' ? 'Folder' : readableSize(entry.size)}
            </small>
          </button>
        ))}
        {!entries.length && (
          <div className="empty-folder">
            <PixelIcon name="trash" />
            <p>The Recycler is empty.</p>
          </div>
        )}
      </div>
      <StatusBar>{entries.length} items</StatusBar>
    </div>
  );
}
