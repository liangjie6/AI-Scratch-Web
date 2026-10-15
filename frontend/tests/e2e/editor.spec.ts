import {readFile} from 'node:fs/promises';
import {unzipSync} from 'fflate';
import {expect, test, type Page} from '@playwright/test';

const fixturePng = 'iVBORw0KGgoAAAANSUhEUgAAAAQAAAAECAYAAACp8Z5+AAAAJElEQVR4nGP8z8Dwn4GBgYERTDIwMEEoBGBhhChg+M/AiF0FAPYWBQjY2fw+AAAAAElFTkSuQmCC';

async function waitForEditor(page: Page) {
  await page.goto('/', {waitUntil: 'domcontentloaded'});
  await expect(page.locator('.blocklySvg')).toBeVisible({timeout: 30_000});
  await expect(page.getByRole('heading', {name: 'Scratch 编辑器'})).toHaveCount(0);
  await expect(page.getByRole('navigation', {name: '项目操作'})).toHaveCount(0);
  await expect(page.locator('canvas')).toHaveCount(3);
  await expect(page.getByRole('button', {name: '角色1'})).toBeVisible({timeout: 30_000});
}

async function openImagePanel(page: Page) {
  const expandButton = page.getByRole('button', {name: '展开生成素材'});
  if (await expandButton.count()) await expandButton.click();
  await expect(page.getByRole('heading', {name: '生成素材'})).toBeVisible();
}

async function enterPrompt(page: Page) {
  await openImagePanel(page);
  await page.getByLabel('画面描述').fill('夜晚的森林小屋，窗户透出暖黄色灯光');
}

async function importCanvasAsSprite(page: Page) {
  await page.getByRole('button', {name: '导入 Scratch'}).click();
  await page.getByRole('dialog', {name: '导入 Scratch'}).getByRole('button', {name: /导入为角色/}).click();
  await expect(page.getByRole('dialog', {name: '无边画布编辑器'})).toHaveCount(0);
}

test('loads the local editor without external requests', async ({page}) => {
  const externalRequests: string[] = [];
  const blockMediaResponses: Array<{url: string; contentType: string}> = [];
  page.on('request', request => {
    if (!request.url().startsWith('http://127.0.0.1:5173')) externalRequests.push(request.url());
  });
  page.on('response', response => {
    if (response.url().includes('/blocks-media/')) {
      blockMediaResponses.push({
        url: response.url(),
        contentType: response.headers()['content-type'] ?? ''
      });
    }
  });
  await waitForEditor(page);
  await expect(page.getByRole('button', {name: '角色1'})).toBeVisible();
  expect(externalRequests).toEqual([]);
  expect(blockMediaResponses.length).toBeGreaterThan(0);
  expect(blockMediaResponses.every(response => response.contentType.startsWith('image/'))).toBe(true);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const heightFit = await page.locator('.scratch-host__container > *').evaluate(element => {
    const rect = element.getBoundingClientRect();
    return Math.abs(rect.height - window.innerHeight) <= 2;
  });
  expect(heightFit).toBe(true);
});

test('previews and imports a Scratch sprite without external requests', async ({page}) => {
  const externalRequests: string[] = [];
  const loadedLibraryAssets: string[] = [];
  await page.route(/^https?:\/\//, async route => {
    const requestUrl = new URL(route.request().url());
    if (requestUrl.hostname === '127.0.0.1') {
      await route.continue();
      return;
    }
    externalRequests.push(requestUrl.href);
    await route.abort('blockedbyclient');
  });
  page.on('response', response => {
    if (response.url().includes('/assets/scratch/library-assets/') && response.status() === 200) {
      loadedLibraryAssets.push(response.url());
    }
  });

  await waitForEditor(page);
  await page.getByRole('button', {name: '选择一个角色'}).first().click();
  const abby = page.getByText('Abby', {exact: true}).first();
  const abbyPreview = abby.locator('xpath=ancestor::button[1]').locator('img');
  await expect(abbyPreview).toBeVisible();
  await expect.poll(() => abbyPreview.evaluate(image => (image as HTMLImageElement).naturalWidth)).toBeGreaterThan(0);
  await abby.click();

  const sprite = page.getByRole('button', {name: 'Abby'});
  await expect(sprite).toBeVisible();
  await expect(sprite.locator('img').first()).toHaveAttribute('src', /^data:image\/svg\+xml/);
  expect(loadedLibraryAssets.length).toBeGreaterThan(0);
  expect(externalRequests).toEqual([]);
});

test('deletes a selected sprite immediately without confirmation', async ({page}) => {
  await waitForEditor(page);
  await expect(page.getByRole('button', {name: '展开生成素材'})).toBeVisible();
  await page.getByRole('button', {name: '选择一个角色'}).first().click();
  await page.getByText('Abby', {exact: true}).first().click();
  const sprite = page.getByRole('button', {name: 'Abby'});
  await expect(sprite).toBeVisible();
  await sprite.click();
  await page.getByRole('button', {name: 'Delete'}).click();
  await expect(sprite).toHaveCount(0);
  await expect(page.getByText('Are you sure you want to delete this sprite?', {exact: true})).toHaveCount(0);
});

