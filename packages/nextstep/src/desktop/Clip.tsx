import { useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import { usePointerGesture } from './usePointerGesture';
import { PixelIcon } from '../controls';
import { resolveApplication } from './applications';
import type {
  ClipShortcut,
  DesktopAPI,
  DesktopLayout,
  DesktopProps,
  MenuItem,
  Point,
} from '../types';

export function Clip({
  props,
  api,
  layout,
  setLayout,
}: {
  props: DesktopProps;
  api: DesktopAPI;
  layout: DesktopLayout;
  setLayout: Dispatch<SetStateAction<DesktopLayout>>;
}) {
  const beginGesture = usePointerGesture();
  const [preview, setPreview] = useState<Point | null>(null);
  const stored = preview ?? layout.clipPosition ?? { x: 10, y: 42 };
  const position = {
    x: Math.max(4, Math.min(stored.x, window.innerWidth - 170)),
    y: Math.max(30, Math.min(stored.y, window.innerHeight - 96)),
  };
  const mobile = window.innerWidth < 600;
  const shortcutMenu = (shortcut: ClipShortcut): MenuItem[] => {
    const available =
      resolveApplication(props.apps, shortcut.appId).unavailable ===
        undefined && props.isShortcutAvailable?.(shortcut) !== false;
    return [
      {
        label: 'Open',
        disabled: !available,
        action: () => {
          if (Object.keys(shortcut.params).length)
            api.openApp(shortcut.appId, shortcut.params);
          else api.launchApp(shortcut.appId);
        },
      },
      {
        label: 'Remove Shortcut',
        action: () => api.removeClipShortcut(shortcut.id),
      },
    ];
  };
  const workspace = api.workspaces.find(
    (item) => item.id === api.currentWorkspaceId,
  )!;
  const index = api.workspaces.indexOf(workspace);
  const menu: MenuItem[] = [
    ...api.workspaces.map((item, i) => ({
      label: `${i + 1}  ${item.name}`,
      checked: item.id === workspace.id,
      action: () => api.switchWorkspace(item.id),
    })),
    { separator: true },
    ...(workspace.clip.length
      ? [
          {
            label: 'Shortcuts',
            children: workspace.clip.map((shortcut) => ({
              label: shortcut.title,
              children: shortcutMenu(shortcut),
            })),
          },
        ]
      : []),
    {
      label: 'Rename Workspace',
      action: () => {
        void api
          .dialog({
            title: 'Rename Workspace',
            input: { label: 'Workspace name', value: workspace.name },
          })
          .then((result) => {
            if (result?.value.trim())
              api.renameWorkspace(workspace.id, result.value);
          });
      },
    },
    {
      label: 'Add Application Shortcut',
      children: props.apps
        .filter((app) => app.launchable !== false)
        .map((app) => ({
          label: app.title,
          action: () =>
            api.addClipShortcut({
              appId: app.id,
              title: app.title,
              params: {},
            }),
        })),
    },
    {
      label: 'Add Active Window',
      disabled: !api.activeWindowId,
      action: () => {
        const win = api.windows.find((item) => item.id === api.activeWindowId);
        if (win)
          api.addClipShortcut({
            appId: win.appId,
            title: win.title,
            params: win.params,
          });
      },
    },
  ];
  const point = (element: HTMLElement): Point => {
    const rect = element.getBoundingClientRect();
    return { x: rect.left, y: rect.bottom };
  };
  return (
    <aside
      className="ns-clip"
      aria-label="Workspace Clip"
      style={mobile ? undefined : { left: position.x, top: position.y }}
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
            if (event.button !== 0 || mobile) return;
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
          const open = () => {
            if (!available) return;
            if (Object.keys(shortcut.params).length)
              api.openApp(shortcut.appId, shortcut.params);
            else api.launchApp(shortcut.appId);
          };
          const items: MenuItem[] = [
            { label: 'Open', disabled: !available, action: open },
            {
              label: 'Remove Shortcut',
              action: () => api.removeClipShortcut(shortcut.id),
            },
          ];
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
                onClick={(event) =>
                  api.contextMenu(items, point(event.currentTarget), {
                    title: shortcut.title,
                  })
                }
              >
                ▾
              </button>
            </div>
          );
        })}
      </div>
    </aside>
  );
}
