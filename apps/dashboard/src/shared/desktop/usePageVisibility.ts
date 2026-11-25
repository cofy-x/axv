import { useSyncExternalStore } from 'react';

const isPageVisible = () => document.visibilityState !== 'hidden';

function subscribe(listener: () => void) {
  document.addEventListener('visibilitychange', listener);
  return () => document.removeEventListener('visibilitychange', listener);
}

export function usePageVisibility() {
  return useSyncExternalStore(subscribe, isPageVisible);
}

export const inactiveSubscription = () => () => {};
