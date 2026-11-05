import type { Locator, Page } from '@playwright/test';
import { expect, menuItem } from './helpers';

/** Opt in through the same browser preference used by a workstation owner. */
export async function showApplicationOnDesktop(
  page: Page,
  title: string,
  touch = false,
) {
  const choose = async (target: Locator) => {
    if (touch) await target.tap();
    else await target.click();
  };
  await choose(
    page.getByRole('button', { name: 'Workspace menu', exact: true }),
  );
  await choose(menuItem(page.locator('.ns-menu-layer'), 'Applications'));
  await choose(
    menuItem(
      page.getByRole('menu', { name: 'Applications', exact: true }),
      'Application Manager',
    ),
  );
  const manager = page.locator('[data-app-id="applications"]');
  await expect(manager).toBeVisible();
  await choose(manager.locator('.applications-builtins summary'));
  const checkbox = manager.getByRole('checkbox', {
    name: `Show ${title} on desktop`,
    exact: true,
  });
  if (!(await checkbox.isChecked())) await choose(checkbox);
  await expect(checkbox).toBeChecked();
  await choose(manager.getByRole('button', { name: /^Close / }));
}

/** Open the active application's group through either shell presentation. */
export async function applicationMenu(
  page: Page,
  group: string,
  touch = false,
) {
  const choose = async (control: Locator) =>
    touch ? control.tap() : control.click();
  await choose(
    page
      .getByRole('navigation', { name: 'Application menus' })
      .getByRole('button', { name: group, exact: true }),
  );
  await expect(
    page.getByRole('menu', { name: group, exact: true }),
  ).toBeVisible();
}
