import {expect, test} from '@playwright/test';

test('discards a closed canvas import before it can overwrite the current recovery document', async ({page}) => {
  await page.addInitScript(() => {
    const state = window as typeof window & {
      releaseClosedCanvasDecode?: () => void;
      closedCanvasDecodeFinished?: boolean;
    };
    const original = window.createImageBitmap;
    let calls = 0;
    window.createImageBitmap = (async (...args: Parameters<typeof createImageBitmap>) => {
      const first = ++calls === 1;
      if (first) await new Promise<void>(resolve => {state.releaseClosedCanvasDecode = resolve;});
      const bitmap = await Reflect.apply(original, window, args) as ImageBitmap;
      if (first) state.closedCanvasDecodeFinished = true;
      return bitmap;
    }) as typeof createImageBitmap;
  });
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto('/', {waitUntil: 'domcontentloaded'});
  await expect(page.locator('.blocklySvg')).toBeVisible({timeout: 45_000});
  const openCanvas = async () => {
    await page.getByRole('button', {name: '打开画布', exact: true}).click();
    await expect(page.getByText('导入一张角色素材', {exact: true})).toBeVisible();
    await expect(page.getByRole('button', {name: '从素材库导入', exact: true})).toBeEnabled();
  };
  const images = await page.evaluate(() => ['#dc2828', '#20b050'].map(color => {
    const canvas = document.createElement('canvas');
    canvas.width = 200;
    canvas.height = 100;
    const context = canvas.getContext('2d')!;
    context.fillStyle = color;
    context.fillRect(0, 0, 200, 100);
    return canvas.toDataURL('image/png').split(',')[1];
  }));
  await page.evaluate(async image => {
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
      transaction.objectStore('generatedAssets').put({
        schemaVersion: 2, id: 'slow-initial-canvas', origin: 'imported', attachment: 'unattached', tags: ['e2e'],
        createdAt: Date.now(), name: '延迟解码素材', prompt: '关闭前尚未解码的素材', style: 'pixel-art', target: 'sprite',
        result: {request_id: 'slow-initial-canvas', image_base64: image, mime_type: 'image/png', target: 'sprite', sheet_width: 200, sheet_height: 100, sheet_layout: {columns: 1, rows: 1}, requested_canvas: {width: 200, height: 100}}
      });
      transaction.oncomplete = () => {database.close(); resolve();};
      transaction.onerror = () => reject(transaction.error);
    });
  }, images[0]);
  await page.getByRole('button', {name: /打开素材库/}).click();
  await page.getByRole('button', {name: '导入到画布', exact: true}).click();
  await page.waitForFunction(() => typeof (window as typeof window & {releaseClosedCanvasDecode?: () => void}).releaseClosedCanvasDecode === 'function');
  await page.locator('.canvas-commandbar__back').click();
  await openCanvas();
  await page.locator('.canvas-workspace input[type="file"]').setInputFiles({
    name: 'new-fast.png', mimeType: 'image/png', buffer: Buffer.from(images[1], 'base64')
  });
  await expect(page.getByLabel('文档名称')).toHaveValue('new-fast');

  const recoveryName = () => page.evaluate(async () => {
    const database = await new Promise<IDBDatabase>((resolve, reject) => {
      const request = indexedDB.open('ai-scratch-canvas', 2);
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
    try {
      const head = await new Promise<{documentId: string} | undefined>((resolve, reject) => {
        const request = database.transaction('recoveryHeads').objectStore('recoveryHeads').get('latest');
        request.onsuccess = () => resolve(request.result);
        request.onerror = () => reject(request.error);
      });
      if (!head) return null;
      return await new Promise<string>((resolve, reject) => {
        const request = database.transaction('documents').objectStore('documents').get(head.documentId);
        request.onsuccess = () => resolve(request.result?.document.name ?? null);
        request.onerror = () => reject(request.error);
      });
    } finally {database.close();}
  });
  await expect.poll(recoveryName).toBe('new-fast');
  await page.evaluate(() => (window as typeof window & {releaseClosedCanvasDecode: () => void}).releaseClosedCanvasDecode());
  await page.waitForFunction(() => (window as typeof window & {closedCanvasDecodeFinished?: boolean}).closedCanvasDecodeFinished);
  // A stale continuation previously scheduled its own 300 ms recovery write after unmount.
  await page.waitForTimeout(650);
  expect(await recoveryName()).toBe('new-fast');
  await expect(page.getByLabel('文档名称')).toHaveValue('new-fast');

  await page.locator('.canvas-commandbar__back').click();
  await page.getByRole('button', {name: '打开画布', exact: true}).click();
  await expect(page.getByLabel('文档名称')).toHaveValue('new-fast');
  await expect.poll(() => page.locator('.layer-row__thumb img').evaluate(async element => {
    const image = element as HTMLImageElement;
    await image.decode();
    const canvas = document.createElement('canvas');
    canvas.width = image.naturalWidth;
    canvas.height = image.naturalHeight;
    const context = canvas.getContext('2d')!;
    context.drawImage(image, 0, 0);
    return Array.from(context.getImageData(20, 20, 1, 1).data);
  })).toEqual([32, 176, 80, 255]);
  expect(errors).toEqual([]);
});