test('starts with the generation panel collapsed and keeps Scratch visible', async ({page}) => {
  await page.setViewportSize({width: 1280, height: 800});
  await waitForEditor(page);

  const expandButton = page.getByRole('button', {name: '展开生成素材'});
  await expect(expandButton).toBeVisible();
  await expect(expandButton.locator('svg.lucide-bot-icon')).toBeVisible();
  const expandButtonFitsHeader = () => page.evaluate(() => {
    const header = document.querySelector('[role="banner"]')?.getBoundingClientRect();
    const button = document.querySelector<HTMLButtonElement>('[aria-label="展开生成素材"]')?.getBoundingClientRect();
    return Boolean(header && button && button.top >= header.top && button.bottom <= header.bottom);
  });
  await expect.poll(expandButtonFitsHeader).toBe(true);
  await expect(page.locator('#image-generation-panel')).toBeHidden();
  await expect(page.locator('.workspace__ai')).toHaveCSS('visibility', 'hidden');

  const stageFitsEditor = () => page.evaluate(() => {
    const editor = document.querySelector('.workspace__editor')?.getBoundingClientRect();
    const stage = document.querySelector('canvas:not([class])')?.getBoundingClientRect();
    return Boolean(editor && stage && stage.left >= editor.left && stage.right <= editor.right);
  });
  await expect.poll(stageFitsEditor).toBe(true);

  await expandButton.click();
  await expect(page.getByRole('button', {name: '收起生成素材'})).toBeVisible();
  await expect(page.locator('#image-generation-panel')).toBeVisible();
  await expect(page.locator('.workspace__ai')).toHaveCSS('visibility', 'visible');
  await expect(page.getByRole('button', {name: '打开画布'})).toBeVisible();

  await page.getByRole('button', {name: '收起生成素材'}).click();
  await expect.poll(stageFitsEditor).toBe(true);
});

test('keeps Vue header controls stable across panel updates', async ({page}) => {
  const pageErrors: Error[] = [];
  page.on('pageerror', error => pageErrors.push(error));

  await page.setViewportSize({width: 1280, height: 800});
  await waitForEditor(page);

  const imageButton = page.getByRole('button', {name: '展开生成素材'});
  await imageButton.click();
  await expect(page.getByRole('button', {name: '收起生成素材'})).toBeVisible();

  await page.getByRole('button', {name: '打开 AI 写积木'}).click();
  await expect(page.getByRole('button', {name: '收起 AI 写积木'})).toBeVisible();
  await page.getByRole('button', {name: '收起 AI 写积木'}).click();
  await expect(page.getByRole('button', {name: '展开生成素材'})).toBeVisible();

  expect(pageErrors).toEqual([]);
});

test('keeps header tools icon-only until hover', async ({page}) => {
  await page.setViewportSize({width: 1280, height: 800});
  await waitForEditor(page);

  const tools = [
    {button: page.getByRole('button', {name: /打开素材库/}), label: '素材库'},
    {button: page.getByRole('button', {name: '展开生成素材'}), label: '生成素材'},
    {button: page.getByRole('button', {name: '打开 AI 写积木'}), label: 'AI 写积木'},
    {button: page.getByRole('button', {name: '打开画布'}), label: '无边画布'}
  ];

  for (const item of tools) {
    await expect.poll(() => item.button.evaluate(element => element.getBoundingClientRect().width)).toBeLessThanOrEqual(34);
    await item.button.hover();
    await expect.poll(() => item.button.evaluate(element => element.getBoundingClientRect().width)).toBeGreaterThan(34);
    await expect(item.button.getByText(item.label, {exact: true})).toHaveCSS('opacity', '1');
    await page.mouse.move(1, 100);
  }
});

test('keeps the creation workflow usable at 390px', async ({page}) => {
  await page.setViewportSize({width: 390, height: 844});
  await waitForEditor(page);
  await openImagePanel(page);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const editorWidth = await page.locator('.scratch-host').evaluate(element => ({
    client: element.clientWidth,
    scroll: element.scrollWidth
  }));
  expect(editorWidth.scroll).toBeGreaterThan(editorWidth.client);
  await page.screenshot({path: 'test-results/mobile-390.png', fullPage: true});
});

test('keeps Backspace editing inside the generation drawer', async ({page}) => {
  await waitForEditor(page);
  await openImagePanel(page);
  const prompt = page.getByLabel('画面描述');
  await expect(prompt).toHaveValue('');
  await prompt.fill('森林小屋');
  await prompt.press('Backspace');
  await expect(prompt).toHaveValue('森林小');
  await expect(page.getByRole('button', {name: '打开画布'})).toBeVisible();
});

