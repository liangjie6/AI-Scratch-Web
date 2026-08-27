import {expect, test} from '@playwright/test';

const ONE_PIXEL_PNG = 'iVBORw0KGgoAAAANSUhEUgAAAAQAAAAECAYAAACp8Z5+AAAAJElEQVR4nGP8z8Dwn4GBgYERTDIwMEEoBGBhhChg+M/AiF0FAPYWBQjY2fw+AAAAAElFTkSuQmCC';

test('offers explicit asset and Scratch import targets without restoring canvas state', async ({page}) => {
  await page.goto('/');
  await page.evaluate(async imageBase64 => {
    await new Promise<void>((resolve, reject) => {
      const request = indexedDB.open('ai-scratch-assets', 2);
      request.onupgradeneeded = () => request.result.createObjectStore('generatedAssets', {keyPath: 'id'});
      request.onerror = () => reject(request.error);
      request.onsuccess = () => {
        const transaction = request.result.transaction('generatedAssets', 'readwrite');
        transaction.objectStore('generatedAssets').put({
          schemaVersion: 2,
           origin: 'ai_generated',
           tags: ['e2e'],
          id: 'e2e-asset',
          createdAt: Date.now(),
          prompt: '测试素材',
          style: 'pixel-art',
          target: 'sprite',
          result: {
            request_id: 'e2e-asset',
            image_base64: imageBase64,
            mime_type: 'image/png',
            target: 'sprite',
            sheet_width: 1,
            sheet_height: 1,
            sheet_layout: {columns: 1, rows: 1},
            requested_canvas: {width: 480, height: 360}
          }
        });
        transaction.oncomplete = () => resolve();
        transaction.onerror = () => reject(transaction.error);
      };
    });
  }, ONE_PIXEL_PNG);
  await page.reload();

  const libraryButton = page.getByRole('button', {name: /打开素材库/});
  await expect(libraryButton).toBeVisible({timeout: 45_000});
  await libraryButton.click();
  await expect(page.getByRole('button', {name: '导入为角色'})).toBeVisible();
  await expect(page.getByRole('button', {name: '导入为背景'})).toBeVisible();
  const importCanvas = page.getByRole('button', {name: '导入到画布'});
  await expect(importCanvas).toBeVisible();
  await importCanvas.click();

  await expect(page.getByRole('dialog', {name: '无边画布编辑器'})).toBeVisible();
  await expect(page.getByRole('tab', {name: /造型 1/})).toBeVisible();
  const preview = page.locator('.layer-row__thumb img');
  await expect(preview).toHaveAttribute('src', /^data:image\/png;base64,/);
  await expect(page.getByRole('button', {name: '导入 Scratch'})).toBeEnabled();
  await page.getByRole('button', {name: '导入 Scratch'}).click();
  const targetDialog = page.getByRole('dialog', {name: '导入 Scratch'});
  await expect(targetDialog.getByRole('button', {name: /导入为角色/})).toBeVisible();
  const importBackdrop = targetDialog.getByRole('button', {name: /导入为背景/});
  await expect(importBackdrop).toBeVisible();
  await importBackdrop.click();
  await expect(page.getByRole('dialog', {name: '无边画布编辑器'})).toHaveCount(0);

  await page.getByRole('button', {name: '打开画布'}).click();
  await expect(page.getByText('导入一张角色素材')).toBeVisible();
});
