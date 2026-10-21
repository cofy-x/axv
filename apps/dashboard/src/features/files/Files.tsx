import { useEffect, useRef, useState } from 'react';
import type { DragEvent, KeyboardEvent, PointerEvent } from 'react';
import {
  Button,
  IconButton,
  PixelIcon,
  Select,
  StatusBar,
} from '@cofy-x/axv-nextstep';
import type { AppWindowProps, MenuItem } from '@cofy-x/axv-nextstep';
import { filesystemApi } from '../../filesystem/api';
import { isInTrash } from '../../filesystem/tree';
import { readableSize } from '../../filesystem/format';
import type { Entry } from '../../filesystem/types';
import { entryIcon, openEntry } from '../../filesystem/presentation';
import {
  pasteEntries,
  renameEntry,
  trashEntries,
  moveEntries,
  getInfo,
} from '../../filesystem/commands';
import { runCommand } from '../../shared/desktop/dialogs';
import { useFilesystemData } from '../../filesystem/context';
import { folderEntries } from './file-list';
import type { FileSort } from './file-list';
import {
  createFilesActionMenu,
  createFilesContextMenu,
  createFilesNewMenu,
} from './menus';
import type { FileMenuOptions } from './menus';
import { folderPath, reconcileNavigation } from './navigation';

