import { useEffect, useState } from 'react';

export function useResource<T>(
  key: string,
  load: () => Promise<T>,
  assertCurrent?: () => void,
) {
  const [state, setState] = useState<{
    key: string;
    data?: T;
    error?: string;
  }>({ key: '' });
  useEffect(() => {
    let current = true;
    void load()
      .then((data) => {
        assertCurrent?.();
        if (current) setState({ key, data });
      })
      .catch((error: unknown) => {
        if (current)
          setState({
            key,
            error: error instanceof Error ? error.message : String(error),
          });
      });
    return () => {
      current = false;
    };
  }, [key, load, assertCurrent]);
  return state.key === key ? state : { key };
}
