import { PixelIcon } from '../controls';
import type { DesktopIcon } from '../types';
import type { DesktopGestures } from './useDesktopGestures';

interface DesktopCanvasProps {
  icons: DesktopIcon[];
  gestures: DesktopGestures;
  inert?: boolean;
}

export function DesktopCanvas({ icons, gestures, inert }: DesktopCanvasProps) {
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
    iconPosition,
    beginIconGesture,
    clickIcon,
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
      inert={inert}
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
            className={`ns-desktop-icon ${icon.status ? 'has-status' : ''} ${selection.includes(icon.id) ? 'is-selected' : ''}`}
            style={{
              left: iconPosition(icon).x - iconArea.x,
              top: iconPosition(icon).y - iconArea.y,
            }}
            onPointerDown={(event) => beginIconGesture(event, icon)}
            onClick={(event) => {
              event.currentTarget.focus({ preventScroll: true });
              clickIcon(icon);
            }}
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
