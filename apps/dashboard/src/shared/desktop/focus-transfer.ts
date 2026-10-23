/** Passive completion may claim a frame or leave its source pane, not a chosen control. */
export function canTransferWindowFocus(
  target: HTMLElement | null,
  source?: HTMLElement | null,
): boolean {
  if (!target) return false;
  const document = target.ownerDocument;
  const active = document.activeElement;
  const frame = target.closest<HTMLElement>('[data-window-id]');
  return (
    active === document.body ||
    (!!frame &&
      (active === frame ||
        (!!source &&
          source.closest('[data-window-id]') === frame &&
          source.contains(active))))
  );
}
