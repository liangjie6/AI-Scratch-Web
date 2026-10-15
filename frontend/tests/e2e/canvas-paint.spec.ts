import {readFile} from 'node:fs/promises';
import {expect, test, type Page} from '@playwright/test';

type Point = {x: number; y: number};
const white = [255, 255, 255, 255];

async function openFixture(page: Page, width = 200, height = 100, barrier = false) {
  await page.setViewportSize({width: 1440, height: 900});
  await page.goto('/', {waitUntil: 'domcontentloaded'});
  await expect(page.locator('.blocklySvg')).toBeVisible({timeout: 30_000});
  await page.getByRole('button', {name: '打开画布', exact: true}).click();
  const workspace = page.getByRole('dialog', {name: '无边画布编辑器'});
  await expect(workspace).toBeVisible();
  const base64 = await page.evaluate(({width, height, barrier}) => {
    const canvas = document.createElement('canvas'); canvas.width = width; canvas.height = height;
    const context = canvas.getContext('2d')!;
    context.fillStyle = '#ffffff'; context.fillRect(0, 0, width, height);
    if (barrier) { context.fillStyle = '#000000'; context.fillRect(98, 0, 4, height); }
    return canvas.toDataURL('image/png').split(',')[1];
  }, {width, height, barrier});
  await workspace.locator('input[type="file"]').setInputFiles({name: 'paint-fixture.png', mimeType: 'image/png', buffer: Buffer.from(base64, 'base64')});
  await expect(page.locator('.canvas-status')).toContainText('原图已载入');
  await expect(page.getByLabel('图层名称')).toHaveValue('原图');
  await paintedFrame(page);
  const box = await page.locator('.canvas-viewport').boundingBox();
  expect(box).not.toBeNull();
  const zoom = Math.min((box!.width - 72) / width, (box!.height - 72) / height, 32);
  return (x: number, y: number): Point => ({x: box!.x + (box!.width - width * zoom) / 2 + x * zoom, y: box!.y + (box!.height - height * zoom) / 2 + y * zoom});
}

async function paintedFrame(page: Page) {
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
}

async function setOption(page: Page, selector: string, value: string) {
  await page.locator('.tool-options').locator(selector).evaluate((element, value) => {
    (element as HTMLInputElement).value = value;
    element.dispatchEvent(new Event('input', {bubbles: true}));
    element.dispatchEvent(new Event('change', {bubbles: true}));
  }, value);
}

async function brush(page: Page, color: string, size = 12) {
  await page.getByRole('button', {name: '画笔 (B)', exact: true}).click();
  await setOption(page, 'input[type="color"]', color);
  await setOption(page, 'input[type="range"]', String(size));
}

async function erase(page: Page, size = 16) {
  await page.getByRole('button', {name: '橡皮擦 (E)', exact: true}).click();
  await setOption(page, 'input[type="range"]', String(size));
}

async function drag(page: Page, start: Point, end: Point, steps = 2) {
  await page.mouse.move(start.x, start.y); await page.mouse.down();
  await page.mouse.move(end.x, end.y, {steps}); await page.mouse.up();
}

/** Validate the exported bitmap, including alpha, instead of sampling selection/transform UI overlays. */
async function exportedPixels(page: Page, points: Point[]) {
  const downloadPromise = page.waitForEvent('download');
  await page.getByRole('button', {name: 'PNG', exact: true}).click();
  const download = await downloadPromise, path = await download.path();
  expect(path).not.toBeNull();
  const base64 = (await readFile(path!)).toString('base64');
  return page.evaluate(async ({base64, points}) => {
    const image = new Image(); image.src = 'data:image/png;base64,' + base64; await image.decode();
    const canvas = document.createElement('canvas'); canvas.width = image.width; canvas.height = image.height;
    const context = canvas.getContext('2d')!; context.drawImage(image, 0, 0);
    const pixels = points.map(point => [...context.getImageData(point.x, point.y, 1, 1).data]);
    return {width: image.width, height: image.height, pixels};
  }, {base64, points});
}

test('paints continuous colored strokes, erases to transparency, and restores both through undo/redo', async ({page}) => {
  const at = await openFixture(page);
  await expect(page.getByRole('navigation', {name: '画布工具'}).getByRole('button', {name: /铅笔/})).toHaveCount(0);
  await page.getByRole('button', {name: '快捷键帮助', exact: true}).click();
  await expect(page.getByRole('dialog', {name: '画布快捷键帮助'})).not.toContainText('铅笔');
  await page.getByRole('button', {name: '关闭快捷键帮助', exact: true}).click();
  await brush(page, '#12ab90');
  await drag(page, at(20, 50), at(180, 50));
  const strokePoints = [30, 50, 70, 90, 110, 130, 150, 170].map(x => ({x, y: 50}));
  const painted = await exportedPixels(page, [...strokePoints, {x: 100, y: 20}]);
  expect(painted).toMatchObject({width: 200, height: 100});
  expect(painted.pixels.slice(0, -1)).toEqual(strokePoints.map(() => [18, 171, 144, 255]));
  expect(painted.pixels.at(-1)).toEqual(white);

  await page.getByRole('button', {name: '撤销', exact: true}).click();
  expect((await exportedPixels(page, [{x: 100, y: 50}])).pixels[0]).toEqual(white);
  await page.getByRole('button', {name: '重做', exact: true}).click();
  expect((await exportedPixels(page, [{x: 100, y: 50}])).pixels[0]).toEqual([18, 171, 144, 255]);

  await erase(page);
  await drag(page, at(100, 20), at(100, 80));
  const erased = await exportedPixels(page, [{x: 100, y: 50}, {x: 60, y: 50}, {x: 100, y: 20}]);
  expect(erased.pixels[0][3]).toBe(0);
  expect(erased.pixels[1]).toEqual([18, 171, 144, 255]);
  expect(erased.pixels[2][3]).toBe(0);
  await page.getByRole('button', {name: '撤销', exact: true}).click();
  expect((await exportedPixels(page, [{x: 100, y: 50}])).pixels[0]).toEqual([18, 171, 144, 255]);
});

