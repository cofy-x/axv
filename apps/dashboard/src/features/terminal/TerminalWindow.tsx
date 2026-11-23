import { useCallback, useEffect, useRef } from 'react';
import type { AppWindowProps } from '@cofy-x/axv-nextstep';
import { request } from '../../shared/api/http';
import { deferWindow } from '../../shared/desktop/defer-window';
import type { TerminalInfo } from './terminal-session';

let moduleAttempt = new AbortController();
const TerminalApp = deferWindow(() =>
  import('./TerminalApp').then(
    (module) => {
      if (moduleAttempt.signal.aborted) moduleAttempt = new AbortController();
      return module.TerminalApp;
    },
    (error: unknown) => {
      moduleAttempt.abort();
      throw error;
    },
  ),
);

const readInfo = (signal: AbortSignal) =>
  request<TerminalInfo>('/terminal/info', { signal });

export function TerminalWindow(props: AppWindowProps) {
  const initial = useRef<{
    controller: AbortController;
    result: Promise<TerminalInfo>;
    moduleSignal: AbortSignal;
  }>(null);

  useEffect(() => {
    const moduleSignal = moduleAttempt.signal;
    if (moduleSignal.aborted) return;
    const controller = new AbortController();
    const result = readInfo(AbortSignal.any([controller.signal, moduleSignal]));
    initial.current = { controller, result, moduleSignal };
    // The view may still be downloading when the read fails.
    void result.catch(() => {});
    return () => {
      initial.current = null;
      controller.abort();
    };
  }, []);

  const getInfo = useCallback((signal: AbortSignal) => {
    const pending = initial.current;
    initial.current = null;
    if (!pending || pending.moduleSignal.aborted) return readInfo(signal);
    const abort = () => pending.controller.abort();
    if (signal.aborted) abort();
    else signal.addEventListener('abort', abort, { once: true });
    return pending.result.finally(() =>
      signal.removeEventListener('abort', abort),
    );
  }, []);

  return <TerminalApp {...props} getInfo={getInfo} />;
}