test('shows AI expansion only for populated scene and custom style fields', async ({page}) => {
  const requests: Record<string, unknown>[] = [];
  await page.route('**/api/text/expand', async route => {
    const body = route.request().postDataJSON() as Record<string, unknown>;
    requests.push(body);
    const text = body.kind === 'scene'
      ? '夜幕下的森林小屋，暖黄色灯光从木窗洒向薄雾。'
      : '复古木刻版画风格，粗粝刀痕，黑白高对比。';
    await route.fulfill({
      status: 200,
      contentType: 'text/event-stream; charset=utf-8',
      body: 'event: content_delta\ndata: ' + JSON.stringify({text}) + '\n\nevent: message_complete\ndata: {}\n\n'
    });
  });
  await waitForEditor(page);
  await openImagePanel(page);

  const prompt = page.getByLabel('画面描述');
  await expect(page.getByRole('button', {name: '使用 AI 扩写场景提示词'})).toHaveCount(0);
  await prompt.fill('森林小屋');
  await expect(page.getByRole('button', {name: '使用 AI 扩写场景提示词'})).toContainText('AI扩写');
  await page.getByRole('button', {name: '使用 AI 扩写场景提示词'}).click();
  await expect(prompt).toHaveValue('夜幕下的森林小屋，暖黄色灯光从木窗洒向薄雾。');

  await page.getByLabel('画面风格').selectOption('custom');
  const customStyle = page.getByLabel('自定义画面风格');
  await expect(page.getByRole('button', {name: '使用 AI 扩写风格提示词'})).toHaveCount(0);
  await customStyle.fill('木刻');
  await expect(page.getByRole('button', {name: '使用 AI 扩写风格提示词'})).toContainText('AI扩写');
  await page.getByRole('button', {name: '使用 AI 扩写风格提示词'}).click();
  await expect(customStyle).toHaveValue('复古木刻版画风格，粗粝刀痕，黑白高对比。');

  expect(requests).toEqual([
    {kind: 'scene', text: '森林小屋'},
    {kind: 'style', text: '木刻'}
  ]);
});

test('shows progress and sends a custom image style', async ({page}) => {
  let requestBody: Record<string, unknown> | null = null;
  await page.route('**/api/image/generate', async route => {
    requestBody = route.request().postDataJSON();
    await new Promise(resolve => setTimeout(resolve, 1_000));
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        request_id: 'playwright-custom-style',
        mime_type: 'image/png',
        sheet_width: 4,
        sheet_height: 4,
        sheet_layout: {columns: 1, rows: 1},
        image_base64: fixturePng,
        target: 'backdrop',
        requested_canvas: {width: 480, height: 360}
      })
    });
  });
  await waitForEditor(page);
  await openImagePanel(page);
  await page.getByLabel('画面风格').selectOption('custom');
  const customStyle = page.getByLabel('自定义画面风格');
  await expect(customStyle).toBeVisible();
  await customStyle.fill('复古木刻版画，黑白高对比');
  await enterPrompt(page);
  await page.getByRole('button', {name: '生成 1 张图片'}).click();
  const progress = page.getByRole('status').filter({hasText: '正在构思画面'});
  await expect(progress).toBeVisible();
  await expect(page.locator('.generation-progress__scan')).toHaveCSS('animation-name', 'generation-scan');
  await expect(page.getByRole('img', {name: 'AI 生成图片'})).toBeVisible();
  expect(requestBody).toMatchObject({style: 'custom', custom_style: '复古木刻版画，黑白高对比'});
});

test('saves generated images locally and reuses them after refresh', async ({page}) => {
  await page.route('**/api/image/generate', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      request_id: 'playwright-library',
      mime_type: 'image/png',
      sheet_width: 4,
      sheet_height: 4,
      sheet_layout: {columns: 1, rows: 1},
      image_base64: fixturePng,
      target: 'backdrop',
      requested_canvas: {width: 480, height: 360}
    })
  }));
  await waitForEditor(page);

  const libraryButton = page.getByRole('button', {name: /打开素材库/});
  await expect(libraryButton).toBeVisible();
  await libraryButton.click();
  await expect(page.getByRole('dialog', {name: '素材库'})).toBeVisible();
  await expect(page.getByText('还没有本地素材')).toBeVisible();
  await page.getByRole('button', {name: '关闭素材库'}).click();

  await enterPrompt(page);
  await page.getByRole('button', {name: '生成 1 张图片'}).click();
  await expect(page.getByRole('img', {name: 'AI 生成图片'})).toBeVisible();
  await expect(libraryButton).toHaveAttribute('aria-label', '打开素材库，共 1 项素材');
  await page.getByRole('button', {name: '生成 1 张图片'}).click();
  await expect(libraryButton).toHaveAttribute('aria-label', '打开素材库，共 2 项素材');

  await page.reload({waitUntil: 'domcontentloaded'});
  await expect(page.locator('.blocklySvg')).toBeVisible({timeout: 30_000});
  const restoredButton = page.getByRole('button', {name: '打开素材库，共 2 项素材'});
  await expect(restoredButton).toBeVisible();
  await restoredButton.click();
  const library = page.getByRole('dialog', {name: '素材库'});
  await expect(library.locator('.asset-card')).toHaveCount(2);
  await expect(library.locator('.asset-card__name')).toHaveCount(2);
  await expect(library.locator('.asset-card__name').first()).toContainText('背景素材');
  await page.screenshot({path: 'test-results/asset-library-1280.png'});

  await page.setViewportSize({width: 390, height: 844});
  await expect(library).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({path: 'test-results/asset-library-390.png'});

  await page.setViewportSize({width: 1280, height: 720});
  await library.getByRole('button', {name: '导入到画布'}).first().click();
  await expect(page.getByRole('dialog', {name: '无边画布编辑器'})).toBeVisible();
});

