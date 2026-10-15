import type {ChromaKeyEffect} from '../model/canvas-types';
import {applyChromaKey} from './chroma-key';

let revision = 0;
export interface PixelProcessingOptions { signal?: AbortSignal }

export function serializableChromaEffect(effect: ChromaKeyEffect): ChromaKeyEffect {
  return {
    keyColor: {r: effect.keyColor.r, g: effect.keyColor.g, b: effect.keyColor.b},
    tolerance: effect.tolerance,
    softness: effect.softness,
    mode: effect.mode,
    seedMode: effect.seedMode,
    connectivity: effect.connectivity,
    spillSuppression: effect.spillSuppression,
    inverted: effect.inverted,
    seedPoints: (effect.seedPoints || []).map(point => ({x: point.x, y: point.y})),
    enabled: effect.enabled
  };
}

export function pixelProcessingAbortError() {
  return new DOMException('像素处理已取消', 'AbortError');
}

/** A separate worker lets cancellation stop a running flood fill immediately. */
export async function applyChromaKeyAsync(source: ImageData, effect: ChromaKeyEffect, {signal}: PixelProcessingOptions = {}) {
  if (signal?.aborted) throw pixelProcessingAbortError();
  const workerEffect = serializableChromaEffect(effect);
  // The fallback supports non-browser callers; browser canvas operations use a Worker.
  if (typeof Worker === 'undefined') return applyChromaKey(source, workerEffect);
  const current = ++revision;
  const worker = new Worker(new URL('./pixel-worker.ts', import.meta.url), {type: 'module'});
  let terminated = false;
  const terminate = () => { if (!terminated) { terminated = true; worker.terminate(); } };
  try {
    return await new Promise<ImageData>((resolve, reject) => {
      let settled = false;
      const finish = (output?: ImageData, error?: unknown) => {
        if (settled) return;
        settled = true;
        signal?.removeEventListener('abort', abort);
        worker.onmessage = null;
        worker.onerror = null;
        worker.onmessageerror = null;
        if (error) reject(error);
        else resolve(output!);
      };
      const abort = () => {
        terminate();
        finish(undefined, pixelProcessingAbortError());
      };
      signal?.addEventListener('abort', abort, {once: true});
      worker.onmessage = (event: MessageEvent<{ok: boolean; revision: number; width?: number; height?: number; data?: ArrayBuffer; error?: string}>) => {
        const response = event.data;
        if (response.revision !== current || settled) return;
        if (!response.ok || !response.data || response.width !== source.width || response.height !== source.height || response.data.byteLength !== source.width * source.height * 4) {
          finish(undefined, new Error(response.error || '像素 Worker 处理失败'));
          return;
        }
        try {
          finish(new ImageData(new Uint8ClampedArray(response.data), response.width, response.height));
        } catch (error) {
          finish(undefined, error);
        }
      };
      worker.onerror = () => finish(undefined, new Error('像素 Worker 运行失败'));
      worker.onmessageerror = () => finish(undefined, new Error('像素 Worker 返回了无效数据'));
      try {
        const data = source.data.slice().buffer;
        worker.postMessage({revision: current, width: source.width, height: source.height, data, effect: workerEffect}, [data]);
      } catch (error) {
        finish(undefined, error);
      }
    });
  } finally {
    terminate();
  }
}
