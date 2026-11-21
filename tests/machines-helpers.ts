import type { Page } from '@playwright/test';
import { applicationMenu } from './desktop-application-helpers';
import { menuItem } from './helpers';

export async function refreshMachines(page: Page, touch = false) {
  const menu = page.getByRole('menu', { name: 'Machines', exact: true });
  const refresh = menuItem(menu, 'Refresh');
  await applicationMenu(page, 'Machines', touch);
  if (touch) await refresh.tap();
  else await refresh.click();
}
