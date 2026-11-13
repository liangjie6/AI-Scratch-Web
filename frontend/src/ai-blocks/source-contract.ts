import {sha256Hex} from '../shared/sha256';
import {SCRATCH_CATALOG_HASH, isBuiltinAiExtension, type BuiltinAiExtension} from './block-catalog';
import {canonicalContractJson, type ProjectBlueprint, type Scalar} from './project-contract';
import type {AiBlocksMode, AiBlocksTargetContext, UnifiedGenerationRequest} from './ai-blocks-api';
import type {AiBlocksScope, ProjectManifest, ProjectManifestResourceSource, TargetRef} from './project-plan';
import type {ValidationIssue} from './program-ir';

export const SOURCE_PIPELINE_VERSION = 'scratch-source/1';
export interface SourceTargetBinding {
  handle: string;
  entity_id: string;
  name: string;
  kind: 'stage' | 'sprite';
  disposition: 'current' | 'existing' | 'new';
  ref: TargetRef;
  context: AiBlocksTargetContext;
  mode: AiBlocksMode;
}
export interface SourceStateBinding {
  handle: string;
  ref_id: string;
  name: string;
  kind: 'variable' | 'list' | 'broadcast';
  scope: 'global' | 'local';
  owner?: string;
  disposition: 'new' | 'reuse';
  value: Scalar | Scalar[] | null;
}
export interface SourceAssetBinding {
  handle: string;
  owner: string;
  kind: 'sprite' | 'costume' | 'backdrop' | 'sound';
  name: string;
  disposition: 'new' | 'reuse';
  op_id?: string;
  need_id?: string;
}
export interface SourceAssetIntent {
  key: string;
  owner: string;
  kind: SourceAssetBinding['kind'];
  name: string;
  purpose: string;
  disposition: 'new' | 'reuse';
  search_terms: string[];
  source?: ProjectManifestResourceSource;
  x?: number;
  y?: number;
  size?: number;
}
export interface SourceScriptSlot {
  key: string;
  owner: string;
  trigger: {kind: string; key?: string; state?: string; asset?: string; seconds?: number};
  purpose: string;
  reads: string[];
  writes: string[];
  emits: string[];
  receives: string[];
  assets: string[];
  edit_ref?: string;
  script_id: string;
  remove_script_id?: string;
}
export interface FrozenSourceContract {
  pipeline_version: typeof SOURCE_PIPELINE_VERSION;
  normalizer_version: 'compact-v1';
  catalog_hash: string;
  scope: AiBlocksScope;
  mode: AiBlocksMode;
  base_snapshot_hash: string;
  request_binding_hash: string;
  expires_at_unix: number;
  interface_version: 1;
  hash: string;
  title: string;
  goal: string;
  targets: SourceTargetBinding[];
  states: SourceStateBinding[];
  assets: SourceAssetBinding[];
  script_slots: SourceScriptSlot[];
  required_extensions: BuiltinAiExtension[];
  assumptions: string[];
  success_criteria: string[];
  asset_intents: SourceAssetIntent[];
}

const record = (value: unknown): value is Record<string, unknown> => Boolean(value) && typeof value === 'object' && !Array.isArray(value);
const hashPattern = /^[a-f0-9]{64}$/;
const symbolPattern = /^[A-Za-z][A-Za-z0-9_-]{0,79}$/;
const error = (path: string, message: string): ValidationIssue => ({path, message, code: 'SOURCE_CONTRACT_INVALID', operation: 'preflight', repair_scope: 'manifest'});

/** Full JSON canonicalization: unlike legacy model contracts, null is significant. */
export function sourceCanonicalJson(value: unknown): string {
  const normalize = (item: unknown): unknown => {
    if (Array.isArray(item)) return item.map(normalize);
    if (record(item)) return Object.fromEntries(Object.keys(item).sort().filter(key => item[key] !== undefined).map(key => [key, normalize(item[key])]));
    return item;
  };
  // Go encoding/json escapes these two code points even with EscapeHTML(false).
  return JSON.stringify(normalize(value)).replace(/\u2028/g, '\\u2028').replace(/\u2029/g, '\\u2029');
}
export async function sourceContractSha256(value: unknown): Promise<string> {
  return sha256Hex(new TextEncoder().encode(sourceCanonicalJson(value)));
}

