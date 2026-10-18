import type { Locator, Page } from '@playwright/test';
import { expect, menuItem } from './helpers';

export async function openTerminal(page: Page, touch = false) {
  const choose = async (target: Locator) => {
    if (touch) await target.tap();
    else await target.click();
  };
  await choose(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
  );
  await choose(menuItem(page, 'Applications'));
  await choose(menuItem(page, 'Terminal'));
  const terminal = page.locator('[data-app-id="terminal"]').last();
  await expect(terminal.getByTestId('terminal-status')).toHaveAttribute(
    'data-state',
    'connected',
  );
  const id = await terminal.getAttribute('data-window-id');
  expect(id).toBeTruthy();
  return page.locator(`[data-window-id="${id}"]`);
}

export async function run(terminal: Locator, command: string) {
  const input = terminal.getByLabel('Host terminal input', { exact: true });
  await input.focus();
  await input.pressSequentially(command);
  await input.press('Enter');
}

export function output(terminal: Locator) {
  return terminal.locator('.xterm-accessibility-tree');
}

export async function terminalAction(
  page: Page,
  terminal: Locator,
  name: string,
  touch = false,
) {
  const more = terminal.getByRole('button', {
    name: 'More terminal actions',
    exact: true,
  });
  if (touch) await more.tap();
  else await more.click();
  const item = page
    .locator('.ns-menu-layer')
    .getByRole('menuitem', { name, exact: true });
  if (touch) await item.tap();
  else await item.click();
}

export function encoded(text: string) {
  return [...Buffer.from(text)]
    .map((byte) => `\\${byte.toString(8).padStart(3, '0')}`)
    .join('');
}

export async function marker(terminal: Locator, text: string) {
  // The marker never appears in echoed shell input, so it proves execution.
  await run(terminal, `printf '${encoded(text)}\\n'`);
  await expect(output(terminal)).toContainText(text);
}

export async function applicationMenu(page: Page, group: string) {
  await page
    .getByRole('navigation', { name: 'Application menus' })
    .getByRole('button', { name: group, exact: true })
    .click();
}
