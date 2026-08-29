import {md5} from 'js-md5';
import type {ImageGenerationResponse, ImageTarget} from '../ai/image-generation-types';
import type {ScratchGuiBridge} from './scratch-gui-bridge';

export interface CroppedImage { blob: Blob; width: number; height: number; assetId?: string }
export interface InlineAssetWire { assetType: {contentType: 'image/png' | 'image/svg+xml'; name: 'ImageBitmap' | 'ImageVector'; runtimeFormat: 'png' | 'svg'; immutable: true}; dataFormat: 'png' | 'svg'; data: number[] }
export interface CostumeExport { name: string; blob: Blob; dataFormat: 'png' | 'svg'; assetWidth: number; assetHeight: number; rotationCenterX: number; rotationCenterY: number; bitmapResolution: 1 | 2 }
export interface SpriteExport { name: string; costumes: CostumeExport[]; initialSize: number; x?: number; y?: number }
export interface ImportedSpriteResult { targetId: string; costumeAssetIds: string[] }
export interface SpriteImportExpectation {name: string; assetId: string; bitmapResolution: 1 | 2; rotationCenterX: number; rotationCenterY: number}

export function getGeneratedImageRect(imageWidth: number, imageHeight: number) { return {x: 0, y: 0, width: imageWidth, height: imageHeight}; }
function base64ToBlob(base64: string, mimeType: string) { const binary = atob(base64); const bytes = new Uint8Array(binary.length); for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index); return new Blob([bytes], {type: mimeType}); }
function canvasBlob(canvas: HTMLCanvasElement) { return new Promise<Blob>((resolve, reject) => canvas.toBlob(blob => blob ? resolve(blob) : reject(new Error('图片编码失败')), 'image/png')); }

async function normalizeBackdrop(result: ImageGenerationResponse): Promise<CroppedImage> {
  const bitmap = await createImageBitmap(base64ToBlob(result.image_base64, result.mime_type)); const canvas = document.createElement('canvas'); canvas.width = result.requested_canvas.width * 2; canvas.height = result.requested_canvas.height * 2; const context = canvas.getContext('2d'); if (!context) { bitmap.close(); throw new Error('当前浏览器不支持图片裁切'); } context.imageSmoothingQuality = 'high'; context.drawImage(bitmap, 0, 0, bitmap.width, bitmap.height, 0, 0, canvas.width, canvas.height); bitmap.close(); return {blob: await canvasBlob(canvas), width: canvas.width, height: canvas.height};
}

export async function buildSpriteImport(sprite: SpriteExport) {
  if (!sprite.costumes.length) throw new Error('至少需要一个造型');
  const expectations: SpriteImportExpectation[] = []; const costumes = []; const usedNames = new Set<string>();
  for (const costume of sprite.costumes) {
    const baseName = costume.name.trim() || '造型'; let name = baseName;
    for (let suffix = 2; usedNames.has(name); suffix += 1) name = baseName + ' ' + suffix;
    usedNames.add(name);
    const vector=costume.dataFormat==='svg';if (!(costume.assetWidth > 0 && costume.assetHeight > 0) || costume.bitmapResolution !== (vector?1:2)) throw new Error(costume.name + ' 的资源规格无效');
    if (!Number.isFinite(costume.rotationCenterX) || !Number.isFinite(costume.rotationCenterY)) throw new Error(costume.name + ' 的旋转中心无效');
    const bytes = new Uint8Array(await costume.blob.arrayBuffer()); if (!vector&&(bytes.length < 8 || bytes[0] !== 137 || bytes[1] !== 80 || bytes[2] !== 78 || bytes[3] !== 71)) throw new Error(costume.name + ' 不是有效 PNG');if(vector&&!new TextDecoder().decode(bytes.subarray(0,512)).includes('<svg'))throw new Error(costume.name + ' 不是有效 SVG');
    const assetId = md5(bytes),dataFormat=vector?'svg':'png'; const asset: InlineAssetWire = {assetType: vector?{contentType:'image/svg+xml',name:'ImageVector',runtimeFormat:'svg',immutable:true}:{contentType:'image/png',name:'ImageBitmap',runtimeFormat:'png',immutable:true}, dataFormat, data: Array.from(bytes)};
    costumes.push({asset, assetId, md5ext: assetId + '.'+dataFormat, dataFormat, name, bitmapResolution: costume.bitmapResolution, rotationCenterX: costume.rotationCenterX, rotationCenterY: costume.rotationCenterY});
    expectations.push({name, assetId, bitmapResolution: costume.bitmapResolution, rotationCenterX: costume.rotationCenterX, rotationCenterY: costume.rotationCenterY});
  }
  const spriteJson = {isStage: false, name: sprite.name || '画布角色', variables: {}, lists: {}, broadcasts: {}, blocks: {}, currentCostume: 0, costumes, sounds: [], volume: 100, visible: true, x: sprite.x ?? 0, y: sprite.y ?? 0, size: Math.min(100, Math.max(1, sprite.initialSize)), direction: 90, draggable: false, rotationStyle: 'all around'};
  return {spriteJson, expectations};
}

