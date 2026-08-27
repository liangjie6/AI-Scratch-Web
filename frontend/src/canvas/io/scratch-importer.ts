import type {ScratchGuiBridge} from '../../scratch/scratch-gui-bridge';
import type {BitmapStore} from '../model/bitmap-store';
import type {CanvasDocument, CostumeEntry, RasterLayer} from '../model/canvas-types';
import {IDENTITY_MATRIX, sanitizeFileName} from '../model/canvas-types';
import {createId} from '../model/create-id';

const MAX_EDGE = 8192;
const MAX_PIXELS = 32_000_000;
const VECTOR_PREVIEW_RESOLUTION = 4;

type ScratchCostume = Record<string, unknown> & {
  name?: string;
  assetId?: string | number;
  asset?: ScratchAssetLike;
  dataFormat?: unknown;
  bitmapResolution?: number;
  rotationCenterX?: number;
  rotationCenterY?: number;
};

export interface ScratchImportOption {
  id: string;
  kind: 'backdrop' | 'sprite';
  targetId: string;
  costumeIndex?: number;
  name: string;
  detail: string;
  previewUrl?: string;
}

export interface RenderedScratchCostume {
  name: string;
  image: ImageData;
  pivot: {x: number; y: number};
  pixelRatio?: number;
  vectorSource?: string;
}

export interface ScratchSpritePlacement {
  x: number;
  y: number;
  size: number;
  currentCostume: number;
}

function targetCostumes(target: ScratchTargetLike): ScratchCostume[] {
  return (target.getCostumes?.() ?? target.sprite?.costumes ?? []) as ScratchCostume[];
}

function costumePreviewUrl(costume: ScratchCostume | undefined) {
  const rawData = costume?.asset?.data;
  if (!costume || !rawData) return undefined;
  const bytes = typeof rawData === 'string' ? new TextEncoder().encode(rawData) : rawData;
  let binary = '';
  for (let offset = 0; offset < bytes.length; offset += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
  }
  const format = formatName(costume.dataFormat ?? costume.asset?.dataFormat ?? 'png');
  return `data:${mimeType(format)};base64,${btoa(binary)}`;
}

export function listScratchImportOptions(bridge: ScratchGuiBridge): ScratchImportOption[] {
  const vm = bridge.currentVm;
  if (!bridge.isReady || !vm) throw new Error('Scratch 项目尚未就绪');
  const targets = vm.runtime.targets.filter(target => target.isStage || target.isOriginal !== false);
  const stage = targets.find(target => target.isStage);
  const options: ScratchImportOption[] = [];
  if (stage) targetCostumes(stage).forEach((costume, costumeIndex) => {
    const name = String(costume.name || '背景' + (costumeIndex + 1));
    const previewUrl = costumePreviewUrl(costume);
    options.push({id: 'backdrop:' + stage.id + ':' + costumeIndex, kind: 'backdrop', targetId: stage.id, costumeIndex, name, detail: '舞台背景', ...(previewUrl ? {previewUrl} : {})});
  });
  targets.filter(target => !target.isStage).forEach((target, index) => {
    const costumes = targetCostumes(target);
    if (!costumes.length) return;
    const name = target.getName?.() || target.sprite?.name || '角色' + (index + 1);
    const currentCostume = Math.max(0, Math.min(costumes.length - 1, Math.trunc(finiteNumber(target.currentCostume, 0))));
    const previewUrl = costumePreviewUrl(costumes[currentCostume]);
    options.push({id: 'sprite:' + target.id, kind: 'sprite', targetId: target.id, name, detail: costumes.length + ' 个造型', ...(previewUrl ? {previewUrl} : {})});
  });
  return options;
}

function finiteNumber(value: unknown, fallback: number) {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback;
}

function formatName(value: unknown) {
  return String(value ?? '').toLowerCase().replace(/^\./, '');
}

function mimeType(format: string) {
  if (format === 'svg') return 'image/svg+xml';
  if (format === 'jpg' || format === 'jpeg') return 'image/jpeg';
  if (format === 'webp') return 'image/webp';
  return 'image/png';
}

