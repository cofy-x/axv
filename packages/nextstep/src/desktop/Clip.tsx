import { useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import { usePointerGesture } from './usePointerGesture';
import { PixelIcon, WindowControlIcon } from '../controls';
import { resolveApplication } from './applications';
import { createClipMenu, createClipShortcutMenu } from './menus';
import type { DesktopAPI, DesktopLayout, DesktopProps, Point } from '../types';

export function Clip({
  props,
  api,
  layout,
  setLayout,
  compact = false,
}: {
  props: DesktopProps;
  api: DesktopAPI;
  layout: DesktopLayout;
  setLayout: Dispatch<SetStateAction<DesktopLayout>>;
  compact?: boolean;
}) {
  const beginGesture = usePointerGesture();
  const [preview, setPreview] = useState<Point | null>(null);
  const stored = preview ?? layout.clipPosition ?? { x: 10, y: 42 };
  const position = {
    x: Math.max(4, Math.min(stored.x, window.innerWidth - 170)),
    y: Math.max(30, Math.min(stored.y, window.innerHeight - 96)),
  };
  const mobile = window.innerWidth < 600;
  const workspace = api.workspaces.find(
    (item) => item.id === api.currentWorkspaceId,
  )!;
  const index = api.workspaces.indexOf(workspace);
  const menu = createClipMenu(props, api);
  const point = (element: HTMLElement): Point => {
    const rect = element.getBoundingClientRect();
    return { x: rect.left, y: rect.bottom };
  };
  return (
    <aside
      className="ns-clip"
      aria-label="Workspace Clip"
      style={
        mobile || compact ? undefined : { left: position.x, top: position.y }
      }
    >
      <div className="ns-clip-tile">
        <button
          type="button"
          className="ns-clip-prev"
          aria-label="Previous workspace"
          title="Previous workspace"
          onClick={() =>
            api.switchWorkspace(
              api.workspaces[
                (index + api.workspaces.length - 1) % api.workspaces.length
              ].id,
            )
          }
        >
          ◢
        </button>
        <button
          type="button"
          className="ns-clip-center"
          aria-label={`Workspace ${index + 1}: ${workspace.name}`}
          title={`Workspace ${index + 1}: ${workspace.name}`}
          aria-haspopup="menu"
          onPointerDown={(event) => {
            if (event.button !== 0 || mobile || compact) return;
            const button = event.currentTarget;
            const origin = { x: event.clientX, y: event.clientY };
            let next = position;
            let moved = false;
            beginGesture(
              event,
              (p) => {
                if (
                  Math.abs(p.x - origin.x) + Math.abs(p.y - origin.y) < 5 &&
                  !moved
                )
                  return;
                moved = true;
                next = {
                  x: position.x + p.x - origin.x,
                  y: position.y + p.y - origin.y,
                };
                setPreview(next);
              },
              (_, cancelled) => {
                setPreview(null);
                if (moved && !cancelled) {
                  setLayout((current) => ({ ...current, clipPosition: next }));
                  button.dataset.suppressClick = 'true';
                }
              },
            );
          }}
          onClick={(event) => {
            const button = event.currentTarget;
            if (button.dataset.suppressClick) {
              delete button.dataset.suppressClick;
              return;
            }
            api.contextMenu(menu, point(button), { title: 'Workspaces' });
          }}
        >
          <strong>{index + 1}</strong>
          <span>{workspace.name}</span>
        </button>
        <button
          type="button"
          className="ns-clip-next"
          aria-label="Next workspace"
          title="Next workspace"
          onClick={() =>
            api.switchWorkspace(
              api.workspaces[(index + 1) % api.workspaces.length].id,
            )
          }
        >
          ◤
        </button>
      </div>
      <div
        className="ns-clip-shortcuts"
        aria-label={`${workspace.name} shortcuts`}
      >
        {workspace.clip.map((shortcut) => {
          const app = resolveApplication(props.apps, shortcut.appId);
          const available =
            app.unavailable === undefined &&
            props.isShortcutAvailable?.(shortcut) !== false;
          const items = createClipShortcutMenu(props, api, shortcut);
          const open = items[0].action;
          return (
            <div
              className={`ns-clip-shortcut ${available ? '' : 'is-unavailable'}`}
              key={shortcut.id}
            >
              <button
                type="button"
                title={
                  available ? shortcut.title : `${shortcut.title} — unavailable`
                }
                aria-label={`Clip: ${shortcut.title}${available ? '' : ' unavailable'}`}
                onClick={open}
                onContextMenu={(event) => {
                  event.preventDefault();
                  api.contextMenu(items, point(event.currentTarget), {
                    title: shortcut.title,
                  });
                }}
              >
                <PixelIcon name={app.icon} size={28} />
                <span>{shortcut.title}</span>
              </button>
              <button
                type="button"
                className="ns-shortcut-options"
                aria-label={`Options for Clip shortcut ${shortcut.title}`}
                title={`Options for Clip shortcut ${shortcut.title}`}
                aria-haspopup="menu"
                onClick={(event) => {
                  event.currentTarget.focus({ preventScroll: true });
                  api.contextMenu(items, point(event.currentTarget), {
                    title: shortcut.title,
                  });
                }}
              >
                <WindowControlIcon icon="menu" />
              </button>
            </div>
          );
        })}
      </div>
    </aside>
  );
}
