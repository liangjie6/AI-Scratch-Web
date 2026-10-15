import {expect, test, type Locator, type Page} from '@playwright/test';

type Point = {x: number; y: number};
async function nextRender(page: Page) {
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
}
async function openFixture(page: Page) {
  await page.setViewportSize({width: 1280, height: 900});
  await page.goto('/', {waitUntil: 'domcontentloaded'});
  await expect(page.locator('.blocklySvg')).toBeVisible({timeout: 30_000});
  await page.getByRole('button', {name: '打开画布', exact: true}).click();
  await expect(page.getByRole('dialog', {name: '无边画布编辑器'})).toBeVisible();
  const base64 = await page.evaluate(() => {
    const image = document.createElement('canvas'); image.width = 200; image.height = 100;
    const context = image.getContext('2d')!;
    context.fillStyle = '#275db4'; context.fillRect(0, 0, 200, 100);
    return image.toDataURL('image/png').split(',')[1]!;
  });
  await page.locator('.canvas-commandbar input[type="file"]').setInputFiles({name: 'interaction-fixture.png', mimeType: 'image/png', buffer: Buffer.from(base64, 'base64')});
  await expect(page.locator('.canvas-status')).toContainText('原图已载入');
  await page.getByRole('button', {name: '适应内容', exact: true}).click();
  await nextRender(page);
  const box = await page.locator('.canvas-viewport canvas').boundingBox();
  expect(box).not.toBeNull();
  const zoom = Math.min((box!.width - 72) / 200, (box!.height - 72) / 100);
  const origin = {x: box!.x + (box!.width - 200 * zoom) / 2, y: box!.y + (box!.height - 100 * zoom) / 2};
  return (point: Point): Point => ({x: origin.x + point.x * zoom, y: origin.y + point.y * zoom});
}
function field(page: Page, name: 'X' | 'Y' | '宽' | '高') {
  return page.locator('.tool-options .transform-grid label').filter({hasText: new RegExp(`^${name}$`)}).locator('input');
}
async function expectNumber(input: Locator, value: number) {
  await expect.poll(async () => Number(await input.inputValue())).toBeCloseTo(value, 4);
}
async function setNumber(input: Locator, value: number) {
  await input.fill(String(value)); await input.press('Tab');
}
async function drag(page: Page, from: Point, to: Point, shift = false) {
  if (shift) await page.keyboard.down('Shift');
  await page.mouse.move(from.x, from.y); await page.mouse.down();
  await page.mouse.move(to.x, to.y, {steps: 8}); await page.mouse.up();
  if (shift) await page.keyboard.up('Shift');
}
async function guidePixelCount(page: Page) {
  return page.locator('.canvas-viewport canvas').evaluate(element => {
    const canvas = element as HTMLCanvasElement;
    const pixels = canvas.getContext('2d')!.getImageData(0, 0, canvas.width, canvas.height).data;
    let count = 0;
    for (let index = 0; index < pixels.length; index += 4) {
      // A one-pixel guide between pixel centers blends with the gray canvas.
      if (pixels[index]! > 120 && pixels[index + 2]! > 80 && pixels[index]! > pixels[index + 1]! + 50 && pixels[index]! > pixels[index + 2]! + 20) count++;
    }
    return count;
  });
}

test('locks transform proportions by default and temporarily releases them with Shift', async ({page}) => {
  const screen = await openFixture(page);
  await page.getByRole('button', {name: '变换 (T)', exact: true}).click();
  await expectNumber(field(page, '宽'), 200); await expectNumber(field(page, '高'), 100);
  await drag(page, screen({x: 200, y: 100}), screen({x: 150, y: 95}));
  await expectNumber(field(page, '宽'), 150); await expectNumber(field(page, '高'), 75);
  await expectNumber(field(page, 'X'), 0); await expectNumber(field(page, 'Y'), 0);
  await page.getByRole('button', {name: '撤销', exact: true}).click();
  await expectNumber(field(page, '宽'), 200);
  await drag(page, screen({x: 200, y: 100}), screen({x: 150, y: 95}), true);
  await expectNumber(field(page, '宽'), 150); await expectNumber(field(page, '高'), 95);
});

