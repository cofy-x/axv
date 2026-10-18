import { useEffect, useState } from 'react';
import type { Size } from '../types';

export const viewportSize = (): Size => ({
  width: globalThis.innerWidth || 1024,
  height: globalThis.visualViewport?.height || globalThis.innerHeight || 768,
});

export function useViewport() {
  const [viewport, setViewport] = useState(viewportSize);
  useEffect(() => {
    const resize = () => setViewport(viewportSize());
    globalThis.addEventListener('resize', resize);
    globalThis.visualViewport?.addEventListener('resize', resize);
    return () => {
      globalThis.removeEventListener('resize', resize);
      globalThis.visualViewport?.removeEventListener('resize', resize);
    };
  }, []);
  return viewport;
}