test('generates one image and imports it as a backdrop', async ({page}) => {
  await page.route('**/api/image/generate', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      request_id: 'playwright-fixture',
      mime_type: 'image/png',
      sheet_width: 4,
      sheet_height: 4,
      sheet_layout: {columns: 1, rows: 1},
      image_base64: fixturePng,
      target: 'backdrop',
      requested_canvas: {width: 480, height: 360}
    })
  }));
  await waitForEditor(page);
  await enterPrompt(page);
  await page.getByRole('button', {name: '生成 1 张图片'}).click();
  await expect(page.getByRole('img', {name: 'AI 生成图片'})).toBeVisible();
  await page.getByRole('button', {name: '直接导入背景'}).click();
  await expect(page.getByText('已导入当前 Scratch 项目')).toHaveCount(0);
  await expect(page.getByText('背景').last()).toBeVisible();
});

test('separates move, transform, and enter-confirmed crop tools', async ({page}) => {
  await page.route('**/api/image/generate', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({request_id:'playwright-controls',mime_type:'image/png',sheet_width:4,sheet_height:4,sheet_layout:{columns:1,rows:1},image_base64:fixturePng,target:'sprite',requested_canvas:{width:480,height:360}})
  }));
  await waitForEditor(page);
  await openImagePanel(page);
  await page.getByText('新角色').click();
  await enterPrompt(page);
  await page.getByRole('button', {name:'生成 1 张图片'}).click();
  await page.getByRole('button', {name:'在画布中编辑'}).click();

  const canvas=page.locator('.canvas-viewport canvas');
  const box=await canvas.boundingBox();
  expect(box).not.toBeNull();
  const center={x:box!.x+box!.width/2,y:box!.y+box!.height/2};
  const readPixel=(x:number,y:number)=>canvas.evaluate((element,point)=>{const target=element as HTMLCanvasElement,rect=target.getBoundingClientRect(),scaleX=target.width/rect.width,scaleY=target.height/rect.height;return Array.from(target.getContext('2d')!.getImageData(Math.round((point.x-rect.left)*scaleX),Math.round((point.y-rect.top)*scaleY),1,1).data);},{x,y});
  await page.getByRole('button',{name:'色度抠图 (K)'}).click();
  await page.mouse.click(center.x-32,center.y-32);
  await expect(page.locator('.canvas-status')).toContainText('保存色度抠图');
  const redAfterFirst=await readPixel(center.x-32,center.y-32);
  await page.mouse.click(center.x-32,center.y+32);
  await expect.poll(async()=>readPixel(center.x-32,center.y+32)).not.toEqual([0,0,255,255]);
  expect(await readPixel(center.x-32,center.y-32)).toEqual(redAfterFirst);

  const moveTool=page.getByRole('button',{name:'移动 (V)'});
  await moveTool.click();
  await moveTool.hover();
  await expect(page.getByRole('tooltip').filter({hasText:'拖动图层位置，不显示缩放控制柄'})).toBeVisible();
  await expect(page.locator('.tool-options .transform-grid')).toHaveCount(0);
  const transformTool=page.getByRole('button',{name:'变换 (T)'});
  await transformTool.click();
  await transformTool.hover();
  await expect(page.getByRole('tooltip').filter({hasText:'移动图层或拖动八向控制柄调整大小'})).toBeVisible();
  const unlock=page.getByRole('button',{name:'解锁图层'});
  if(await unlock.count())await unlock.click();
  const layerWidth=page.locator('.tool-options .transform-grid label').filter({hasText:'宽'}).locator('input');
  await expect(layerWidth).toHaveValue('4');
  await page.getByRole('button',{name:'锁定比例',exact:true}).click();
  await page.mouse.move(center.x+64,center.y);
  await page.mouse.down();
  await page.mouse.move(center.x+96,center.y);
  await page.mouse.up();
  await expect(layerWidth).toHaveValue('5');
  await expect(page.locator('.canvas-status')).toContainText('已缩放图层');
  await page.getByRole('button',{name:'复制图层'}).click();
  await expect(page.locator('.layer-row input[value="原图 副本"]')).toHaveCount(1);
  await page.locator('.layer-row.is-active').getByRole('button',{name:'加入或移出造型队列'}).click();
  await expect(page.getByRole('tab',{name:/造型/})).toContainText('2');

  await page.getByRole('button',{name:'裁剪 (C)'}).click();
  const cropWidth=page.locator('.crop-options .transform-grid label').filter({hasText:'宽'}).locator('input');
  await expect(cropWidth).toHaveValue('5');
  await cropWidth.fill('3');
  await cropWidth.press('Tab');
  await page.screenshot({path:'test-results/canvas-crop-overlay.png'});
  await page.locator('.canvas-viewport').focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('.canvas-viewport')).toHaveAttribute('data-tool','move');
  await expect(page.locator('.canvas-status')).toContainText('确认裁剪');
  await expect(page.locator('.canvas-status')).toContainText('当前图层 3 × 4');
  const zipPromise=page.waitForEvent('download');
  await page.getByRole('button',{name:'造型 ZIP'}).click();
  const zipPath=await (await zipPromise).path();
  expect(zipPath).not.toBeNull();
  const files=unzipSync(await readFile(zipPath!));
  const png=files[Object.keys(files).find(name=>name.endsWith('.png'))!];
  const pngWidth=((png[16]<<24)|(png[17]<<16)|(png[18]<<8)|png[19])>>>0;
  const pngHeight=((png[20]<<24)|(png[21]<<16)|(png[22]<<8)|png[23])>>>0;
  expect({width:pngWidth,height:pngHeight}).toEqual({width:10,height:8});
  await importCanvasAsSprite(page);
  await page.screenshot({path:'test-results/canvas-transform-handles.png'});
});

