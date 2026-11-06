function available(element: Element | null): boolean {
  return (
    !!element &&
    element.isConnected &&
    !element.matches(':disabled') &&
    !element.closest('[inert], [hidden]') &&
    element.getClientRects().length > 0 &&
    element.ownerDocument.defaultView?.getComputedStyle(element).visibility ===
      'visible'
  );
}

/** Passive completion may leave an unavailable focus target, not a live chosen control. */
export function canTransferWindowFocus(
  target: HTMLElement | null,
  source?: HTMLElement | null,
): boolean {
  if (!target || !available(target)) return false;
  const document = target.ownerDocument;
  const active = document.activeElement;
  const frame = target.closest<HTMLElement>('[data-window-id]');
  return (
    active === document.body ||
    // Browsers can retain focus briefly after the outgoing window becomes inert.
    !available(active) ||
    (!!frame &&
      (active === frame ||
        (!!source &&
          source.closest('[data-window-id]') === frame &&
          source.contains(active))))
  );
}