async function loadAsset(costume: ScratchCostume, storage: ScratchStorageLike) {
  if (costume.asset?.data) return costume.asset;
  const format = formatName(costume.dataFormat ?? costume.asset?.dataFormat ?? 'png');
  const assetType = format === 'svg' ? storage.AssetType.ImageVector : storage.AssetType.ImageBitmap;
  const dataFormat = storage.DataFormat[format.toUpperCase()] ?? format;
  const assetId = costume.assetId ?? costume.asset?.assetId;
  if (!assetId || !storage.load) throw new Error('无法读取造型资源：' + String(costume.name || '未命名造型'));
  const asset = await storage.load(assetType, assetId, dataFormat);
  if (!asset?.data) throw new Error('Scratch 造型资源缺失：' + String(costume.name || assetId));
  return asset;
}

async function renderCostume(costume: ScratchCostume, storage: ScratchStorageLike, asBackdrop: boolean): Promise<RenderedScratchCostume> {
  const asset = await loadAsset(costume, storage);
  const format = formatName(costume.dataFormat ?? asset.dataFormat ?? 'png');
  const rawData = asset.data;
  if (!rawData) throw new Error('Scratch 造型资源缺失：' + String(costume.name || asset.assetId || '未命名造型'));
  const data = typeof rawData === 'string' ? new TextEncoder().encode(rawData) : rawData;
  const blob = new Blob([data], {type: mimeType(format)});
  const objectUrl = URL.createObjectURL(blob);
  const image = new Image();
  try {
    image.src = objectUrl;
    await image.decode();
  } catch {
    throw new Error('无法解析 Scratch 造型：' + String(costume.name || '未命名造型'));
  } finally {
    URL.revokeObjectURL(objectUrl);
  }
  if (!image.naturalWidth || !image.naturalHeight) throw new Error('Scratch 造型没有有效尺寸：' + String(costume.name || '未命名造型'));
  const sourceResolution = finiteNumber(costume.bitmapResolution, format === 'svg' ? 1 : 2);
  const sourcePivot = {
    x: finiteNumber(costume.rotationCenterX, image.naturalWidth / 2),
    y: finiteNumber(costume.rotationCenterY, image.naturalHeight / 2)
  };
  const pixelRatio = format === 'svg' ? VECTOR_PREVIEW_RESOLUTION : 2;
  const outputScale = asBackdrop ? 1 / sourceResolution : pixelRatio / sourceResolution;
  const outputWidth = asBackdrop ? 480 : Math.max(1, Math.round(image.naturalWidth * outputScale));
  const outputHeight = asBackdrop ? 360 : Math.max(1, Math.round(image.naturalHeight * outputScale));
  if (outputWidth > MAX_EDGE || outputHeight > MAX_EDGE || outputWidth * outputHeight > MAX_PIXELS) {
    throw new Error('Scratch 造型尺寸超过上限：' + String(costume.name || '未命名造型'));
  }
  const canvas = document.createElement('canvas');
  canvas.width = outputWidth;
  canvas.height = outputHeight;
  const context = canvas.getContext('2d', {willReadFrequently: true});
  if (!context) throw new Error('当前浏览器无法分配图片画布');
  if (asBackdrop) {
    context.fillStyle = '#fff';
    context.fillRect(0, 0, canvas.width, canvas.height);
    context.drawImage(image, 0, 0, 480, 360);
  } else {
    context.drawImage(image, 0, 0, outputWidth, outputHeight);
  }
  return {
    name: String(costume.name || '未命名造型'),
    image: context.getImageData(0, 0, canvas.width, canvas.height),
    pivot: asBackdrop ? {x: 240, y: 180} : {x: sourcePivot.x * outputScale, y: sourcePivot.y * outputScale},
    pixelRatio: asBackdrop ? 1 : pixelRatio,
    ...(format === 'svg' && !asBackdrop ? {vectorSource: new TextDecoder().decode(data)} : {})
  };
}