test('edits a generated sprite in the canvas and keeps Scratch mounted', async ({page}) => {
  await page.route('**/api/image/generate', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      request_id: 'playwright-sprite',
      mime_type: 'image/png',
      sheet_width: 4,
      sheet_height: 4,
      sheet_layout: {columns: 1, rows: 1},
      image_base64: fixturePng,
      target: 'sprite',
      requested_canvas: {width: 480, height: 360},
      processing_hints: {background_mode: 'solid', suggested_key_color: '#00ff00'}
    })
  }));
  await waitForEditor(page);
  await openImagePanel(page);
  await page.getByText('新角色').click();
  await enterPrompt(page);
  await page.getByRole('button', {name: '生成 1 张图片'}).click();
  await expect(page.getByRole('img', {name: 'AI 生成图片'})).toBeVisible();
  await page.getByRole('button', {name: '在画布中编辑'}).click();

  const workspace = page.getByRole('dialog', {name: '无边画布编辑器'});
  await expect(workspace).toBeVisible();
  await expect(page.locator('.scratch-host')).toHaveCount(1);
  await expect(page.getByLabel('图层名称')).toHaveValue('原图');
  await expect(page.getByRole('button', {name: '解锁图层'})).toBeVisible();

  await page.getByRole('button', {name: '矩形选区 (M)'}).click();
  const canvas = page.locator('.canvas-viewport canvas');
  const box = await canvas.boundingBox();
  expect(box).not.toBeNull();
  const regions = [[-45,-45,45,45],[-45,-45,45,45],[-45,-45,45,45],[-45,-45,45,45]];
  for (let index=0; index<regions.length; index+=1) {
    if (index>0) {
      await page.getByRole('tab', {name: /图层/}).click();
      await page.locator('.layer-row').filter({has: page.locator('input[value="原图"]')}).click();
    }
    const [x1,y1,x2,y2]=regions[index];
    await page.mouse.move(box!.x+box!.width/2+x1,box!.y+box!.height/2+y1);
    await page.mouse.down();
    await page.mouse.move(box!.x+box!.width/2+x2,box!.y+box!.height/2+y2);
    await page.mouse.up();
    await expect(page.locator('.canvas-viewport')).toHaveAttribute('data-tool','select');
    if (index===0) await page.keyboard.press('Alt+KeyJ');
    else await page.getByRole('button',{name:'复制为新图层'}).click();
  }
  expect(await page.locator('.canvas-status > span').first().textContent()).toBe('复制选区为新图层');
  await expect(page.getByRole('tab', {name: /造型/})).toContainText('5');
  await page.getByRole('tab', {name: /造型/}).click();
  await expect(page.getByLabel('造型名称')).toHaveCount(5);
  await page.getByRole('tab', {name: /图层/}).click();
  await expect(page.getByRole('tab', {name: /图层/})).toContainText('5');
  await page.getByRole('button', {name: '复制图层'}).click();
  await expect(page.getByRole('tab', {name: /图层/})).toContainText('6');
  await expect(page.locator('.canvas-status')).toContainText('复制图层');
  await page.getByRole('tab', {name: /造型/}).click();
  await page.getByRole('button',{name:'预览造型'}).first().click();
  await expect(page.locator('.canvas-status')).toContainText('正在预览造型');
  await page.getByRole('button',{name:'预览造型'}).first().click();
  await expect(page.locator('.canvas-status')).toContainText('已退出造型预览');
  const zipDownloadPromise=page.waitForEvent('download');
  await page.getByRole('button', {name: '造型 ZIP'}).click();
  const zipDownload=await zipDownloadPromise;
  const zipPath=await zipDownload.path();
  expect(zipPath).not.toBeNull();
  const costumeZip=unzipSync(await readFile(zipPath!));
  expect(Object.keys(costumeZip).filter(name=>name.endsWith('.png'))).toHaveLength(5);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({path: 'test-results/canvas-workspace-1280.png'});

  await page.setViewportSize({width: 390, height: 844});
  await expect(page.getByRole('button', {name: '返回 Scratch'})).toBeVisible();
  await page.getByRole('button', {name: '收起属性面板'}).click();
  await expect(page.locator('.canvas-inspector')).toBeHidden();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({path: 'test-results/canvas-workspace-390.png'});

  await page.setViewportSize({width: 1280, height: 720});
  await importCanvasAsSprite(page);
  await expect(workspace).toHaveCount(0);
  await page.getByRole('button', {name: '收起生成素材'}).click();
  await expect(page.getByRole('button', {name: '角色1'})).toBeVisible();
  await expect(page.getByRole('button', {name: 'AI 角色素材'})).toBeVisible();

  await page.getByText('文件', {exact: true}).click();
  const downloadPromise=page.waitForEvent('download');
  await page.getByText('保存到电脑', {exact: true}).click();
  const download=await downloadPromise;
  const downloadPath=await download.path();
  expect(downloadPath).not.toBeNull();
  const archive=unzipSync(await readFile(downloadPath!));
  const project=JSON.parse(new TextDecoder().decode(archive['project.json'])) as {targets:Array<{name:string;costumes:Array<{name:string;md5ext:string;bitmapResolution:number;rotationCenterX:number;rotationCenterY:number}>}>};
  const imported=project.targets.filter(target=>target.name==='AI 角色素材');
  expect(imported).toHaveLength(1);
  expect(imported[0].costumes.map(costume=>costume.name)).toEqual(['AI 造型','造型2','造型3','造型4','造型5']);
  expect(imported[0].costumes.every(costume=>costume.bitmapResolution===2)).toBe(true);
  expect(imported[0].costumes.every(costume=>Number.isFinite(costume.rotationCenterX)&&Number.isFinite(costume.rotationCenterY))).toBe(true);
  expect(imported[0].costumes.every(costume=>Boolean(archive[costume.md5ext]))).toBe(true);

  await page.getByText('文件',{exact:true}).click();
  const chooserPromise=page.waitForEvent('filechooser');
  await page.getByText(/从电脑中上传|Load from your computer/).click();
  const chooser=await chooserPromise;
  page.once('dialog',dialog=>dialog.accept());
  await chooser.setFiles(downloadPath!);
  await expect(page.getByRole('button',{name:'AI 角色素材'})).toBeVisible({timeout:30_000});
  await page.getByRole('button',{name:'AI 角色素材'}).click();
  await page.getByRole('tab',{name:'造型'}).click();
  for(const name of ['AI 造型','造型2','造型3','造型4','造型5'])await expect(page.getByText(name,{exact:true}).last()).toBeVisible();
});