export function Files({ window, desktop }: AppWindowProps) {
  const data = useFilesystemData();
  const state = window.viewState;
  const { updateWindowState, setTitle } = desktop;
  const { entries } = data;
  const anchorId = window.params.folderId || 'documents';
  const { folderId } = reconcileNavigation(entries, state, anchorId);
  const path = folderPath(entries, folderId);
  const folder = path.at(-1);
  const view: FileMenuOptions['view'] =
    state.view === 'list' ? 'list' : 'columns';
  const sort: FileSort = ['name', 'modified', 'size', 'kind'].includes(
    String(state.sort),
  )
    ? (state.sort as FileSort)
    : 'name';
  const descending = state.descending === true;
  const widths =
    state.columnWidths && typeof state.columnWidths === 'object'
      ? (state.columnWidths as Record<string, number>)
      : {};
  const [busy, setBusy] = useState(false);
  const [dropTarget, setDropTarget] = useState<string | null>(null);
  const input = useRef<HTMLInputElement>(null);
  const browser = useRef<HTMLDivElement>(null);
  const selected =
    data.selections[window.id] ||
    (Array.isArray(state.selectionIds)
      ? state.selectionIds.filter((id): id is string => typeof id === 'string')
      : []);
  const live = data.entries.filter((entry) => !isInTrash(entry, data.entries));
  const selection = live.filter((entry) => selected.includes(entry.id));
  const typeahead = useRef({ value: '', time: 0 });
  const hold = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const entryPointerType = useRef('mouse');
  const lastSelected = useRef<string | undefined>(undefined);
  const firstRender = useRef(true);
  const mounted = useRef(false);
  const latest = useRef({ data, desktop, folderId, selected, view });
  latest.current = { data, desktop, folderId, selected, view };
  const command = (action: () => Promise<void>) => {
    void runCommand(desktop, action);
  };

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      clearTimeout(hold.current);
    };
  }, []);
  useEffect(() => {
    // Metadata refreshes repair the latest location, never a prior render's
    // snapshot that could overwrite navigation queued by a click or tap.
    updateWindowState(window.id, (current) =>
      reconcileNavigation(entries, current, anchorId),
    );
  }, [updateWindowState, window.id, entries, anchorId]);
  useEffect(() => {
    if (folder) setTitle(window.id, folder.name);
  }, [folder, setTitle, window.id]);
  useEffect(() => {
    if (firstRender.current && browser.current) {
      browser.current.scrollLeft =
        typeof state.scrollLeft === 'number' ? state.scrollLeft : 0;
      firstRender.current = false;
    }
  }, [state.scrollLeft]);

  function update(patch: Record<string, unknown>) {
    updateWindowState(window.id, patch);
  }

  function select(ids: string[]) {
    data.select(window.id, ids);
    update({ selectionIds: ids });
  }

  function navigate(id: string) {
    const next = folderPath(data.entries, id);
    update({ folderId: id, pathIds: next.map((entry) => entry.id) });
  }

  function focusColumn(id: string) {
    requestAnimationFrame(() => {
      const element = browser.current?.querySelector<HTMLElement>(
        `[data-column-id="${CSS.escape(id)}"]`,
      );
      element?.focus();
      element?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    });
  }

  function pick(
    entry: Entry,
    siblings: Entry[],
    event: { shiftKey: boolean; metaKey: boolean; ctrlKey: boolean },
  ) {
    let ids = [entry.id];
    if (event.shiftKey && lastSelected.current) {
      const anchor = siblings.findIndex(
        (item) => item.id === lastSelected.current,
      );
      const end = siblings.findIndex((item) => item.id === entry.id);
      if (anchor >= 0)
        ids = siblings
          .slice(Math.min(anchor, end), Math.max(anchor, end) + 1)
          .map((item) => item.id);
    } else if (event.metaKey || event.ctrlKey) {
      ids = selected.includes(entry.id)
        ? selected.filter((id) => id !== entry.id)
        : [
            ...selected.filter((id) => siblings.some((item) => item.id === id)),
            entry.id,
          ];
    }
    lastSelected.current = entry.id;
    select(ids);
    if (!event.shiftKey && !event.metaKey && !event.ctrlKey)
      navigate(entry.kind === 'folder' ? entry.id : entry.parentId);
  }

  function menu(entry?: Entry, parentId = folderId): MenuItem[] {
    return createFilesContextMenu(
      {
        data,
        desktop,
        folderId: parentId,
        selected,
        selection,
        view,
        onViewChange: (next) => update({ view: next }),
        onImport: () => input.current?.click(),
      },
      entry,
    );
  }

  function toolbarMenu(kind: 'new' | 'actions'): MenuItem[] {
    const captured = latest.current;
    const targetIds = [...captured.selected];
    const build = kind === 'new' ? createFilesNewMenu : createFilesActionMenu;
    const options = (current: typeof captured): FileMenuOptions => ({
      data: current.data,
      desktop: current.desktop,
      folderId: current.folderId,
      selected: current.selected,
      selection: current.data.entries.filter(
        (entry) =>
          current.selected.includes(entry.id) &&
          !isInTrash(entry, current.data.entries),
      ),
      view: current.view,
      onViewChange: (next) => update({ view: next }),
      onImport: () => input.current?.click(),
    });
    return build(options(captured)).map((item) => ({
      ...item,
      ...(item.action
        ? {
            action: () => {
              if (!mounted.current) return;
              command(async () => {
                const current = latest.current;
                if (item.id !== 'refresh') {
                  const destination = current.data.entries.find(
                    (entry) => entry.id === captured.folderId,
                  );
                  if (
                    current.folderId !== captured.folderId ||
                    !destination ||
                    isInTrash(destination, current.data.entries)
                  )
                    throw new Error(
                      'This folder changed. Open the menu again.',
                    );
                }
                if (
                  kind === 'actions' &&
                  item.id !== 'paste' &&
                  item.id !== 'refresh'
                ) {
                  if (
                    current.selected.length !== targetIds.length ||
                    !targetIds.every((id) => current.selected.includes(id))
                  )
                    throw new Error(
                      'The selection changed. Open the actions menu again.',
                    );
                  if (
                    !targetIds.every((id) =>
                      current.data.entries.some(
                        (entry) =>
                          entry.id === id &&
                          !isInTrash(entry, current.data.entries),
                      ),
                    )
                  )
                    throw new Error(
                      'The selected items are no longer available.',
                    );
                }
                const action = build(options(current)).find(
                  (candidate) => candidate.id === item.id,
                );
                if (!action?.action || action.disabled)
                  throw new Error('This action is no longer available.');
                action.action();
              });
            },
          }
        : {}),
    }));
  }

  function context(
    entry: Entry | undefined,
    x: number,
    y: number,
    parentId = folderId,
  ) {
    if (entry && !selected.includes(entry.id)) select([entry.id]);
    desktop.contextMenu(menu(entry, parentId), { x, y });
  }

  function touchMenu(event: PointerEvent, entry?: Entry) {
    if (event.pointerType !== 'touch') return;
    clearTimeout(hold.current);
    hold.current = setTimeout(
      () => context(entry, event.clientX, event.clientY),
      550,
    );
  }

  async function importFiles(files: File[], parentId: string) {
    setBusy(true);
    try {
      await filesystemApi.import(files, parentId);
    } finally {
      try {
        await data.refresh();
      } finally {
        setBusy(false);
      }
    }
  }

  function drop(event: DragEvent, parentId: string) {
    event.preventDefault();
    event.stopPropagation();
    setDropTarget(null);
    if (event.dataTransfer.files.length)
      command(() =>
        importFiles(Array.from(event.dataTransfer.files), parentId),
      );
    else {
      const payload = event.dataTransfer.getData(
        'application/x-nextstep-entries',
      );
      if (!payload) return;
      command(async () => {
        const ids: unknown = JSON.parse(payload);
        if (Array.isArray(ids) && ids.every((id) => typeof id === 'string'))
          await moveEntries(data, ids as string[], parentId);
      });
    }
  }

  function keyboard(event: KeyboardEvent, parentId: string, siblings: Entry[]) {
    if (
      event.target instanceof HTMLElement &&
      event.target.closest('button,input,select,textarea')
    )
      return;
    const current = siblings.findIndex((entry) => entry.id === selected.at(-1));
    const chosen = siblings[current];
    if (
      (event.ctrlKey || event.metaKey) &&
      ['a', 'c', 'x', 'v'].includes(event.key.toLowerCase())
    ) {
      event.preventDefault();
      if (event.key.toLowerCase() === 'a')
        select(siblings.map((entry) => entry.id));
      else if (event.key.toLowerCase() === 'v')
        command(() => pasteEntries(data, parentId));
      else if (selection.length && !selection.some((entry) => entry.system))
        data.setClipboard({
          ids: selected,
          mode: event.key.toLowerCase() === 'x' ? 'cut' : 'copy',
        });
    } else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault();
      const index =
        event.key === 'Home'
          ? 0
          : event.key === 'End'
            ? siblings.length - 1
            : Math.max(
                0,
                Math.min(
                  siblings.length - 1,
                  current + (event.key === 'ArrowUp' ? -1 : 1),
                ),
              );
      const next = siblings[index];
      if (next) {
        pick(next, siblings, event);
        document
          .getElementById(`file-${window.id}-${parentId}-${next.id}`)
          ?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
      }
    } else if (event.key === 'ArrowRight' && chosen?.kind === 'folder') {
      event.preventDefault();
      navigate(chosen.id);
      focusColumn(chosen.id);
    } else if (event.key === 'ArrowLeft') {
      event.preventDefault();
      const parent = live.find((entry) => entry.id === parentId);
      if (parent?.parentId) {
        navigate(parent.parentId);
        select([parent.id]);
        focusColumn(parent.parentId);
      }
    } else if (event.key === 'Enter' && chosen) {
      event.preventDefault();
      if (chosen.kind === 'folder') {
        navigate(chosen.id);
        focusColumn(chosen.id);
      } else openEntry(chosen, desktop);
    } else if (
      event.key === 'F2' &&
      selection.length === 1 &&
      !selection[0]!.system
    ) {
      event.preventDefault();
      command(() => renameEntry(data, desktop, selection[0]!));
    } else if (event.key === 'Delete' && selection.length) {
      event.preventDefault();
      command(() => trashEntries(data, desktop, selected));
    } else if (
      event.key.length === 1 &&
      !event.ctrlKey &&
      !event.metaKey &&
      !event.altKey
    ) {
      const now = Date.now();
      typeahead.current = {
        value:
          (now - typeahead.current.time < 700 ? typeahead.current.value : '') +
          event.key.toLowerCase(),
        time: now,
      };
      const target = siblings.find((entry) =>
        entry.name.toLowerCase().startsWith(typeahead.current.value),
      );
      if (target) pick(target, siblings, event);
    }
  }

  function renderEntry(entry: Entry, parentId: string, siblings: Entry[]) {
    const onPath = path.some((item) => item.id === entry.id);
    return (
      <div
        key={entry.id}
        id={`file-${window.id}-${parentId}-${entry.id}`}
        className={`finder-entry ${selected.includes(entry.id) ? 'selected' : onPath ? 'on-path' : ''} ${dropTarget === entry.id ? 'drop-target' : ''}`}
        role="option"
        aria-selected={selected.includes(entry.id)}
        aria-label={entry.name}
        tabIndex={-1}
        draggable={!entry.system}
        onDragStart={(event) => {
          const ids = selected.includes(entry.id) ? selected : [entry.id];
          select(ids);
          event.dataTransfer.setData(
            'application/x-nextstep-entries',
            JSON.stringify(ids),
          );
          event.dataTransfer.effectAllowed = 'move';
        }}
        onDragOver={(event) => {
          if (entry.kind === 'folder') {
            event.preventDefault();
            event.stopPropagation();
            setDropTarget(entry.id);
          }
        }}
        onDrop={(event) => {
          if (entry.kind === 'folder') drop(event, entry.id);
        }}
        onClick={(event) => {
          event.stopPropagation();
          pick(entry, siblings, event);
          event.currentTarget.closest<HTMLElement>('[role="listbox"]')?.focus();
        }}
        onDoubleClick={(event) => {
          event.stopPropagation();
          // Browsers can synthesize dblclick from nearby successive taps, even
          // as folder navigation changes the rows. Touch selects; Open opens.
          if (entryPointerType.current !== 'mouse') {
            event.preventDefault();
            return;
          }
          openEntry(entry, desktop);
        }}
        onPointerDown={(event) => {
          entryPointerType.current = event.pointerType;
          touchMenu(event, entry);
        }}
        onPointerUp={() => clearTimeout(hold.current)}
        onContextMenu={(event) => {
          event.preventDefault();
          event.stopPropagation();
          context(entry, event.clientX, event.clientY, parentId);
        }}
      >
        <PixelIcon name={entryIcon(entry)} size={16} />
        <span className="finder-name">{entry.name}</span>
        {view === 'columns' ? (
          <span className="column-disclosure" aria-hidden="true">
            {entry.kind === 'folder' ? '▸' : ''}
          </span>
        ) : (
          <>
            <span className="finder-meta">
              {entry.kind === 'folder' ? 'Folder' : readableSize(entry.size)}
            </span>
            <span className="finder-meta">
              {new Date(entry.updatedAt).toLocaleDateString()}
            </span>
          </>
        )}
      </div>
    );
  }

  if (!folder)
    return (
      <div className="app-pad">
        <p>This folder is unavailable.</p>
        <Button onClick={() => command(data.refresh)}>Refresh</Button>
      </div>
    );
  const columns = view === 'columns' ? path : [folder];
  const selectedFile =
    selection.length === 1 && selection[0]!.kind === 'file'
      ? selection[0]
      : undefined;
  return (
    <div className="files-app">
      <div className="app-toolbar files-toolbar">
        <IconButton
          icon="up"
          label="Up"
          disabled={folderId === 'root'}
          onClick={() => {
            navigate(folder.parentId);
            select([folder.id]);
          }}
        />
        <Button
          aria-haspopup="menu"
          onClick={(event) => {
            const rect = event.currentTarget.getBoundingClientRect();
            desktop.contextMenu(
              toolbarMenu('new'),
              { x: rect.left, y: rect.bottom },
              { title: 'New' },
            );
          }}
        >
          New
        </Button>
        <Button disabled={busy} onClick={() => input.current?.click()}>
          {busy ? 'Importing…' : 'Import'}
        </Button>
        <Select
          aria-label="Folder view"
          value={view}
          onChange={(event) => update({ view: event.target.value })}
        >
          <option value="columns">Columns</option>
          <option value="list">List</option>
        </Select>
        <input
          ref={input}
          className="visually-hidden"
          type="file"
          multiple
          aria-label="Import files"
          onChange={(event) => {
            const files = Array.from(event.target.files || []);
            event.target.value = '';
            if (files.length) command(() => importFiles(files, folderId));
          }}
        />
      </div>
      <nav className="files-path" aria-label="Folder path">
        {path.map((item) => (
          <button
            key={item.id}
            onClick={() => {
              navigate(item.id);
              select([]);
            }}
          >
            <PixelIcon
              name={item.id === 'root' ? 'computer' : 'folder'}
              size={16}
            />
            {item.name}
            <span aria-hidden="true">›</span>
          </button>
        ))}
      </nav>
      <div className="files-sort">
        <span>
          {folderEntries(live, folderId, sort, descending).length} items
        </span>
        <Select
          aria-label="Sort files"
          value={sort}
          onChange={(event) => update({ sort: event.target.value })}
        >
          <option value="name">Name</option>
          <option value="modified">Modified</option>
          <option value="size">Size</option>
          <option value="kind">Kind</option>
        </Select>
        <IconButton
          icon={descending ? 'sort-descending' : 'sort-ascending'}
          label={descending ? 'Sort ascending' : 'Sort descending'}
          title={descending ? 'Sort ascending' : 'Sort descending'}
          onClick={() => update({ descending: !descending })}
        />
      </div>
      <div
        ref={browser}
        className={`miller-browser ${view}`}
        onScroll={(event) =>
          update({ scrollLeft: event.currentTarget.scrollLeft })
        }
      >
        {columns.map((parent) => {
          const siblings = folderEntries(live, parent.id, sort, descending);
          const width = Math.max(
            160,
            Math.min(480, Number(widths[parent.id]) || 210),
          );
          return (
            <div
              key={parent.id}
              className="miller-column-wrap"
              style={view === 'columns' ? { width } : undefined}
            >
              <div className="miller-column-heading">{parent.name}</div>
              <div
                data-column-id={parent.id}
                className={`miller-column finder-body ${dropTarget === parent.id ? 'drop-target' : ''}`}
                role="listbox"
                tabIndex={0}
                aria-label={`${parent.name} ${view === 'columns' ? 'column' : 'files'}`}
                aria-multiselectable="true"
                aria-activedescendant={
                  siblings.some((entry) => entry.id === selected.at(-1))
                    ? `file-${window.id}-${parent.id}-${selected.at(-1)}`
                    : undefined
                }
                onKeyDown={(event) => keyboard(event, parent.id, siblings)}
                onClick={(event) => {
                  if (event.target === event.currentTarget) {
                    select([]);
                    navigate(parent.id);
                  }
                }}
                onContextMenu={(event) => {
                  event.preventDefault();
                  context(undefined, event.clientX, event.clientY, parent.id);
                }}
                onPointerUp={() => clearTimeout(hold.current)}
                onPointerMove={() => clearTimeout(hold.current)}
                onPointerCancel={() => clearTimeout(hold.current)}
                onDragOver={(event) => {
                  event.preventDefault();
                  setDropTarget(parent.id);
                }}
                onDragLeave={() => setDropTarget(null)}
                onDrop={(event) => drop(event, parent.id)}
              >
                {siblings.map((entry) =>
                  renderEntry(entry, parent.id, siblings),
                )}
                {!siblings.length && (
                  <p className="empty-folder">Empty folder</p>
                )}
              </div>
              {view === 'columns' && (
                <ColumnSeparator
                  name={parent.name}
                  width={width}
                  onCommit={(next) =>
                    update({ columnWidths: { ...widths, [parent.id]: next } })
                  }
                />
              )}
            </div>
          );
        })}
        {view === 'columns' && selectedFile && (
          <div className="column-preview">
            <PixelIcon name={entryIcon(selectedFile)} size={48} />
            <strong>{selectedFile.name}</strong>
            <span>{readableSize(selectedFile.size)}</span>
            <span>
              {selectedFile.editable
                ? 'Plain text document'
                : selectedFile.mime}
            </span>
          </div>
        )}
      </div>
      <div className="app-toolbar bottom-toolbar files-action-toolbar">
        <Button
          disabled={!selection.length}
          onClick={() => {
            if (selection[0]) openEntry(selection[0], desktop);
          }}
        >
          Open
        </Button>
        <div className="files-secondary-actions">
          <Button
            disabled={selection.length !== 1}
            onClick={() => {
              if (selection[0]) void getInfo(desktop, selection[0], data);
            }}
          >
            Inspect
          </Button>
          <IconButton
            icon="more"
            label="More file actions"
            aria-haspopup="menu"
            onClick={(event) => {
              const rect = event.currentTarget.getBoundingClientRect();
              desktop.contextMenu(
                toolbarMenu('actions'),
                { x: rect.left, y: rect.bottom },
                {
                  title:
                    selection.length === 1
                      ? selection[0]!.name
                      : 'File actions',
                },
              );
            }}
          />
        </div>
      </div>
      <StatusBar>
        {selection.length ? `${selection.length} selected` : 'Select an item.'}
      </StatusBar>
    </div>
  );
}

