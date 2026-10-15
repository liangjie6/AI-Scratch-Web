import {expect, test, type Locator, type Page} from '@playwright/test';

const colors = [[32, 96, 176, 255], [110, 145, 190, 255], [220, 40, 40, 255], [255, 255, 255, 255]];

async function openEmptyCanvas(page: Page) {
  await page.setViewportSize({width: 1440, height: 900});
  await page.goto('/', {waitUntil: 'domcontentloaded'});
  await expect(page.locator('.blocklySvg')).toBeVisible({timeout: 45_000});
  await page.getByRole('button', {name: '打开画布', exact: true}).click();
  await expect(page.getByRole('dialog', {name: '无边画布编辑器', exact: true})).toBeVisible();
  await expect(page.getByText('导入一张角色素材', {exact: true})).toBeVisible();
}

async function makeFixture(page: Page, solid = false) {
  return page.evaluate(solidColor => {
    const canvas = document.createElement('canvas');
    canvas.width = 200;
    canvas.height = 100;
    const context = canvas.getContext('2d')!;
    const shades = solidColor ? ['#20b050', '#20b050', '#20b050', '#20b050'] : ['#2060b0', '#6e91be', '#dc2828', '#ffffff'];
    shades.forEach((color, index) => {context.fillStyle = color; context.fillRect(index * 50, 0, 50, 100);});
    return canvas.toDataURL('image/png').split(',')[1];
  }, solid);
}

async function uploadFixture(page: Page) {
  await page.locator('.canvas-workspace input[type="file"]').setInputFiles({
    name: 'multicolor.png', mimeType: 'image/png', buffer: Buffer.from(await makeFixture(page), 'base64')
  });
  await expect(page.locator('.layer-row')).toHaveCount(1);
  await expect(page.locator('.canvas-status')).toContainText('当前图层 200 × 100');
}

function rowByName(page: Page, name: string) {
  return page.locator('.layer-row').filter({has: page.locator(`input[value="${name}"]`)});
}

async function previewPixels(row: Locator) {
  return row.locator('.layer-row__thumb img').evaluate(async element => {
    const image = new Image();
    image.src = (element as HTMLImageElement).src;
    await image.decode();
    const canvas = document.createElement('canvas');
    canvas.width = image.naturalWidth;
    canvas.height = image.naturalHeight;
    const context = canvas.getContext('2d')!;
    context.drawImage(image, 0, 0);
    return [0.125, 0.375, 0.625, 0.875].map(x => Array.from(context.getImageData(Math.floor(x * canvas.width), Math.floor(canvas.height / 2), 1, 1).data));
  });
}

async function worldPoint(page: Page, x: number, y: number) {
  const rect = await page.locator('.canvas-viewport canvas').boundingBox();
  expect(rect).not.toBeNull();
  const zoom = Math.min((rect!.width - 72) / 200, (rect!.height - 72) / 100);
  return {x: rect!.x + rect!.width / 2 + (x - 100) * zoom, y: rect!.y + rect!.height / 2 + (y - 50) * zoom};
}

async function dragWorld(page: Page, from: {x: number; y: number}, to: {x: number; y: number}) {
  const start = await worldPoint(page, from.x, from.y), end = await worldPoint(page, to.x, to.y);
  await page.mouse.move(start.x, start.y);
  await page.mouse.down();
  await page.mouse.move(end.x, end.y, {steps: 6});
  await page.mouse.up();
}

async function screenPixel(page: Page, x: number, y: number) {
  const point = await worldPoint(page, x, y);
  return page.locator('.canvas-viewport canvas').evaluate((element, position) => {
    const canvas = element as HTMLCanvasElement, rect = canvas.getBoundingClientRect();
    return Array.from(canvas.getContext('2d')!.getImageData(Math.floor((position.x - rect.x) * canvas.width / rect.width), Math.floor((position.y - rect.y) * canvas.height / rect.height), 1, 1).data);
  }, point);
}

async function setRange(input: Locator, value: number, commit = true) {
  await input.evaluate((element, settings) => {
    (element as HTMLInputElement).value = String(settings.value);
    element.dispatchEvent(new Event('input', {bubbles: true}));
    if (settings.commit) element.dispatchEvent(new Event('change', {bubbles: true}));
  }, {value, commit});
}

