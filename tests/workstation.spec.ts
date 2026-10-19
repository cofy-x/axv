import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import type { Locator } from '@playwright/test';
import {
  actionMenu,
  activate,
  createEntry,
  entries,
  launch,
  menuItem,
} from './helpers';
import { expect, openCodexManager, test } from './codex-helpers';

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
  { width: 390, height: 844 },
]) {
  test(`Codex and Virtual Machines coexist at ${viewport.width}×${viewport.height}`, async ({
    page,
    request,
  }, info) => {
    await page.setViewportSize(viewport);
    test.skip(
      info.project.use.hasTouch === true && viewport.width !== 390,
      'Touch projects use the narrow viewport.',
    );
    const welcome = page.locator('[data-window-id="welcome:main"]');
    await activate(page, welcome);
    await expect(
      welcome.getByRole('button', { name: /^Codex / }),
    ).toBeVisible();
    const machinesLink = welcome.getByRole('button', {
      name: /^Virtual Machines /,
    });
    if (info.project.use.hasTouch) await machinesLink.tap();
    else await machinesLink.click();
    const machines = page.locator('[data-window-id="machines:main"]');
    await expect(machines).toBeVisible();
    await expect(
      machines.getByRole('heading', { name: 'Enable virtual machines' }),
    ).toBeVisible();
    await expect(
      machines.getByRole('button', { name: 'Review', exact: true }),
    ).toBeVisible();
    await expect(
      machines.getByRole('button', { name: 'New VM', exact: true }),
    ).toHaveCount(0);
    await expect(
      machines.getByRole('button', { name: 'Refresh', exact: true }),
    ).toBeInViewport();
    const compute = await request.get('/api/compute/info');
    expect((await compute.json()).configured).toBe(false);
    const screenshotDir = resolve('screenshots', `final-${info.project.name}`);
    await mkdir(screenshotDir, { recursive: true });
    const capture = async (name: string) => {
      const path = resolve(
        screenshotDir,
        `${viewport.width}x${viewport.height}-${name}.png`,
      );
      await page.screenshot({ path, fullPage: true });
      await info.attach(name, { path, contentType: 'image/png' });
    };
    await capture('machines-integrated');
    const codex = await openCodexManager(page);
    await expect(
      codex.getByRole('button', { name: 'New Session', exact: true }),
    ).toBeEnabled();
    await capture('codex-integrated');
    await activate(page, welcome);
    await capture('welcome-integrated');
    const more = welcome.getByRole('button', {
      name: 'More welcome actions',
      exact: true,
    });
    await more.focus();
    await more.press('Enter');
    await expect(menuItem(page, 'Preferences')).toBeInViewport();
    await expect(menuItem(page, 'About AXV')).toBeInViewport();
    await capture('welcome-more-menu');
    await page.keyboard.press('Escape');
    await expect(more).toBeFocused();
    await actionMenu(
      page,
      welcome,
      'More welcome actions',
      'Preferences',
      info.project.use.hasTouch === true,
    );
    const preferences = page.locator('[data-app-id="appearance"]');
    await expect(preferences).toBeVisible();
    await preferences.getByRole('button', { name: /^Close / }).click();
    await activate(page, welcome);
    await actionMenu(
      page,
      welcome,
      'More welcome actions',
      'About AXV',
      info.project.use.hasTouch === true,
    );
    const about = page.locator('[data-window-id="about:main"]');
    await expect(about).toBeVisible();
    await capture('about-integrated');
    await about.getByRole('button', { name: /^Close / }).click();
    // Layout restoration must retain both registered application identities.
    await expect
      .poll(async () => {
        const clientId = await page.evaluate(() =>
          localStorage.getItem('nextstep-browser-id'),
        );
        const response = await request.get(
          `/api/bootstrap?clientId=${encodeURIComponent(clientId!)}`,
        );
        const layout = (await response.json()).layout;
        return ['machines', 'codex'].every((appId) =>
          layout?.windows.some(
            (window: { appId: string }) => window.appId === appId,
          ),
        );
      })
      .toBe(true);
    await page.reload();
    await expect(machines).toBeAttached();
    await expect(codex).toBeAttached();
  });
}

test('Recycler menus preserve their selection and confirm permanent deletion', async ({
  page,
  request,
  isMobile,
}, info) => {
  const choose = (control: Locator) =>
    isMobile ? control.tap() : control.click();
  const first = await createEntry(request, 'E2E Recycler original.txt');
  const second = await createEntry(request, 'E2E Recycler later.txt');
  for (const entry of [first, second])
    expect(
      (await request.post(`/api/entries/${entry.id}/trash`)).ok(),
    ).toBeTruthy();
  await page.reload();
  await launch(page, 'trash');
  const recycler = page.locator('[data-app-id="trash"]');
  const original = recycler.getByRole('option', {
    name: new RegExp(first.name),
  });
  const later = recycler.getByRole('option', { name: new RegExp(second.name) });
  const more = recycler.getByRole('button', {
    name: 'More recycler actions',
    exact: true,
  });
  await choose(original);
  await more.focus();
  await more.press('Enter');
  await expect(menuItem(page, 'Delete')).toBeEnabled();
  await expect(menuItem(page, 'Empty Recycler')).toBeInViewport();
  const screenshotDir = resolve('screenshots', `final-${info.project.name}`);
  await mkdir(screenshotDir, { recursive: true });
  const path = resolve(screenshotDir, 'recycler-more-menu.png');
  await page.screenshot({ path, fullPage: true });
  await info.attach('recycler-more-menu', { path, contentType: 'image/png' });
  await page.keyboard.press('Escape');
  await expect(more).toBeFocused();
  await actionMenu(page, recycler, 'More recycler actions', 'Delete', isMobile);
  const confirmation = page.getByRole('dialog', {
    name: 'Delete permanently?',
    exact: true,
  });
  await choose(
    confirmation.getByRole('button', { name: 'Cancel', exact: true }),
  );
  await expect(original).toBeVisible();
  await expect(later).toBeVisible();

  await choose(more);
  // A background selection change must not retarget an already open menu.
  await later.evaluate((element) => (element as HTMLButtonElement).click());
  await expect(later).toHaveAttribute('aria-selected', 'true');
  await choose(menuItem(page, 'Delete'));
  await choose(
    confirmation.getByRole('button', { name: 'Delete', exact: true }),
  );
  await expect(original).toHaveCount(0);
  await expect(later).toBeVisible();
  await expect
    .poll(async () =>
      (await entries(request)).some((entry) => entry.id === first.id),
    )
    .toBe(false);
  await expect
    .poll(async () =>
      (await entries(request)).some((entry) => entry.id === second.id),
    )
    .toBe(true);

  await actionMenu(
    page,
    recycler,
    'More recycler actions',
    'Empty Recycler',
    isMobile,
  );
  const empty = page.getByRole('dialog', {
    name: 'Empty the Recycler?',
    exact: true,
  });
  await choose(empty.getByRole('button', { name: 'Cancel', exact: true }));
  await expect(later).toBeVisible();
  await actionMenu(
    page,
    recycler,
    'More recycler actions',
    'Empty Recycler',
    isMobile,
  );
  await choose(empty.getByRole('button', { name: 'Delete', exact: true }));
  await expect(later).toHaveCount(0);
});