test('keeps a Scratch sprite selection aligned when copied to a layer',async({page})=>{
  await page.addInitScript(() => Object.defineProperty(Crypto.prototype, 'randomUUID', {configurable: true, value: undefined}));
  await waitForEditor(page);
  await page.getByRole('button',{name:'打开画布'}).click();
  await page.getByRole('button',{name:'从 Scratch 导入'}).click();
  const picker=page.getByRole('dialog',{name:'从 Scratch 导入'});
  const spriteOption=picker.locator('.scratch-import-list button').filter({hasText:'角色1'});
  await expect(spriteOption.getByRole('img',{name:'角色1预览'})).toBeVisible();
  await spriteOption.click();
  await expect(page.locator('.canvas-status')).toContainText('已从 Scratch 导入角色“角色1”');

  const helpButton=page.getByRole('button',{name:'快捷键帮助'});
  await helpButton.click();
  await expect(page.getByRole('dialog',{name:'画布快捷键帮助'})).toBeVisible();
  await page.screenshot({path:'test-results/canvas-shortcuts-help.png'});
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog',{name:'画布快捷键帮助'})).toHaveCount(0);
  await expect(page.getByRole('dialog',{name:'无边画布编辑器'})).toBeVisible();
  await page.keyboard.press('m');
  await expect(page.locator('.canvas-viewport')).toHaveAttribute('data-tool','select');
  const canvas=page.locator('.canvas-viewport canvas');
  const box=await canvas.boundingBox();
  expect(box).not.toBeNull();
  const selection={x:box!.x+box!.width/2-45,y:box!.y+box!.height/2-45,width:90,height:75};
  await page.mouse.move(selection.x,selection.y);
  await page.mouse.down();
  await page.mouse.move(selection.x+selection.width,selection.y+selection.height);
  await page.mouse.up();
  const readSelection=()=>canvas.evaluate((element,area)=>{
    const target=element as HTMLCanvasElement,rect=target.getBoundingClientRect(),scaleX=target.width/rect.width,scaleY=target.height/rect.height;
    const x=Math.round((area.x-rect.left+3)*scaleX),y=Math.round((area.y-rect.top+3)*scaleY),width=Math.round((area.width-6)*scaleX),height=Math.round((area.height-6)*scaleY);
    return Array.from(target.getContext('2d')!.getImageData(x,y,width,height).data);
  },selection);
  const before=await readSelection();

  await page.getByRole('button',{name:'复制为新图层'}).click();
  await page.getByRole('tab',{name:/图层/}).click();
  const copiedRow=page.locator('.layer-row').filter({has:page.locator('input[value="造型3"]')});
  await expect(copiedRow).toBeVisible();
  const sourceRows=page.locator('.layer-row').filter({hasNot:page.locator('input[value="造型3"]')});
  for(let index=0;index<await sourceRows.count();index+=1){const hide=sourceRows.nth(index).getByRole('button',{name:'隐藏图层'});if(await hide.count())await hide.click();}
  const after=await readSelection();
  expect(after).toHaveLength(before.length);
  const changed=before.reduce((total,value,index)=>total+(Math.abs(value-after[index])>40?1:0),0);
  expect(changed/before.length).toBeLessThan(0.08);
  await page.screenshot({path:'test-results/scratch-selection-copy-fixed.png'});
});

