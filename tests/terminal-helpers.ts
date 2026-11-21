import type { Locator, Page } from '@playwright/test';
import { bounds, expect, menuItem } from './helpers';

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

export async function expectShellToolbar(
  terminal: Locator,
  caption: string,
  labels: string[],
) {
  await expect(terminal.locator('.terminal-project-info')).toHaveCount(0);
  await expect(
    terminal.getByRole('button', { name: 'Details', exact: true }),
  ).toHaveCount(0);
  const toolbar = terminal.locator('.terminal-toolbar');
  const shell = toolbar.getByText(caption, { exact: true });
  await expect(shell).toBeInViewport({ ratio: 1 });
  await expect(toolbar.getByRole('button')).toHaveCount(labels.length);
  const shellBounds = await bounds(shell);
  const center = shellBounds.y + shellBounds.height / 2;
  for (const name of labels) {
    const button = toolbar.getByRole('button', { name, exact: true });
    await expect(button).toBeInViewport({ ratio: 1 });
    const buttonBounds = await bounds(button);
    expect(
      Math.abs(buttonBounds.y + buttonBounds.height / 2 - center),
    ).toBeLessThanOrEqual(1);
  }
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

export { applicationMenu } from './desktop-application-helpers';
