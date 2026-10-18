import { useEffect, useRef } from 'react';
import type { PointerEvent as ReactPointerEvent } from 'react';
import type { Point } from '../types';

/** Listeners live only for the active gesture, and cancel restores the prior layout. */
function trackPointer(
  event: ReactPointerEvent,
  move: (point: Point) => void,
  end: (point: Point, cancelled: boolean) => void,
) {
  const pointerId = event.pointerId;
  const target = event.currentTarget;
  const origin = { x: event.clientX, y: event.clientY };
  let completed = false;
  try {
    target.setPointerCapture(pointerId);
  } catch {
    /* A cancelled pointer may already have been released. */
  }
  const handleMove = (next: PointerEvent) => {
    if (next.pointerId === pointerId) {
      next.preventDefault();
      move({ x: next.clientX, y: next.clientY });
    }
  };
  const finish = (point: Point, cancelled: boolean) => {
    if (completed) return;
    completed = true;
    document.removeEventListener('pointermove', handleMove);
    document.removeEventListener('pointerup', handleEnd);
    document.removeEventListener('pointercancel', handleEnd);
    globalThis.removeEventListener('blur', cancel);
    try {
      if (target.hasPointerCapture(pointerId))
        target.releasePointerCapture(pointerId);
    } catch {
      /* The original target may have unmounted. */
    }
    end(point, cancelled);
  };
  const handleEnd = (next: PointerEvent) => {
    if (next.pointerId === pointerId)
      finish(
        { x: next.clientX, y: next.clientY },
        next.type === 'pointercancel',
      );
  };
  const cancel = () => finish(origin, true);
  document.addEventListener('pointermove', handleMove, { passive: false });
  document.addEventListener('pointerup', handleEnd);
  document.addEventListener('pointercancel', handleEnd);
  globalThis.addEventListener('blur', cancel);
  return cancel;
}

/** One gesture owner coordinates window, icon, and marquee interactions. */
export function usePointerGesture() {
  const cancelGesture = useRef<(() => void) | null>(null);

  useEffect(
    () => () => {
      cancelGesture.current?.();
    },
    [],
  );
  const beginGesture = (
    event: ReactPointerEvent,
    move: (point: Point) => void,
    end: (point: Point, cancelled: boolean) => void,
  ) => {
    cancelGesture.current?.();
    cancelGesture.current = trackPointer(event, move, (point, cancelled) => {
      cancelGesture.current = null;
      end(point, cancelled);
    });
  };
  return beginGesture;
}