test('imports a chroma-keyed duplicate of an existing Scratch costume',async({page})=>{
  await waitForEditor(page);
  await page.getByRole('button',{name:'打开画布'}).click();
  await page.getByRole('button',{name:'从 Scratch 导入'}).click();
  const picker=page.getByRole('dialog',{name:'从 Scratch 导入'});
  await picker.locator('.scratch-import-list button').filter({hasText:'角色1'}).click();
  await expect(page.locator('.canvas-status')).toContainText('已从 Scratch 导入角色“角色1”');
  await page.getByRole('tab',{name:/图层/}).click();
  const costume1Row=page.locator('.layer-row').filter({has:page.locator('input[value="造型1"]')});
  const costume2Row=page.locator('.layer-row').filter({has:page.locator('input[value="造型2"]')});
  await costume1Row.getByRole('button',{name:'隐藏图层'}).click();
  await costume2Row.getByRole('button',{name:'显示图层'}).click();
  await costume2Row.click();
  await page.getByRole('button',{name:'色度抠图 (K)'}).click();
  const canvas=page.locator('.canvas-viewport canvas');
  const box=await canvas.boundingBox();
  expect(box).not.toBeNull();
  await page.mouse.click(box!.x+box!.width/2,box!.y+box!.height/2);
  await expect(page.locator('.canvas-status')).toContainText('保存色度抠图');
  await page.getByRole('tab',{name:/图层/}).click();
  await page.getByRole('button',{name:'复制图层'}).click();
  await expect(page.locator('.layer-row input[value="造型2 副本"]')).toHaveCount(1);
  await page.locator('.layer-row.is-active').getByRole('button',{name:'加入或移出造型队列'}).click();
  await costume2Row.click();
  await page.getByRole('button',{name:'复制图层'}).click();
  await expect(page.locator('.layer-row input[value="造型2 副本 2"]')).toHaveCount(1);
  await page.locator('.layer-row.is-active').getByRole('button',{name:'加入或移出造型队列'}).click();
  await expect(page.getByRole('tab',{name:/造型/})).toContainText('4');
  const zipPromise=page.waitForEvent('download');
  await page.getByRole('button',{name:'造型 ZIP'}).click();
  const zipPath=await (await zipPromise).path();
  expect(zipPath).not.toBeNull();
  const exported=unzipSync(await readFile(zipPath!));
  const processedName=Object.keys(exported).find(name=>name.includes('造型2')&&name.includes('副本')&&name.endsWith('.png'));
  expect(processedName).toBeTruthy();
  const processedPng=exported[processedName!];
  expect(Array.from(processedPng.subarray(0,8))).toEqual([137,80,78,71,13,10,26,10]);
  const readAlpha=(bytes:Uint8Array)=>page.evaluate(async values=>{const bitmap=await createImageBitmap(new Blob([new Uint8Array(values)],{type:'image/png'})),canvas=document.createElement('canvas');canvas.width=bitmap.width;canvas.height=bitmap.height;canvas.getContext('2d')!.drawImage(bitmap,0,0);bitmap.close();const data=canvas.getContext('2d')!.getImageData(0,0,canvas.width,canvas.height).data;let transparent=0,visible=0;for(let index=3;index<data.length;index+=4)data[index]===0?transparent++:visible++;return{transparent,visible};},Array.from(bytes));
  const alpha=await readAlpha(processedPng);
  expect(alpha.transparent).toBeGreaterThan(0);
  expect(alpha.visible).toBeGreaterThan(0);
  await importCanvasAsSprite(page);
  await page.getByRole('tab',{name:'造型'}).click();
  await page.getByText('造型2 副本',{exact:true}).last().click();
  const paintCanvas=page.locator('canvas[class*="paper-canvas"]').last();
  await expect(paintCanvas).toBeVisible();
  await expect.poll(()=>paintCanvas.evaluate(element=>{const canvas=element as HTMLCanvasElement,data=canvas.getContext('2d')?.getImageData(0,0,canvas.width,canvas.height).data;if(!data)return 0;let visible=0;for(let index=3;index<data.length;index+=4)if(data[index]>0)visible++;return visible;}),{timeout:15_000}).toBeGreaterThan(0);
  await page.screenshot({path:'test-results/scratch-processed-bitmap-costume.png'});
  const toVector=page.getByRole('button',{name:/转换为矢量|Convert to Vector/});
  await expect(toVector).toBeVisible();
  await toVector.click();
  const toBitmap=page.getByRole('button',{name:/转换为位图|Convert to Bitmap/});
  await expect(toBitmap).toBeVisible();
  await toBitmap.click();
  await expect(toVector).toBeVisible();

  await page.getByText('文件',{exact:true}).click();
  const projectPromise=page.waitForEvent('download');
  await page.getByText('保存到电脑',{exact:true}).click();
  const projectPath=await (await projectPromise).path();
  expect(projectPath).not.toBeNull();
  const roundTripArchive=unzipSync(await readFile(projectPath!));
  const roundTripProject=JSON.parse(new TextDecoder().decode(roundTripArchive['project.json'])) as {targets:Array<{costumes:Array<{name:string;md5ext:string;dataFormat:string;bitmapResolution:number}>}>};
  const converted=roundTripProject.targets.flatMap(target=>target.costumes).find(costume=>costume.name==='造型2 副本');
  expect(converted).toMatchObject({dataFormat:'png',bitmapResolution:2});
  expect(converted?.md5ext.endsWith('.png')).toBe(true);
  const convertedAsset=roundTripArchive[converted!.md5ext];
  expect(convertedAsset).toBeTruthy();
  const convertedAlpha=await readAlpha(convertedAsset);
  expect(convertedAlpha.transparent).toBeGreaterThan(0);
  expect(convertedAlpha.visible).toBeGreaterThan(0);
});

