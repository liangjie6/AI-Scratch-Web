import {afterEach, beforeAll, beforeEach, describe, expect, it, vi} from 'vitest';
import {BitmapStore} from '../src/canvas/model/bitmap-store';
import {IDENTITY_MATRIX, type ChromaKeyEffect, type RasterLayer} from '../src/canvas/model/canvas-types';
import * as chroma from '../src/canvas/pixels/chroma-key';
import {applyChromaKeyAsync} from '../src/canvas/pixels/pixel-client';
import {cacheLayerRaster, getPreparedLayerRaster, layerImageData, layerRasterCanvas, prepareLayerRaster, prepareLayersRaster, rasterizeLayers, readLayerPixel, retainPreparedLayerRaster, warmLayerRaster} from '../src/canvas/pixels/rasterize';

beforeAll(() => {
  if (!globalThis.ImageData) {
    class ImageDataPolyfill {
      data: Uint8ClampedArray; width: number; height: number;
      constructor(dataOrWidth: Uint8ClampedArray | number, widthOrHeight: number, height?: number) {
        this.width = typeof dataOrWidth === 'number' ? dataOrWidth : widthOrHeight;
        this.height = typeof dataOrWidth === 'number' ? widthOrHeight : height ?? dataOrWidth.length / 4 / widthOrHeight;
        this.data = typeof dataOrWidth === 'number' ? new Uint8ClampedArray(this.width * this.height * 4) : dataOrWidth;
      }
    }
    Object.defineProperty(globalThis, 'ImageData', {value: ImageDataPolyfill, configurable: true});
  }
});
const effect: ChromaKeyEffect = {keyColor: {r: 0, g: 255, b: 0}, tolerance: 0, softness: 0, mode: 'connected', seedMode: 'points', seedPoints: [{x: 0, y: 0}], connectivity: 4, spillSuppression: 0, inverted: false, enabled: true};
function fixture() {
  const bitmaps = new BitmapStore(), image = new ImageData(new Uint8ClampedArray([0, 255, 0, 255, 255, 0, 0, 255]), 2, 1);
  const layer: RasterLayer = {id: 'layer', name: '图层', bitmapId: bitmaps.put(image), bounds: {x: 0, y: 0, width: 2, height: 1}, transform: {...IDENTITY_MATRIX}, visible: true, locked: false, opacity: 1, blendMode: 'normal', chromaKey: {...effect, seedPoints: [{x: 0, y: 0}]}, createdAt: 1, updatedAt: 1};
  return {bitmaps, layer, image};
}
interface WorkerRequest {revision: number; width: number; height: number; data: ArrayBuffer; effect: ChromaKeyEffect}
class FakeWorker {
  static instances: FakeWorker[] = [];
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  onmessageerror: (() => void) | null = null;
  request?: WorkerRequest;
  terminate = vi.fn();
  postMessage = vi.fn((request: WorkerRequest) => { this.request = request; });
  constructor() { FakeWorker.instances.push(this); }
  complete(output?: ImageData) {
    const request = this.request!;
    const image = output ?? chroma.applyChromaKey(new ImageData(new Uint8ClampedArray(request.data), request.width, request.height), request.effect);
    this.onmessage?.({data: {ok: true, revision: request.revision, width: image.width, height: image.height, data: image.data.buffer}} as MessageEvent);
  }
}
function mockCanvases() {
  const contexts: {putImageData: ReturnType<typeof vi.fn>; drawImage: ReturnType<typeof vi.fn>}[] = [];
  const createElement = vi.fn(() => {
    const context = {putImageData: vi.fn(), drawImage: vi.fn(), scale: vi.fn(), translate: vi.fn(), save: vi.fn(), restore: vi.fn(), transform: vi.fn(), globalAlpha: 1};
    contexts.push(context);
    return {width: 0, height: 0, getContext: () => context};
  });
  vi.stubGlobal('document', {createElement});
  return {createElement, contexts};
}
beforeEach(() => { FakeWorker.instances = []; vi.stubGlobal('Worker', FakeWorker); });
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe('cancelable chroma workers', () => {
  it('transfers a private source copy and snapshots effect values before a later UI edit', async () => {
    const {image, layer} = fixture(), original = [...image.data];
    const result = applyChromaKeyAsync(image, layer.chromaKey!);
    const worker = FakeWorker.instances[0];
    layer.chromaKey!.keyColor = {r: 255, g: 0, b: 0}; layer.chromaKey!.seedPoints[0].x = 1;
    expect(worker.request!.data).not.toBe(image.data.buffer);
    expect(worker.request!.effect.keyColor).toEqual(effect.keyColor);
    expect(worker.request!.effect.seedPoints).toEqual([{x: 0, y: 0}]);
    worker.complete();
    expect([...(await result).data]).toEqual([0, 255, 0, 0, 255, 0, 0, 255]);
    expect([...image.data]).toEqual(original);
    expect(worker.terminate).toHaveBeenCalledOnce();
  });
  it('terminates immediately on cancellation and rejects without waiting for the worker', async () => {
    const {image} = fixture(), controller = new AbortController();
    const promise = applyChromaKeyAsync(image, effect, {signal: controller.signal});
    const rejected = expect(promise).rejects.toMatchObject({name: 'AbortError'});
    controller.abort();
    expect(FakeWorker.instances[0].terminate).toHaveBeenCalledOnce();
    await rejected;
    expect(FakeWorker.instances[0].onmessage).toBeNull();
  });
  it('does not create a worker for an already canceled operation', async () => {
    const {image} = fixture(), controller = new AbortController(); controller.abort();
    await expect(applyChromaKeyAsync(image, effect, {signal: controller.signal})).rejects.toMatchObject({name: 'AbortError'});
    expect(FakeWorker.instances).toHaveLength(0);
  });
  it('reports worker failures and disposes the failed worker', async () => {
    const {image} = fixture(), promise = applyChromaKeyAsync(image, effect);
    const rejected = expect(promise).rejects.toThrow('像素 Worker 运行失败');
    FakeWorker.instances[0].onerror?.(); await rejected;
    expect(FakeWorker.instances[0].terminate).toHaveBeenCalledOnce();
  });
});