test('constrains brush, eraser and whole-layer filling to a retained rectangular selection', async ({page}) => {
  const at = await openFixture(page);
  await page.getByRole('button', {name: '矩形选区 (M)', exact: true}).click();
  await page.getByRole('checkbox', {name: '手动建立选区'}).check();
  await drag(page, at(60, 30), at(140, 70));
  await brush(page, '#ff0000', 16);
  await expect(page.locator('.canvas-status')).toContainText('选区');
  await drag(page, at(20, 50), at(180, 50));
  expect((await exportedPixels(page, [{x: 40, y: 50}, {x: 80, y: 50}, {x: 160, y: 50}])).pixels).toEqual([white, [255, 0, 0, 255], white]);

  await erase(page, 20);
  await drag(page, at(100, 10), at(100, 90));
  const erased = await exportedPixels(page, [{x: 100, y: 20}, {x: 100, y: 50}, {x: 100, y: 80}]);
  expect(erased.pixels[0]).toEqual(white); expect(erased.pixels[1][3]).toBe(0); expect(erased.pixels[2]).toEqual(white);

  await page.getByRole('button', {name: '油漆桶', exact: true}).click();
  await page.getByLabel('填充方式').selectOption('all');
  await setOption(page, 'input[type="color"]', '#123456');
  await page.mouse.click(at(100, 50).x, at(100, 50).y);
  expect((await exportedPixels(page, [{x: 20, y: 50}, {x: 80, y: 50}, {x: 100, y: 50}, {x: 180, y: 50}])).pixels).toEqual([white, [18, 52, 86, 255], [18, 52, 86, 255], white]);
});

test('distinguishes contiguous region filling from filling the complete layer', async ({page}) => {
  const at = await openFixture(page, 200, 100, true);
  await page.getByRole('button', {name: '油漆桶', exact: true}).click();
  await expect(page.getByLabel('填充方式')).toHaveValue('region');
  await setOption(page, 'input[type="color"]', '#ff0000');
  await page.mouse.click(at(40, 50).x, at(40, 50).y);
  const points = [{x: 40, y: 50}, {x: 100, y: 50}, {x: 160, y: 50}];
  expect((await exportedPixels(page, points)).pixels).toEqual([[255, 0, 0, 255], [0, 0, 0, 255], white]);
  await page.getByLabel('填充方式').selectOption({label: '全部填充'});
  await setOption(page, 'input[type="color"]', '#0000ff');
  await page.mouse.click(at(40, 50).x, at(40, 50).y);
  expect((await exportedPixels(page, points)).pixels).toEqual(points.map(() => [0, 0, 255, 255]));
  await page.getByRole('button', {name: '撤销', exact: true}).click();
  expect((await exportedPixels(page, points)).pixels).toEqual([[255, 0, 0, 255], [0, 0, 0, 255], white]);
});

test('avoids bitmap readbacks, bitmap clones and PNG encoding during a 2048px paint or erase gesture', async ({page}) => {
  const at = await openFixture(page, 2048, 2048);
  await brush(page, '#12ab90', 80);
  await paintedFrame(page);
  await page.evaluate(async () => {
    const metrics = {readbacks: 0, encodes: 0, bitmapReads: 0};
    Object.assign(window, {__canvasPaintMetrics: metrics});
    const originalRead = CanvasRenderingContext2D.prototype.getImageData;
    CanvasRenderingContext2D.prototype.getImageData = function (...args: Parameters<CanvasRenderingContext2D['getImageData']>) {
      if (this.canvas.width === 2048 && this.canvas.height === 2048) metrics.readbacks++;
      return originalRead.apply(this, args);
    };
    const originalEncode = HTMLCanvasElement.prototype.toDataURL;
    HTMLCanvasElement.prototype.toDataURL = function (...args: Parameters<HTMLCanvasElement['toDataURL']>) { metrics.encodes++; return originalEncode.apply(this, args); };
    const modulePath = '/src/canvas/model/bitmap-store.ts';
    const {BitmapStore} = await import(modulePath), originalBitmapRead = BitmapStore.prototype.getImageData;
    BitmapStore.prototype.getImageData = function (id: string) { metrics.bitmapReads++; return originalBitmapRead.call(this, id); };
  });
  const counts = () => page.evaluate(() => ({...(window as unknown as {__canvasPaintMetrics: {readbacks: number; encodes: number; bitmapReads: number}}).__canvasPaintMetrics}));
  for (const tool of ['brush', 'erase']) {
    if (tool === 'erase') await erase(page, 80);
    await page.mouse.move(at(300, 1024).x, at(300, 1024).y); await page.mouse.down();
    await paintedFrame(page);
    const beforeMoves = await counts();
    await page.mouse.move(at(1748, 1024).x, at(1748, 1024).y, {steps: 60});
    await paintedFrame(page);
    expect(await counts()).toEqual(beforeMoves);
    await page.mouse.up();
    await paintedFrame(page);
    expect((await counts()).readbacks - beforeMoves.readbacks).toBe(1);
    expect((await counts()).encodes - beforeMoves.encodes).toBeLessThanOrEqual(1);
  }
});
