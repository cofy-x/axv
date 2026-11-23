import type { DesktopAPI } from '../types';
import type { createApplicationMenus } from './menus';

export function ApplicationMenu({
  api,
  menus,
}: {
  api: DesktopAPI;
  menus: NonNullable<ReturnType<typeof createApplicationMenus>>;
}) {
  return (
    <nav className="ns-application-menu" aria-label="Application menus">
      <div className="ns-menu-title">
        <span>{menus.title}</span>
      </div>
      {menus.groups.map((group) => (
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
              { x: rect.left, y: rect.bottom },
              { title: group.label, ownerWindowId: menus.owner.id },
            );
          }}
        >
          {group.label}
          <span aria-hidden="true">▾</span>
        </button>
      ))}
    </nav>
  );
}
