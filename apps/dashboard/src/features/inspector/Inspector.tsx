import { useEffect, useRef, useState } from 'react';
import { Button, IconButton, PixelIcon, StatusBar } from '@cofy-x/axv-nextstep';
import type { AppWindowProps, MenuItem } from '@cofy-x/axv-nextstep';
import { useFilesystemData } from '../../filesystem/context';
import type { FilesystemData } from '../../filesystem/context';
import { filesystemApi } from '../../filesystem/api';
import type { Entry } from '../../filesystem/types';
import { affectedEntries, isInTrash } from '../../filesystem/tree';
import { readableSize } from '../../filesystem/format';
import {
  entryIcon,
  openEntry,
  downloadEntry,
} from '../../filesystem/presentation';
import {
  renameEntry,
  trashEntries,
  updateMetadata,
} from '../../filesystem/commands';
import { runCommand } from '../../shared/desktop/dialogs';
import { entryPath } from '../files/navigation';
import { isMarkdownFilename, MarkdownPreview } from '../../shared/markdown';

export function Inspector({ window, desktop }: AppWindowProps) {
  const data = useFilesystemData();
  const pinned = window.viewState.pinned === true;
  const latest = data.selectionWindowId
    ? data.selections[data.selectionWindowId]?.[0]
    : undefined;
  const storedId =
    typeof window.viewState.targetId === 'string'
      ? window.viewState.targetId
      : undefined;
  const targetId = !pinned && latest ? latest : storedId;
  const entry = data.entries.find((item) => item.id === targetId);
  const mounted = useRef(false);
  const current = useRef({ data, targetId });
  current.current = { data, targetId };
  const [preview, setPreview] = useState<{
    id: string;
    revision: number;
    text?: string;
    error?: string;
  } | null>(null);
  const trashed = entry ? isInTrash(entry, data.entries) : false;
  const descendants =
    entry?.kind === 'folder'
      ? affectedEntries(
          data.entries.filter((item) => !isInTrash(item, data.entries)),
          [entry.id],
        ).filter((item) => item.id !== entry.id)
      : [];
  const size =
    entry?.kind === 'folder'
      ? descendants.reduce((total, item) => total + item.size, 0)
      : entry?.size || 0;
  const path = entry ? entryPath(data.entries, entry) : '';
  const entryId = entry?.id;
  const entryRevision = entry?.revision;
  const editable = entry?.editable;
  const command = (action: () => Promise<void>) => {
    void runCommand(desktop, action);
  };

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (targetId)
      desktop.updateWindowState(window.id, {
        targetId,
        ...(entry ? { lastName: entry.name } : {}),
      });
  }, [targetId, entry, desktop, window.id]);
  useEffect(() => {
    if (!entryId || !editable || entryRevision === undefined) return;
    let active = true;
    void filesystemApi
      .entry(entryId)
      .then((result) => {
        if (active)
          setPreview({
            id: entryId,
            revision: entryRevision,
            text: result.content?.slice(0, 8000) ?? '',
          });
      })
      .catch((error: unknown) => {
        if (active)
          setPreview({
            id: entryId,
            revision: entryRevision,
            error: error instanceof Error ? error.message : String(error),
          });
      });
    return () => {
      active = false;
    };
  }, [entryId, entryRevision, editable]);

  async function move(target: Entry, model: FilesystemData) {
    const excluded = new Set(
      affectedEntries(model.entries, [target.id]).map((item) => item.id),
    );
    const destinations = model.entries.filter(
      (item) =>
        item.kind === 'folder' &&
        !isInTrash(item, model.entries) &&
        !excluded.has(item.id),
    );
    let destination = target.parentId;
    const result = await desktop.dialog({
      title: 'Move Item',
      message: (
        <label className="move-destination">
          Move “{target.name}” to
          <select
            aria-label="Destination folder"
            defaultValue={destination}
            onChange={(event) => {
              destination = event.target.value;
            }}
          >
            {destinations.map((folder) => (
              <option key={folder.id} value={folder.id}>
                {entryPath(model.entries, folder)}
              </option>
            ))}
          </select>
        </label>
      ),
      buttons: [
        { label: 'Cancel', value: 'cancel', cancel: true },
        { label: 'Move', value: 'move', default: true },
      ],
    });
    if (result?.button !== 'move' || destination === target.parentId) return;
    await updateMetadata(model, target, { parentId: destination });
    await model.refresh();
  }

  function actionMenu(target: Entry): MenuItem[] {
    const available = (item: Entry, model: FilesystemData) =>
      !isInTrash(item, model.entries);
    const mutable = (item: Entry, model: FilesystemData) =>
      !item.system && available(item, model);
    const invoke =
      (
        allowed: (item: Entry, model: FilesystemData) => boolean,
        action: (item: Entry, model: FilesystemData) => void | Promise<void>,
      ) =>
      () => {
        if (!mounted.current) return;
        command(async () => {
          const latest = current.current;
          if (latest.targetId !== target.id)
            throw new Error(
              'The Inspector target changed. Open the actions menu again.',
            );
          const item = latest.data.entries.find(
            (item) => item.id === target.id,
          );
          if (!item) throw new Error('This item is no longer available.');
          if (!allowed(item, latest.data))
            throw new Error(
              'This action is no longer available for this item.',
            );
          await action(item, latest.data);
        });
      };
    return [
      ...(target.editable
        ? [
            {
              label: 'Details',
              disabled: trashed,
              action: invoke(
                (item, model) => item.editable && available(item, model),
                (item) => {
                  const id = desktop.openApp('document-workflow', {
                    entryId: item.id,
                    task: 'details',
                  });
                  desktop.updateWindowState(id, { task: 'details' });
                },
              ),
            },
          ]
        : []),
      {
        label: 'Rename',
        disabled: target.system || trashed,
        action: invoke(mutable, (item, model) =>
          renameEntry(model, desktop, item),
        ),
      },
      {
        label: 'Move',
        disabled: target.system || trashed,
        action: invoke(mutable, move),
      },
      ...(target.kind === 'file'
        ? [
            {
              label: 'Download',
              action: invoke(
                (item) => item.kind === 'file',
                (item) => downloadEntry(item),
              ),
            },
          ]
        : []),
      ...(!trashed
        ? [
            { separator: true },
            {
              label: 'Recycle',
              disabled: target.system,
              action: invoke(mutable, (item, model) =>
                trashEntries(model, desktop, [item.id]),
              ),
            },
          ]
        : []),
    ];
  }

  const currentPreview =
    preview && preview.id === entryId && preview.revision === entryRevision
      ? preview
      : null;
  return (
    <div className="inspector-app">
      <div className="app-toolbar">
        <Button
          aria-pressed={pinned}
          onClick={() =>
            desktop.updateWindowState(window.id, { pinned: !pinned, targetId })
          }
        >
          {pinned ? 'Follow' : 'Pin'}
        </Button>
        <span className="toolbar-caption">
          {pinned ? 'Pinned to this item' : 'Following selection'}
        </span>
      </div>
      {!entry ? (
        <div className="app-pad">
          <PixelIcon name="info" />
          <p>
            {targetId
              ? `“${String(window.viewState.lastName || 'This item')}” is no longer available.`
              : 'Select an item in Files to inspect it.'}
          </p>
          {targetId && (
            <p>
              Your pinned target is retained until you follow another selection.
            </p>
          )}
        </div>
      ) : (
        <div className="inspector-content">
          <div className="inspector-heading">
            <PixelIcon name={entryIcon(entry)} size={48} />
            <div>
              <strong data-testid="inspector-name">{entry.name}</strong>
              <span>
                {entry.kind === 'folder'
                  ? 'Folder'
                  : entry.editable
                    ? 'Plain text document'
                    : entry.mime}
              </span>
            </div>
          </div>
          {trashed && (
            <p className="editor-recovery" role="status">
              This item is in the Recycler.
            </p>
          )}
          <dl className="info-list inspector-metadata">
            <dt>Location</dt>
            <dd data-testid="inspector-path">{path}</dd>
            <dt>Size</dt>
            <dd>
              {readableSize(size)}
              {entry.kind === 'folder'
                ? ` · ${descendants.length} nested items`
                : ''}
            </dd>
            <dt>Created</dt>
            <dd>{new Date(entry.createdAt).toLocaleString()}</dd>
            <dt>Modified</dt>
            <dd>{new Date(entry.updatedAt).toLocaleString()}</dd>
            <dt>Revision</dt>
            <dd data-testid="inspector-revision">{entry.revision}</dd>
            <dt>Access</dt>
            <dd>
              {entry.system
                ? 'Protected system folder'
                : trashed
                  ? 'Restore to edit'
                  : entry.kind === 'folder'
                    ? 'Read and organize'
                    : entry.editable
                      ? 'Read and write text'
                      : 'Read and download'}
            </dd>
          </dl>
          <div className="inspector-actions">
            <Button
              disabled={trashed}
              onClick={() => openEntry(entry, desktop)}
            >
              Open
            </Button>
            <IconButton
              icon="more"
              label="More inspector actions"
              aria-haspopup="menu"
              onClick={(event) => {
                const rect = event.currentTarget.getBoundingClientRect();
                desktop.contextMenu(
                  actionMenu(entry),
                  { x: rect.left, y: rect.bottom },
                  { title: entry.name },
                );
              }}
            />
            {trashed && (
              <Button
                onClick={() =>
                  command(async () => {
                    await filesystemApi.action(entry.id, 'restore');
                    await data.refresh();
                  })
                }
              >
                Restore
              </Button>
            )}
          </div>
          {entry.kind === 'file' && (
            <section className="inspector-preview" aria-label="File preview">
              <h2>Preview</h2>
              {entry.previewable ? (
                <img
                  alt={`Preview of ${entry.name}`}
                  src={`${filesystemApi.contentURL(entry.id)}&revision=${entry.revision}`}
                />
              ) : entry.editable ? (
                currentPreview?.error ? (
                  <p role="alert">{currentPreview.error}</p>
                ) : currentPreview ? (
                  <>
                    {isMarkdownFilename(entry.name) ? (
                      <MarkdownPreview content={currentPreview.text ?? ''} />
                    ) : (
                      <pre>{currentPreview.text}</pre>
                    )}
                    {entry.size > 8000 && (
                      <small>First 8,000 characters. Open for full text.</small>
                    )}
                  </>
                ) : (
                  <p>Loading preview…</p>
                )
              ) : (
                <p>This format is available for download.</p>
              )}
            </section>
          )}
        </div>
      )}
      <StatusBar>
        {pinned ? 'Pinned inspector' : 'Selection inspector'}
        {entry?.system ? ' · protected' : ''}
      </StatusBar>
    </div>
  );
}
