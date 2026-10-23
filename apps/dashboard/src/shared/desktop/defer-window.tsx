import { useEffect, useSyncExternalStore } from 'react';
import type { ComponentType } from 'react';
import { Button } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';

type WindowContent = ComponentType<AppWindowProps>;

type LoadState =
  | { status: 'idle' | 'loading' }
  | { status: 'ready'; Content: WindowContent }
  | { status: 'failed'; error: string };

/** Declare once per view, outside registry factories, to retain mounted owners. */
export function deferWindow(load: () => Promise<WindowContent>): WindowContent {
  let state: LoadState = { status: 'idle' };
  const listeners = new Set<() => void>();
  const snapshot = () => state;
  const subscribe = (listener: () => void) => {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  };
  const publish = (next: LoadState) => {
    state = next;
    for (const listener of listeners) listener();
  };

  function start() {
    if (state.status === 'loading' || state.status === 'ready') return;
    publish({ status: 'loading' });
    // Share module loading, never a window's props, state or execution target.
    void Promise.resolve()
      .then(load)
      .then(
        (Content) => publish({ status: 'ready', Content }),
        (error: unknown) =>
          publish({
            status: 'failed',
            error: error instanceof Error ? error.message : String(error),
          }),
      );
  }

  return function DeferredWindow(props: AppWindowProps) {
    const current = useSyncExternalStore(subscribe, snapshot, snapshot);
    useEffect(() => {
      if (state.status === 'idle') start();
    }, []);

    if (current.status === 'ready') {
      const Content = current.Content;
      return <Content {...props} />;
    }
    if (current.status === 'failed') {
      return (
        <div className="app-pad" role="alert">
          <p>Could not load this window.</p>
          <p>Check your connection, then retry.</p>
          <Button disabled={props.interactive === false} onClick={start}>
            Retry
          </Button>
          <details className="deferred-window-details">
            <summary>Details</summary>
            <p className="deferred-window-error">{current.error}</p>
            <p>If retry keeps failing, save other work before reloading AXV.</p>
          </details>
        </div>
      );
    }
    return (
      <div className="app-pad" role="status">
        Loading…
      </div>
    );
  };
}
