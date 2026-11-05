import { useEffect, useState } from 'react';
import type { Viewport } from '../types';

export const viewportSize = (): Viewport => ({
  width: globalThis.innerWidth || 1024,
  height: globalThis.visualViewport?.height || globalThis.innerHeight || 768,
  coarsePointer: globalThis.matchMedia?.('(pointer: coarse)').matches ?? false,
});

export function useViewport() {
  const [viewport, setViewport] = useState(viewportSize);
  useEffect(() => {
    const resize = () => setViewport(viewportSize());
    const pointer = globalThis.matchMedia?.('(pointer: coarse)');
    globalThis.addEventListener('resize', resize);
    globalThis.visualViewport?.addEventListener('resize', resize);
    pointer?.addEventListener('change', resize);
    return () => {
      globalThis.removeEventListener('resize', resize);
      globalThis.visualViewport?.removeEventListener('resize', resize);
      pointer?.removeEventListener('change', resize);
    };
  }, []);
  return viewport;
}