test('uses the proportion toggle for both dragging and numeric transform fields', async ({page}) => {
  const screen = await openFixture(page);
  await page.getByRole('button', {name: '变换 (T)', exact: true}).click();
  const lock = page.getByRole('button', {name: '锁定比例', exact: true});
  await expect(lock).toHaveAttribute('aria-pressed', 'true'); await lock.click();
  await expect(lock).toHaveAttribute('aria-pressed', 'false');
  const east = screen({x: 200, y: 50});
  await page.mouse.click(east.x - 9, east.y);
  await expectNumber(field(page, '宽'), 200); await expectNumber(field(page, '高'), 100);
  await drag(page, screen({x: 200, y: 100}), screen({x: 150, y: 95}));
  await expectNumber(field(page, '宽'), 150); await expectNumber(field(page, '高'), 95);
  await page.getByRole('button', {name: '撤销', exact: true}).click(); await expectNumber(field(page, '宽'), 200);
  await setNumber(field(page, '宽'), 120); await expectNumber(field(page, '高'), 100);
  await lock.click(); await setNumber(field(page, '宽'), 180); await expectNumber(field(page, '高'), 150);
});

test('snaps translated layers and transform handles and clears guides after release', async ({page}) => {
  const screen = await openFixture(page);
  await page.getByRole('button', {name: '变换 (T)', exact: true}).click();
  await setNumber(field(page, 'X'), 20); await setNumber(field(page, 'Y'), 20);
  const from = screen({x: 120, y: 70}), to = screen({x: 100.7, y: 50.7});
  await page.mouse.move(from.x, from.y); await page.mouse.down(); await page.mouse.move(to.x, to.y, {steps: 8});
  await nextRender(page); expect(await guidePixelCount(page)).toBeGreaterThan(10); await page.mouse.up();
  await expectNumber(field(page, 'X'), 0); await expectNumber(field(page, 'Y'), 0);
  await nextRender(page); expect(await guidePixelCount(page)).toBe(0);
  await setNumber(field(page, '宽'), 100); await expectNumber(field(page, '高'), 50);
  await drag(page, screen({x: 100, y: 50}), screen({x: 199.3, y: 99.3}));
  await expectNumber(field(page, '宽'), 200); await expectNumber(field(page, '高'), 100);
  await expectNumber(field(page, 'X'), 0); await expectNumber(field(page, 'Y'), 0);
});

test('moves and resizes a selection frame and retains the source layer after copying', async ({page}) => {
  const screen = await openFixture(page);
  await page.getByRole('button', {name: '矩形选区 (M)', exact: true}).click();
  await drag(page, screen({x: 20, y: 20}), screen({x: 80, y: 60}));
  await expect(page.locator('.canvas-status')).toContainText('选区 60 × 40');
  await drag(page, screen({x: 50, y: 40}), screen({x: 70, y: 50}));
  await expect(page.locator('.canvas-status')).toContainText('选区 60 × 40');
  await drag(page, screen({x: 100, y: 70}), screen({x: 120, y: 80}));
  await expect(page.locator('.canvas-status')).toContainText('选区 80 × 50');
  await page.getByRole('button', {name: '复制为新图层', exact: true}).click();
  await expect(page.locator('.layer-row')).toHaveCount(2);
  await expect(page.locator('.layer-row.is-active input')).toHaveValue('原图');
  await page.locator('.layer-row').first().locator('.layer-row__thumb').click();
  await page.getByRole('button', {name: '变换 (T)', exact: true}).click();
  await expectNumber(field(page, 'X'), 40); await expectNumber(field(page, 'Y'), 30);
  await expectNumber(field(page, '宽'), 80); await expectNumber(field(page, '高'), 50);
});

test('snaps crop frame translation and corner resizing before confirming the crop', async ({page}) => {
  const screen = await openFixture(page);
  await page.getByRole('button', {name: '裁剪 (C)', exact: true}).click();
  await setNumber(field(page, 'X'), 20); await setNumber(field(page, 'Y'), 20);
  await setNumber(field(page, '宽'), 120); await setNumber(field(page, '高'), 60);
  await drag(page, screen({x: 80, y: 50}), screen({x: 60.7, y: 30.7}));
  await expectNumber(field(page, 'X'), 0); await expectNumber(field(page, 'Y'), 0);
  await expectNumber(field(page, '宽'), 120); await expectNumber(field(page, '高'), 60);
  await drag(page, screen({x: 120, y: 60}), screen({x: 199.3, y: 99.3}));
  await expectNumber(field(page, '宽'), 200); await expectNumber(field(page, '高'), 100);
  await page.locator('.canvas-viewport').focus(); await page.keyboard.press('Enter');
  await expect(page.locator('.canvas-viewport')).toHaveAttribute('data-tool', 'move');
  await expect(page.locator('.canvas-status')).toContainText('确认裁剪');
  await expect(page.locator('.canvas-status')).toContainText('当前图层 200 × 100');
});

