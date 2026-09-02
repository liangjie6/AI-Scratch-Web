import {generateImage} from '../ai/image-generation-api';
import type {ImageGenerationRequest, ImageGenerationResponse, ImageStyle} from '../ai/image-generation-types';
import {deleteGeneratedAsset, saveGeneratedAsset, type GeneratedAsset} from '../ai/asset-library-store';
import {preloadScratchAsset, searchScratchLibrary, type ScratchCostumeItem, type ScratchLibraryItem, type ScratchSpriteItem} from '../ai/scratch-library';
import type {ImageGenerationSource, ProjectManifest, ProjectManifestResourceSource, ResourceOp, CostumeResource, AssetPolicy} from './project-plan';
import type {PreparedResource} from './project-resource-runner';
import type {CostumeExport} from '../scratch/asset-adapter';

export interface GenerationConfirmation {
  op: ResourceOp;
  resource_key: string;
  reason: string;
  prompt: string;
  style: string;
  width: number;
  height: number;
  tags: string[];
}
export interface ResourcePreparationOptions {
  plan: ProjectManifest;
  assetPolicy?: AssetPolicy;
  selections?: Map<string, ScratchSpriteItem | ScratchLibraryItem>;
  generationFallbacks?: Map<string, ImageGenerationSource>;
  confirmGeneration?: (request: GenerationConfirmation) => Promise<boolean> | boolean;
  signal?: AbortSignal;
  maxConcurrency?: number;
  onProgress?: (current: number, total: number, label: string) => void;
}
export interface PreparedProjectResources {
  prepared: Map<string, PreparedResource>;
  generatedAssets: GeneratedAsset[];
}

function abortIfNeeded(signal?: AbortSignal) {
  if (signal?.aborted) throw new DOMException('Resource preparation cancelled', 'AbortError');
}

function imageBlob(result: ImageGenerationResponse): Blob {
  const binary = atob(result.image_base64);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return new Blob([bytes], {type: result.mime_type});
}

function costumeFromLibrary(item: ScratchCostumeItem, blob: Blob): CostumeExport {
  return {
    name: item.name,
    blob,
    dataFormat: item.dataFormat,
    assetWidth: item.assetWidth ?? 480,
    assetHeight: item.assetHeight ?? 360,
    rotationCenterX: item.rotationCenterX,
    rotationCenterY: item.rotationCenterY,
    bitmapResolution: item.bitmapResolution
  };
}

function costumeFromGenerated(name: string, result: ImageGenerationResponse): CostumeExport {
  const assetWidth = result.sheet_width;
  const assetHeight = result.sheet_height;
  if (!Number.isInteger(assetWidth) || assetWidth <= 0 || !Number.isInteger(assetHeight) || assetHeight <= 0) {
    throw new Error('生图响应缺少有效的实际图片尺寸');
  }
  return {
    name,
    blob: imageBlob(result),
    dataFormat: 'png',
    assetWidth,
    assetHeight,
    rotationCenterX: assetWidth / 2,
    rotationCenterY: assetHeight / 2,
    bitmapResolution: 2
  };
}

async function generateCostume(
  op: ResourceOp,
  name: string,
  source: Extract<ProjectManifestResourceSource, {kind: 'image_generation'}>,
  tags: string[],
  options: ResourcePreparationOptions,
  generatedAssets: GeneratedAsset[],
  resourceKey = op.op_id + ':' + name,
): Promise<CostumeExport> {
  const confirmed = options.confirmGeneration
    ? await options.confirmGeneration({op, resource_key: resourceKey, reason: source.reason, prompt: source.prompt, style: source.style, width: source.width, height: source.height, tags})
    : false;
  if (!confirmed) throw new DOMException('Image generation declined', 'AbortError');
  abortIfNeeded(options.signal);
  const request: ImageGenerationRequest = {
    prompt: source.prompt,
    style: source.style as ImageStyle,
    custom_style: source.custom_style,
    width: source.width,
    height: source.height,
    target: op.op === 'add_backdrop' ? 'backdrop' : 'sprite',
    sheet_layout: '1x1'
  };
  const generated = await generateImage(request, options.signal);
  generatedAssets.push(await saveGeneratedAsset(generated, request, {tags, groupId: op.op_id + ':' + name}));
  return costumeFromGenerated(name, generated);
}

