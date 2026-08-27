import {openDB} from 'idb';
import type {ImageGenerationRequest, ImageGenerationResponse} from './image-generation-types';

const DB_NAME = 'ai-scratch-assets';
const STORE_NAME = 'generatedAssets';
export type AssetOrigin = 'ai_generated' | 'scratch_library' | 'canvas_derived' | 'imported';
export interface AssetSaveOptions {tags?: string[]; origin?: AssetOrigin; groupId?: string; derivedFrom?: string;}

export interface GeneratedAsset {
  schemaVersion: 2;
  origin: AssetOrigin;
  tags: string[];
  groupId?: string;
  derivedFrom?: string;
  id: string;
  createdAt: number;
  name: string;
  prompt: string;
  style: ImageGenerationRequest['style'];
  customStyle?: string;
  target: ImageGenerationRequest['target'];
  result: ImageGenerationResponse;
}

async function database() {
  return openDB(DB_NAME, 2, {
    upgrade(db, oldVersion, _newVersion, transaction) {
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME, {keyPath: 'id'});
      }
      if (oldVersion < 2) {
        const store = transaction.objectStore(STORE_NAME);
        store.openCursor().then(cursor => {
          if (!cursor) return;
          const value = cursor.value as Partial<GeneratedAsset>;
          cursor.update({...value, schemaVersion: 2, origin: value.origin ?? 'ai_generated', tags: value.tags ?? []});
          cursor.continue();
        });
      }
    }
  });
}

function createLocalAssetId(requestId: string, createdAt: number) {
  const uniquePart = globalThis.crypto?.randomUUID?.() ?? Math.random().toString(36).slice(2);
  return `${requestId || 'generated'}-${createdAt}-${uniquePart}`;
}

function createAssetName(request: ImageGenerationRequest, createdAt: number) {
  const targetName = request.target === 'backdrop' ? '背景素材' : '角色素材';
  const timestamp = new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false
  }).format(createdAt);
  return `${targetName} ${timestamp}`;
}

export async function saveGeneratedAsset(
  result: ImageGenerationResponse,
  request: ImageGenerationRequest,
  options: AssetSaveOptions = {}
): Promise<GeneratedAsset> {
  const createdAt = Date.now();
  const id = createLocalAssetId(result.request_id, createdAt);
  const asset: GeneratedAsset = {
    schemaVersion: 2,
    origin: options.origin ?? 'ai_generated',
    tags: [...new Set(options.tags ?? [])].slice(0, 20),
    groupId: options.groupId,
    derivedFrom: options.derivedFrom,
    id,
    createdAt,
    name: `${createAssetName(request, createdAt)} ${id.slice(-4).toUpperCase()}`,
    prompt: request.prompt,
    style: request.style,
    customStyle: request.custom_style,
    target: request.target,
    result
  };
  const db = await database();
  await db.put(STORE_NAME, asset);
  return asset;
}

export async function listGeneratedAssets(): Promise<GeneratedAsset[]> {
  const db = await database();
  const assets = await db.getAll(STORE_NAME) as GeneratedAsset[];
  return assets
    .filter(asset => (asset?.schemaVersion === 2 || asset?.schemaVersion === 1) && Boolean(asset.result?.image_base64))
    .map(asset => ({...asset, schemaVersion: 2 as const, origin: asset.origin ?? 'ai_generated', tags: asset.tags ?? [], name: asset.name || asset.prompt || '未命名素材'}))
    .sort((left, right) => right.createdAt - left.createdAt);
}

export async function updateGeneratedAsset(id: string, patch: Pick<AssetSaveOptions, 'tags' | 'groupId'> & {name?: string}): Promise<GeneratedAsset | undefined> {
  const db = await database();
  const asset = await db.get(STORE_NAME, id) as GeneratedAsset | undefined;
  if (!asset) return undefined;
  const updated = {...asset, ...patch, tags: patch.tags ? [...new Set(patch.tags)].slice(0, 20) : asset.tags};
  await db.put(STORE_NAME, updated);
  return updated;
}

export async function countGeneratedAssets(): Promise<number> {
  const db = await database();
  return db.count(STORE_NAME);
}

export async function deleteGeneratedAsset(id: string): Promise<void> {
  const db = await database();
  await db.delete(STORE_NAME, id);
}

export async function clearGeneratedAssets(): Promise<void> {
  const db = await database();
  await db.clear(STORE_NAME);
}