function ColumnSeparator({
  name,
  width,
  onCommit,
}: {
  name: string;
  width: number;
  onCommit: (width: number) => void;
}) {
  const gesture = useRef<{ x: number; width: number } | null>(null);
  const clamp = (value: number) => Math.max(160, Math.min(480, value));
  return (
    <div
      className="column-separator"
      role="separator"
      tabIndex={0}
      aria-label={`Resize ${name} column`}
      aria-orientation="vertical"
      aria-valuemin={160}
      aria-valuemax={480}
      aria-valuenow={width}
      onKeyDown={(event) => {
        if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
          event.preventDefault();
          onCommit(clamp(width + (event.key === 'ArrowLeft' ? -20 : 20)));
        }
      }}
      onPointerDown={(event) => {
        if (event.button !== 0) return;
        event.preventDefault();
        gesture.current = { x: event.clientX, width };
        event.currentTarget.setPointerCapture(event.pointerId);
      }}
      onPointerMove={(event) => {
        const start = gesture.current;
        if (start && event.currentTarget.parentElement)
          event.currentTarget.parentElement.style.width = `${clamp(start.width + event.clientX - start.x)}px`;
      }}
      onPointerUp={(event) => {
        const start = gesture.current;
        if (start) {
          gesture.current = null;
          event.currentTarget.releasePointerCapture(event.pointerId);
          onCommit(clamp(start.width + event.clientX - start.x));
        }
      }}
      onPointerCancel={(event) => {
        gesture.current = null;
        if (event.currentTarget.parentElement)
          event.currentTarget.parentElement.style.width = `${width}px`;
      }}
    />
  );
}