test('imports library assets into an empty canvas and appends without replacing existing pixels', async ({page}) => {
  await openEmptyCanvas(page);
  const multicolor = await makeFixture(page), solid = await makeFixture(page, true);
  await page.evaluate(async images => {
    const database = await new Promise<IDBDatabase>((resolve, reject) => {
      const request = indexedDB.open('ai-scratch-assets', 3);
      request.onupgradeneeded = () => {
        if (!request.result.objectStoreNames.contains('generatedAssets')) request.result.createObjectStore('generatedAssets', {keyPath: 'id'});
      };
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
    await new Promise<void>((resolve, reject) => {
      const transaction = database.transaction('generatedAssets', 'readwrite');
      images.forEach((image, index) => transaction.objectStore('generatedAssets').put({
        schemaVersion: 2, id: `canvas-library-e2e-${index}`, origin: 'imported', attachment: 'unattached', tags: ['e2e'],
        createdAt: Date.now() + index, name: index ? '追加绿图' : '测试四色图', prompt: '已知 200 × 100 像素素材', style: 'pixel-art', target: 'sprite',
        result: {request_id: `canvas-library-e2e-${index}`, image_base64: image, mime_type: 'image/png', target: 'sprite', sheet_width: 200, sheet_height: 100, sheet_layout: {columns: 1, rows: 1}, requested_canvas: {width: 200, height: 100}}
      }));
      transaction.oncomplete = () => {database.close(); resolve();};
      transaction.onerror = () => reject(transaction.error);
    });
  }, [multicolor, solid]);

  await page.getByRole('button', {name: '从素材库导入', exact: true}).click();
  const picker = page.getByRole('dialog', {name: '从素材库导入', exact: true});
  await picker.getByRole('button', {name: '导入素材 测试四色图', exact: true}).click();
  await expect(picker).toHaveCount(0);
  const source = rowByName(page, '测试四色图');
  await expect(source).toHaveAttribute('aria-selected', 'true');
  await expect.poll(() => previewPixels(source)).toEqual(colors);
  const sourcePreview = await source.locator('img').getAttribute('src');
  const documentName = await page.getByLabel('文档名称').inputValue();

  await page.getByRole('button', {name: '从素材库导入', exact: true}).click();
  await picker.getByRole('button', {name: '导入素材 追加绿图', exact: true}).click();
  await expect(page.locator('.layer-row')).toHaveCount(2);
  await expect(rowByName(page, '追加绿图')).toHaveAttribute('aria-selected', 'true');
  await expect(source.locator('img')).toHaveAttribute('src', sourcePreview!);
  await expect(page.getByLabel('文档名称')).toHaveValue(documentName);
  await expect.poll(() => screenPixel(page, 25, 50)).toEqual([32, 176, 80, 255]);

  await page.getByRole('button', {name: '撤销', exact: true}).click();
  await expect(page.locator('.layer-row')).toHaveCount(1);
  await expect(source).toHaveAttribute('aria-selected', 'true');
  await expect.poll(() => previewPixels(source)).toEqual(colors);
  await expect.poll(() => screenPixel(page, 25, 50)).toEqual(colors[0]);
  await page.getByRole('button', {name: '重做', exact: true}).click();
  await expect(page.locator('.layer-row')).toHaveCount(2);
  await expect.poll(() => screenPixel(page, 25, 50)).toEqual([32, 176, 80, 255]);
});

test('keeps the source selected when copying selections from different parts of the image', async ({page}) => {
  await openEmptyCanvas(page);
  await uploadFixture(page);
  await page.getByRole('button', {name: '矩形选区 (M)', exact: true}).click();
  await dragWorld(page, {x: 10, y: 20}, {x: 40, y: 80});
  await page.getByRole('button', {name: '复制为新图层', exact: true}).click();
  await expect(page.locator('.layer-row')).toHaveCount(2);
  await expect(rowByName(page, '原图')).toHaveAttribute('aria-selected', 'true');
  await expect.poll(() => previewPixels(rowByName(page, '造型1'))).toEqual(Array(4).fill(colors[0]));

  // Moving the existing selection to a disjoint red region catches accidental copying from the prior derived layer.
  await dragWorld(page, {x: 25, y: 50}, {x: 125, y: 50});
  await page.getByRole('button', {name: '复制为新图层', exact: true}).click();
  await expect(page.locator('.layer-row')).toHaveCount(3);
  await expect(rowByName(page, '原图')).toHaveAttribute('aria-selected', 'true');
  await expect.poll(() => previewPixels(rowByName(page, '造型2'))).toEqual(Array(4).fill(colors[2]));
  await expect.poll(() => previewPixels(rowByName(page, '原图'))).toEqual(colors);
});

test('previews chroma slider input immediately and restores its mask through undo and redo', async ({page}) => {
  await openEmptyCanvas(page);
  await uploadFixture(page);
  await page.getByRole('button', {name: '色度抠图 (K)', exact: true}).click();
  const tolerance = page.locator('.tool-options label').filter({hasText: '容差'}).locator('input');
  await setRange(tolerance, 0);
  await setRange(page.locator('.tool-options label').filter({hasText: '柔化'}).locator('input'), 0);
  await page.locator('.tool-options select').selectOption('global');
  const point = await worldPoint(page, 25, 50);
  await page.mouse.click(point.x, point.y);
  await expect(page.locator('.canvas-status')).toContainText('保存色度抠图');
  const source = rowByName(page, '原图');
  const alphas = async () => (await previewPixels(source)).map(pixel => pixel[3]);
  await expect.poll(alphas).toEqual([0, 255, 255, 255]);
  await expect.poll(() => screenPixel(page, 75, 50)).toEqual(colors[1]);

  // Input alone must update both the actual rendered canvas and the effective layer pixels before change commits history.
  await setRange(tolerance, 40, false);
  await expect.poll(alphas).toEqual([0, 0, 255, 255]);
  await expect.poll(() => screenPixel(page, 75, 50)).not.toEqual(colors[1]);
  await tolerance.dispatchEvent('change');
  await page.getByRole('button', {name: '撤销', exact: true}).click();
  await expect(tolerance).toHaveValue('0');
  await expect.poll(alphas).toEqual([0, 255, 255, 255]);
  await page.getByRole('button', {name: '重做', exact: true}).click();
  await expect(tolerance).toHaveValue('40');
  await expect.poll(alphas).toEqual([0, 0, 255, 255]);
  await setRange(tolerance, 100);
  await expect.poll(alphas).toEqual([0, 0, 0, 0]);
});
