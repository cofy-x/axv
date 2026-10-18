import { defineConfig } from '@playwright/test';

if (!process.env.E2E_BASE_URL)
  throw new Error('Run make test-e2e to start an isolated test server.');

export default defineConfig({
  testDir: './tests',
  testMatch: '**/*.spec.ts',
  fullyParallel: false,
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: process.env.E2E_BASE_URL,
    viewport: { width: 1440, height: 900 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    acceptDownloads: true,
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
    { name: 'firefox', use: { browserName: 'firefox' } },
    { name: 'webkit', use: { browserName: 'webkit' } },
    {
      name: 'chromium-dpr2',
      testMatch: [
        '**/visual.spec.ts',
        '**/codex.visual.spec.ts',
        '**/desktop-icons.spec.ts',
        '**/workstation.spec.ts',
        '**/document-workflow.spec.ts',
        '**/machines.spec.ts',
        '**/machines-experience.spec.ts',
        '**/machines-forms.spec.ts',
        '**/machines-preparation.spec.ts',
      ],
      use: { browserName: 'chromium', deviceScaleFactor: 2 },
    },
    {
      name: 'chromium-touch',
      testMatch: [
        '**/touch.spec.ts',
        '**/codex.touch.spec.ts',
        '**/desktop-icons.spec.ts',
        '**/workstation.spec.ts',
        '**/document-workflow.spec.ts',
        '**/machines.spec.ts',
        '**/machines-experience.spec.ts',
        '**/machines-forms.spec.ts',
        '**/machines-preparation.spec.ts',
      ],
      use: {
        browserName: 'chromium',
        viewport: { width: 390, height: 844 },
        deviceScaleFactor: 2,
        hasTouch: true,
        isMobile: true,
      },
    },
    {
      name: 'webkit-touch',
      testMatch: [
        '**/touch.spec.ts',
        '**/codex.touch.spec.ts',
        '**/desktop-icons.spec.ts',
        '**/workstation.spec.ts',
        '**/document-workflow.spec.ts',
        '**/machines.spec.ts',
        '**/machines-experience.spec.ts',
        '**/machines-forms.spec.ts',
        '**/machines-preparation.spec.ts',
      ],
      use: {
        browserName: 'webkit',
        viewport: { width: 390, height: 844 },
        deviceScaleFactor: 3,
        hasTouch: true,
        isMobile: true,
      },
    },
  ],
});
