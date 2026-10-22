import { useCallback, useEffect, useRef } from 'react';
import type { RefObject } from 'react';
import { createVMFileObserver } from './vm-file-observer';

type ObserverOptions = Parameters<typeof createVMFileObserver>[0];

export function useVMFileObserver({
  enabled,
  targetKey,
  activityKey,
  surface,
  ...options
}: ObserverOptions & {
  enabled: boolean;
  targetKey: string;
  activityKey: string;
  surface: RefObject<HTMLElement | null>;
}) {
  const latest = useRef(options);
  latest.current = options;
  const observer = useRef<ReturnType<typeof createVMFileObserver> | null>(null);
  const check = useCallback(() => observer.current?.check(), []);

  useEffect(() => {
    if (!enabled || !targetKey) return;
    const current = createVMFileObserver({
      snapshot: () => latest.current.snapshot(),
      read: (captured, signal) => latest.current.read(captured, signal),
      receive: (value, decision, captured) =>
        latest.current.receive(value, decision, captured),
      fail: (error, captured) => latest.current.fail(error, captured),
    });
    observer.current = current;
    const checkVisibility = () =>
      current.setVisible(
        document.visibilityState === 'visible' &&
          !!surface.current?.getClientRects().length,
      );
    document.addEventListener('visibilitychange', checkVisibility);
    const resize = new ResizeObserver(checkVisibility);
    if (surface.current) resize.observe(surface.current);
    checkVisibility();
    current.check();
    return () => {
      current.stop();
      observer.current = null;
      resize.disconnect();
      document.removeEventListener('visibilitychange', checkVisibility);
    };
  }, [enabled, targetKey, surface]);

  useEffect(() => {
    // A save/composition/recovery transition resumes checking immediately.
    void activityKey;
    check();
  }, [activityKey, check]);

  return check;
}
