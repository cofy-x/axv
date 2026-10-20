import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { Button, Field, PixelIcon } from './controls';
import { usePointerGesture } from './desktop/usePointerGesture';
import { useViewport } from './desktop/useViewport';
import type {
  DialogOptions,
  DialogResult,
  MenuItem,
  MenuOptions,
  Point,
} from './types';

export interface MenuRequest {
  id: number;
  items: MenuItem[];
  point: Point;
  opener: HTMLElement | null;
  options?: MenuOptions;
}

interface MenuLevel {
  items: MenuItem[];
  point: Point;
  selected: number;
  title?: string;
}

const firstEnabled = (items: MenuItem[]) =>
  items.findIndex((item) => !item.separator && !item.disabled);

export function MenuOverlay({
  request,
  close,
  pin,
}: {
  request: MenuRequest;
  close: () => void;
  pin?: (point: Point) => void;
}) {
  const beginGesture = usePointerGesture();
  const viewport = useViewport();
  const compact = viewport.width < 600;
  const [dragPoint, setDragPoint] = useState<Point | null>(null);
  const menuId = useId();
  const [levels, setLevels] = useState<MenuLevel[]>([
    {
      items: request.items,
      point: request.point,
      selected: firstEnabled(request.items),
      title: request.options?.title,
    },
  ]);
  const refs = useRef<Array<HTMLDivElement | null>>([]);
  const [positions, setPositions] = useState<Point[]>([]);
  const latest = useRef(levels);
  latest.current = levels;
  const dismiss = () => {
    close();
    request.opener?.focus({ preventScroll: true });
  };

  useLayoutEffect(() => {
    const next = levels.map((level, index) => {
      const rect = refs.current[index]?.getBoundingClientRect();
      const width = rect?.width ?? 200;
      const height = rect?.height ?? 100;
      const point = compact ? request.point : level.point;
      let x = point.x;
      if (!compact && index > 0 && x + width > viewport.width - 4) {
        const previous = refs.current[index - 1]?.getBoundingClientRect();
        x = (previous?.left ?? x) - width + 2;
      }
      return {
        x: Math.max(2, Math.min(x, viewport.width - width - 2)),
        y: Math.max(22, Math.min(point.y, viewport.height - height - 3)),
      };
    });
    setPositions(next);
    refs.current[levels.length - 1]?.focus({ preventScroll: true });
  }, [levels, request, compact, viewport.width, viewport.height]);

  const openSubmenu = (
    levelIndex: number,
    itemIndex: number,
    revealChildren = true,
  ) => {
    const level = latest.current[levelIndex];
    const item = level?.items[itemIndex];
    if (!item || item.disabled) return;
    const row =
      refs.current[levelIndex]?.querySelectorAll<HTMLButtonElement>(
        '[role^="menuitem"]',
      )[
        level.items.slice(0, itemIndex).filter((entry) => !entry.separator)
          .length
      ];
    setLevels((current) => {
      const next = current
        .slice(0, levelIndex + 1)
        .map((entry, index) =>
          index === levelIndex ? { ...entry, selected: itemIndex } : entry,
        );
      if (item.children && revealChildren) {
        const rect = row?.getBoundingClientRect();
        next.push({
          items: item.children,
          point: {
            x: (rect?.right ?? level.point.x + 190) - 1,
            y: rect?.top ?? level.point.y,
          },
          selected: firstEnabled(item.children),
          title: item.label,
        });
      }
      return next;
    });
  };
  const activate = (levelIndex: number, itemIndex: number) => {
    const item = latest.current[levelIndex]?.items[itemIndex];
    if (!item || item.disabled || item.separator) return;
    if (item.children) {
      openSubmenu(levelIndex, itemIndex);
      return;
    }
    dismiss();
    item.action?.();
  };
  return (
    <div
      className="ns-menu-layer"
      onPointerDown={(event) => {
        if (event.target === event.currentTarget) dismiss();
      }}
      onContextMenu={(event) => {
        event.preventDefault();
        dismiss();
      }}
    >
      {levels.map((level, levelIndex) => (
        <div
          key={levelIndex}
          ref={(node) => {
            refs.current[levelIndex] = node;
          }}
          tabIndex={-1}
          role="menu"
          aria-label={level.title}
          hidden={compact && levelIndex < levels.length - 1}
          aria-activedescendant={
            level.selected >= 0
              ? `${menuId}-${levelIndex}-${level.selected}`
              : undefined
          }
          className="ns-menu"
          style={{
            left:
              levelIndex === 0 && dragPoint
                ? dragPoint.x
                : (positions[levelIndex]?.x ?? level.point.x),
            top:
              levelIndex === 0 && dragPoint
                ? dragPoint.y
                : (positions[levelIndex]?.y ?? level.point.y),
          }}
          onKeyDown={(event) => {
            event.stopPropagation();
            const current = latest.current[levelIndex];
            if (event.key === 'Escape' || event.key === 'Tab') {
              event.preventDefault();
              dismiss();
            } else if (event.key === 'ArrowLeft') {
              event.preventDefault();
              if (levelIndex) setLevels((all) => all.slice(0, levelIndex));
              else dismiss();
            } else if (event.key === 'ArrowRight') {
              event.preventDefault();
              openSubmenu(levelIndex, current.selected);
            } else if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault();
              activate(levelIndex, current.selected);
            } else if (
              ['ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)
            ) {
              event.preventDefault();
              const enabled = current.items
                .map((item, index) =>
                  !item.separator && !item.disabled ? index : -1,
                )
                .filter((index) => index >= 0);
              const offset = event.key === 'ArrowDown' ? 1 : -1;
              const selected =
                event.key === 'Home'
                  ? enabled[0]
                  : event.key === 'End'
                    ? enabled.at(-1)
                    : enabled[
                        (enabled.indexOf(current.selected) +
                          offset +
                          enabled.length) %
                          enabled.length
                      ];
              setLevels((all) =>
                all
                  .slice(0, levelIndex + 1)
                  .map((entry, index) =>
                    index === levelIndex
                      ? { ...entry, selected: selected ?? -1 }
                      : entry,
                  ),
              );
            }
          }}
        >
          {compact && levelIndex > 0 && (
            <div className="ns-menu-title ns-submenu-title">
              <button
                type="button"
                className="ns-menu-back"
                aria-label={`Back to ${levels[levelIndex - 1].title ?? 'previous'} menu`}
                tabIndex={-1}
                onClick={() => setLevels((all) => all.slice(0, levelIndex))}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' || event.key === ' ')
                    event.stopPropagation();
                }}
              >
                ‹ Back
              </button>
              <span>{level.title}</span>
            </div>
          )}
          {levelIndex === 0 && request.options?.title && (
            <div
              className="ns-menu-title"
              onPointerDown={(event) => {
                if (
                  event.button !== 0 ||
                  !pin ||
                  !request.options?.pin ||
                  (event.target as HTMLElement).closest('button')
                )
                  return;
                event.preventDefault();
                const origin = { x: event.clientX, y: event.clientY };
                const start = positions[0] ?? request.point;
                let next = start;
                let moved = false;
                beginGesture(
                  event,
                  (point) => {
                    if (
                      Math.abs(point.x - origin.x) +
                        Math.abs(point.y - origin.y) >
                      4
                    )
                      moved = true;
                    next = {
                      x: Math.max(
                        2,
                        Math.min(
                          window.innerWidth - 190,
                          start.x + point.x - origin.x,
                        ),
                      ),
                      y: Math.max(
                        26,
                        Math.min(
                          window.innerHeight - 45,
                          start.y + point.y - origin.y,
                        ),
                      ),
                    };
                    setDragPoint(next);
                  },
                  (_, cancelled) => {
                    setDragPoint(null);
                    if (moved && !cancelled) pin(next);
                  },
                );
              }}
            >
              <span>{request.options.title}</span>
              {pin && request.options.pin && (
                <button
                  type="button"
                  aria-label={`Pin ${request.options.title} menu`}
                  onClick={() => pin(positions[0] ?? request.point)}
                >
                  ◆
                </button>
              )}
            </div>
          )}
          {level.items.map((item, index) =>
            item.separator ? (
              <div
                key={item.id ?? index}
                role="separator"
                className="ns-menu-separator"
              />
            ) : (
              <button
                id={`${menuId}-${levelIndex}-${index}`}
                key={item.id ?? index}
                type="button"
                role={
                  item.checked !== undefined ? 'menuitemcheckbox' : 'menuitem'
                }
                aria-checked={item.checked}
                aria-disabled={item.disabled || undefined}
                aria-haspopup={item.children ? 'menu' : undefined}
                aria-expanded={
                  item.children
                    ? level.selected === index && levels.length > levelIndex + 1
                    : undefined
                }
                tabIndex={-1}
                className={`ns-menu-item ${level.selected === index && !item.disabled ? 'is-selected' : ''}`}
                onPointerEnter={(event) => {
                  if (event.pointerType !== 'touch')
                    openSubmenu(levelIndex, index, !compact);
                }}
                onClick={() => activate(levelIndex, index)}
              >
                <span aria-hidden="true" className="ns-menu-mark">
                  {item.checked ? '✓' : ''}
                </span>
                <span className="ns-menu-label">{item.label}</span>
                <span aria-hidden="true" className="ns-menu-shortcut">
                  {item.shortcut}
                </span>
                {item.children && (
                  <span aria-hidden="true" className="ns-menu-chevron">
                    ▸
                  </span>
                )}
              </button>
            ),
          )}
        </div>
      ))}
    </div>
  );
}

