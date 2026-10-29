import { readFile, writeFile } from 'node:fs/promises';
import { chromium } from '@playwright/test';
import { initializeTestTemp } from './test-temp.mjs';

initializeTestTemp();

const directory = new URL('../apps/dashboard/public/icons/', import.meta.url);
const source = await readFile(new URL('../favicon.svg', directory), 'utf8');
const browser = await chromium.launch();
try {
  const page = await browser.newPage();
  for (const [size, name] of [
    [192, 'axv-192.png'],
    [512, 'axv-512.png'],
    [180, 'apple-touch-icon.png'],
  ]) {
    const png = await page.evaluate(
      async ({ source, size }) => {
        const image = new globalThis.Image();
        image.src = `data:image/svg+xml,${encodeURIComponent(source)}`;
        await image.decode();
        const canvas = globalThis.document.createElement('canvas');
        canvas.width = size;
        canvas.height = size;
        canvas.getContext('2d').drawImage(image, 0, 0, size, size);
        return canvas.toDataURL('image/png').split(',')[1];
      },
      { source, size },
    );
    await writeFile(new URL(name, directory), Buffer.from(png, 'base64'));
  }
} finally {
  await browser.close();
}
