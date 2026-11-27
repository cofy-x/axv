type TerminalKey = Pick<
  KeyboardEvent,
  'key' | 'ctrlKey' | 'altKey' | 'metaKey'
> & { shiftKey?: boolean };

/** Keep desktop navigation reachable without consuming shell control sequences. */
export function isDesktopTerminalKey(event: TerminalKey) {
  const lower = event.key.toLowerCase();
  return (
    (event.ctrlKey && event.key === 'F6') ||
    (event.altKey && /^[1-4]$/.test(event.key)) ||
    event.key === 'F10' ||
    (event.metaKey && lower === 'w') ||
    (event.metaKey && lower === 'k') ||
    (event.ctrlKey && !!event.shiftKey && lower === 'k')
  );
}
