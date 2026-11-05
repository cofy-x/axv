import { desktopDatabase } from './desktop';
import { useResource } from './useResource';

/** Validate the original store immediately before committing a resource view. */
export function useDesktopResource<T>(key: string, load: () => Promise<T>) {
  return useResource(key, load, desktopDatabase.assertActive);
}
