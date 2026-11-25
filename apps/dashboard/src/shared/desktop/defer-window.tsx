import { useEffect, useState, useSyncExternalStore } from 'react';
import type { ComponentType } from 'react';
import { Button } from '@cofy-x/axv-nextstep';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';

type LoadState<Props extends AppWindowProps> =
  | { status: 'idle' | 'loading' }
  | { status: 'ready'; Content: ComponentType<Props> }
  | { status: 'failed'; error: string };

/** Declare once per view, outside registry factories, to retain mounted owners. */
export function deferWindow<Props extends AppWindowProps>(
  load: () => Promise<ComponentType<Props>>,
): ComponentType<Props> {
  let state: LoadState<Props> = { status: 'idle' };
  const listeners = new Set<() => void>();
  const snapshot = () => state;
  const subscribe = (listener: () => void) => {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  };
  const publish = (next: LoadState<Props>) => {
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

  return function DeferredWindow(props: Props) {
    const current = useSyncExternalStore(subscribe, snapshot, snapshot);
    const visible = props.visible !== false;
    const [presented, setPresented] = useState(false);
    // Once rendered, a view keeps its mounted owner even when its window is hidden.
    if (visible && current.status === 'ready' && !presented) setPresented(true);
    useEffect(() => {
      if (visible && state.status === 'idle') start();
    }, [visible]);

    if (!visible && !presented) return null;
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
          <p className="fine-print">
            Save, download or copy unsaved work before reloading.
          </p>
          <details className="deferred-window-details">
            <summary>Details</summary>
            <p className="deferred-window-error">{current.error}</p>
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