export function fitSpriteInitialSize(costumes: CostumeExport[], maximum = 100) {
  if (!costumes.length) return maximum;
  const scale = costumes.reduce((value, costume) => {
    const logicalWidth = costume.assetWidth / costume.bitmapResolution;
    const logicalHeight = costume.assetHeight / costume.bitmapResolution;
    return Math.min(value, 480 / logicalWidth * 100, 360 / logicalHeight * 100);
  }, maximum);
  return Math.max(1, Math.min(maximum, scale));
}

export async function importCostumeSet(bridge: ScratchGuiBridge, sprite: SpriteExport): Promise<ImportedSpriteResult> { const built = await buildSpriteImport(sprite); return bridge.commitSpriteImport(built.spriteJson, built.expectations); }

export async function importBackdropCostume(bridge: ScratchGuiBridge, backdrop: CostumeExport) {
  const vm = bridge.currentVm;
  const storage = bridge.currentStorage;
  if (!vm || !storage) throw new Error('Scratch 编辑器尚未就绪');
  if (backdrop.dataFormat !== 'png' || backdrop.bitmapResolution !== 2) throw new Error('Scratch 背景必须是高分辨率 PNG');
  const bytes = new Uint8Array(await backdrop.blob.arrayBuffer());
  const asset = storage.createAsset?.(storage.AssetType.ImageBitmap, storage.DataFormat.PNG, bytes, null, true);
  const assetId = String(asset?.assetId ?? '');
  if (!asset || !assetId) throw new Error('无法生成 Scratch 背景资源');
  await vm.addBackdrop(assetId + '.png', {asset, assetId, md5ext: assetId + '.png', dataFormat: 'png', name: backdrop.name || '画布背景', rotationCenterX: backdrop.rotationCenterX, rotationCenterY: backdrop.rotationCenterY, bitmapResolution: 2});
  return {assetId};
}

export async function importSelectedImage(bridge: ScratchGuiBridge, result: ImageGenerationResponse, target: ImageTarget) {
  if (target === 'sprite') {
    const blob = base64ToBlob(result.image_base64, result.mime_type); const bitmap = await createImageBitmap(blob); const width = bitmap.width; const height = bitmap.height; bitmap.close();
    const costumes: CostumeExport[] = [{name: 'AI 造型', blob, dataFormat: 'png', assetWidth: width, assetHeight: height, rotationCenterX: width / 2, rotationCenterY: height / 2, bitmapResolution: 2}];
    return importCostumeSet(bridge, {name: 'AI 角色', initialSize: fitSpriteInitialSize(costumes), costumes});
  }
  const image = await normalizeBackdrop(result);
  await importBackdropCostume(bridge, {name: 'AI 背景', blob: image.blob, dataFormat: 'png', assetWidth: image.width, assetHeight: image.height, rotationCenterX: image.width / 2, rotationCenterY: image.height / 2, bitmapResolution: 2});
  return image;
}
