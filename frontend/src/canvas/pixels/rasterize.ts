import type {BitmapSnapshot, BitmapStore} from '../model/bitmap-store';
import type {CanvasDocument, ChromaKeyEffect, RasterLayer, Rect} from '../model/canvas-types';
import {applyChromaKey} from './chroma-key';
import {applyChromaKeyAsync, pixelProcessingAbortError, serializableChromaEffect, type PixelProcessingOptions} from './pixel-client';

interface PreparedRaster { image: ImageData; canvas?: HTMLCanvasElement }
interface PendingRaster { controller: AbortController; promise: Promise<ImageData>; consumers: Set<symbol> }
interface BitmapRasterCache { revision: number; ready: Map<string, PreparedRaster>; pending: Map<string, PendingRaster>; retainedSignature?: string }
const bitmapRasterCache = new WeakMap<BitmapSnapshot, BitmapRasterCache>();
const MAX_EFFECT_CACHE_ENTRIES = 3;

function bitmapCache(bitmap: BitmapSnapshot) {
  let cached = bitmapRasterCache.get(bitmap);
  if (!cached || cached.revision !== bitmap.revision) {
    if (cached) for (const pending of cached.pending.values()) pending.controller.abort();
    cached = {revision: bitmap.revision, ready: new Map(), pending: new Map()};
    bitmapRasterCache.set(bitmap, cached);
  }
  return cached;
}
function effectSignature(effect: ChromaKeyEffect | null) {
  return effect?.enabled ? JSON.stringify(serializableChromaEffect(effect)) : '';
}
function copyImage(image: ImageData) {
  return new ImageData(new Uint8ClampedArray(image.data), image.width, image.height);
}
function rememberRaster(cache: BitmapRasterCache, signature: string, image: ImageData) {
  const prepared: PreparedRaster = {image};
  cache.ready.delete(signature);
  cache.ready.set(signature, prepared);
  // Bound full-size pixel/canvas memory, counting the committed effect in this limit.
  if (signature && cache.retainedSignature === undefined) cache.retainedSignature = signature;
  const effects = [...cache.ready.keys()].filter(key => key !== '');
  while (effects.length > MAX_EFFECT_CACHE_ENTRIES) {
    const index = effects.findIndex(key => key !== cache.retainedSignature);
    if (index < 0) break;
    cache.ready.delete(effects.splice(index, 1)[0]);
  }
  return prepared;
}
function preparedRaster(cache: BitmapRasterCache, signature: string) {
  const prepared = cache.ready.get(signature);
  if (prepared) { cache.ready.delete(signature); cache.ready.set(signature, prepared); }
  return prepared;
}
function rawRaster(bitmap: BitmapSnapshot, cache: BitmapRasterCache) {
  const cached = preparedRaster(cache, '');
  if (cached) return cached;
  // BitmapStore updates replace data and advance revision; only owned copies leave this cache.
  return rememberRaster(cache, '', new ImageData(bitmap.data, bitmap.width, bitmap.height));
}

/** Externally supplied results are copied so later painting cannot mutate the cache. */
export function cacheLayerRaster(layer: RasterLayer, bitmaps: BitmapStore, image: ImageData) {
  const bitmap = bitmaps.get(layer.bitmapId);
  if (bitmap.width !== image.width || bitmap.height !== image.height) throw new Error('图层像素尺寸不匹配');
  const cache = bitmapCache(bitmap), signature = effectSignature(layer.chromaKey);
  cache.retainedSignature = signature;
  rememberRaster(cache, signature, copyImage(image));
}

/** Keep the committed result available while temporary slider previews replace one another. */
export function retainPreparedLayerRaster(layer: RasterLayer, bitmaps: BitmapStore) {
  const cache = bitmapCache(bitmaps.get(layer.bitmapId)), signature = effectSignature(layer.chromaKey);
  if (cache.ready.has(signature)) cache.retainedSignature = signature;
}

