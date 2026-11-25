import { useSyncExternalStore } from 'react';
import {
  inactiveSubscription,
  usePageVisibility,
} from '../../shared/desktop/usePageVisibility';
import { computeStore } from './compute-store';
import { preparationStore } from './preparation-store';

function refresh() {
  computeStore.refresh();
  preparationStore.refresh();
}

export type { ComputeSnapshot } from './compute-store';

export function useCompute(visible = true) {
  const pageVisible = usePageVisibility();
  const observe = visible && pageVisible;
  const snapshot = useSyncExternalStore(
    observe ? computeStore.subscribe : inactiveSubscription,
    computeStore.getSnapshot,
  );
  const preparation = useSyncExternalStore(
    observe ? preparationStore.subscribe : inactiveSubscription,
    preparationStore.getSnapshot,
  );
  return {
    ...snapshot,
    preparation,
    prepare: preparationStore,
    refresh,
  };
}
