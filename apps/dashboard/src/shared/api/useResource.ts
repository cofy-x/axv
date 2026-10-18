import { useEffect, useState } from 'react';

export function useResource<T>(key: string, load: () => Promise<T>) {
  const [state, setState] = useState<{
    key: string;
    data?: T;
    error?: string;
  }>({ key: '' });
  useEffect(() => {
    let current = true;
    void load().then(
      (data) => {
        if (current) setState({ key, data });
      },
      (error: unknown) => {
        if (current)
          setState({
            key,
            error: error instanceof Error ? error.message : String(error),
          });
      },
    );
    return () => {
      current = false;
    };
  }, [key, load]);
  return state.key === key ? state : { key };
}