/** Returns owned pixels; mutable paint/fill operations must never receive cached storage. */
export function getPreparedLayerRaster(layer: RasterLayer, bitmaps: BitmapStore): ImageData | undefined {
  const bitmap = bitmaps.get(layer.bitmapId);
  const prepared = preparedRaster(bitmapCache(bitmap), effectSignature(layer.chromaKey));
  return prepared ? copyImage(prepared.image) : undefined;
}

function subscribeToRaster(pending: PendingRaster, signal?: AbortSignal, copyResult = true) {
  return new Promise<ImageData>((resolve, reject) => {
    const token = Symbol();
    let settled = false;
    pending.consumers.add(token);
    const finish = (image?: ImageData, error?: unknown) => {
      if (settled) return;
      settled = true;
      signal?.removeEventListener('abort', abort);
      pending.consumers.delete(token);
      if (error) reject(error);
      else resolve(copyResult ? copyImage(image!) : image!);
    };
    const abort = () => {
      finish(undefined, pixelProcessingAbortError());
      if (!pending.consumers.size) pending.controller.abort();
    };
    signal?.addEventListener('abort', abort, {once: true});
    if (signal?.aborted) abort();
    pending.promise.then(image => finish(image), error => finish(undefined, error));
  });
}

async function prepareRaster(layer: RasterLayer, bitmaps: BitmapStore, {signal}: PixelProcessingOptions = {}, copyResult = true): Promise<ImageData> {
  if (signal?.aborted) throw pixelProcessingAbortError();
  const bitmap = bitmaps.get(layer.bitmapId), cache = bitmapCache(bitmap);
  const effect = layer.chromaKey ? serializableChromaEffect(layer.chromaKey) : null;
  const signature = effectSignature(effect), ready = preparedRaster(cache, signature);
  if (ready) return copyResult ? copyImage(ready.image) : ready.image;
  if (!signature || !effect) { const image = rawRaster(bitmap, cache).image; return copyResult ? copyImage(image) : image; }
  let pending = cache.pending.get(signature);
  if (pending?.controller.signal.aborted) pending = undefined;
  if (!pending) {
    const controller = new AbortController(), revision = bitmap.revision;
    const job: PendingRaster = {controller, consumers: new Set(), promise: Promise.resolve(null as unknown as ImageData)};
    // The worker client copies its source before transferring; this view avoids another full-image copy.
    const source = new ImageData(bitmap.data, bitmap.width, bitmap.height);
    job.promise = applyChromaKeyAsync(source, effect, {signal: controller.signal}).then(image => {
      let currentBitmap: BitmapSnapshot | undefined;
      try { currentBitmap = bitmaps.get(bitmap.id); } catch { /* Removed layers invalidate their pending results. */ }
      if (controller.signal.aborted || bitmap.revision !== revision || currentBitmap !== bitmap || bitmapRasterCache.get(bitmap) !== cache) throw pixelProcessingAbortError();
      rememberRaster(cache, signature, image);
      return image;
    }).finally(() => {
      if (cache.pending.get(signature) === job) cache.pending.delete(signature);
    });
    cache.pending.set(signature, job);
    pending = job;
  }
  return subscribeToRaster(pending, signal, copyResult);
}

/** Return owned pixels for operations that bake or modify the visible raster. */
export function prepareLayerRaster(layer: RasterLayer, bitmaps: BitmapStore, options: PixelProcessingOptions = {}): Promise<ImageData> {
  return prepareRaster(layer, bitmaps, options);
}

/** Prepare render/thumbnail/export pixels without copying a full image back to a caller. */
export async function warmLayerRaster(layer: RasterLayer, bitmaps: BitmapStore, options: PixelProcessingOptions = {}): Promise<void> {
  await prepareRaster(layer, bitmaps, options, false);
}

export async function prepareLayersRaster(layers: RasterLayer[], bitmaps: BitmapStore, options: PixelProcessingOptions = {}) {
  if (options.signal?.aborted) throw pixelProcessingAbortError();
  await Promise.all(layers.map(layer => {
    const bitmap = bitmaps.get(layer.bitmapId), cache = bitmapCache(bitmap), signature = effectSignature(layer.chromaKey);
    if (cache.ready.has(signature)) { cache.retainedSignature = signature; return; }
    if (!signature) { rawRaster(bitmap, cache); cache.retainedSignature = signature; return; }
    return warmLayerRaster(layer, bitmaps, options).then(() => { cache.retainedSignature = signature; });
  }));
}

