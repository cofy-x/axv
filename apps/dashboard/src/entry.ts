// Load application CSS without blocking the HTML startup surface's first paint.
void import('./main').catch((error: unknown) => {
  console.error('AXV startup failed.', error);
  const status = document.querySelector('.startup-status');
  const panel = document.querySelector<HTMLElement>('.startup-panel');
  const recovery = document.querySelector<HTMLElement>('.startup-error');
  const reload = document.querySelector<HTMLButtonElement>('.startup-reload');
  if (status) status.textContent = 'Unable to load AXV';
  if (panel) panel.dataset.failed = 'true';
  if (recovery) recovery.hidden = false;
  reload?.addEventListener('click', () => location.reload(), { once: true });
});
