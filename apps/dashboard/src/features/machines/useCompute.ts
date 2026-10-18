import { useEffect, useSyncExternalStore } from 'react';
import { computeStore } from './compute-store';
import { preparationStore } from './preparation-store';

export type { ComputeSnapshot } from './compute-store';

export function useCompute() {
  useEffect(() => {
    if (computeStore.getSnapshot().lastUpdated) computeStore.refresh();
  }, []);
  const snapshot = useSyncExternalStore(
    computeStore.subscribe,
    computeStore.getSnapshot,
  );
  const preparation = useSyncExternalStore(
    preparationStore.subscribe,
    preparationStore.getSnapshot,
  );
  return {
    ...snapshot,
    preparation,
    prepare: preparationStore,
    refresh: () => {
      computeStore.refresh();
      preparationStore.refresh();
    },
  };
}
