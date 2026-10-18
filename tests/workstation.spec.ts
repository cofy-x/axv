import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { activate } from './helpers';
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
    await welcome
      .getByRole('button', { name: 'About AXV', exact: true })
      .click();
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
