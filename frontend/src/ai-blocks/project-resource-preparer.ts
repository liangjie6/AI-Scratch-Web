import {generateImage} from '../ai/image-generation-api';
import type {ImageGenerationRequest, ImageGenerationResponse, ImageStyle} from '../ai/image-generation-types';
import {saveGeneratedAsset, type GeneratedAsset} from '../ai/asset-library-store';
import {preloadScratchAsset, type ScratchCostumeItem, type ScratchLibraryItem, type ScratchSpriteItem} from '../ai/scratch-library';
import type {ProjectPlan, ResourceOp} from './project-plan';
import type {PreparedResource} from './project-resource-runner';
import type {CostumeExport} from '../scratch/asset-adapter';

export interface GenerationConfirmation {op: ResourceOp; reason: string; prompt: string; style: string; width: number; height: number; tags: string[]}
export interface ResourcePreparationOptions {plan: ProjectPlan; selections?: Map<string, ScratchSpriteItem | ScratchLibraryItem>; confirmGeneration?: (request: GenerationConfirmation) => Promise<boolean> | boolean; signal?: AbortSignal; onProgress?: (current: number, total: number, label: string) => void}
export interface PreparedProjectResources {prepared: Map<string, PreparedResource>; generatedAssets: GeneratedAsset[]}

function abortIfNeeded(signal?: AbortSignal) {if (signal?.aborted) throw new DOMException('Resource preparation cancelled', 'AbortError');}
function imageBlob(result: ImageGenerationResponse) {const binary = atob(result.image_base64); const bytes = new Uint8Array(binary.length); for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index); return new Blob([bytes], {type: result.mime_type});}
function costumeFromLibrary(item: ScratchCostumeItem, blob: Blob): CostumeExport {return {name: item.name, blob, dataFormat: item.dataFormat, assetWidth: item.assetWidth ?? 480, assetHeight: item.assetHeight ?? 360, rotationCenterX: item.rotationCenterX, rotationCenterY: item.rotationCenterY, bitmapResolution: item.bitmapResolution};}
function costumeFromGenerated(op: ResourceOp, result: ImageGenerationResponse): CostumeExport {return {name: op.name, blob: imageBlob(result), dataFormat: 'png', assetWidth: result.requested_canvas.width * 2, assetHeight: result.requested_canvas.height * 2, rotationCenterX: result.requested_canvas.width, rotationCenterY: result.requested_canvas.height, bitmapResolution: 2};}
function sourceOf(op: ResourceOp) {return 'source' in op ? op.source : undefined;}
function targetFor(op: ResourceOp): 'sprite' | 'backdrop' {return op.op === 'add_backdrop' || op.op === 'select_backdrop' ? 'backdrop' : 'sprite';}

async function prepareOne(op: ResourceOp, options: ResourcePreparationOptions, generatedAssets: GeneratedAsset[]): Promise<PreparedResource> {
  const source = sourceOf(op);
  if (op.op === 'add_sprite' || op.op === 'add_builtin_sprite') {
    const selected = options.selections?.get(op.op_id);
    if (!selected || !('costumes' in selected)) throw new Error('请先为新增角色选择本地角色素材：' + op.name);
    const costumes = [];
    for (const item of selected.costumes) costumes.push(costumeFromLibrary(item, await preloadScratchAsset(item)));
    return {op_id: op.op_id, kind: 'builtin_sprite', value: {name: op.name, initialSize: op.size, costumes}};
  }
  if (op.op === 'select_backdrop') return {op_id: op.op_id, kind: 'backdrop_selection', value: {name: op.name}};
  if (source?.kind === 'scratch_library') {
    const selected = options.selections?.get(op.op_id);
    if (!selected) throw new Error('请先为资源选择本地素材：' + op.name);
    const item = 'costumes' in selected ? selected.costumes[0] : selected;
    if (!item) throw new Error('本地素材没有可用造型：' + op.name);
    return {op_id: op.op_id, kind: targetFor(op) === 'backdrop' ? 'backdrop' : 'costume', value: costumeFromLibrary(item, await preloadScratchAsset(item))};
  }
  if (source?.kind !== 'image_generation') throw new Error('资源来源未定义：' + op.name);
  const confirmation = options.confirmGeneration?.({op, reason: source.reason, prompt: source.prompt, style: source.style, width: source.width, height: source.height, tags: 'tags' in op ? op.tags : []});
  if (!await confirmation) throw new DOMException('Image generation declined', 'AbortError');
  abortIfNeeded(options.signal);
  const request: ImageGenerationRequest = {prompt: source.prompt, style: source.style as ImageStyle, custom_style: source.custom_style, width: source.width, height: source.height, target: targetFor(op), sheet_layout: '1x1'};
  const result = await generateImage(request, options.signal);
  const saved = await saveGeneratedAsset(result, request, {tags: 'tags' in op ? op.tags : [], groupId: op.op_id});
  generatedAssets.push(saved);
  return {op_id: op.op_id, kind: targetFor(op) === 'backdrop' ? 'backdrop' : 'costume', value: costumeFromGenerated(op, result)};
}

export async function prepareProjectResources(options: ResourcePreparationOptions): Promise<PreparedProjectResources> {
  const prepared = new Map<string, PreparedResource>(); const generatedAssets: GeneratedAsset[] = []; const total = options.plan.resource_ops.length;
  for (let index = 0; index < options.plan.resource_ops.length; index += 1) {abortIfNeeded(options.signal); const op = options.plan.resource_ops[index]; prepared.set(op.op_id, await prepareOne(op, options, generatedAssets)); options.onProgress?.(index + 1, total, op.name);}
  return {prepared, generatedAssets};
}