describe('shared layer raster results', () => {
  it('reuses a staged result through the real layer, render, thumbnail and pixel sampling', async () => {
    const {bitmaps, layer} = fixture(), {createElement, contexts} = mockCanvases();
    const stagedLayer = {...layer, chromaKey: structuredClone(layer.chromaKey)};
    const prepared = prepareLayerRaster(stagedLayer, bitmaps);
    expect(getPreparedLayerRaster(layer, bitmaps)).toBeUndefined();
    FakeWorker.instances[0].complete(); await prepared;
    const algorithm = vi.spyOn(chroma, 'applyChromaKey');
    const canvas = layerRasterCanvas(layer, bitmaps); layer.transform.e = 100;
    expect(layerRasterCanvas({...layer}, bitmaps)).toBe(canvas);
    expect(createElement).toHaveBeenCalledOnce();
    expect(contexts[0].putImageData.mock.calls[0][0].data[3]).toBe(0);
    expect(readLayerPixel(layer, bitmaps, .9, .2)?.a).toBe(0);
    expect(readLayerPixel(layer, bitmaps, 0, 0, true)?.a).toBe(255);
    expect(readLayerPixel(layer, bitmaps, 2, 0)).toBeNull();
    expect(layerImageData(layer, bitmaps).data[3]).toBe(0);
    await prepareLayersRaster([layer], bitmaps);
    expect(FakeWorker.instances).toHaveLength(1);
    expect(algorithm).not.toHaveBeenCalled();
  });
  it('never executes an unprepared effect during synchronous render or pixel sampling', () => {
    const {bitmaps, layer} = fixture(); mockCanvases();
    const algorithm = vi.spyOn(chroma, 'applyChromaKey');
    layerRasterCanvas(layer, bitmaps);
    expect(readLayerPixel(layer, bitmaps, 0, 0)?.a).toBe(255);
    expect(algorithm).not.toHaveBeenCalled();
    expect(getPreparedLayerRaster(layer, bitmaps)).toBeUndefined();
  });
  it('returns owned pixels so fill operations and async callers cannot corrupt the cache', async () => {
    const {bitmaps, layer} = fixture();
    const prepared = prepareLayerRaster(layer, bitmaps); FakeWorker.instances[0].complete();
    const first = await prepared; first.data[3] = 77;
    const mutable = layerImageData(layer, bitmaps); mutable.data.fill(0);
    const alsoMutable = getPreparedLayerRaster(layer, bitmaps)!; alsoMutable.data[7] = 33;
    expect([...layerImageData(layer, bitmaps).data]).toEqual([0, 255, 0, 0, 255, 0, 0, 255]);
    expect(bitmaps.getImageData(layer.bitmapId).data[3]).toBe(255);
    layer.chromaKey = null;
    layerImageData(layer, bitmaps).data.fill(0);
    expect(readLayerPixel(layer, bitmaps, 0, 0)?.a).toBe(255);
    expect(bitmaps.getImageData(layer.bitmapId).data[3]).toBe(255);
  });
  it('warms render pixels without returning a mutable buffer and shares cancellation with owned callers', async () => {
    const {bitmaps, layer} = fixture(), controller = new AbortController();
    const warmed = warmLayerRaster(layer, bitmaps, {signal: controller.signal});
    const canceled = expect(warmed).rejects.toMatchObject({name: 'AbortError'});
    const owned = prepareLayerRaster(layer, bitmaps);
    expect(FakeWorker.instances).toHaveLength(1);
    controller.abort(); await canceled;
    expect(FakeWorker.instances[0].terminate).not.toHaveBeenCalled();
    FakeWorker.instances[0].complete();
    const image = await owned; image.data[3] = 99;
    expect(await warmLayerRaster(layer, bitmaps)).toBeUndefined();
    expect(readLayerPixel(layer, bitmaps, 0, 0)?.a).toBe(0);
    expect(FakeWorker.instances).toHaveLength(1);
  });

  it('keeps shared work running if another consumer still needs it', async () => {
    const {bitmaps, layer} = fixture(), firstController = new AbortController();
    const first = prepareLayerRaster(layer, bitmaps, {signal: firstController.signal});
    const rejected = expect(first).rejects.toMatchObject({name: 'AbortError'});
    const second = prepareLayerRaster({...layer}, bitmaps);
    expect(FakeWorker.instances).toHaveLength(1);
    firstController.abort(); await rejected;
    expect(FakeWorker.instances[0].terminate).not.toHaveBeenCalled();
    FakeWorker.instances[0].complete(); expect((await second).data[3]).toBe(0);
  });
  it('cancels obsolete work when its last consumer aborts and permits replacement work', async () => {
    const {bitmaps, layer} = fixture(), controller = new AbortController();
    const old = prepareLayerRaster(layer, bitmaps, {signal: controller.signal});
    const rejected = expect(old).rejects.toMatchObject({name: 'AbortError'});
    controller.abort(); await rejected;
    expect(FakeWorker.instances[0].terminate).toHaveBeenCalledOnce();
    const replacement = prepareLayerRaster(layer, bitmaps);
    expect(FakeWorker.instances).toHaveLength(2);
    FakeWorker.instances[1].complete(); await replacement;
    expect(getPreparedLayerRaster(layer, bitmaps)?.data[3]).toBe(0);
  });
  it('rejects results for edited or restored resources, even with the same id and revision', async () => {
    const {bitmaps, layer, image} = fixture();
    const old = prepareLayerRaster(layer, bitmaps);
    const rejected = expect(old).rejects.toMatchObject({name: 'AbortError'});
    bitmaps.restore(bitmaps.snapshot()); FakeWorker.instances[0].complete(); await rejected;
    expect(getPreparedLayerRaster(layer, bitmaps)).toBeUndefined();
    const edited = prepareLayerRaster(layer, bitmaps);
    const editedRejected = expect(edited).rejects.toMatchObject({name: 'AbortError'});
    bitmaps.update(layer.bitmapId, image); FakeWorker.instances[1].complete(); await editedRejected;
    expect(getPreparedLayerRaster(layer, bitmaps)).toBeUndefined();
  });
  it('supports atomic bake commits with results attached to the new bitmap revision', () => {
    const {bitmaps, layer, image} = fixture();
    const output = chroma.applyChromaKey(image, layer.chromaKey!);
    bitmaps.update(layer.bitmapId, image); cacheLayerRaster({...layer}, bitmaps, output); output.data[3] = 123;
    const algorithm = vi.spyOn(chroma, 'applyChromaKey');
    expect(layerImageData(layer, bitmaps).data[3]).toBe(0);
    expect(algorithm).not.toHaveBeenCalled();
    layer.chromaKey!.tolerance = 100;
    expect(getPreparedLayerRaster(layer, bitmaps)).toBeUndefined();
  });
  it('limits previews to three effects while retaining the committed effect and evicting by use', async () => {
    const {bitmaps, layer} = fixture(); mockCanvases();
    const variants = [0, 10, 20, 30, 40].map(tolerance => ({...layer, chromaKey: {...layer.chromaKey!, tolerance}}));
    const prepare = async (variant: RasterLayer) => {
      const result = prepareLayerRaster(variant, bitmaps); FakeWorker.instances.at(-1)!.complete(); await result;
    };
    for (const variant of variants.slice(0, 4)) await prepare(variant);
    expect(getPreparedLayerRaster(variants[0], bitmaps)).toBeDefined();
    expect(getPreparedLayerRaster(variants[1], bitmaps)).toBeUndefined();
    // Accessing an older preview refreshes it; the other preview is then evicted.
    layerRasterCanvas(variants[2], bitmaps);
    await prepare(variants[4]);
    expect(getPreparedLayerRaster(variants[0], bitmaps)).toBeDefined();
    expect(getPreparedLayerRaster(variants[2], bitmaps)).toBeDefined();
    expect(getPreparedLayerRaster(variants[3], bitmaps)).toBeUndefined();
    expect(getPreparedLayerRaster(variants[4], bitmaps)).toBeDefined();
    // Committing another preview releases the old committed signature for eviction.
    retainPreparedLayerRaster(variants[4], bitmaps);
    layerRasterCanvas(variants[2], bitmaps);
    await prepare(variants[1]);
    expect(getPreparedLayerRaster(variants[0], bitmaps)).toBeUndefined();
    expect(getPreparedLayerRaster(variants[4], bitmaps)).toBeDefined();
  });

  it('retains the exact synchronous fallback for model operations and standalone exports', () => {
    const {bitmaps, layer} = fixture(), {contexts} = mockCanvases();
    const documentState = {} as Parameters<typeof rasterizeLayers>[0];
    rasterizeLayers(documentState, bitmaps, {x: 0, y: 0, width: 2, height: 1}, [layer]);
    expect(contexts[1].putImageData.mock.calls[0][0].data[3]).toBe(0);
    expect(layerImageData(layer, bitmaps).data[3]).toBe(0);
  });
});