export function buildScratchCanvasDocument(option: ScratchImportOption, rendered: RenderedScratchCostume[], bitmaps: BitmapStore, placement?: ScratchSpritePlacement): CanvasDocument {
  if (!rendered.length) throw new Error(option.kind === 'sprite' ? '角色没有可导入的造型' : '背景资源不存在');
  const now = Date.now();
  const spritePlacement = placement ?? {x: 0, y: 0, size: 100, currentCostume: 0};
  const stageCenter = {x: 240 + spritePlacement.x, y: 180 - spritePlacement.y};
  const displayScale = Math.max(0.0001, spritePlacement.size / 100);
  const currentCostume = Math.max(0, Math.min(rendered.length - 1, Math.trunc(spritePlacement.currentCostume)));
  const costumeLayers: RasterLayer[] = rendered.map((costume, index) => {
    const pixelRatio = costume.pixelRatio ?? 2;
    const bounds = option.kind === 'sprite' ? {
      x: stageCenter.x - costume.pivot.x / pixelRatio * displayScale,
      y: stageCenter.y - costume.pivot.y / pixelRatio * displayScale,
      width: costume.image.width / pixelRatio * displayScale,
      height: costume.image.height / pixelRatio * displayScale
    } : {x: 0, y: 0, width: 480, height: 360};
    return {
      id: createId(), name: costume.name, bitmapId: bitmaps.put(costume.image, createId(), costume.vectorSource), bounds,
      transform: {...IDENTITY_MATRIX}, visible: option.kind === 'backdrop' || index === currentCostume,
      locked: false, opacity: 1, blendMode: 'normal', chromaKey: null, createdAt: now + index, updatedAt: now + index
    };
  });
  // LayersPanel displays the stack in reverse, so store sprite layers with the first Scratch costume on top.
  const layers = option.kind === 'sprite' ? [...costumeLayers].reverse() : costumeLayers;
  const costumeEntries: CostumeEntry[] = option.kind === 'sprite' ? costumeLayers.map((layer, order) => ({
    id: createId(), layerId: layer.id, name: layer.name, order, frameRect: {...layer.bounds},
    pivot: {...stageCenter}, alignment: 'custom', exportScale: 2, enabled: true
  })) : [];
  return {
    schemaVersion: 1, id: createId(), name: sanitizeFileName(option.name), createdAt: now, updatedAt: now,
    artboard: {x: 0, y: 0, width: 480, height: 360}, layers, costumeEntries,
    activeLayerIds: [costumeLayers[option.kind === 'sprite' ? currentCostume : 0].id], selection: null,
    viewport: {x: 0, y: 0, zoom: 1},
    source: {kind: 'scratch', targetId: option.targetId, targetType: option.kind}
  };
}

export async function importScratchSelection(bridge: ScratchGuiBridge, option: ScratchImportOption, bitmaps: BitmapStore) {
  const vm = bridge.currentVm;
  const storage = bridge.currentStorage;
  if (!bridge.isReady || !vm || !storage) throw new Error('Scratch 项目尚未就绪');
  const target = vm.runtime.targets.find(item => item.id === option.targetId);
  if (!target) throw new Error('Scratch 中已找不到所选资源');
  const costumes = targetCostumes(target);
  const costumeIndex = option.costumeIndex ?? -1;
  const selected = option.kind === 'backdrop' ? costumes.slice(costumeIndex, costumeIndex + 1) : costumes;
  const rendered: RenderedScratchCostume[] = [];
  for (const costume of selected) rendered.push(await renderCostume(costume, storage, option.kind === 'backdrop'));
  const placement = option.kind === 'sprite' ? {
    x: finiteNumber(target.x, 0),
    y: finiteNumber(target.y, 0),
    size: finiteNumber(target.size, 100),
    currentCostume: finiteNumber(target.currentCostume, 0)
  } : undefined;
  return buildScratchCanvasDocument(option, rendered, bitmaps, placement);
}