export function DialogOverlay({
  options,
  resolve,
}: {
  options: DialogOptions;
  resolve: (result: DialogResult) => void;
}) {
  const [value, setValue] = useState(options.input?.value ?? '');
  const ref = useRef<HTMLDivElement>(null);
  const buttons = options.buttons ?? [
    { label: 'Cancel', value: 'cancel', cancel: true },
    { label: 'OK', value: 'ok', default: true },
  ];
  const finish = (button: (typeof buttons)[number] | undefined) =>
    resolve(!button || button.cancel ? null : { button: button.value, value });
  useEffect(() => {
    const opener =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    const first =
      ref.current?.querySelector<HTMLElement>(
        'input:not(:disabled):not([type="hidden"]), textarea:not(:disabled)',
      ) ??
      ref.current?.querySelector<HTMLElement>('[data-default="true"]') ??
      ref.current?.querySelector<HTMLElement>('button:not(:disabled)');
    first?.focus();
    if (first instanceof HTMLInputElement) first.select();
    const preventEscape = (event: FocusEvent) => {
      if (event.target instanceof Node && !ref.current?.contains(event.target))
        first?.focus();
    };
    document.addEventListener('focusin', preventEscape);
    return () => {
      document.removeEventListener('focusin', preventEscape);
      opener?.focus({ preventScroll: true });
    };
  }, []);
  return (
    <div className="ns-dialog-layer">
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={options.title}
        className="ns-dialog"
        onKeyDown={(event) => {
          event.stopPropagation();
          if (event.key === 'Escape') {
            event.preventDefault();
            finish(buttons.find((button) => button.cancel));
          }
          if (
            event.key === 'Enter' &&
            !(event.target instanceof HTMLTextAreaElement) &&
            !(event.target instanceof HTMLButtonElement)
          ) {
            event.preventDefault();
            finish(buttons.find((button) => button.default) ?? buttons.at(-1));
          }
          if (event.key === 'Tab') {
            const focusable = Array.from(
              ref.current?.querySelectorAll<HTMLElement>(
                'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex="0"]',
              ) ?? [],
            );
            const current = focusable.indexOf(
              document.activeElement as HTMLElement,
            );
            const next =
              (current + (event.shiftKey ? -1 : 1) + focusable.length) %
              focusable.length;
            event.preventDefault();
            focusable[next]?.focus();
          }
        }}
      >
        <div className="ns-dialog-header">{options.title}</div>
        <div className="ns-dialog-main">
          <PixelIcon name="info" />
          <div className="ns-dialog-copy">
            {options.message && (
              <div className="ns-dialog-message">{options.message}</div>
            )}
            {options.input && (
              <Field
                label={options.input.label}
                value={value}
                onChange={(event) => setValue(event.target.value)}
              />
            )}
          </div>
        </div>
        <div className="ns-dialog-actions">
          {buttons.map((button) => (
            <Button
              key={button.value}
              primary={button.default}
              data-default={button.default}
              onClick={() => finish(button)}
            >
              {button.label}
            </Button>
          ))}
        </div>
      </div>
    </div>
  );
}
