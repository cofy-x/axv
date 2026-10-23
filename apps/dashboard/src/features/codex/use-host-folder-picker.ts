import { useCallback, useEffect, useRef, useState } from 'react';
import { codexAPI } from './api';
import type { HostDirectories } from './api';

/** Directory observations never replace a newer, unsubmitted path draft. */
export function useHostFolderPicker() {
  const [open, setOpen] = useState(false);
  const [path, setPath] = useState('');
  const [directories, setDirectories] = useState<HostDirectories | null>(null);
  const [error, setError] = useState('');
  const [browsing, setBrowsing] = useState(false);
  const draftRevision = useRef(0);
  const pending = useRef<AbortController | null>(null);
  const mounted = useRef(false);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      pending.current?.abort();
      pending.current = null;
    };
  }, []);

  const changePath = useCallback((value: string) => {
    draftRevision.current++;
    setPath(value);
  }, []);

  const browse = useCallback(async (next?: string, preserveDraft = false) => {
    pending.current?.abort();
    const controller = new AbortController();
    pending.current = controller;
    const revision = draftRevision.current;
    setBrowsing(true);
    setError('');
    try {
      const result = await codexAPI.directories(next, controller.signal);
      if (
        mounted.current &&
        pending.current === controller &&
        !controller.signal.aborted
      ) {
        setDirectories(result);
        if (!preserveDraft && draftRevision.current === revision)
          setPath(result.path);
      }
    } catch (failure) {
      if (
        mounted.current &&
        pending.current === controller &&
        !controller.signal.aborted
      )
        setError(
          failure instanceof Error
            ? failure.message
            : 'Could not read this directory.',
        );
    } finally {
      if (mounted.current && pending.current === controller) {
        pending.current = null;
        setBrowsing(false);
      }
    }
  }, []);

  const show = () => {
    setOpen(true);
    // Refresh the last confirmed location, without submitting a saved draft.
    void browse(
      directories?.path,
      directories !== null || draftRevision.current > 0,
    );
  };
  const close = () => {
    pending.current?.abort();
    pending.current = null;
    setBrowsing(false);
    setOpen(false);
  };

  return {
    open,
    path,
    directories,
    error,
    browsing,
    changePath,
    browse,
    show,
    close,
  };
}
