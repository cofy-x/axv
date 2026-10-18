type TerminalKey = Pick<
  KeyboardEvent,
  'key' | 'ctrlKey' | 'altKey' | 'metaKey'
>;

/** Keep desktop navigation reachable without consuming shell control sequences. */
export function isDesktopTerminalKey(event: TerminalKey) {
  return (
    (event.ctrlKey && event.key === 'F6') ||
    (event.altKey && /^[1-4]$/.test(event.key)) ||
    event.key === 'F10' ||
    (event.metaKey && event.key.toLowerCase() === 'w')
  );
}
