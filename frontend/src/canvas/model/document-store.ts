import type {BitmapStore} from './bitmap-store';
import type {CanvasDocument, CostumeEntry, RasterLayer, Rect} from './canvas-types';
import {IDENTITY_MATRIX} from './canvas-types';
import {createId} from './create-id';
import {layerImageData} from '../pixels/rasterize';
import {transformedRectBounds} from './interaction-geometry';

export function activeLayer(document: CanvasDocument) { return document.layers.find(layer => layer.id === document.activeLayerIds[0]) ?? null; }

export function hitTestLayer(document: CanvasDocument, bitmaps: BitmapStore, point: {x: number; y: number}, raw = false) {
  for (let index = document.layers.length - 1; index >= 0; index -= 1) {
    const layer = document.layers[index];
    if (!layer.visible || layer.opacity <= 0) continue;
    const t = layer.transform;
    const determinant = t.a * t.d - t.b * t.c;
    if (Math.abs(determinant) < 1e-8) continue;
    const localX = (t.d * (point.x - t.e) - t.c * (point.y - t.f)) / determinant;
    const localY = (-t.b * (point.x - t.e) + t.a * (point.y - t.f)) / determinant;
    if (localX < layer.bounds.x || localY < layer.bounds.y || localX >= layer.bounds.x + layer.bounds.width || localY >= layer.bounds.y + layer.bounds.height) continue;
    const image = raw ? bitmaps.get(layer.bitmapId) : layerImageData(layer, bitmaps);
    const x = Math.floor((localX - layer.bounds.x) * image.width / layer.bounds.width);
    const y = Math.floor((localY - layer.bounds.y) * image.height / layer.bounds.height);
    if (image.data[(y * image.width + x) * 4 + 3] > 0) return layer;
  }
  return null;
}
export function duplicateLayer(document: CanvasDocument, bitmaps: BitmapStore) {
  const source = activeLayer(document); if (!source) throw new Error('请先选择图层'); const now = Date.now();
  const baseName = source.name.replace(/ 副本(?: \d+)?$/, '') + ' 副本';
  const usedNames = new Set([...document.layers.map(item => item.name), ...document.costumeEntries.map(entry => entry.name)]);
  let copyName = baseName;
  for (let suffix = 2; usedNames.has(copyName); suffix += 1) copyName = baseName + ' ' + suffix;
  // Vue makes document layers reactive proxies, which cannot be passed to structuredClone.
  const layer: RasterLayer = {
    ...source,
    id: createId(),
    bitmapId: bitmaps.clone(source.bitmapId),
    name: copyName,
    bounds: {...source.bounds},
    transform: {...source.transform},
    chromaKey: source.chromaKey ? {
      ...source.chromaKey,
      keyColor: {...source.chromaKey.keyColor},
      seedPoints: source.chromaKey.seedPoints.map(point => ({...point}))
    } : null,
    locked: false,
    createdAt: now,
    updatedAt: now
  };
  const index = document.layers.findIndex(item => item.id === source.id); document.layers.splice(index + 1, 0, layer); document.activeLayerIds = [layer.id]; return layer;
}
export function copySelectionToLayer(document: CanvasDocument, bitmaps: BitmapStore, cut = false) {
  const source = activeLayer(document); const selection = document.selection?.rect; if (!source || !selection) throw new Error('请先选择图层并框选区域');
  if (selection.width <= 0 || selection.height <= 0) throw new Error('选区尺寸必须大于 0');
  if (cut && source.locked) throw new Error('锁定图层不能剪切');
  const sourceImage = layerImageData(source, bitmaps); const t = source.transform; const determinant = t.a * t.d - t.b * t.c; if (Math.abs(determinant) < 1e-8) throw new Error('图层变换不可逆，请先重置变换');
  const bitmapScaleX = Math.min(4, Math.max(1, Math.hypot(t.d / determinant * sourceImage.width / source.bounds.width, -t.b / determinant * sourceImage.height / source.bounds.height)));
  const bitmapScaleY = Math.min(4, Math.max(1, Math.hypot(-t.c / determinant * sourceImage.width / source.bounds.width, t.a / determinant * sourceImage.height / source.bounds.height)));
  const width = Math.max(1, Math.round(selection.width * bitmapScaleX)); const height = Math.max(1, Math.round(selection.height * bitmapScaleY)); const output = new ImageData(width, height);
  const toBitmap = (x: number, y: number) => {
    const localX = (t.d * (x - t.e) - t.c * (y - t.f)) / determinant;
    const localY = (-t.b * (x - t.e) + t.a * (y - t.f)) / determinant;
    return {x: (localX - source.bounds.x) * sourceImage.width / source.bounds.width, y: (localY - source.bounds.y) * sourceImage.height / source.bounds.height};
  };
  for (let y = 0; y < height; y += 1) for (let x = 0; x < width; x += 1) {
    const worldX = selection.x + (x + 0.5) * selection.width / width; const worldY = selection.y + (y + 0.5) * selection.height / height;
    const bitmap = toBitmap(worldX, worldY); const sx = Math.floor(bitmap.x); const sy = Math.floor(bitmap.y); if (sx < 0 || sy < 0 || sx >= sourceImage.width || sy >= sourceImage.height) continue;
    const from = (sy * sourceImage.width + sx) * 4; const to = (y * width + x) * 4; output.data.set(sourceImage.data.subarray(from, from + 4), to);
  }
  if (cut) {
    // Bake the visible result before removing pixels. A connected key may lose
    // its seed when cut, so reevaluating it against the raw bitmap could reveal
    // background pixels that were already transparent.
    const sourceRaw = new ImageData(new Uint8ClampedArray(sourceImage.data), sourceImage.width, sourceImage.height);
    const right = selection.x + selection.width; const bottom = selection.y + selection.height;
    for (let sy = 0; sy < sourceRaw.height; sy += 1) for (let sx = 0; sx < sourceRaw.width; sx += 1) {
      const localX = source.bounds.x + (sx + 0.5) * source.bounds.width / sourceRaw.width; const localY = source.bounds.y + (sy + 0.5) * source.bounds.height / sourceRaw.height;
      const worldX = t.a * localX + t.c * localY + t.e; const worldY = t.b * localX + t.d * localY + t.f;
      if (worldX >= selection.x && worldX < right && worldY >= selection.y && worldY < bottom) sourceRaw.data[(sy * sourceRaw.width + sx) * 4 + 3] = 0;
    }
    bitmaps.update(source.bitmapId, sourceRaw);
    source.chromaKey = null;
    source.updatedAt = Date.now();
  }
  const bitmapId = bitmaps.put(output); const now = Date.now(); const index = document.costumeEntries.length + 1; const frameRect = {...selection};
  const layer: RasterLayer = {id: createId(), name: '造型' + index, bitmapId, bounds: frameRect, transform: {...IDENTITY_MATRIX}, visible: true, locked: false, opacity: 1, blendMode: 'normal', chromaKey: null, createdAt: now, updatedAt: now};
  document.layers.splice(document.layers.findIndex(item => item.id === source.id) + 1, 0, layer);
  const entry: CostumeEntry = {id: createId(), layerId: layer.id, name: layer.name, order: document.costumeEntries.length, frameRect: {...frameRect}, pivot: {x: frameRect.x + frameRect.width / 2, y: frameRect.y + frameRect.height / 2}, alignment: 'center', exportScale: 2, enabled: true}; document.costumeEntries.push(entry); return {layer, entry};
}
export function cropActiveLayer(document: CanvasDocument, bitmaps: BitmapStore, requestedRect: Rect) {
  const layer = activeLayer(document);
  if (!layer) throw new Error('请先选择图层');
  if (layer.locked) throw new Error('请先解锁当前图层');
  if (requestedRect.width <= 0 || requestedRect.height <= 0) throw new Error('裁剪范围必须大于 0');

  const frame = intersectRect(requestedRect, transformedRectBounds(layer.bounds, layer.transform));
  if (!frame) throw new Error('裁剪范围必须与当前图层相交');
  const source = layerImageData(layer, bitmaps);
  const t = layer.transform;
  const determinant = t.a * t.d - t.b * t.c;
  if (Math.abs(determinant) < 1e-8) throw new Error('图层变换不可逆，请先重置变换');

  const scaleX = Math.min(4, Math.max(1, Math.hypot(t.d / determinant * source.width / layer.bounds.width, -t.b / determinant * source.height / layer.bounds.height)));
  const scaleY = Math.min(4, Math.max(1, Math.hypot(-t.c / determinant * source.width / layer.bounds.width, t.a / determinant * source.height / layer.bounds.height)));
  const width = Math.max(1, Math.round(frame.width * scaleX));
  const height = Math.max(1, Math.round(frame.height * scaleY));
  const output = new ImageData(width, height);

  for (let y = 0; y < height; y += 1) for (let x = 0; x < width; x += 1) {
    const worldX = frame.x + (x + 0.5) * frame.width / width;
    const worldY = frame.y + (y + 0.5) * frame.height / height;
    const localX = (t.d * (worldX - t.e) - t.c * (worldY - t.f)) / determinant;
    const localY = (-t.b * (worldX - t.e) + t.a * (worldY - t.f)) / determinant;
    const sourceX = Math.floor((localX - layer.bounds.x) * source.width / layer.bounds.width);
    const sourceY = Math.floor((localY - layer.bounds.y) * source.height / layer.bounds.height);
    if (sourceX < 0 || sourceY < 0 || sourceX >= source.width || sourceY >= source.height) continue;
    const from = (sourceY * source.width + sourceX) * 4;
    output.data.set(source.data.subarray(from, from + 4), (y * width + x) * 4);
  }

  bitmaps.update(layer.bitmapId, output);
  layer.bounds = {...frame};
  layer.transform = {...IDENTITY_MATRIX};
  layer.chromaKey = null;
  layer.updatedAt = Date.now();
  for (const entry of document.costumeEntries.filter(item => item.layerId === layer.id)) {
    entry.frameRect = {...frame};
    entry.alignment = 'custom';
  }
  document.selection = null;
  return frame;
}

