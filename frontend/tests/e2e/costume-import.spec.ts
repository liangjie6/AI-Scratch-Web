import {expect, test} from '@playwright/test';

test('retains the top, bottom and transparency of a tall costume in Scratch paint', async ({page}) => {
  await page.goto('/', {waitUntil: 'domcontentloaded'});
  await expect(page.getByRole('button', {name: '角色1'})).toBeVisible({timeout: 30_000});

  const result = await page.evaluate(async () => {
    // Use the application import boundary with a real browser canvas and PNG.
    const modulePath = '/src/scratch/asset-adapter.ts';
    const {buildSpriteImport} = await import(/* @vite-ignore */ modulePath);
    const source = document.createElement('canvas');
    source.width = 400;
    source.height = 2000;
    const context = source.getContext('2d')!;
    context.fillStyle = '#00ff00';
    context.fillRect(100, 0, 200, 2000);
    context.fillStyle = '#ff0000';
    context.fillRect(100, 0, 200, 100);
    context.fillStyle = '#0000ff';
    context.fillRect(100, 1900, 200, 100);
    const blob = await new Promise<Blob>(resolve => source.toBlob(value => resolve(value!), 'image/png'));
    const built = await buildSpriteImport({name: '长角色测试', initialSize: 36, costumes: [{name: '完整长角色', blob, dataFormat: 'png', assetWidth: 400, assetHeight: 2000, rotationCenterX: 200, rotationCenterY: 1000, bitmapResolution: 2}]});
    const costume = built.spriteJson.costumes[0];
    const bitmap = await createImageBitmap(new Blob([new Uint8Array(costume.asset.data)], {type: 'image/png'}));
    // Scratch paint places the bitmap relative to its pivot on a fixed canvas.
    const paintCanvas = document.createElement('canvas');
    paintCanvas.width = 960;
    paintCanvas.height = 720;
    const paint = paintCanvas.getContext('2d')!;
    paint.drawImage(bitmap, 480 - costume.rotationCenterX, 360 - costume.rotationCenterY);
    bitmap.close();
    const pixels = paint.getImageData(0, 0, 960, 720).data;
    let red = 0;
    let blue = 0;
    for (let index = 0; index < pixels.length; index += 4) {
      if (pixels[index]! > 240 && pixels[index + 1]! < 10 && pixels[index + 2]! < 10 && pixels[index + 3]! > 240) red += 1;
      if (pixels[index]! < 10 && pixels[index + 1]! < 10 && pixels[index + 2]! > 240 && pixels[index + 3]! > 240) blue += 1;
    }
    const host = document.querySelector('.scratch-host') as HTMLElement & {__vueParentComponent: {exposed: {bridge: {commitSpriteImport: (...args: unknown[]) => Promise<unknown>}}}};
    await host.__vueParentComponent.exposed.bridge.commitSpriteImport(built.spriteJson, built.expectations, built.soundExpectations);
    return {red, blue, transparentCorner: pixels[3], initialSize: built.spriteJson.size};
  });

  // Both markers were outside the paint canvas before normalization.
  expect(result.red).toBeGreaterThan(100);
  expect(result.blue).toBeGreaterThan(100);
  expect(result.transparentCorner).toBe(0);
  expect(result.initialSize).toBe(100);
  await page.getByRole('tab', {name: '造型'}).click();
  const paperCanvas = page.locator('canvas[class*="paper-canvas_paper-canvas"]');
  await expect(paperCanvas).toBeVisible();
  await expect.poll(() => paperCanvas.evaluate(element => {
    const canvas = element as HTMLCanvasElement;
    const data = canvas.getContext('2d')!.getImageData(0, 0, canvas.width, canvas.height).data;
    let red = 0;
    let blue = 0;
    for (let index = 0; index < data.length; index += 4) {
      if (data[index]! > 240 && data[index + 1]! < 10 && data[index + 2]! < 10) red += 1;
      if (data[index]! < 10 && data[index + 1]! < 10 && data[index + 2]! > 240) blue += 1;
    }
    return red > 10 && blue > 10;
  })).toBe(true);
});
