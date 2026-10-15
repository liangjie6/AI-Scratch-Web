import {readFile} from 'node:fs/promises';
import {expect, test, type Locator, type Page} from '@playwright/test';

function range(page: Page, name: string) {
  return page.locator('.tool-options label').filter({hasText: name}).locator('input');
}

async function setRange(input: Locator, value: number) {
  await input.evaluate((element, next) => {
    (element as HTMLInputElement).value = String(next);
    element.dispatchEvent(new Event('input', {bubbles: true}));
    element.dispatchEvent(new Event('change', {bubbles: true}));
  }, value);
}

async function previewAlphas(row: Locator) {
  return row.locator('.layer-row__thumb img').evaluate(async element => {
    const image = new Image();
    image.src = (element as HTMLImageElement).src;
    await image.decode();
    const canvas = document.createElement('canvas');
    canvas.width = image.naturalWidth;
    canvas.height = image.naturalHeight;
    const context = canvas.getContext('2d')!;
    context.drawImage(image, 0, 0);
    return [0.25, 0.75].map(x => context.getImageData(Math.floor(x * canvas.width), Math.floor(canvas.height / 2), 1, 1).data[3]);
  });
}

async function clickImage(page: Page, x: number) {
  const rect = await page.locator('.canvas-viewport canvas').boundingBox();
  expect(rect).not.toBeNull();
  const zoom = Math.min((rect!.width - 72) / 200, (rect!.height - 72) / 100);
  await page.mouse.click(rect!.x + rect!.width / 2 + (x - 100) * zoom, rect!.y + rect!.height / 2);
}

async function exportedAlphas(page: Page) {
  const downloaded = page.waitForEvent('download');
  await page.getByRole('button', {name: 'PNG', exact: true}).click();
  const path = await (await downloaded).path();
  expect(path).not.toBeNull();
  const base64 = (await readFile(path!)).toString('base64');
  return page.evaluate(async png => {
    const image = new Image();
    image.src = `data:image/png;base64,${png}`;
    await image.decode();
    const canvas = document.createElement('canvas');
    canvas.width = image.naturalWidth;
    canvas.height = image.naturalHeight;
    const context = canvas.getContext('2d')!;
    context.drawImage(image, 0, 0);
    return [50, 150].map(x => context.getImageData(x, 50, 1, 1).data[3]);
  }, base64);
}

test('keeps the clicked chroma color when a canvas click activates another keyed layer', async ({page}) => {
  await page.setViewportSize({width: 1440, height: 900});
  await page.goto('/', {waitUntil: 'domcontentloaded'});
  await expect(page.locator('.blocklySvg')).toBeVisible({timeout: 45_000});
  await page.getByRole('button', {name: '打开画布', exact: true}).click();
  await expect(page.getByText('导入一张角色素材', {exact: true})).toBeVisible();
  const base64 = await page.evaluate(() => {
    const canvas = document.createElement('canvas');
    canvas.width = 200;
    canvas.height = 100;
    const context = canvas.getContext('2d')!;
    context.fillStyle = '#2060b0'; context.fillRect(0, 0, 100, 100);
    context.fillStyle = '#dc2828'; context.fillRect(100, 0, 100, 100);
    return canvas.toDataURL('image/png').split(',')[1]!;
  });
  await page.locator('.canvas-workspace input[type="file"]').setInputFiles({
    name: 'blue-red.png', mimeType: 'image/png', buffer: Buffer.from(base64, 'base64')
  });
  await expect(page.locator('.canvas-status')).toContainText('当前图层 200 × 100');
  await page.getByRole('button', {name: '色度抠图 (K)', exact: true}).click();
  await setRange(range(page, '容差'), 0);
  await setRange(range(page, '柔化'), 0);
  await setRange(range(page, '去色边'), 0);
  const targetColor = range(page, '目标颜色');
  const original = page.locator('.layer-row').filter({has: page.locator('input[value="原图"]')});
  await clickImage(page, 50);
  await expect.poll(() => previewAlphas(original)).toEqual([0, 255]);
  await expect(targetColor).toHaveValue('#2060b0');

  await page.getByRole('button', {name: '复制图层', exact: true}).click();
  await expect(page.locator('.layer-row')).toHaveCount(2);
  const duplicate = page.locator('.layer-row').filter({has: page.locator('input[value="原图 副本"]')});
  await expect(duplicate).toHaveAttribute('aria-selected', 'true');
  await expect.poll(() => previewAlphas(duplicate)).toEqual([0, 255]);
  await original.locator('.layer-row__thumb').click();
  await expect(original).toHaveAttribute('aria-selected', 'true');
  await expect(targetColor).toHaveValue('#2060b0');

  // Clicking the upper duplicate must activate it while its old blue effect is
  // being baked asynchronously, without the selection watcher restoring blue.
  await clickImage(page, 150);
  await expect(duplicate).toHaveAttribute('aria-selected', 'true');
  await expect.poll(() => previewAlphas(duplicate)).toEqual([0, 0]);
  await expect(targetColor).toHaveValue('#dc2828');
  await setRange(range(page, '容差'), 12);
  await expect(page.locator('.canvas-status')).toContainText('已调整色度抠图');
  await expect(targetColor).toHaveValue('#dc2828');
  await expect.poll(() => previewAlphas(duplicate)).toEqual([0, 0]);
  await expect.poll(() => previewAlphas(original)).toEqual([0, 255]);

  // The unchanged lower layer still supplies red to the composite. Hiding it
  // then verifies both colors were removed from the upper duplicate itself.
  expect(await exportedAlphas(page)).toEqual([0, 255]);
  await original.getByRole('button', {name: '隐藏图层', exact: true}).click();
  expect(await exportedAlphas(page)).toEqual([0, 0]);
});
