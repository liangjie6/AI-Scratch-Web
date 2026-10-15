import {readFile} from 'node:fs/promises';
import {expect, test, type Locator, type Page} from '@playwright/test';

type Point = {x: number; y: number};
type ProbeWindow = Window & {
  __syncChromaCalls: number;
  __syncChromaModuleInstrumented: boolean;
  __chromaWorkers: {url: string; sent: number; completed: number; terminated: boolean}[];
  __chromaProbe?: {timer: number; ticks: number[]; longTasks: number[]; observer: PerformanceObserver};
};

async function instrumentProcessing(page: Page) {
  await page.addInitScript(() => {
    const target = window as unknown as ProbeWindow;
    target.__syncChromaCalls = 0;
    target.__syncChromaModuleInstrumented = false;
    target.__chromaWorkers = [];
    const NativeWorker = window.Worker;
    window.Worker = class extends NativeWorker {
      private record: ProbeWindow['__chromaWorkers'][number];
      constructor(url: string | URL, options?: WorkerOptions) {
        super(url, options);
        this.record = {url: String(url), sent: 0, completed: 0, terminated: false};
        target.__chromaWorkers.push(this.record);
        this.addEventListener('message', () => {this.record.completed++;});
      }
      override postMessage(message: unknown, transfer?: Transferable[] | StructuredSerializeOptions) {
        this.record.sent++;
        super.postMessage(message, transfer as Transferable[]);
      }
      override terminate() {this.record.terminated = true; super.terminate();}
    };
  });
  // Count algorithm calls on the window thread. The same module runs in the real
  // worker, where there is no window and the counter deliberately stays absent.
  await page.route('**/src/canvas/pixels/chroma-key.ts*', async route => {
    const response = await route.fetch();
    const source = await response.text();
    const instrumented = source.replace(/function applyChromaKey\(source, effect\)\s*\{/, match => `${match}\nif (typeof window !== 'undefined') window.__syncChromaCalls++;`);
    expect(instrumented).not.toBe(source);
    await route.fulfill({response, body: `if (typeof window !== 'undefined') window.__syncChromaModuleInstrumented = true;\n${instrumented}`});
  });
}

async function openCanvas(page: Page) {
  await page.setViewportSize({width: 1440, height: 900});
  await page.goto('/', {waitUntil: 'domcontentloaded'});
  await expect(page.locator('.blocklySvg')).toBeVisible({timeout: 45_000});
  await page.getByRole('button', {name: '打开画布', exact: true}).click();
  await expect(page.getByText('导入一张角色素材', {exact: true})).toBeVisible();
}

async function upload(page: Page, kind: 'bridge' | 'diagonal' | 'large') {
  const fixture = await page.evaluate(shape => {
    const canvas = document.createElement('canvas');
    canvas.width = shape === 'large' ? 3072 : shape === 'bridge' ? 256 : 160;
    canvas.height = shape === 'large' ? 3072 : shape === 'bridge' ? 128 : 160;
    const context = canvas.getContext('2d')!;
    context.fillStyle = '#dc2828'; context.fillRect(0, 0, canvas.width, canvas.height);
    if (shape === 'bridge') {
      context.fillStyle = '#2060b0'; context.fillRect(16, 16, 64, 96); context.fillRect(160, 16, 64, 96);
      // RGB distance is 27.45%, between tolerance 18% and tolerance + softness 30%.
      context.fillStyle = '#66a6f6'; context.fillRect(80, 56, 80, 16);
    } else if (shape === 'diagonal') {
      context.fillStyle = '#2060b0'; context.fillRect(16, 16, 64, 64); context.fillRect(80, 80, 64, 64);
    } else {
      context.fillStyle = '#2060b0'; context.fillRect(0, 0, canvas.width / 2, canvas.height);
      context.fillStyle = '#507faf'; context.fillRect(canvas.width / 2, 0, canvas.width / 2, canvas.height);
    }
    return {width: canvas.width, height: canvas.height, base64: canvas.toDataURL('image/png').split(',')[1]!};
  }, kind);
  await page.locator('.canvas-workspace input[type="file"]').setInputFiles({name: `${kind}.png`, mimeType: 'image/png', buffer: Buffer.from(fixture.base64, 'base64')});
  await expect(page.locator('.canvas-status')).toContainText(`当前图层 ${fixture.width} × ${fixture.height}`);
  await expect(page.locator('.layer-row')).toHaveCount(1);
  return {width: fixture.width, height: fixture.height};
}

function range(page: Page, name: string) {return page.locator('.tool-options label').filter({hasText: name}).locator('input');}
async function setRange(input: Locator, value: number) {
  await input.evaluate((element, next) => {
    (element as HTMLInputElement).value = String(next);
    element.dispatchEvent(new Event('input', {bubbles: true}));
    element.dispatchEvent(new Event('change', {bubbles: true}));
  }, value);
}
async function useChroma(page: Page, tolerance = 18, softness = 12) {
  await page.getByRole('button', {name: '色度抠图 (K)', exact: true}).click();
  await setRange(range(page, '容差'), tolerance);
  await setRange(range(page, '柔化'), softness);
  await setRange(range(page, '去色边'), 0);
}
async function clickImage(page: Page, size: {width: number; height: number}, point: Point) {
  const rect = await page.locator('.canvas-viewport canvas').boundingBox();
  expect(rect).not.toBeNull();
  const zoom = Math.min((rect!.width - 72) / size.width, (rect!.height - 72) / size.height);
  await page.mouse.click(rect!.x + rect!.width / 2 + (point.x - size.width / 2) * zoom, rect!.y + rect!.height / 2 + (point.y - size.height / 2) * zoom);
}
async function previewAlphas(page: Page, points: Point[], size: {width: number; height: number}) {
  return page.locator('.layer-row__thumb img').evaluate(async (element, settings) => {
    // The thumbnail may change src while a worker completes. Decode a snapshot
    // rather than cancelling decode on the live image element during polling.
    const image = new Image(); image.src = (element as HTMLImageElement).src;
    await image.decode();
    const canvas = document.createElement('canvas');
    canvas.width = image.naturalWidth; canvas.height = image.naturalHeight;
    const context = canvas.getContext('2d')!; context.drawImage(image, 0, 0);
    return settings.points.map(point => context.getImageData(Math.floor(point.x / settings.size.width * canvas.width), Math.floor(point.y / settings.size.height * canvas.height), 1, 1).data[3]);
  }, {points, size});
}
async function exportedAlphas(page: Page, points: Point[]) {
  const downloaded = page.waitForEvent('download');
  await page.getByRole('button', {name: 'PNG', exact: true}).click();
  const path = await (await downloaded).path();
  expect(path).not.toBeNull();
  const base64 = (await readFile(path!)).toString('base64');
  return page.evaluate(async settings => {
    const image = new Image(); image.src = `data:image/png;base64,${settings.base64}`; await image.decode();
    const canvas = document.createElement('canvas'); canvas.width = image.naturalWidth; canvas.height = image.naturalHeight;
    const context = canvas.getContext('2d')!; context.drawImage(image, 0, 0);
    return settings.points.map(point => context.getImageData(point.x, point.y, 1, 1).data[3]);
  }, {base64, points});
}
async function startProbe(page: Page) {
  await page.evaluate(() => {
    const target = window as unknown as ProbeWindow;
    const ticks: number[] = [], longTasks: number[] = [];
    let previous = performance.now();
    const timer = window.setInterval(() => {const now = performance.now(); ticks.push(now - previous); previous = now;}, 16);
    const observer = new PerformanceObserver(list => {longTasks.push(...list.getEntries().map(entry => entry.duration));});
    observer.observe({entryTypes: ['longtask']});
    target.__chromaProbe = {timer, ticks, longTasks, observer};
  });
}
async function stopProbe(page: Page) {
  return page.evaluate(() => {
    const target = window as unknown as ProbeWindow, probe = target.__chromaProbe!;
    clearInterval(probe.timer); probe.observer.disconnect();
    return {heartbeatCount: probe.ticks.length, maxHeartbeatMs: Math.max(0, ...probe.ticks), maxLongTaskMs: Math.max(0, ...probe.longTasks), syncCalls: target.__syncChromaCalls, instrumented: target.__syncChromaModuleInstrumented, workers: target.__chromaWorkers.filter(worker => worker.url.includes('pixel-worker'))};
  });
}

test('connected chroma cannot cross a soft-color bridge; global mode reaches the other island and undo/export agree', async ({page}) => {
  await instrumentProcessing(page);
  await openCanvas(page);
  const size = await upload(page, 'bridge'), points = [{x: 48, y: 64}, {x: 192, y: 64}];
  await useChroma(page);
  await clickImage(page, size, points[0]!);
  await expect.poll(() => previewAlphas(page, points, size)).toEqual([0, 255]);
  expect(await exportedAlphas(page, points)).toEqual([0, 255]);
  await page.locator('.tool-options select').selectOption('global');
  await expect.poll(() => previewAlphas(page, points, size)).toEqual([0, 0]);
  expect(await exportedAlphas(page, points)).toEqual([0, 0]);
  await page.getByRole('button', {name: '撤销', exact: true}).click();
  await expect(page.locator('.tool-options select')).toHaveValue('connected');
  await expect.poll(() => previewAlphas(page, points, size)).toEqual([0, 255]);
  expect(await exportedAlphas(page, points)).toEqual([0, 255]);
  await page.getByRole('button', {name: '重做', exact: true}).click();
  await expect.poll(() => previewAlphas(page, points, size)).toEqual([0, 0]);
  expect(await exportedAlphas(page, points)).toEqual([0, 0]);
  expect(await page.evaluate(() => (window as unknown as ProbeWindow).__syncChromaCalls)).toBe(0);
});

test('connected chroma preserves a same-color island that touches only diagonally', async ({page}) => {
  await openCanvas(page);
  const size = await upload(page, 'diagonal'), points = [{x: 48, y: 48}, {x: 112, y: 112}];
  await useChroma(page, 0, 0);
  await clickImage(page, size, points[0]!);
  await expect.poll(() => previewAlphas(page, points, size)).toEqual([0, 255]);
  expect(await exportedAlphas(page, points)).toEqual([0, 255]);
});

test('3072px connected chroma and twenty rapid slider previews stay responsive and present only the latest result', async ({page}, testInfo) => {
  await instrumentProcessing(page);
  await openCanvas(page);
  const size = await upload(page, 'large'), points = [{x: 512, y: 1536}, {x: 2560, y: 1536}];
  await useChroma(page, 0, 0);
  await startProbe(page);
  await clickImage(page, size, points[0]!);
  await expect.poll(() => previewAlphas(page, points, size)).toEqual([0, 255]);
  await page.waitForTimeout(60);
  const click = await stopProbe(page);
  await startProbe(page);
  await range(page, '容差').evaluate(async element => {
    const input = element as HTMLInputElement;
    const workers = (window as unknown as ProbeWindow).__chromaWorkers;
    const before = workers.filter(worker => worker.url.includes('pixel-worker')).length;
    // Alternating requests make a stale worker completion observable. The final
    // 10% tolerance is uncached and must leave the right half opaque.
    for (let index = 0; index < 20; index++) {
      input.value = String(index === 19 ? 10 : index % 2 ? 0 : 40);
      input.dispatchEvent(new Event('input', {bubbles: true}));
      // Let one superseded preview enter a real worker before racing the rest.
      // This covers aborting in-flight work as well as debounce cancellation.
      if (index === 0) for (let attempt = 0; attempt < 125 && workers.filter(worker => worker.url.includes('pixel-worker')).length === before; attempt++) await new Promise(resolve => setTimeout(resolve, 8));
      await new Promise(resolve => setTimeout(resolve, 8));
    }
    input.dispatchEvent(new Event('change', {bubbles: true}));
  });
  await expect(range(page, '容差')).toHaveValue('10');
  await expect.poll(() => previewAlphas(page, points, size)).toEqual([0, 255]);
  await expect(page.locator('.canvas-status')).toContainText('已调整色度抠图');
  await page.waitForTimeout(250);
  expect(await previewAlphas(page, points, size)).toEqual([0, 255]);
  const sliders = await stopProbe(page);
  await testInfo.attach('main-thread-responsiveness.json', {body: JSON.stringify({click, sliders}, null, 2), contentType: 'application/json'});
  console.log('chroma responsiveness', JSON.stringify({click, sliders}));
  for (const result of [click, sliders]) {
    expect(result.instrumented).toBe(true);
    expect(result.syncCalls).toBe(0);
    expect(result.heartbeatCount).toBeGreaterThan(0);
    expect(result.maxHeartbeatMs).toBeLessThan(250);
    expect(result.maxLongTaskMs).toBeLessThan(250);
  }
  expect(sliders.workers.reduce((sum, worker) => sum + worker.sent, 0)).toBeGreaterThan(click.workers.reduce((sum, worker) => sum + worker.sent, 0));
  expect(sliders.workers.some(worker => worker.terminated && worker.sent > worker.completed)).toBe(true);
  expect(await exportedAlphas(page, points)).toEqual([0, 255]);
});

test('closing the canvas while a large chroma request is pending terminates its workers cleanly', async ({page}) => {
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await instrumentProcessing(page);
  // Keep an actual worker request pending so closing tests cancellation rather
  // than merely cleaning up a computation which already finished.
  for (const workerUrl of ['**/src/canvas/pixels/pixel-worker.ts?worker_file*', '**/assets/pixel-worker-*.js']) {
    await page.route(workerUrl, async route => {
      const response = await route.fetch();
      await route.fulfill({response, body: `await new Promise(resolve => setTimeout(resolve, 400));\n${await response.text()}`});
    });
  }
  await openCanvas(page);
  const size = await upload(page, 'large');
  await useChroma(page, 100, 0);
  await startProbe(page);
  await clickImage(page, size, {x: 512, y: 1536});
  await expect.poll(() => page.evaluate(() => (window as unknown as ProbeWindow).__chromaWorkers.some(worker => worker.url.includes('pixel-worker') && worker.sent > worker.completed && !worker.terminated))).toBe(true);
  await page.getByRole('button', {name: '返回 Scratch', exact: true}).click();
  await expect(page.getByRole('dialog', {name: '无边画布编辑器', exact: true})).toHaveCount(0);
  await page.waitForTimeout(300);
  const result = await stopProbe(page);
  expect(result.workers.length).toBeGreaterThan(0);
  expect(result.workers.every(worker => worker.terminated)).toBe(true);
  expect(errors).toEqual([]);
});
