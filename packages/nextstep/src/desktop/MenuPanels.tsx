import { useState } from 'react';
import type { Dispatch, SetStateAction } from 'react';
import type {
  DesktopAPI,
  DesktopLayout,
  DesktopProps,
  MenuGroup,
  MenuItem,
  PinnedMenu,
} from '../types';
import {
  createDesktopContextMenu,
  createDesktopMenus,
  createWindowMenu,
} from './menus';
import { usePointerGesture } from './usePointerGesture';
import { resolveApplication } from './applications';

export function ApplicationMenu({
  groups,
  api,
  props,
}: {
  groups: MenuGroup[];
  api: DesktopAPI;
  props: DesktopProps;
}) {
  const win = api.windows.find((item) => item.id === api.activeWindowId);
  const app = win ? resolveApplication(props.apps, win.appId) : undefined;
  return (
    <nav className="ns-application-menu" aria-label="Application menus">
      <div className="ns-menu-title">
        <span>{app?.title ?? 'Workspace'}</span>
      </div>
      {groups
        .filter((group) => group.id !== 'desktop')
        .map((group) => (
          <button
            type="button"
            className="ns-application-menu-item"
            key={group.id}
            aria-haspopup="menu"
            onClick={(event) => {
              event.currentTarget.focus();
              const rect = event.currentTarget.getBoundingClientRect();
              api.contextMenu(
                group.items,
                { x: rect.right + 2, y: rect.top },
                {
                  title: group.label,
                  pin: {
                    kind: win ? 'application' : 'desktop',
                    ownerWindowId: win?.id,
                    groupId: group.id,
                    title: group.label,
                  },
                },
              );
            }}
          >
            {group.label}
            <span aria-hidden="true">▸</span>
          </button>
        ))}
      <div className="ns-menu-owner">{win?.title ?? 'Desktop commands'}</div>
    </nav>
  );
}

function panelItems(
  panel: PinnedMenu,
  props: DesktopProps,
  api: DesktopAPI,
): MenuItem[] {
  const owner = api.windows.find((win) => win.id === panel.ownerWindowId);
  if (panel.kind === 'window') return owner ? createWindowMenu(owner, api) : [];
  if (panel.kind === 'desktop' && !panel.groupId)
    return createDesktopContextMenu(props, api, []);
  return (
    createDesktopMenus(props, api, [], panel.ownerWindowId ?? null).groups.find(
      (group) => group.id === panel.groupId,
    )?.items ?? []
  );
}

function PinnedMenuPanel({
  panel,
  props,
  api,
  setLayout,
}: {
  panel: PinnedMenu;
  props: DesktopProps;
  api: DesktopAPI;
  setLayout: Dispatch<SetStateAction<DesktopLayout>>;
}) {
  const [preview, setPreview] = useState<{ x: number; y: number } | null>(null);
  const beginGesture = usePointerGesture();
  const items = panelItems(panel, props, api);
  const owner = api.windows.find((win) => win.id === panel.ownerWindowId);
  const point = preview ?? panel;
  const close = () =>
    setLayout((current) => ({
      ...current,
      pinnedMenus: current.pinnedMenus.filter((item) => item.id !== panel.id),
    }));
  return (
    <section
      className="ns-pinned-menu ns-menu"
      data-owner-window-id={panel.ownerWindowId}
      aria-label={`${panel.title} pinned menu`}
      style={{
        left: Math.max(2, Math.min(point.x, window.innerWidth - 204)),
        top: Math.max(
          26,
          Math.min(
            point.y,
            (window.visualViewport?.height ?? window.innerHeight) - 100,
          ),
        ),
      }}
    >
      <div
        className="ns-menu-title"
        onPointerDown={(event) => {
          if (
            event.button !== 0 ||
            (event.target as HTMLElement).closest('button')
          )
            return;
          event.preventDefault();
          const origin = { x: event.clientX, y: event.clientY };
          let next = point;
          beginGesture(
            event,
            (p) => {
              next = {
                x: Math.max(
                  2,
                  Math.min(window.innerWidth - 204, point.x + p.x - origin.x),
                ),
                y: Math.max(
                  26,
                  Math.min(window.innerHeight - 50, point.y + p.y - origin.y),
                ),
              };
              setPreview(next);
            },
            (_, cancelled) => {
              setPreview(null);
              if (!cancelled)
                setLayout((current) => ({
                  ...current,
                  pinnedMenus: current.pinnedMenus.map((item) =>
                    item.id === panel.id ? { ...item, ...next } : item,
                  ),
                }));
            },
          );
        }}
      >
        <span>{panel.title}</span>
        <button
          type="button"
          aria-label={`Close ${panel.title} menu`}
          title={`Close ${panel.title} menu`}
          onClick={close}
        >
          ×
        </button>
      </div>
      <div className="ns-menu-owner" title={owner?.title}>
        {owner?.title ?? 'Workspace'}
      </div>
      <div
        role="menu"
        aria-label={`${panel.title} pinned commands`}
        onKeyDown={(event) => {
          if (
            !['ArrowUp', 'ArrowDown', 'Home', 'End', 'Escape'].includes(
              event.key,
            )
          )
            return;
          event.preventDefault();
          event.stopPropagation();
          if (event.key === 'Escape') {
            close();
            return;
          }
          const buttons = Array.from(
            event.currentTarget.querySelectorAll<HTMLButtonElement>(
              'button:not(:disabled)',
            ),
          );
          const index = buttons.indexOf(
            document.activeElement as HTMLButtonElement,
          );
          buttons[
            event.key === 'Home'
              ? 0
              : event.key === 'End'
                ? buttons.length - 1
                : (index +
                    (event.key === 'ArrowDown' ? 1 : -1) +
                    buttons.length) %
                  buttons.length
          ]?.focus();
        }}
      >
        {items.map((item, index) =>
          item.separator ? (
            <div role="separator" className="ns-menu-separator" key={index} />
          ) : (
            <button
              type="button"
              key={item.id ?? index}
              className="ns-menu-item"
              role={
                item.checked === undefined ? 'menuitem' : 'menuitemcheckbox'
              }
              aria-checked={item.checked}
              disabled={item.disabled}
              aria-haspopup={item.children ? 'menu' : undefined}
              onClick={(event) => {
                if (item.children) {
                  const rect = event.currentTarget.getBoundingClientRect();
                  api.contextMenu(
                    item.children,
                    { x: rect.right, y: rect.top },
                    { title: item.label },
                  );
                } else item.action?.();
              }}
            >
              <span className="ns-menu-mark" aria-hidden="true">
                {item.checked ? '✓' : ''}
              </span>
              <span className="ns-menu-label">{item.label}</span>
              <span className="ns-menu-shortcut" aria-hidden="true">
                {item.shortcut}
              </span>
              {item.children && <span aria-hidden="true">▸</span>}
            </button>
          ),
        )}
      </div>
    </section>
  );
}

export function PinnedMenus({
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
  return layout.pinnedMenus.map((panel) => (
    <PinnedMenuPanel
      key={panel.id}
      panel={panel}
      props={props}
      api={api}
      setLayout={setLayout}
    />
  ));
}
