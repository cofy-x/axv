import { expect, test } from './helpers';
import { openTerminal, output, run } from './terminal-helpers';

test('Quick Switcher opens via Ctrl+K, filters items, and launches apps', async ({
  page,
}) => {
  await page.keyboard.press('Control+k');
  const switcher = page.getByTestId('quick-switcher');
  await expect(switcher).toBeVisible();

  const search = switcher.getByRole('searchbox', { name: 'Search workspace' });
  await expect(search).toBeFocused();
  await search.fill('Project Files');
  await page.keyboard.press('Enter');

  await expect(switcher).toHaveCount(0);
  await expect(page.locator('[data-app-id="project-files"]')).toBeVisible();
});

test('Quick Switcher preserves Ctrl+K for terminal shell and opens via Ctrl+Shift+K', async ({
  page,
}) => {
  test.skip(process.platform === 'win32', 'Uses POSIX shell commands.');
  const terminal = await openTerminal(page);
  await run(terminal, 'echo SWITCHER_TERM_READY');
  await expect(output(terminal)).toContainText('SWITCHER_TERM_READY');

  // Plain Ctrl+K inside a focused terminal stays with the shell.
  await page.keyboard.press('Control+k');
  await expect(page.getByTestId('quick-switcher')).toHaveCount(0);

  // Ctrl+Shift+K opens Quick Switcher even when terminal is focused.
  await page.keyboard.press('Control+Shift+K');
  await expect(page.getByTestId('quick-switcher')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByTestId('quick-switcher')).toHaveCount(0);
});