export function sourceRequestBindingHash(request: UnifiedGenerationRequest): Promise<string> {
  return sourceContractSha256({session_id: request.session_id, request_id: request.request_id});
}

/** Match SnapshotBindings' Go structs, including absent optional slice fields. */
export function sourceSnapshotHash(request: UnifiedGenerationRequest): Promise<string> {
  const target = (value: AiBlocksTargetContext) => ({name: value.name, is_stage: value.is_stage, has_scripts: value.has_scripts, mode: value.mode,
    costumes: value.costumes ?? null, backdrops: value.backdrops ?? null, sounds: value.sounds ?? null, sprite_names: value.sprite_names ?? null, current_program: value.current_program ?? ''});
  const context = request.project_context;
  return sourceContractSha256({scope: request.scope, target: target(request.target), project_context: context ? {
    project_id: context.project_id, version: context.version, targets: context.targets?.map(item => ({...target(item), target_id: item.target_id, revision: item.revision})) ?? null, shared_state: context.shared_state ?? null,
  } : null});
}

/** Validate the complete wire artifact before using any of its projections. */
export async function assertFrozenSourceContract(value: unknown, envelopeHash: unknown, request: UnifiedGenerationRequest): Promise<FrozenSourceContract> {
  if (!record(value) || value.pipeline_version !== SOURCE_PIPELINE_VERSION || value.normalizer_version !== 'compact-v1' || value.interface_version !== 1
    || value.catalog_hash !== SCRATCH_CATALOG_HASH || value.scope !== request.scope || value.mode !== request.target.mode
    || typeof value.hash !== 'string' || !hashPattern.test(value.hash) || typeof value.base_snapshot_hash !== 'string' || !hashPattern.test(value.base_snapshot_hash)
    || typeof value.request_binding_hash !== 'string' || !hashPattern.test(value.request_binding_hash)
    || !Number.isSafeInteger(value.expires_at_unix) || Number(value.expires_at_unix) <= 0
    || typeof envelopeHash !== 'string' || !hashPattern.test(envelopeHash)) throw new Error('源码生成合同版本、目录或快照绑定无效');
  for (const [name, min, max] of [['targets', 1, 12], ['states', 0, 150], ['assets', 0, 24], ['script_slots', 1, 80], ['required_extensions', 0, 3], ['assumptions', 0, 10], ['success_criteria', 0, 40], ['asset_intents', 0, 24]] as const) {
    if (!Array.isArray(value[name]) || value[name].length < min || value[name].length > max) throw new Error('源码生成合同数组无效：' + name);
  }
  if (typeof value.title !== 'string' || !value.title.trim() || typeof value.goal !== 'string' || !value.goal.trim()) throw new Error('源码生成合同缺少需求');
  if (await sourceContractSha256(value) !== envelopeHash || await sourceContractSha256({...value, hash: ''}) !== value.hash) throw new Error('源码生成合同哈希校验失败');
  if (await sourceRequestBindingHash(request) !== value.request_binding_hash) throw new Error('源码生成合同与会话或请求 ID 不匹配');
  if (await sourceSnapshotHash(request) !== value.base_snapshot_hash) throw new Error('源码生成合同与请求快照不匹配');
  const contract = value as unknown as FrozenSourceContract;
  const unique = (values: string[]) => values.every(item => typeof item === 'string' && symbolPattern.test(item)) && new Set(values).size === values.length;
  if (!contract.targets.every(record) || !contract.states.every(record) || !contract.assets.every(record) || !contract.script_slots.every(record)
    || !unique(contract.targets.map(item => item.handle)) || !unique(contract.states.map(item => item.handle)) || !unique(contract.assets.map(item => item.handle)) || !unique(contract.script_slots.map(item => item.key))) throw new Error('源码生成合同包含重复或无效符号');
  const targets = new Map(contract.targets.map(target => [target.handle, target]));
  for (const target of contract.targets) {
    if (!record(target.ref) || !record(target.context) || !['stage', 'sprite'].includes(target.kind) || !['append', 'modify', 'rewrite'].includes(target.mode)
      || (target.kind === 'stage') !== target.context.is_stage || target.context.name !== target.name || target.context.mode !== target.mode) throw new Error('源码生成合同目标上下文无效');
    if (target.disposition === 'current') {
      if (target.ref.kind !== 'current_target' || target.name !== request.target.name || target.context.is_stage !== request.target.is_stage || target.mode !== request.target.mode) throw new Error('源码生成合同当前目标不匹配');
    } else if (target.disposition === 'existing') {
      const matches = request.project_context?.targets.filter(item => item.name === target.name && item.is_stage === target.context.is_stage) ?? [];
      if (matches.length !== 1 || target.mode !== 'append' || (target.kind === 'stage' ? target.ref.kind !== 'stage' : target.ref.kind !== 'existing_sprite' || target.ref.name !== target.name)) throw new Error('源码生成合同已有目标不匹配');
    } else if (target.disposition !== 'new' || contract.scope !== 'project' || target.kind !== 'sprite' || target.ref.kind !== 'created_sprite' || target.mode !== 'append') throw new Error('源码生成合同新增目标无效');
  }
  for (const state of contract.states) {
    if (!['variable', 'list', 'broadcast'].includes(state.kind) || !['new', 'reuse'].includes(state.disposition) || typeof state.ref_id !== 'string' || typeof state.name !== 'string'
      || (state.scope === 'local' ? targets.get(state.owner ?? '')?.kind !== 'sprite' || state.kind === 'broadcast' : state.scope !== 'global' || Boolean(state.owner))) throw new Error('源码生成合同状态作用域无效');
  }
  const states = new Set(contract.states.map(item => item.handle));
  const assets = new Set(contract.assets.map(item => item.handle));
  for (const asset of contract.assets) if (!targets.has(asset.owner) || !['sprite', 'costume', 'backdrop', 'sound'].includes(asset.kind) || !['new', 'reuse'].includes(asset.disposition)) throw new Error('源码生成合同资源归属无效');
  for (const slot of contract.script_slots) {
    if (!targets.has(slot.owner) || !record(slot.trigger) || typeof slot.purpose !== 'string' || !/^script_[A-Za-z0-9_-]+$/.test(slot.script_id)) throw new Error('源码生成合同脚本槽位无效');
    for (const name of ['reads', 'writes', 'emits', 'receives'] as const) if (!Array.isArray(slot[name]) || slot[name].some(handle => !states.has(handle))) throw new Error('源码生成合同脚本状态引用无效');
    if (!Array.isArray(slot.assets) || slot.assets.some(handle => !assets.has(handle) && !targets.has(handle))) throw new Error('源码生成合同脚本资源引用无效');
  }
  if (contract.required_extensions.some(extension => !isBuiltinAiExtension(extension) || extension === 'text2speech' && !request.permitted_extensions?.includes('text2speech'))) throw new Error('源码生成合同扩展未获许可');
  return contract;
}

