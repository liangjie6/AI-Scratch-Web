import type {CanvasDocument, CanvasInput, RasterLayer} from '../model/canvas-types';
import {IDENTITY_MATRIX, sanitizeFileName} from '../model/canvas-types';
import type {BitmapStore} from '../model/bitmap-store';
import {createId} from '../model/create-id';
const MAX_EDGE = 8192; const MAX_PIXELS = 32_000_000;
function base64ToBlob(base64: string, mimeType: string) { const binary = atob(base64); const bytes = new Uint8Array(binary.length); for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index); return new Blob([bytes], {type: mimeType}); }
export async function importCanvasImage(input: CanvasInput, bitmapStore: BitmapStore): Promise<CanvasDocument> {
  const blob = input.kind === 'blob' ? input.blob : base64ToBlob(input.base64, input.mimeType); if (!['image/png', 'image/jpeg', 'image/webp'].includes(blob.type)) throw new Error('仅支持 PNG、JPEG 和 WebP 图片');
  let bitmap: ImageBitmap; try { bitmap = await createImageBitmap(blob, {imageOrientation: 'from-image', colorSpaceConversion: 'default'}); } catch { bitmap = await createImageBitmap(blob); }
  if (bitmap.width > MAX_EDGE || bitmap.height > MAX_EDGE || bitmap.width * bitmap.height > MAX_PIXELS) { bitmap.close(); throw new Error('图片尺寸超过上限（最大单边 8192px、总像素 3200 万）'); }
  const canvas = document.createElement('canvas'); canvas.width = bitmap.width; canvas.height = bitmap.height; const context = canvas.getContext('2d', {willReadFrequently: true}); if (!context) { bitmap.close(); throw new Error('当前浏览器无法分配图片画布'); } context.drawImage(bitmap, 0, 0); if (input.kind === 'base64' && input.target === 'backdrop') { context.globalCompositeOperation = 'destination-over'; context.fillStyle = '#fff'; context.fillRect(0, 0, canvas.width, canvas.height); } bitmap.close();
  const imageData = context.getImageData(0, 0, canvas.width, canvas.height); const bitmapId = bitmapStore.put(imageData); const now = Date.now();
  const target = input.kind === 'base64' ? input.target : undefined;
  const artboard = target === 'backdrop' ? {x: 0, y: 0, width: 480, height: 360} : {x: 0, y: 0, width: canvas.width, height: canvas.height};
  const layer: RasterLayer = {id: createId(), name: target === 'backdrop' ? '背景' : '原图', bitmapId, bounds: {...artboard}, transform: {...IDENTITY_MATRIX}, visible: true, locked: input.kind === 'base64', opacity: 1, blendMode: 'normal', chromaKey: null, createdAt: now, updatedAt: now};
  const rawName = input.kind === 'blob' ? input.fileName?.replace(/.[^.]+$/, '') : target === 'backdrop' ? 'AI 背景素材' : 'AI 角色素材';
  const costumeEntries = target === 'sprite' ? [{id: createId(), layerId: layer.id, name: 'AI 造型', order: 0, frameRect: {...layer.bounds}, pivot: {x: layer.bounds.x + layer.bounds.width / 2, y: layer.bounds.y + layer.bounds.height / 2}, alignment: 'center' as const, exportScale: 2, enabled: true}] : [];
  return {schemaVersion: 1, id: createId(), name: sanitizeFileName(rawName || '未命名画布'), createdAt: now, updatedAt: now, artboard, layers: [layer], costumeEntries, activeLayerIds: [layer.id], selection: null, viewport: {x: 0, y: 0, zoom: 1}, source: input.kind === 'base64' ? {kind: 'ai', requestId: input.requestId, processingHints: input.processingHints, targetType: target} : {kind: input.source ?? 'upload', fileName: input.fileName}};
}
