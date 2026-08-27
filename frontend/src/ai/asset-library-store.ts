import {openDB} from 'idb';
import type {ImageGenerationRequest, ImageGenerationResponse} from './image-generation-types';

const DB_NAME = 'ai-scratch-assets';
const STORE_NAME = 'generatedAssets';

export interface GeneratedAsset {
  schemaVersion: 1;
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
  return openDB(DB_NAME, 1, {
    upgrade(db) {
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME, {keyPath: 'id'});
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
  request: ImageGenerationRequest
): Promise<GeneratedAsset> {
  const createdAt = Date.now();
  const id = createLocalAssetId(result.request_id, createdAt);
  const asset: GeneratedAsset = {
    schemaVersion: 1,
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
    .filter(asset => asset?.schemaVersion === 1 && Boolean(asset.result?.image_base64))
    .map(asset => ({...asset, name: asset.name || asset.prompt || '未命名素材'}))
    .sort((left, right) => right.createdAt - left.createdAt);
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
