import { PixelIcon } from '../controls';
import type { DesktopProps } from '../types';
import type { DesktopGestures } from './useDesktopGestures';

interface DesktopCanvasProps extends Pick<
  DesktopProps,
  'icons' | 'onTrashIcons'
> {
  gestures: DesktopGestures;
}

export function DesktopCanvas({
  icons,
  onTrashIcons,
  gestures,
}: DesktopCanvasProps) {
  const {
    desktopRef,
    iconArea,
    iconContentHeight,
    beginMarquee,
    focusDesktop,
    contextMenu,
    desktopItems,
    selection,
    setSelection,
    trashHover,
    setTrashHover,
    iconPosition,
    beginIconGesture,
    doubleClickIcon,
    marquee,
  } = gestures;
  return (
    <div
      ref={desktopRef}
      className={`ns-desktop-canvas ${iconContentHeight > iconArea.height ? 'is-scrollable' : ''}`}
      style={{
        left: iconArea.x,
        top: iconArea.y,
        width: iconArea.width,
        height: iconArea.height,
      }}
      role="group"
      aria-label="Desktop icons"
      tabIndex={0}
      onPointerDown={beginMarquee}
      onContextMenu={(event) => {
        if (event.target !== event.currentTarget) return;
        event.preventDefault();
        focusDesktop();
        contextMenu(desktopItems(selection), {
          x: event.clientX,
          y: event.clientY,
        });
      }}
    >
      <div className="ns-icon-surface" style={{ height: iconContentHeight }}>
        {icons.map((icon) => (
          <button
            type="button"
            key={icon.id}
            data-icon-id={icon.id}
            aria-label={
              icon.status ? `${icon.label} — ${icon.status.label}` : icon.label
            }
            title={
              icon.status ? `${icon.label} — ${icon.status.label}` : icon.label
            }
            aria-pressed={selection.includes(icon.id)}
            className={`ns-desktop-icon ${icon.status ? 'has-status' : ''} ${selection.includes(icon.id) ? 'is-selected' : ''} ${icon.isTrash && trashHover ? 'is-trash-target' : ''}`}
            style={{
              left: iconPosition(icon).x - iconArea.x,
              top: iconPosition(icon).y - iconArea.y,
            }}
            onPointerDown={(event) => beginIconGesture(event, icon)}
            onClick={(event) =>
              event.currentTarget.focus({ preventScroll: true })
            }
            onDoubleClick={() => doubleClickIcon(icon)}
            onContextMenu={(event) => {
              event.preventDefault();
              event.stopPropagation();
              event.currentTarget.focus({ preventScroll: true });
              focusDesktop();
              const ids = selection.includes(icon.id) ? selection : [icon.id];
              setSelection(ids);
              contextMenu(desktopItems(ids), {
                x: event.clientX,
                y: event.clientY,
              });
            }}
            onDragOver={(event) => {
              if (
                icon.isTrash &&
                event.dataTransfer.types.includes(
                  'application/x-nextstep-entries',
                )
              ) {
                event.preventDefault();
                event.dataTransfer.dropEffect = 'move';
                setTrashHover(true);
              }
            }}
            onDragLeave={() => {
              if (icon.isTrash) setTrashHover(false);
            }}
            onDrop={(event) => {
              if (!icon.isTrash) return;
              event.preventDefault();
              setTrashHover(false);
              try {
                const ids: unknown = JSON.parse(
                  event.dataTransfer.getData('application/x-nextstep-entries'),
                );
                if (
                  Array.isArray(ids) &&
                  ids.every((id) => typeof id === 'string')
                ) {
                  const trashable = ids.filter((id) => {
                    const source = icons.find((item) => item.id === id);
                    return (
                      !source || (source.trashable === true && !source.isTrash)
                    );
                  });
                  if (trashable.length) onTrashIcons?.(trashable);
                }
              } catch {
                /* Ignore unrelated native drag payloads. */
              }
            }}
          >
            <PixelIcon name={icon.icon} />
            <span className="ns-desktop-icon-label">{icon.label}</span>
            {icon.status && (
              <span className={`ns-desktop-icon-status is-${icon.status.tone}`}>
                {icon.status.label}
              </span>
            )}
          </button>
        ))}
      </div>
      {marquee && (
        <div
          className="ns-marquee"
          style={{
            left: marquee.x,
            top: marquee.y,
            width: marquee.width,
            height: marquee.height,
          }}
        />
      )}
    </div>
  );
}
