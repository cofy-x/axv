import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import type { Locator, Page, TestInfo } from '@playwright/test';
import { bounds, expect } from './helpers';

export interface WindowRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export function rect(value: WindowRect): WindowRect {
  return { x: value.x, y: value.y, width: value.width, height: value.height };
}

export async function expectRect(window: Locator, expected: WindowRect) {
  await expect.poll(() => bounds(window)).toEqual(expected);
}

export async function captureWorkArea(
  page: Page,
  info: TestInfo,
  state: string,
) {
  const viewport = page.viewportSize()!;
  const directory = resolve('screenshots', `final-${info.project.name}`);
  await mkdir(directory, { recursive: true });
  const path = resolve(
    directory,
    `${viewport.width}x${viewport.height}-work-area-${state}.png`,
  );
  await page.screenshot({ path, fullPage: true });
  await info.attach(`work-area-${state}`, { path, contentType: 'image/png' });
}
