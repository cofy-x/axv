import { expect, test } from './helpers';
import { openTerminal, output, run } from './terminal-helpers';

test('Quick Switcher trigger fits cleanly on one line inside the top bar', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  const header = page.locator('.ns-desktop-status');
  const trigger = header.getByRole('button', { name: 'Quick Switcher' });
  await expect(trigger).toBeVisible();

  const headerBox = (await header.boundingBox())!;
  const triggerBox = (await trigger.boundingBox())!;
  const labelBox = (await trigger
    .locator('.desktop-quick-switcher-label')
    .boundingBox())!;
  const kbdBox = (await trigger.locator('kbd').boundingBox())!;

  expect(triggerBox.y).toBeGreaterThanOrEqual(headerBox.y);
  expect(triggerBox.y + triggerBox.height).toBeLessThanOrEqual(
    headerBox.y + headerBox.height,
  );
  // Label and keycap sit horizontally side by side on a single row.
  expect(kbdBox.x).toBeGreaterThanOrEqual(labelBox.x + labelBox.width);
  expect(Math.abs(kbdBox.y - labelBox.y)).toBeLessThan(6);
  expect(triggerBox.width).toBeGreaterThan(50);

  await trigger.click();
  await expect(page.getByTestId('quick-switcher')).toBeVisible();
  await page.keyboard.press('Escape');
});

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