async function resolveLibrarySelection(
  source: Extract<ProjectManifestResourceSource, {kind: 'scratch_library'}>,
  options: ResourcePreparationOptions,
  key: string,
): Promise<ScratchSpriteItem | ScratchLibraryItem | undefined> {
  const selected = options.selections?.get(key);
  if (selected) return selected;
  const matches = source.asset_type === 'sprite'
    ? await searchScratchLibrary('sprite', source.search_terms, 1)
    : await searchScratchLibrary(source.asset_type, source.search_terms, 1);
  return matches[0];
}

async function prepareLibraryCostume(selection: ScratchSpriteItem | ScratchLibraryItem | undefined, label: string): Promise<CostumeExport> {
  if (!selection) throw new Error('未找到可用的 Scratch 素材：' + label);
  const item: ScratchCostumeItem | undefined = 'costumes' in selection
    ? (selection as ScratchSpriteItem).costumes[0]
    : selection as ScratchLibraryItem;
  if (!item) throw new Error('本地素材没有可用造型：' + label);
  return costumeFromLibrary(item, await preloadScratchAsset(item));
}

function assertSourceAllowed(source: ProjectManifestResourceSource, policy?: AssetPolicy) {
  if (policy === 'existing_only' && source.kind === 'image_generation') {
    throw new Error('当前素材策略为“仅已有素材”，已拒绝生图资源：' + source.prompt);
  }
}

async function cleanupGeneratedAssets(assets: readonly GeneratedAsset[]) {
  const outcomes = await Promise.allSettled(assets.map(asset => deleteGeneratedAsset(asset.id)));
  const failures = outcomes.filter(outcome => outcome.status === 'rejected');
  if (failures.length) throw new Error(failures.length + ' 个生成素材清理失败');
}

async function mapWithConcurrency<T, R>(items: readonly T[], limit: number, worker: (item: T, index: number) => Promise<R>): Promise<R[]> {
  const output = new Array<R>(items.length);
  let cursor = 0;
  let failure: unknown;
  let failed = false;
  const run = async () => {
    while (!failed) {
      const index = cursor++;
      if (index >= items.length) return;
      try {
        output[index] = await worker(items[index], index);
      } catch (error) {
        failure = error;
        failed = true;
      }
    }
  };
  await Promise.all(Array.from({length: Math.min(Math.max(1, limit), Math.max(1, items.length))}, () => run()));
  if (failed) throw failure;
  return output;
}