export function applyLayerTransform(document: CanvasDocument, bitmaps: BitmapStore) {
  const layer = activeLayer(document); if (!layer) throw new Error('请先选择图层'); if (layer.locked) throw new Error('请先解锁当前图层');
  const image = layerImageData(layer, bitmaps); const t = layer.transform; const b = layer.bounds;
  const points = [{x:b.x,y:b.y},{x:b.x+b.width,y:b.y},{x:b.x,y:b.y+b.height},{x:b.x+b.width,y:b.y+b.height}].map(point=>({x:t.a*point.x+t.c*point.y+t.e,y:t.b*point.x+t.d*point.y+t.f}));
  const minX=Math.floor(Math.min(...points.map(point=>point.x))),minY=Math.floor(Math.min(...points.map(point=>point.y))),maxX=Math.ceil(Math.max(...points.map(point=>point.x))),maxY=Math.ceil(Math.max(...points.map(point=>point.y)));
  const canvas=globalThis.document.createElement('canvas');canvas.width=Math.max(1,maxX-minX);canvas.height=Math.max(1,maxY-minY);const context=canvas.getContext('2d');if(!context)throw new Error('无法应用图层变换');
  const source=globalThis.document.createElement('canvas');source.width=image.width;source.height=image.height;source.getContext('2d')?.putImageData(image,0,0);context.translate(-minX,-minY);context.transform(t.a,t.b,t.c,t.d,t.e,t.f);context.drawImage(source,b.x,b.y,b.width,b.height);
  const result=context.getImageData(0,0,canvas.width,canvas.height);bitmaps.update(layer.bitmapId,result);layer.bounds={x:minX,y:minY,width:canvas.width,height:canvas.height};layer.transform={...IDENTITY_MATRIX};layer.chromaKey=null;layer.updatedAt=Date.now();
}
export function reorderLayer(document: CanvasDocument, layerId: string, direction: -1 | 1) { const index=document.layers.findIndex(layer=>layer.id===layerId),target=index+direction;if(index<0||target<0||target>=document.layers.length)return;[document.layers[index],document.layers[target]]=[document.layers[target],document.layers[index]]; }
export function deleteActiveLayer(document: CanvasDocument, bitmaps: BitmapStore) { const layer = activeLayer(document); if (!layer) return; document.layers = document.layers.filter(item => item.id !== layer.id); document.costumeEntries = document.costumeEntries.filter(entry => entry.layerId !== layer.id).map((entry, order) => ({...entry, order})); bitmaps.remove(layer.bitmapId); document.activeLayerIds = document.layers.length ? [document.layers.at(-1)!.id] : []; }
export function unifyCostumeFrames(document: CanvasDocument, alignment: 'center' | 'bottom-center') { const entries = document.costumeEntries.filter(entry => entry.enabled); if (!entries.length) return; const width = Math.max(...entries.map(entry => entry.frameRect.width)); const height = Math.max(...entries.map(entry => entry.frameRect.height)); for (const entry of entries) { const old = entry.frameRect; const x = alignment === 'center' ? old.x - (width - old.width) / 2 : old.x + old.width / 2 - width / 2; const y = alignment === 'center' ? old.y - (height - old.height) / 2 : old.y + old.height - height; entry.frameRect = {x, y, width, height}; entry.pivot = alignment === 'center' ? {x: x + width / 2, y: y + height / 2} : {x: x + width / 2, y: y + height}; entry.alignment = alignment; } }
export function intersectRect(a: Rect, b: Rect) { const x = Math.max(a.x, b.x); const y = Math.max(a.y, b.y); const right = Math.min(a.x + a.width, b.x + b.width); const bottom = Math.min(a.y + a.height, b.y + b.height); return right > x && bottom > y ? {x, y, width: right - x, height: bottom - y} : null; }
