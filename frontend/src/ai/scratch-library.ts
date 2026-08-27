import {expandScratchSearchTerms} from './scratch-library-aliases';

export type ScratchAssetType = 'sprite' | 'costume' | 'backdrop';
export interface ScratchCostumeItem {name: string; assetId: string; md5ext: string; dataFormat: 'svg' | 'png'; bitmapResolution: 1 | 2; rotationCenterX: number; rotationCenterY: number; tags: string[]; assetWidth?: number; assetHeight?: number}
export interface ScratchSpriteItem {name: string; tags: string[]; isStage: false; costumes: ScratchCostumeItem[]}
export interface ScratchLibraryItem extends ScratchCostumeItem {assetType: 'costume' | 'backdrop'}

const manifestPaths: Record<ScratchAssetType, string> = {sprite: '/assets/scratch/libraries/sprites.json', costume: '/assets/scratch/libraries/costumes.json', backdrop: '/assets/scratch/libraries/backdrops.json'};
const assetRoot = '/assets/scratch/library-assets/';
const cache = new Map<ScratchAssetType, Promise<unknown[]>>();

function normalize(value: string) {return value.trim().toLocaleLowerCase();}
export async function loadScratchManifest<T extends ScratchAssetType>(type: T): Promise<unknown[]> {
  let request = cache.get(type);
  if (!request) {request = fetch(manifestPaths[type]).then(response => {if (!response.ok) throw new Error('Scratch 素材清单加载失败'); return response.json();}); cache.set(type, request);}
  return request as Promise<unknown[]>;
}

export function scoreScratchLibraryItem(item: {name: string; tags?: string[]}, terms: string[]) {
  const wanted = expandScratchSearchTerms(terms).map(normalize);
  const name = normalize(item.name); const tags = (item.tags ?? []).map(normalize);
  return wanted.reduce((score, term) => score + (name === term ? 8 : name.includes(term) ? 4 : tags.includes(term) ? 3 : tags.some(tag => tag.includes(term)) ? 1 : 0), 0);
}

export async function searchScratchLibrary(type: 'sprite', terms: string[], limit?: number): Promise<ScratchSpriteItem[]>;
export async function searchScratchLibrary(type: 'costume' | 'backdrop', terms: string[], limit?: number): Promise<ScratchLibraryItem[]>;
export async function searchScratchLibrary(type: ScratchAssetType, terms: string[], limit = 5) {
  const items = await loadScratchManifest(type);
  return items.map(raw => ({raw, score: scoreScratchLibraryItem(raw as {name: string; tags?: string[]}, terms)})).filter(item => item.score > 0).sort((a, b) => b.score - a.score || String((a.raw as {name?: string}).name).localeCompare(String((b.raw as {name?: string}).name))).slice(0, limit).map(item => item.raw);
}

export function scratchAssetUrl(md5ext: string) {
  if (!/^[a-f0-9]{32}\.(?:png|svg)$/i.test(md5ext)) throw new Error('无效的 Scratch 素材标识');
  return assetRoot + md5ext;
}

export async function preloadScratchAsset(item: ScratchCostumeItem): Promise<Blob> {
  const response = await fetch(scratchAssetUrl(item.md5ext));
  if (!response.ok) throw new Error('Scratch 素材加载失败：' + item.name);
  return response.blob();
}

export function clearScratchLibraryCache() {cache.clear();}
