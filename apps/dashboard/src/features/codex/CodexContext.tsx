import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from 'react';
import type { ReactNode } from 'react';
import { codexAPI } from './api';
import type { CodexInfo } from './api';

interface CodexAvailability {
  info: CodexInfo | null;
  loading: boolean;
  error: string;
  status: string;
  refresh: () => Promise<void>;
}

const CodexContext = createContext<CodexAvailability | null>(null);

export function CodexProvider({ children }: { children: ReactNode }) {
  const [info, setInfo] = useState<CodexInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const pending = useRef<AbortController | null>(null);
  const refresh = useCallback(async () => {
    pending.current?.abort();
    const controller = new AbortController();
    pending.current = controller;
    setLoading(true);
    try {
      const result = await codexAPI.info(controller.signal);
      if (controller.signal.aborted) return;
      setInfo(result);
      setError('');
    } catch (failure) {
      if (controller.signal.aborted) return;
      setError(
        failure instanceof Error
          ? failure.message
          : 'Could not check host Codex.',
      );
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, []);
  useEffect(() => {
    void refresh();
    return () => pending.current?.abort();
  }, [refresh]);
  const status = loading
    ? 'Checking environment…'
    : error
      ? 'Environment check failed'
      : info?.ready
        ? 'CLI ready'
        : 'Setup required';
  return (
    <CodexContext.Provider value={{ info, loading, error, status, refresh }}>
      {children}
    </CodexContext.Provider>
  );
}

export function useCodexAvailability() {
  const value = useContext(CodexContext);
  if (!value) throw new Error('Codex requires its availability provider.');
  return value;
}