test('restores the canvas document after a browser refresh',async({page})=>{
  await page.route('**/api/image/generate',route=>route.fulfill({status:200,contentType:'application/json',body:JSON.stringify({request_id:'playwright-recovery',mime_type:'image/png',sheet_width:4,sheet_height:4,sheet_layout:{columns:1,rows:1},image_base64:fixturePng,target:'sprite',requested_canvas:{width:480,height:360}})}));
  await waitForEditor(page);
  await openImagePanel(page);
  await page.getByText('新角色').click();
  await enterPrompt(page);
  await page.getByRole('button',{name:'生成 1 张图片'}).click();
  await page.getByRole('button',{name:'在画布中编辑'}).click();
  await expect(page.getByLabel('图层名称')).toHaveValue('原图');
  const recoveryHeadCount=()=>page.evaluate(async()=>{const exists=(await indexedDB.databases()).some(database=>database.name==='ai-scratch-canvas');if(!exists)return 0;return new Promise<number>((resolve,reject)=>{const request=indexedDB.open('ai-scratch-canvas');request.onerror=()=>reject(request.error);request.onsuccess=()=>{if(!request.result.objectStoreNames.contains('recoveryHeads')){request.result.close();resolve(0);return;}const transaction=request.result.transaction('recoveryHeads','readonly');const count=transaction.objectStore('recoveryHeads').count();count.onerror=()=>reject(count.error);count.onsuccess=()=>{request.result.close();resolve(count.result);};};});});
  await expect.poll(recoveryHeadCount,{timeout:15_000}).toBe(1);
  await page.getByRole('button',{name:'返回 Scratch'}).click();
  await page.reload({waitUntil:'domcontentloaded'});
  await expect(page.locator('.blocklySvg')).toBeVisible({timeout: 30_000});
  await page.getByRole('button',{name:'打开画布'}).click();
  await expect(page.getByRole('dialog',{name:'无边画布编辑器'})).toBeVisible();
  await expect(page.getByLabel('图层名称')).toHaveValue('原图');
  await expect(page.locator('.canvas-status')).toContainText('已恢复上次画布');
});