async function prepareOne(op: ResourceOp, options: ResourcePreparationOptions, generatedAssets: GeneratedAsset[]): Promise<PreparedResource> {
  abortIfNeeded(options.signal);
  if (op.op === 'delete_sprite') return {op_id: op.op_id, kind: 'builtin_sprite', value: null};
  if (op.op === 'add_builtin_sprite') {
    const selected = options.selections?.get(op.op_id) ?? await resolveLibrarySelection(op.source, options, op.op_id);
    if (!selected) {
      const fallback = options.generationFallbacks?.get(op.op_id);
      if (!fallback) throw new Error('未找到可用的 Scratch 角色素材：' + op.name);
      assertSourceAllowed(fallback, options.assetPolicy);
      const costume = await generateCostume(op, op.name, fallback, op.tags ?? [], options, generatedAssets, op.op_id);
      return {op_id: op.op_id, kind: 'builtin_sprite', value: {name: op.name, initialSize: op.size, x: op.x, y: op.y, costumes: [costume]}};
    }
    if (!('costumes' in selected)) throw new Error('Scratch 角色素材没有可用造型：' + op.name);
    const costumes: CostumeExport[] = [];
    for (const item of (selected as ScratchSpriteItem).costumes) costumes.push(costumeFromLibrary(item, await preloadScratchAsset(item)));
    return {op_id: op.op_id, kind: 'builtin_sprite', value: {name: op.name, initialSize: op.size, x: op.x, y: op.y, costumes}};
  }
  if (op.op === 'add_sprite') {
    const specs: CostumeResource[] = op.costumes;
    const costumes: CostumeExport[] = [];
    for (const costume of specs) {
      const source = costume.source;
      assertSourceAllowed(source, options.assetPolicy);
      if (source.kind === 'scratch_library') {
        const selectionKey = op.op_id + ':' + costume.name;
        const selected = options.selections?.get(selectionKey)
          ?? options.selections?.get(op.op_id)
          ?? await resolveLibrarySelection(source, options, selectionKey);
        const fallback = options.generationFallbacks?.get(selectionKey) ?? options.generationFallbacks?.get(op.op_id);
        if (selected) costumes.push(await prepareLibraryCostume(selected, costume.name));
        else if (fallback) {
          assertSourceAllowed(fallback, options.assetPolicy);
          costumes.push(await generateCostume(op, costume.name, fallback, costume.tags ?? [], options, generatedAssets));
        } else costumes.push(await prepareLibraryCostume(undefined, costume.name));
      } else {
        costumes.push(await generateCostume(op, costume.name, source, costume.tags ?? [], options, generatedAssets));
      }
    }
    if (!costumes.length) throw new Error('新增角色至少需要一个造型资源：' + op.name);
    return {op_id: op.op_id, kind: 'builtin_sprite', value: {name: op.name, initialSize: op.size, x: op.x, y: op.y, costumes}};
  }
  if (op.op === 'select_backdrop') return {op_id: op.op_id, kind: 'backdrop_selection', value: {name: op.name}};
  const source = op.source;
  assertSourceAllowed(source, options.assetPolicy);
  if (source.kind === 'scratch_library') {
    const selected = options.selections?.get(op.op_id) ?? await resolveLibrarySelection(source, options, op.op_id);
    if (selected) return {op_id: op.op_id, kind: op.op === 'add_backdrop' ? 'backdrop' : 'costume', value: await prepareLibraryCostume(selected, op.name)};
    const fallback = options.generationFallbacks?.get(op.op_id);
    if (fallback) {
      assertSourceAllowed(fallback, options.assetPolicy);
      const value = await generateCostume(op, op.name, fallback, op.tags ?? [], options, generatedAssets, op.op_id);
      return {op_id: op.op_id, kind: op.op === 'add_backdrop' ? 'backdrop' : 'costume', value};
    }
    return {op_id: op.op_id, kind: op.op === 'add_backdrop' ? 'backdrop' : 'costume', value: await prepareLibraryCostume(undefined, op.name)};
  }
  const value = await generateCostume(op, op.name, source, op.tags ?? [], options, generatedAssets, op.op_id);
  return {op_id: op.op_id, kind: op.op === 'add_backdrop' ? 'backdrop' : 'costume', value};
}

export async function prepareProjectResources(options: ResourcePreparationOptions): Promise<PreparedProjectResources> {
  const prepared = new Map<string, PreparedResource>();
  const generatedAssets: GeneratedAsset[] = [];
  const total = options.plan.resource_ops.length;
  try {
    const operations = options.plan.resource_ops;
    const preparedValues = await mapWithConcurrency(
      operations,
      options.maxConcurrency ?? 4,
      async (op) => prepareOne(op, options, generatedAssets)
    );
    for (let index = 0; index < operations.length; index += 1) {
      abortIfNeeded(options.signal);
      const op = operations[index];
      prepared.set(op.op_id, preparedValues[index]);
      options.onProgress?.(index + 1, total, op.name);
    }
    return {prepared, generatedAssets};
  } catch (error) {
    let cleanupError: unknown;
    try {
      await cleanupGeneratedAssets(generatedAssets);
    } catch (caught) {
      cleanupError = caught;
    }
    if (cleanupError) throw new Error('资源准备失败且无法清理已生成素材：' + (cleanupError instanceof Error ? cleanupError.message : '未知错误'));
    throw error;
  }
}