/** Synchronous model operations remain exact, including callers outside the canvas UI. */
export function layerImageData(layer: RasterLayer, bitmaps: BitmapStore) {
  const bitmap = bitmaps.get(layer.bitmapId), cache = bitmapCache(bitmap), signature = effectSignature(layer.chromaKey);
  const ready = preparedRaster(cache, signature);
  if (ready) return copyImage(ready.image);
  if (!signature || !layer.chromaKey) return copyImage(rawRaster(bitmap, cache).image);
  const image = applyChromaKey(new ImageData(bitmap.data, bitmap.width, bitmap.height), layer.chromaKey);
  rememberRaster(cache, signature, image);
  return copyImage(image);
}

/** Read a single pixel without cloning a whole image or processing an unprepared effect. */
export function readLayerPixel(layer: RasterLayer, bitmaps: BitmapStore, x: number, y: number, raw = false) {
  const bitmap = bitmaps.get(layer.bitmapId);
  x = Math.floor(x); y = Math.floor(y);
  if (!Number.isFinite(x) || !Number.isFinite(y) || x < 0 || y < 0 || x >= bitmap.width || y >= bitmap.height) return null;
  const prepared = raw ? undefined : preparedRaster(bitmapCache(bitmap), effectSignature(layer.chromaKey));
  const data = prepared?.image.data ?? bitmap.data, offset = (y * bitmap.width + x) * 4;
  return {r: data[offset], g: data[offset + 1], b: data[offset + 2], a: data[offset + 3]};
}

export function imageDataCanvas(image: ImageData) {
  const canvas = document.createElement('canvas');
  canvas.width = image.width; canvas.height = image.height;
  canvas.getContext('2d')?.putImageData(image, 0, 0);
  return canvas;
}

/** Render only prepared pixels. Restored documents are prepared asynchronously by the workspace. */
export function layerRasterCanvas(layer: RasterLayer, bitmaps: BitmapStore): HTMLCanvasElement {
  const bitmap = bitmaps.get(layer.bitmapId), cache = bitmapCache(bitmap);
  const ready = preparedRaster(cache, effectSignature(layer.chromaKey)) ?? rawRaster(bitmap, cache);
  return ready.canvas ??= imageDataCanvas(ready.image);
}

export function rasterizeLayers(documentState: CanvasDocument, bitmaps: BitmapStore, frame: Rect, layers: RasterLayer[], scale = 1) {
  void documentState;
  const canvas = document.createElement('canvas');
  canvas.width = Math.max(1, Math.round(frame.width * scale)); canvas.height = Math.max(1, Math.round(frame.height * scale));
  const context = canvas.getContext('2d');
  if (!context) throw new Error('当前浏览器无法创建导出画布');
  context.scale(scale, scale); context.translate(-frame.x, -frame.y);
  for (const layer of layers) {
    // Standalone synchronous exports remain exact; UI export paths prewarm this cache asynchronously.
    const bitmap = bitmaps.get(layer.bitmapId), cache = bitmapCache(bitmap);
    if (layer.chromaKey?.enabled && !cache.ready.has(effectSignature(layer.chromaKey))) layerImageData(layer, bitmaps);
    const source = layerRasterCanvas(layer, bitmaps), t = layer.transform;
    context.save(); context.globalAlpha = layer.opacity;
    context.transform(t.a, t.b, t.c, t.d, t.e, t.f);
    context.drawImage(source, layer.bounds.x, layer.bounds.y, layer.bounds.width, layer.bounds.height);
    context.restore();
  }
  return canvas;
}
export function canvasToBlob(canvas: HTMLCanvasElement) {
  return new Promise<Blob>((resolve, reject) => canvas.toBlob(blob => blob ? resolve(blob) : reject(new Error('PNG 编码失败')), 'image/png'));
}
