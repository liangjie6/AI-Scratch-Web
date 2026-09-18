import {expandScratchSearchTerms} from './scratch-library-aliases';

export type ScratchAssetType = 'sprite' | 'costume' | 'backdrop' | 'sound';
export interface ScratchCostumeItem {name: string; assetId: string; md5ext: string; dataFormat: 'svg' | 'png'; bitmapResolution: 1 | 2; rotationCenterX: number; rotationCenterY: number; tags: string[]; assetWidth?: number; assetHeight?: number}
export interface ScratchSpriteItem {name: string; tags: string[]; isStage: false; costumes: ScratchCostumeItem[]; sounds?: ScratchSoundItem[]}
export interface ScratchLibraryItem extends ScratchCostumeItem {assetType: 'costume' | 'backdrop'}
export interface ScratchSoundItem {name: string; assetId: string; md5ext: string; dataFormat: 'wav' | 'mp3'; rate: number; sampleCount: number; tags: string[]; assetType: 'sound'}
export type ScratchLibrarySearchItem = ScratchSpriteItem | ScratchLibraryItem | ScratchSoundItem;
export interface ScratchLibraryMatch {item: ScratchLibrarySearchItem; score: number}

function boundedThreshold(raw: unknown, fallback: number, maximum: number) {
  const value = Number(raw);
  return Number.isInteger(value) && value >= fallback && value <= maximum ? value : fallback;
}

// Keep the calibrated defaults review-safe; deployment may only tighten them.
export const SCRATCH_AUTO_MATCH_MIN_SCORE = boundedThreshold(import.meta.env.VITE_AI_ASSET_AUTO_MATCH_MIN_SCORE, 8, 100);
export const SCRATCH_AUTO_MATCH_MIN_MARGIN = boundedThreshold(import.meta.env.VITE_AI_ASSET_AUTO_MATCH_MIN_MARGIN, 5, 100);

const manifestPaths: Record<ScratchAssetType, string> = {sprite: '/assets/scratch/libraries/sprites.json', costume: '/assets/scratch/libraries/costumes.json', backdrop: '/assets/scratch/libraries/backdrops.json', sound: '/assets/scratch/libraries/sounds.json'};
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

export function selectHighConfidenceScratchMatch(matches: readonly ScratchLibraryMatch[], minScore = SCRATCH_AUTO_MATCH_MIN_SCORE, minMargin = SCRATCH_AUTO_MATCH_MIN_MARGIN): ScratchLibrarySearchItem | undefined {
  const top = matches[0];
  const next = matches[1];
  if (!top || top.score < minScore) return undefined;
  if (next && top.score - next.score < minMargin) return undefined;
  return top.item;
}

export async function rankScratchLibrary(type: ScratchAssetType, terms: string[], limit = 5): Promise<ScratchLibraryMatch[]> {
  const items = await loadScratchManifest(type);
  return items
    .map(raw => ({item: raw as ScratchLibrarySearchItem, score: scoreScratchLibraryItem(raw as {name: string; tags?: string[]}, terms)}))
    .filter(match => match.score > 0)
    .sort((left, right) => right.score - left.score || left.item.name.localeCompare(right.item.name))
    .slice(0, limit);
}

export async function searchScratchLibrary(type: 'sprite', terms: string[], limit?: number): Promise<ScratchSpriteItem[]>;
export async function searchScratchLibrary(type: 'costume' | 'backdrop', terms: string[], limit?: number): Promise<ScratchLibraryItem[]>;
export async function searchScratchLibrary(type: 'sound', terms: string[], limit?: number): Promise<ScratchSoundItem[]>;
export async function searchScratchLibrary(type: ScratchAssetType, terms: string[], limit?: number): Promise<Array<ScratchSpriteItem | ScratchLibraryItem | ScratchSoundItem>>;
export async function searchScratchLibrary(type: ScratchAssetType, terms: string[], limit = 5) {
  return (await rankScratchLibrary(type, terms, limit)).map(match => match.item);
}

export function scratchAssetUrl(md5ext: string) {
  if (!/^[a-f0-9]{32}\.(?:png|svg|wav|mp3)$/i.test(md5ext)) throw new Error('无效的 Scratch 素材标识');
  return assetRoot + md5ext;
}

export async function preloadScratchAsset(item: Pick<ScratchCostumeItem | ScratchSoundItem, 'md5ext' | 'name'>): Promise<Blob> {
  const response = await fetch(scratchAssetUrl(item.md5ext));
  if (!response.ok) throw new Error('Scratch 素材加载失败：' + item.name);
  return response.blob();
}

export function clearScratchLibraryCache() {cache.clear();}