const trimRunes = (value: string, max: number) => Array.from(value).slice(0, max).join('');
async function stableId(prefix: string, key: string): Promise<string> { return prefix + (await sha256Hex(new TextEncoder().encode(key))).slice(0, 16); }

/** Reproduce derive.go, without inventing legacy TaskSpec actions or broadcast dependencies. */
export async function validateFrozenProjection(contract: FrozenSourceContract, blueprint: ProjectBlueprint, manifest?: ProjectManifest): Promise<ValidationIssue[]> {
  const issues: ValidationIssue[] = [];
  const targets = new Map(contract.targets.map(item => [item.handle, item]));
  const states = new Map(contract.states.map(item => [item.handle, item]));
  const expectedBlueprint = {schema_version: 2, task_spec_hash: contract.hash, asset_needs: [] as unknown[], entity_bindings: [] as unknown[], state_plan: [] as unknown[], behavior_graph: [] as unknown[], program_jobs: [] as unknown[], acceptance_tests: []};
  const expectedManifest = {schema_version: 3, scope: 'project', summary: contract.title, assumptions: contract.assumptions, required_extensions: contract.required_extensions, resource_ops: [] as unknown[], local_state: [] as unknown[], program_targets: [] as unknown[], shared_state: {variables: [] as unknown[], lists: [] as unknown[], broadcasts: [] as unknown[]}};
  for (const target of contract.targets) {
    const newSprite = contract.assets.find(asset => asset.owner === target.handle && asset.kind === 'sprite' && asset.disposition === 'new');
    expectedBlueprint.entity_bindings.push({entity_id: target.entity_id, resolution: target.disposition === 'new' ? {kind: 'new_sprite', need_id: newSprite?.need_id} : {kind: 'existing_target', target_ref: target.ref}});
    const slots = contract.script_slots.filter(slot => slot.owner === target.handle);
    const behaviorIds: string[] = [];
    for (const slot of slots) {
      const behaviorId = await stableId('behavior_', slot.key);
      behaviorIds.push(behaviorId);
      expectedBlueprint.behavior_graph.push({behavior_id: behaviorId, owner_entity_id: target.entity_id, depends_on: [], produces_broadcasts: slot.emits.map(handle => states.get(handle)?.ref_id), consumes_broadcasts: slot.receives.map(handle => states.get(handle)?.ref_id)});
    }
    const job = {job_id: await stableId('job_', target.handle), owner_entity_id: target.entity_id, mode: target.mode, behavior_ids: behaviorIds};
    if (!behaviorIds.length) continue;
    expectedBlueprint.program_jobs.push(job);
    expectedManifest.program_targets.push({...job, target: target.ref, intent: trimRunes(slots.map(slot => slot.purpose).join('；'), 120)});
  }
  for (const state of contract.states) {
    expectedBlueprint.state_plan.push({ref_id: state.ref_id, kind: state.kind, scope: state.scope, owner_entity_id: state.owner ? targets.get(state.owner)?.entity_id : null, name: state.name, initial_value: state.value});
    if (state.scope === 'local') expectedManifest.local_state.push({ref_id: state.ref_id, kind: state.kind, target: targets.get(state.owner ?? '')?.ref, name: state.name, value: state.value});
    else if (state.kind === 'broadcast') expectedManifest.shared_state.broadcasts.push({ref_id: state.ref_id, name: state.name});
    else expectedManifest.shared_state[state.kind === 'list' ? 'lists' : 'variables'].push({ref_id: state.ref_id, name: state.name, scope: 'global', value: state.value});
  }
  for (const asset of contract.assets.filter(item => item.disposition === 'new')) {
    const intent = contract.asset_intents.find(item => item.key === asset.handle);
    const owner = targets.get(asset.owner);
    if (!intent || !owner) { issues.push(error('/assets', '资源缺少冻结来源或目标')); continue; }
    const source = intent.source ?? {kind: 'scratch_library', asset_type: asset.kind, search_terms: intent.search_terms ?? []};
    const fallback = source.kind === 'scratch_library' ? ['project_asset', 'scratch_library'] : [source.kind];
    expectedBlueprint.asset_needs.push({need_id: asset.need_id, kind: asset.kind, owner_entity_id: owner.entity_id, purpose: intent.purpose, search_terms: intent.search_terms?.length ? intent.search_terms : [asset.name], tags: [], fallback_order: fallback, constraints: {}});
    const operation: Record<string, unknown> = {op_id: asset.op_id, source_need_id: asset.need_id, name: asset.name, tags: []};
    if (asset.kind === 'sprite') {
      Object.assign(operation, {op: source.kind === 'scratch_library' ? 'add_builtin_sprite' : 'add_sprite', owner_entity_id: owner.entity_id, x: intent.x ?? 0, y: intent.y ?? 0, size: intent.size ?? 100});
      if (source.kind === 'scratch_library') Object.assign(operation, {source, include_costumes: 'all', include_sounds: 'all'});
      else operation.costumes = [{name: asset.name, source, tags: []}];
    } else {
      Object.assign(operation, {op: 'add_' + asset.kind, source});
      if (asset.kind !== 'backdrop') operation.target = owner.ref;
      if (asset.kind !== 'sound') operation.select_after = false;
    }
    expectedManifest.resource_ops.push(operation);
  }
  if (canonicalContractJson(expectedBlueprint) !== canonicalContractJson(blueprint)) issues.push(error('/blueprint', '蓝图与冻结源码合同的目标、状态、脚本或资源投影不一致'));
  if (contract.scope === 'project') {
    if (!manifest || canonicalContractJson(expectedManifest) !== canonicalContractJson(manifest)) issues.push(error('/manifest', '项目清单与冻结源码合同的目标、状态、脚本或资源投影不一致'));
  } else if (manifest) issues.push(error('/manifest', '单目标源码合同不允许项目清单'));
  return issues;
}