test('keeps the copied selection origin fixed when editing numeric dimensions', async ({page}) => {
  const screen = await openFixture(page);
  await page.getByRole('button', {name: '矩形选区 (M)', exact: true}).click();
  await drag(page, screen({x: 40, y: 30}), screen({x: 120, y: 80}));
  await page.getByRole('button', {name: '复制为新图层', exact: true}).click();
  await page.locator('.layer-row').first().locator('.layer-row__thumb').click();
  await page.getByRole('button', {name: '变换 (T)', exact: true}).click();
  await expectNumber(field(page, 'X'), 40); await expectNumber(field(page, 'Y'), 30);
  await setNumber(field(page, '宽'), 160);
  await expectNumber(field(page, '宽'), 160); await expectNumber(field(page, '高'), 100);
  await expectNumber(field(page, 'X'), 40); await expectNumber(field(page, 'Y'), 30);
});

for (const event of ['pointercancel', 'lostpointercapture']) {
  test(`finishes a transform without jumping when receiving ${event}`, async ({page}) => {
    const screen = await openFixture(page);
    await page.getByRole('button', {name: '变换 (T)', exact: true}).click();
    const from = screen({x: 100, y: 50}), to = screen({x: 120, y: 70});
    await page.mouse.move(from.x, from.y); await page.mouse.down(); await page.mouse.move(to.x, to.y, {steps: 8});
    await expectNumber(field(page, 'X'), 20); await expectNumber(field(page, 'Y'), 20);
    await page.locator('.canvas-viewport').dispatchEvent(event, {pointerId: 1, clientX: 0, clientY: 0});
    await page.mouse.up();
    await expectNumber(field(page, 'X'), 20); await expectNumber(field(page, 'Y'), 20);
    const undo = page.getByRole('button', {name: '撤销', exact: true});
    await expect(undo).toBeEnabled(); await undo.click();
    await expectNumber(field(page, 'X'), 0); await expectNumber(field(page, 'Y'), 0);
  });
}

test('cancels transform and selection gestures with Escape without adding transform history', async ({page}) => {
  const screen = await openFixture(page);
  await page.getByRole('button', {name: '变换 (T)', exact: true}).click();
  const corner = screen({x: 200, y: 100}), resized = screen({x: 150, y: 95});
  await page.mouse.move(corner.x, corner.y); await page.mouse.down(); await page.mouse.move(resized.x, resized.y, {steps: 8});
  await expectNumber(field(page, '宽'), 150); await page.keyboard.press('Escape'); await page.mouse.up();
  await expectNumber(field(page, '宽'), 200); await expectNumber(field(page, '高'), 100);
  await expectNumber(field(page, 'X'), 0); await expectNumber(field(page, 'Y'), 0);
  await expect(page.getByRole('button', {name: '撤销', exact: true})).toBeDisabled();
  await page.getByRole('button', {name: '矩形选区 (M)', exact: true}).click();
  await drag(page, screen({x: 20, y: 20}), screen({x: 80, y: 60}));
  const center = screen({x: 50, y: 40}), moved = screen({x: 70, y: 50});
  await page.mouse.move(center.x, center.y); await page.mouse.down(); await page.mouse.move(moved.x, moved.y, {steps: 8});
  await page.keyboard.press('Escape'); await page.mouse.up();
  await expect(page.getByRole('dialog', {name: '无边画布编辑器'})).toBeVisible();
  await page.getByRole('button', {name: '复制为新图层', exact: true}).click();
  await page.locator('.layer-row').first().locator('.layer-row__thumb').click();
  await page.getByRole('button', {name: '变换 (T)', exact: true}).click();
  await expectNumber(field(page, 'X'), 20); await expectNumber(field(page, 'Y'), 20);
  await expectNumber(field(page, '宽'), 60); await expectNumber(field(page, '高'), 40);
});
