import {markGeneratedAssetsAttached, type GeneratedAsset} from '../ai/asset-library-store';
import {LOCAL_PROJECT_STORAGE_KEY} from './run-checkpoint-store';
import {
  compileProgramIR,
  validateCompiledGraph,
  validateProgramIR,
  type CompiledProgram,
  type ProgramResolver
} from './program-compiler';
import {
  ProgramIRValidationError,
  type ProgramIR,
  type ScriptIR,
  type TargetKind
} from './program-ir';
import {preflightProgram} from './program-preflight';
import {isBuiltinAiExtension, type BuiltinAiExtension} from './block-catalog';
import {
  streamProjectManifest,
  streamUnifiedGeneration,
  type AiBlocksMode,
  type AiBlocksPlan,
  type AiBlocksRequirements,
  type UnifiedGenerationDraft,
  type UnifiedGenerationEvent,
  type ProjectManifestGenerationRequest,
  type UnifiedGenerationRequest,
  type UnifiedProgramDraft
} from './ai-blocks-api';
import {assertProjectManifest, frontendEnabledBuiltinExtensions, normalizeProjectManifestTargets, validateProjectProgramSharedState, validateProjectProgramStateRefs, type ProjectManifest, type TargetRef} from './project-plan';
import {contractSha256, projectBlueprintToManifest, projectTaskSpecToBlueprint, type ProjectBlueprint, type ScratchTaskSpec} from './project-contract';
import {assertProjectSnapshotIsCurrent, assertSnapshotIsCurrent, isAiEditableScript, readAiProjectSnapshot, readAiTargetSnapshot, type AiProjectSnapshot, type AiTargetSnapshot} from './target-context';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';
import {
  applyProjectResources,
  createProjectApplyResult,
  type ApplyEntry,
  type PreparedResource,
  type ProjectApplyResult,
  type ProjectResourceExecutor
} from './project-resource-runner';
import {createProjectResourceExecutor} from './project-resource-executor';
import {
  createCheckpointStore,
  createEditorSessionId,
  fingerprintBytes,
  hashCheckpointPlan,
  type AiRunCheckpointStore,
  type LogicalTargetLocator,
  type SerializableApplyReceipt
} from './run-checkpoint-store';
import {prewarmCandidateVerificationRuntime, verifyCandidateSb3, type CandidateVerificationFailureCode} from './verification-service';
import {createScratchProgramPort, type PendingScratchVariable} from '../scratch/scratch-program-port';
import {toRaw} from 'vue';

export interface AiGenerationProgress {
  current: number;
  total: number;
  label: string;
}

export interface AiGenerationResult {
  targetId: string;
  targetName: string;
  addedBlocks: number;
  addedScripts: number;
  removedScripts: number;
  revisionAtComplete: number;
}

export interface UnifiedCommitResult {
  backup: Uint8Array;
  originalTargetId: string;
  revisionAtComplete: number;
  programs: AiGenerationResult[];
  project?: ProjectApplyResult;
  checkpointRunId?: string;
}

export interface CommitTelemetry {
  outcome: 'COMPLETED' | 'ROLLED_BACK' | 'FAILED_NEEDS_RECOVERY' | 'REJECTED_BEFORE_WRITE';
  commitDurationMs: number;
  verifyDurationMs: number;
  behaviorTestCount: number;
  behaviorPassedCount: number;
  behaviorMustCount: number;
  behaviorMustPassedCount: number;
  errorCode?: 'COMMIT_FAILED' | 'COMMIT_CONFLICT' | 'COMMIT_RECOVERY_FAILED' | CandidateVerificationFailureCode;
}

export interface UnifiedCommitOptions {
  preparedResources?: Map<string, PreparedResource>;
  resourceExecutor?: ProjectResourceExecutor;
  signal?: AbortSignal;
  onProgress?: (progress: AiGenerationProgress) => void;
  generatedAssets?: GeneratedAsset[];
  runId?: string;
  checkpointStore?: AiRunCheckpointStore;
  /** The commit path is fail-closed when browser recovery is disabled. */
  checkpointEnabled?: boolean;
  /** Synchronous only: callers must defer telemetry I/O until the transaction releases. */
  onCommitTelemetry?: (telemetry: CommitTelemetry) => void;
}

export type ProjectManifestEventSource = (
  request: ProjectManifestGenerationRequest,
  onEvent: (event: UnifiedGenerationEvent) => void,
  signal?: AbortSignal,
) => Promise<void>;

interface PreparedProgram {
  targetIndex: number;
  targetRef: TargetRef;
  mode: AiBlocksMode;
  program: ProgramIR;
}

type LogicalResourceBindings = Map<string, {resourceType: 'sprite' | 'costume' | 'backdrop' | 'sound'; name: string}>;
type LogicalStateBindings = Map<string, {stateKind: 'variable' | 'list' | 'broadcast'; name: string; id: string}>;

function clone<T>(value: T): T {
  const seen = new WeakMap<object, unknown>();
  const unwrap = (input: unknown): unknown => {
    if (!input || typeof input !== 'object') return input;
    const raw = toRaw(input as object) as object;
    const cached = seen.get(raw);
    if (cached) return cached;
    if (Array.isArray(raw)) {
      const output: unknown[] = [];
      seen.set(raw, output);
      raw.forEach(item => output.push(unwrap(item)));
      return output;
    }
    const output: Record<string, unknown> = {};
    seen.set(raw, output);
    for (const [key, item] of Object.entries(raw)) output[key] = unwrap(item);
    return output;
  };
  return structuredClone(unwrap(value)) as T;
}

function throwIfAborted(signal?: AbortSignal) {
  if (signal?.aborted) throw new DOMException('AI block generation cancelled', 'AbortError');
}

function monotonicNow(): number {
  return globalThis.performance?.now?.() ?? Date.now();
}

async function compensateJournal(bridge: ScratchGuiBridge, journal: readonly ApplyEntry[]): Promise<boolean> {
  const failures: unknown[] = [];
  let requiresBackupRestore = false;
  for (const entry of [...journal].reverse()) {
    try {
      if (entry.kind === 'program' || entry.kind === 'resource_batch') {
        requiresBackupRestore = true;
      } else if (entry.kind === 'asset') {
        // Generated media is intentionally retained as unattached output when
        // the project transaction fails; deleting it would lose paid work.
      } else if (entry.kind === 'sprite') {
        const vm = bridge.currentVm;
        if (!vm) throw new Error('Scratch VM 尚未就绪');
        const mutate = bridge.runProjectTransactionCompensationAsync;
        const deleted = typeof mutate === 'function'
          ? await mutate.call(bridge, () => vm.deleteSprite?.(entry.targetId))
          : await Promise.resolve(vm.deleteSprite?.(entry.targetId));
        if (deleted === false || vm.runtime.targets.some(target => target.id === entry.targetId)) throw new Error('角色补偿删除未完成：' + entry.targetId);
      } else if (entry.kind === 'costume' || entry.kind === 'backdrop') {
        if (typeof bridge.removeCostumeFromTarget !== 'function') throw new Error('Scratch 不支持素材补偿删除');
        await bridge.removeCostumeFromTarget(entry.targetId, entry.costumeIndex, entry.assetId, true);
      } else if (entry.kind === 'sound') {
        if (typeof bridge.removeSoundFromTarget !== 'function') throw new Error('Scratch 不支持声音补偿删除');
        await bridge.removeSoundFromTarget(entry.targetId, entry.soundIndex, entry.assetId, true);
      } else if (entry.kind === 'backdrop_selection') {
        if (typeof bridge.restoreCostumeSelection !== 'function') throw new Error('Scratch 不支持背景选择补偿');
        bridge.restoreCostumeSelection(entry.targetId, entry.previousCostumeIndex, true);
      } else if (entry.kind === 'deleted_sprite') {
        const mutate = bridge.runProjectTransactionCompensationAsync;
        if (typeof mutate === 'function') await mutate.call(bridge, entry.restore);
        else await entry.restore();
      }
    } catch (error) {
      failures.push(error);
    }
  }
  if (failures.length) throw new Error(failures.map(error => error instanceof Error ? error.message : '未知补偿错误').join('；'));
  return requiresBackupRestore;
}

function randomId(prefix: string) {
  const random = globalThis.crypto?.randomUUID?.().replaceAll('-', '');
  return prefix + '_' + (random ?? Math.random().toString(36).slice(2));
}

function targetById(bridge: ScratchGuiBridge, targetId: string): ScratchTargetLike {
  const vm = bridge.currentVm;
  const target = vm?.runtime.getTargetById?.(targetId) ?? vm?.runtime.targets.find(item => item.id === targetId);
  if (!target?.blocks) throw new Error('当前 Scratch 目标已不存在');
  return target;
}

function stageFor(bridge: ScratchGuiBridge): ScratchTargetLike {
  const vm = bridge.currentVm;
  const stage = vm?.runtime.getTargetForStage?.() ?? vm?.runtime.targets.find(item => item.isStage);
  if (!stage?.blocks) throw new Error('Scratch 舞台尚未就绪');
  return stage;
}

function buildLogicalResourceBindings(result: ProjectApplyResult | undefined): LogicalResourceBindings {
  const bindings: LogicalResourceBindings = new Map();
  if (!result) return bindings;
  for (const item of result.addedTargets) bindings.set(item.op_id, {resourceType: 'sprite', name: item.name});
  for (const item of result.addedCostumes) bindings.set(item.op_id, {resourceType: 'costume', name: item.name});
  for (const item of result.addedBackdrops) bindings.set(item.op_id, {resourceType: 'backdrop', name: item.name});
  for (const item of result.addedSounds) bindings.set(item.op_id, {resourceType: 'sound', name: item.name});
  return bindings;
}

function ownVariable(target: ScratchTargetLike, name: string, type: ScratchVariableLike['type']) {
  return Object.values(target.variables ?? {}).find(variable => variable.name === name && variable.type === type);
}

/** Materialize v3 manifest state before compiling any ProgramIR against the post-resource VM. */
function installManifestState(
  bridge: ScratchGuiBridge,
  manifest: ProjectManifest,
  originalTargetId: string,
  result: ProjectApplyResult | undefined,
): void {
  const create = (owner: ScratchTargetLike, name: string, type: ScratchVariableLike['type'], value: ScratchVariableLike['value']) => {
    const existing = ownVariable(owner, name, type);
    if (existing) return;
    const id = randomId(type === 'list' ? 'ai_list' : type === 'broadcast_msg' ? 'ai_broadcast' : 'ai_variable');
    owner.createVariable?.(id, name, type, false);
    const created = owner.variables?.[id];
    if (!created) throw new Error('Scratch 无法创建清单状态：' + name);
    created.value = clone(value);
  };
  const stage = stageFor(bridge);
  for (const item of manifest.shared_state.variables) create(stage, item.name, '', item.value ?? 0);
  for (const item of manifest.shared_state.lists) create(stage, item.name, 'list', item.value ? [...item.value] : []);
  for (const item of manifest.shared_state.broadcasts) {
    const name = typeof item === 'string' ? item : item.name;
    create(stage, name, 'broadcast_msg', name);
  }
  for (const item of manifest.local_state ?? []) {
    const targetId = targetIdForRef(bridge, item.target, originalTargetId, result);
    const owner = targetById(bridge, targetId);
    create(owner, item.name, item.kind === 'list' ? 'list' : '', item.kind === 'list' ? (Array.isArray(item.value) ? [...item.value] : []) : (Array.isArray(item.value) ? 0 : item.value ?? 0));
  }
}

function buildLogicalStateBindings(bridge: ScratchGuiBridge, manifest: ProjectManifest, originalTargetId: string, result: ProjectApplyResult | undefined): LogicalStateBindings {
  const bindings: LogicalStateBindings = new Map();
  const stage = stageFor(bridge);
  const bind = (refId: string | undefined, stateKind: 'variable' | 'list' | 'broadcast', owner: ScratchTargetLike, name: string) => {
    if (!refId) throw new Error('清单状态缺少 ref_id');
    const item = ownVariable(owner, name, stateKind === 'list' ? 'list' : stateKind === 'broadcast' ? 'broadcast_msg' : '');
    if (!item) throw new Error('清单状态尚未安装：' + refId);
    bindings.set(refId, {stateKind, name: item.name, id: item.id});
  };
  for (const item of manifest.shared_state.variables) bind(item.ref_id, 'variable', stage, item.name);
  for (const item of manifest.shared_state.lists) bind(item.ref_id, 'list', stage, item.name);
  for (const item of manifest.shared_state.broadcasts) {
    if (typeof item === 'string') throw new Error('v3 广播状态缺少 ref_id');
    bind(item.ref_id, 'broadcast', stage, item.name);
  }
  for (const item of manifest.local_state ?? []) {
    const targetId = targetIdForRef(bridge, item.target, originalTargetId, result);
    bind(item.ref_id, item.kind, targetById(bridge, targetId), item.name);
  }
  return bindings;
}


function resolveExistingAssetName(
  bridge: ScratchGuiBridge,
  target: ScratchTargetLike,
  stage: ScratchTargetLike,
  assetType: 'sprite' | 'costume' | 'backdrop' | 'sound',
  name: string,
): string {
  const requireUnique = (candidates: string[], label: string) => {
    const matches = candidates.filter(candidate => candidate === name);
    if (matches.length !== 1) throw new Error(`已有${label}必须唯一存在：${name}`);
    return matches[0];
  };
  if (assetType === 'sprite') {
    const names = (bridge.currentVm?.runtime.targets ?? [])
      .filter(item => !item.isStage && item.isOriginal !== false)
      .map(item => String(item.getName?.() ?? item.sprite?.name ?? ''));
    return requireUnique(names, '角色');
  }
  if (assetType === 'backdrop') return requireUnique((stage.getCostumes?.() ?? []).map(item => String(item.name ?? '')), '背景');
  if (assetType === 'costume') return requireUnique((target.getCostumes?.() ?? []).map(item => String(item.name ?? '')), '造型');
  return requireUnique((target.getSounds?.() ?? []).map(item => String(item.name ?? '')), '声音');
}

function createResolver(
  bridge: ScratchGuiBridge,
  target: ScratchTargetLike,
  stage: ScratchTargetLike,
  program: ProgramIR,
  pending: PendingScratchVariable[],
  stateBindings: LogicalStateBindings = new Map(),
): ProgramResolver {
  const variableMap = new Map<string, {name: string; id: string}>();
  const listMap = new Map<string, {name: string; id: string}>();
  const broadcastMap = new Map<string, {name: string; id: string}>();

  const ensure = (
    name: string,
    type: ScratchVariableLike['type'],
    scope: 'local' | 'global' | undefined,
    value: ScratchVariableLike['value']
  ) => {
    const owner = scope === 'global' || target.isStage ? stage : target;
    const existing = ownVariable(owner, name, type);
    if (existing) return {name: existing.name, id: existing.id};
    const id = randomId(type === 'list' ? 'ai_list' : type === 'broadcast_msg' ? 'ai_broadcast' : 'ai_variable');
    pending.push({owner, id, name, type, value});
    return {name, id};
  };

  for (const variable of program.variables) variableMap.set(variable.name, ensure(variable.name, '', variable.scope, variable.value ?? 0));
  for (const list of program.lists) listMap.set(list.name, ensure(list.name, 'list', list.scope, list.value ? [...list.value] : []));
  for (const name of program.broadcasts) broadcastMap.set(name, ensure(name, 'broadcast_msg', 'global', name));

  const resolveExisting = (name: string, type: ScratchVariableLike['type']) => {
    const found = ownVariable(target, name, type) ?? ownVariable(stage, name, type);
    if (!found) throw new Error('AI 引用了未声明的' + (type === 'list' ? '列表：' : type === 'broadcast_msg' ? '广播：' : '变量：') + name);
    return {name: found.name, id: found.id};
  };

  return {
    variable: name => variableMap.get(name) ?? resolveExisting(name, ''),
    list: name => listMap.get(name) ?? resolveExisting(name, 'list'),
    broadcast: name => broadcastMap.get(name) ?? resolveExisting(name, 'broadcast_msg'),
    state: (kind, refId) => {
      const binding = stateBindings.get(refId);
      if (!binding || binding.stateKind !== kind) throw new Error('逻辑状态引用未绑定：' + kind + '/' + refId);
      return {name: binding.name, id: binding.id};
    }
  };
}

function positionProgram(program: ProgramIR, mode: AiBlocksMode, blocks: ScratchBlocksLike, removeTopIds: string[]): ProgramIR {
  const positioned = clone(program);
  const topIds = blocks.getScripts();
  const existing = topIds.map(id => blocks.getBlock(id)).filter(Boolean) as ScratchBlockLike[];
  const removed = new Set(removeTopIds);
  const survivors = existing.filter(block => !removed.has(block.id));
  const removedPositions = removeTopIds
    .map(id => blocks.getBlock(id))
    .filter(Boolean)
    .map(block => ({x: block!.x ?? 40, y: block!.y ?? 40}));
  const sideOf = mode === 'append' ? existing : survivors;
  const baseX = sideOf.length ? Math.max(...sideOf.map(block => block.x ?? 0)) + 360 : 40;
  positioned.scripts.forEach((script, index) => {
    if (mode === 'modify' && removedPositions[index]) {
      script.x = removedPositions[index].x;
      script.y = removedPositions[index].y;
    } else {
      script.x = baseX + Math.floor(index / 4) * 360;
      script.y = 40 + (index % 4) * 220;
    }
  });
  return positioned;
}

function prepareProgramForTarget(
  bridge: ScratchGuiBridge,
  snapshot: AiTargetSnapshot,
  mode: AiBlocksMode,
  program: ProgramIR,
  bindings: LogicalResourceBindings = new Map(),
  stateBindings: LogicalStateBindings = new Map(),
  enabledExtensions: ReadonlySet<BuiltinAiExtension> = new Set(),
): {compiled: CompiledProgram; pending: PendingScratchVariable[]; removeTopIds: string[]} {
  assertSnapshotIsCurrent(bridge, snapshot);
  const target = targetById(bridge, snapshot.targetId);
  const stage = stageFor(bridge);
  const blocks = createScratchProgramPort(bridge).read(target);
  const removeTopIds = mode === 'rewrite'
    ? blocks.getScripts().filter(id => isAiEditableScript(blocks, id))
    : mode === 'modify'
      ? program.remove_script_ids.map(ref => {
        const id = snapshot.scriptRefs[ref];
        if (!id) throw new Error('AI 尝试修改不存在的脚本：' + ref);
        return id;
      })
      : [];
  if (mode !== 'modify' && program.remove_script_ids.length) throw new Error('当前生成模式不允许删除已有脚本');
  const pending: PendingScratchVariable[] = [];
  const positioned = positionProgram(program, mode, blocks, removeTopIds);
  const compiled = compileProgramIR(positioned, {
    target: (target.isStage ? 'stage' : 'sprite') as TargetKind,
    resolver: createResolver(bridge, target, stage, program, pending, stateBindings),
    resourceResolver: {
      resource: (ref, expectedType) => {
        const opId = ref.ref?.op_id ?? ref.op_id;
        const binding = opId ? bindings.get(opId) : undefined;
        if (!binding || binding.resourceType !== expectedType || (ref.resource_type && binding.resourceType !== ref.resource_type)) {
          throw new Error('逻辑资源引用未绑定：' + expectedType + '/' + (opId ?? 'unknown'));
        }
        return binding.name;
      },
      existing: ref => {
        return resolveExistingAssetName(bridge, target, stage, ref.asset_type, ref.name);
      }
    },
    enabledExtensions,
    idGenerator: (kind, ordinal, scriptId) => randomId('ai_' + scriptId + '_' + kind + '_' + ordinal)
  });
  validateCompiledGraph(compiled);
  return {compiled, pending, removeTopIds};
}

function asRequirements(event: UnifiedGenerationEvent): AiBlocksRequirements {
  const data = event.data;
  const arrays = ['behavior_steps', 'triggers', 'controls', 'success_criteria', 'missing_questions'];
  if (typeof data.ready !== 'boolean' || typeof data.title !== 'string' || !data.title.trim() || typeof data.goal !== 'string' || !data.goal.trim()) {
    throw new Error('统一流未提供有效需求');
  }
  if (arrays.some(key => !Array.isArray(data[key]) || (data[key] as unknown[]).some(item => typeof item !== 'string'))) {
    throw new Error('统一流未提供完整需求字段');
  }
  return data as unknown as AiBlocksRequirements;
}

function asPlan(event: UnifiedGenerationEvent): AiBlocksPlan {
  const steps = event.data.steps;
  if (!Array.isArray(steps) || steps.length === 0 || steps.some(item => typeof item !== 'string' || !item.trim())) throw new Error('统一流未提供有效计划');
  return {steps: [...steps]};
}

function buildProgram(metadata: Record<string, unknown>, shared: Record<string, unknown>, scripts: ScriptIR[]): ProgramIR {
  const program: ProgramIR = {
    schema_version: metadata.schema_version as 1 | 2,
    summary: typeof metadata.summary === 'string' ? metadata.summary : '',
    remove_script_ids: Array.isArray(metadata.remove_script_ids) ? metadata.remove_script_ids as string[] : [],
    variables: Array.isArray(shared.variables) ? shared.variables as ProgramIR['variables'] : [],
    lists: Array.isArray(shared.lists) ? shared.lists as ProgramIR['lists'] : [],
    broadcasts: Array.isArray(shared.broadcasts) ? shared.broadcasts as string[] : [],
    procedures: Array.isArray(metadata.procedures) ? metadata.procedures as NonNullable<ProgramIR['procedures']> : [],
    scripts
  };
  return program;
}

function targetPlan(request: UnifiedGenerationRequest, manifest: ProjectManifest | undefined, index: number): {targetRef: TargetRef; mode: AiBlocksMode} {
  if (request.scope === 'single_sprite') {
    if (index !== 0) throw new Error('当前目标只能接收一个程序');
    return {targetRef: {kind: 'current_target'}, mode: request.target.mode};
  }
  const target = manifest?.program_targets[index];
  if (!target) throw new Error('项目程序没有对应的目标清单');
  return {targetRef: target.target, mode: target.mode};
}

function targetIdForRef(bridge: ScratchGuiBridge, ref: TargetRef, originalTargetId: string, result?: Pick<ProjectApplyResult, 'addedTargets'>): string {
  if (ref.kind === 'current_target') return originalTargetId;
  const targets = bridge.currentVm?.runtime.targets ?? [];
  if (ref.kind === 'stage') {
    const stage = targets.find(target => target.isStage);
    if (!stage) throw new Error('Scratch 舞台尚未就绪');
    return stage.id;
  }
  if (ref.kind === 'created_sprite') {
    const created = result?.addedTargets.find(item => item.op_id === ref.op_id);
    if (!created) throw new Error('清单引用的角色尚未创建：' + ref.op_id);
    return created.target_id;
  }
  const wanted = ref.name.trim().toLocaleLowerCase();
  const matches = targets.filter(target => !target.isStage && target.isOriginal !== false && String(target.getName?.() ?? target.sprite?.name ?? '').trim().toLocaleLowerCase() === wanted);
  if (matches.length > 1) throw new Error('已有角色名称不唯一：' + ref.name);
  const existing = matches[0];
  if (!existing) throw new Error('找不到已有角色：' + ref.name);
  return existing.id;
}

async function originalTargetLocator(bridge: ScratchGuiBridge, snapshot: AiTargetSnapshot): Promise<LogicalTargetLocator> {
  if (snapshot.isStage) return {kind: 'stage'};
  const sprites = (bridge.currentVm?.runtime.targets ?? []).filter(target => !target.isStage && target.isOriginal !== false);
  const target = sprites.find(item => item.id === snapshot.targetId);
  const name = String(target?.getName?.() ?? target?.sprite?.name ?? snapshot.targetName);
  const ordinal = Math.max(0, sprites.findIndex(item => item.id === snapshot.targetId));
  const fingerprint = await hashCheckpointPlan({
    name,
    costumes: (target?.getCostumes?.() ?? []).map(item => String(item.name ?? '')),
    sounds: (target?.getSounds?.() ?? []).map(item => String(item.name ?? '')),
    program: snapshot.context.current_program
  });
  return {kind: 'sprite', ordinal, name, fingerprint};
}

export class UnifiedGenerationRunner {
  constructor(
    private readonly bridge: ScratchGuiBridge,
    private readonly enabledExtensions: ReadonlySet<BuiltinAiExtension> = frontendEnabledBuiltinExtensions(),
  ) {}

  async prepare(
    request: UnifiedGenerationRequest,
    onEvent?: (event: UnifiedGenerationEvent) => void,
    signal?: AbortSignal,
    projectManifestEventSource: ProjectManifestEventSource = streamProjectManifest,
  ): Promise<UnifiedGenerationDraft & {request: UnifiedGenerationRequest; snapshot: AiTargetSnapshot; projectSnapshot?: AiProjectSnapshot}> {
    const snapshot = readAiTargetSnapshot(this.bridge, request.target.mode);
    const projectSnapshot = request.scope === 'project' ? readAiProjectSnapshot(this.bridge) : undefined;
    assertSnapshotIsCurrent(this.bridge, snapshot);
    if (projectSnapshot) assertProjectSnapshotIsCurrent(this.bridge, projectSnapshot);
    const readFrameIndex = (value: unknown, label: string) => {
      if (typeof value !== 'number' || !Number.isInteger(value) || value < 0) throw new Error('统一流 ' + label + ' 无效');
      return value;
    };
    const requirements: {value: AiBlocksRequirements | null} = {value: null};
    const taskSpec: {value: ScratchTaskSpec | null} = {value: null};
    const blueprint: {value: ProjectBlueprint | null} = {value: null};
    const taskSpecHash: {value: string | null} = {value: null};
    const blueprintHash: {value: string | null} = {value: null};
    const plan: {value: AiBlocksPlan | null} = {value: null};
    const manifest: {value: ProjectManifest | undefined} = {value: undefined};
    const programs = new Map<number, UnifiedProgramDraft>();
    const metadata = new Map<number, Record<string, unknown>>();
    const shared = new Map<number, Record<string, unknown>>();
    const scripts = new Map<number, ScriptIR[]>();
    const scriptTotals = new Map<number, number>();

    const handleEvent = (event: UnifiedGenerationEvent) => {
      onEvent?.(event);
      if (event.type === 'repair') {
        // 服务端校验失败后进入自修复重试：重置局部帧收集器，等待修复轮重新输出
        metadata.clear();
        shared.clear();
        scripts.clear();
        scriptTotals.clear();
        programs.clear();
      }
      if (event.type === 'phase_complete' && event.phase === 'requirements') requirements.value = asRequirements(event);
      if (event.type === 'phase_complete' && event.phase === 'task_spec') {
        if (!event.data.task_spec || typeof event.data.task_spec !== 'object' || Array.isArray(event.data.task_spec)) throw new Error('统一流未提供有效 TaskSpec');
        taskSpec.value = event.data.task_spec as ScratchTaskSpec;
        taskSpecHash.value = typeof event.data.task_spec_hash === 'string' ? event.data.task_spec_hash : null;
      }
      if (event.type === 'phase_complete' && event.phase === 'blueprint') {
        if (!event.data.blueprint || typeof event.data.blueprint !== 'object' || Array.isArray(event.data.blueprint)) throw new Error('统一流未提供有效项目蓝图');
        blueprint.value = event.data.blueprint as ProjectBlueprint;
        blueprintHash.value = typeof event.data.blueprint_hash === 'string' ? event.data.blueprint_hash : null;
      }
      if (event.type === 'phase_complete' && event.phase === 'plan') {
        plan.value = asPlan(event);
        if (!taskSpec.value && event.data.task_spec && typeof event.data.task_spec === 'object' && !Array.isArray(event.data.task_spec)) taskSpec.value = event.data.task_spec as ScratchTaskSpec;
        if (!blueprint.value && event.data.blueprint && typeof event.data.blueprint === 'object' && !Array.isArray(event.data.blueprint)) blueprint.value = event.data.blueprint as ProjectBlueprint;
        if (!taskSpecHash.value && typeof event.data.task_spec_hash === 'string') taskSpecHash.value = event.data.task_spec_hash;
        if (!blueprintHash.value && typeof event.data.blueprint_hash === 'string') blueprintHash.value = event.data.blueprint_hash;
        if (event.data.manifest) manifest.value = normalizeProjectManifestTargets(event.data.manifest as ProjectManifest);
      }
      if (event.type === 'metadata') {
        const index = readFrameIndex(event.data.target_index, 'metadata target_index');
        if (metadata.has(index)) throw new Error('统一流重复输出 metadata');
        metadata.set(index, {...event.data});
      }
      if (event.type === 'shared_state') {
        const index = readFrameIndex(event.data.target_index, 'shared_state target_index');
        if (shared.has(index)) throw new Error('统一流重复输出 shared_state');
        shared.set(index, {...event.data});
      }
      if (event.type === 'script_ready') {
        const index = readFrameIndex(event.data.target_index, 'script target_index');
        const scriptIndex = readFrameIndex(event.data.index, 'script index');
        const total = readFrameIndex(event.data.total, 'script total');
        if (!event.data.script || typeof event.data.script !== 'object' || Array.isArray(event.data.script)) throw new Error('统一流 script_ready 数据无效');
        const previousTotal = scriptTotals.get(index);
        if (previousTotal !== undefined && previousTotal !== total) throw new Error('统一流脚本总数不一致');
        scriptTotals.set(index, total);
        const items = scripts.get(index) ?? [];
        if (scriptIndex !== items.length || scriptIndex >= total) throw new Error('统一流脚本序号无效');
        items.push(event.data.script as unknown as ScriptIR);
        scripts.set(index, items);
      }
      if (event.type === 'phase_complete' && event.phase === 'program') {
        const index = readFrameIndex(event.data.target_index, 'program target_index');
        const scriptCount = readFrameIndex(event.data.script_count, 'program script_count');
        if (programs.has(index) || !metadata.has(index) || !shared.has(index) || scriptCount !== (scripts.get(index) ?? []).length || scriptCount !== scriptTotals.get(index)) throw new Error('统一流程序完成信息无效');
        const program = buildProgram(metadata.get(index)!, shared.get(index)!, scripts.get(index) ?? []);
        programs.set(index, {
          targetIndex: index,
          metadata: metadata.get(index) as unknown as UnifiedProgramDraft['metadata'],
          state: shared.get(index) as unknown as UnifiedProgramDraft['state'],
          scripts: scripts.get(index) ?? [],
          program
        });
      }
    };
    if (request.scope === 'project') {
      const manifestRequest: ProjectManifestGenerationRequest = {
        ...request,
        scope: 'project',
        project_context: request.project_context!,
        output_contract: 'project_manifest'
      };
      await projectManifestEventSource(manifestRequest, handleEvent, signal);
    } else {
      await streamUnifiedGeneration(request, handleEvent, signal);
    }

    assertSnapshotIsCurrent(this.bridge, snapshot);
    if (projectSnapshot) assertProjectSnapshotIsCurrent(this.bridge, projectSnapshot);
    if (!requirements.value || !plan.value) throw new Error('统一生成流缺少需求或计划');
    if (request.scope === 'project') {
      if (!manifest.value || !taskSpec.value || !blueprint.value) throw new Error('统一生成流缺少任务合同、蓝图或项目清单');
      if (!taskSpecHash.value || !blueprintHash.value || await contractSha256(taskSpec.value) !== taskSpecHash.value || await contractSha256(blueprint.value) !== blueprintHash.value) throw new Error('任务合同或蓝图哈希校验失败');
      assertProjectManifest(manifest.value, this.existingSpriteNames(), request.permitted_extensions, this.enabledExtensions);
      const lineageIssues = projectTaskSpecToBlueprint(taskSpec.value, blueprint.value);
      lineageIssues.push(...projectBlueprintToManifest(blueprint.value, manifest.value));
      if (lineageIssues.length) throw new ProgramIRValidationError(lineageIssues);
    }
    const ordered = [...programs.values()].sort((left, right) => left.targetIndex - right.targetIndex);
    if (request.scope === 'single_sprite' && ordered.length !== 1) throw new Error('统一生成流缺少当前目标程序');
    if (request.scope === 'project' && ordered.length !== 0 && ordered.length !== manifest.value!.program_targets.length) throw new Error('统一生成流缺少项目目标程序');
    for (const item of ordered) {
      const planForTarget = targetPlan(request, manifest.value, item.targetIndex);
      const targetKind = planForTarget.targetRef.kind === 'stage' || (planForTarget.targetRef.kind === 'current_target' && snapshot.isStage) ? 'stage' : 'sprite';
      const enabledExtensions = new Set<BuiltinAiExtension>((manifest.value?.required_extensions ?? []).filter(isBuiltinAiExtension));
      const result = validateProgramIR(item.program, targetKind, enabledExtensions);
      if (!result.valid) throw new ProgramIRValidationError(result.issues);
      if (request.scope === 'project') {
        const sharedResult = validateProjectProgramSharedState(item.program!, manifest.value!.shared_state, targetKind);
        if (!sharedResult.valid) throw new ProgramIRValidationError(sharedResult.issues);
        const stateResult = validateProjectProgramStateRefs(item.program!, manifest.value!);
        if (!stateResult.valid) throw new ProgramIRValidationError(stateResult.issues);
      }
    }
    // The verifier uses a dynamically imported fresh VM. Load it before the
    // transaction so a Commit never starts a code-split network request.
    throwIfAborted(signal);
    await prewarmCandidateVerificationRuntime();
    throwIfAborted(signal);
    return {request, snapshot, projectSnapshot, requirements: requirements.value, taskSpec: taskSpec.value ?? undefined, blueprint: blueprint.value ?? undefined, plan: plan.value, manifest: manifest.value, programs: ordered};
  }

  async commit(
    draft: UnifiedGenerationDraft & {request: UnifiedGenerationRequest; snapshot: AiTargetSnapshot; projectSnapshot?: AiProjectSnapshot},
    options: UnifiedCommitOptions = {}
  ): Promise<UnifiedCommitResult> {
    const commitStartedAt = monotonicNow();
    let backup: Uint8Array | undefined;
    let transactionLock: {assertActive: () => void; release: () => void} | undefined;
    let transactionVm = this.bridge.currentVm;
    const appliedPrograms: AiGenerationResult[] = [];
    const journal: ApplyEntry[] = (options.generatedAssets ?? []).map(asset => ({kind: 'asset', assetId: asset.id}));
    // A mutation can throw before its detailed journal receipt is returned.
    // Track the attempt separately so recovery never treats a partially
    // applied operation as a clean rollback.
    let mutationAttempted = false;
    const checkpointStore = options.checkpointStore ?? createCheckpointStore();
    const checkpointRunId = options.runId ?? randomId('run');
    let checkpointStarted = false;
    let verificationErrorCode: CandidateVerificationFailureCode | undefined;
    const reportTelemetry = (telemetry: CommitTelemetry) => {
      try {
        options.onCommitTelemetry?.(telemetry);
      } catch {
        // Observability must never affect the project transaction outcome.
      }
    };
    const recordReceipt = async (
      kind: SerializableApplyReceipt['kind'],
      logicalId: string,
      targetLocator?: LogicalTargetLocator,
    ) => {
      if (!checkpointStarted) return;
      const after = new Uint8Array(await this.bridge.saveProject());
      await checkpointStore.appendReceipt(checkpointRunId, {
        sequence: ((await checkpointStore.get(checkpointRunId))?.journal.length ?? 0) + 1,
        kind,
        logical_id: logicalId,
        ...(targetLocator ? {target_locator: targetLocator} : {}),
        after_fingerprint: await fingerprintBytes(after)
      });
    };
    try {
      if (draft.request.scope === 'project' && draft.manifest && draft.programs.length !== draft.manifest.program_targets.length) {
        throw new Error('项目程序必须在提交前全部生成并校验');
      }
      transactionLock = this.bridge.beginProjectTransaction();
      transactionVm = this.bridge.currentVm;
      throwIfAborted(options.signal);
      assertSnapshotIsCurrent(this.bridge, draft.snapshot);
      if (draft.projectSnapshot) assertProjectSnapshotIsCurrent(this.bridge, draft.projectSnapshot);
      const capturedBackup = new Uint8Array(await this.bridge.saveProject());
      backup = capturedBackup;
      assertSnapshotIsCurrent(this.bridge, draft.snapshot);
      if (draft.projectSnapshot) assertProjectSnapshotIsCurrent(this.bridge, draft.projectSnapshot);
      const originalLocator = await originalTargetLocator(this.bridge, draft.snapshot);
      const baseProjectFingerprint = await fingerprintBytes(capturedBackup);
      if (options.checkpointEnabled === false) throw new Error('当前部署已暂停 AI 项目恢复保护，无法安全提交');
      await checkpointStore.begin({
        runId: checkpointRunId,
        editorSessionId: createEditorSessionId(),
        projectEpoch: draft.projectSnapshot?.revision ?? draft.snapshot.revision,
        // The embedded editor currently exposes one local project storage slot;
        // keep checkpoint discovery aligned with ScratchHost recovery.
        projectStorageKey: LOCAL_PROJECT_STORAGE_KEY,
        baseSnapshotHash: await hashCheckpointPlan({
          target: draft.snapshot.targetName,
          revision: draft.snapshot.revision,
          projectRevision: draft.projectSnapshot?.revision
        }),
        baseProjectFingerprint,
        resolvedPlanHash: await hashCheckpointPlan({manifest: draft.manifest, programs: draft.programs.map(item => item.program)}),
        originalTargetLocator: originalLocator,
        backup: capturedBackup
      });
      checkpointStarted = true;
      const builtinExtensions = (draft.manifest?.required_extensions ?? [])
        .filter(isBuiltinAiExtension);
      if (builtinExtensions.length) {
        // Built-in extension loading is a project mutation, so it happens only
        // after the durable SB3 backup/checkpoint exists and before any graph write.
        mutationAttempted = true;
        this.bridge.runProjectTransactionMutation(() => this.bridge.ensureBuiltinExtensions(builtinExtensions));
        transactionLock.assertActive();
        await recordReceipt('state', 'builtin_extensions', originalLocator);
      }
      const project = draft.manifest ? createProjectApplyResult(draft.snapshot, capturedBackup) : undefined;
      const transaction = project ? {
        result: project,
        journal,
        assertActive: () => transactionLock!.assertActive(),
        onResourceApplied: async (operation: ProjectManifest['resource_ops'][number]) => recordReceipt('resource', operation.op_id)
      } : undefined;
      if (draft.manifest && draft.manifest.resource_ops.length) {
        if (!options.preparedResources) throw new Error('项目资源尚未准备完成');
        if (!transaction) throw new Error('项目资源事务尚未初始化');
        mutationAttempted = true;
        const resourceExecutor = options.resourceExecutor ?? createProjectResourceExecutor({bridge: this.bridge, currentTargetId: draft.snapshot.targetId});
        await applyProjectResources({
          bridge: this.bridge,
          manifest: draft.manifest,
          snapshot: draft.snapshot,
          prepared: options.preparedResources,
          executor: resourceExecutor,
          signal: options.signal,
          onProgress: (current, total, label) => options.onProgress?.({current, total, label})
        }, transaction);
      }
      if (draft.manifest) {
        transactionLock!.assertActive();
        // State mutations are restored from the pre-commit backup if anything later fails.
        mutationAttempted = true;
        journal.push({kind: 'resource_batch', opId: 'manifest_state'});
        installManifestState(this.bridge, draft.manifest, draft.snapshot.targetId, project);
        await recordReceipt('state', 'manifest_state', originalLocator);
        transactionLock!.assertActive();
      }
      const programCount = draft.programs.length;
      const isProjectCommit = Boolean(draft.request.scope === 'project' && draft.manifest);
      const resourceBindings = buildLogicalResourceBindings(project);
      const stateBindings = draft.manifest ? buildLogicalStateBindings(this.bridge, draft.manifest, draft.snapshot.targetId, project) : new Map();
      const programPort = createScratchProgramPort(this.bridge);
      const compiledPrograms: Array<{
        index: number;
        targetId: string;
        target: ScratchTargetLike;
        snapshot: AiTargetSnapshot;
        program: ProgramIR;
        prepared: ReturnType<typeof prepareProgramForTarget>;
      }> = [];
      // No block is written until every target's program has passed validation and
      // compilation against the post-resource VM state.
      for (let index = 0; index < programCount; index += 1) {
        transactionLock!.assertActive();
        throwIfAborted(options.signal);
        const item = draft.programs[index];
        if (!item) throw new Error('项目程序草稿序号无效：' + index);
        const targetIndex = item.targetIndex;
        const targetPlanForProgram = targetPlan(draft.request, draft.manifest, targetIndex);
        const targetId = targetIdForRef(this.bridge, targetPlanForProgram.targetRef, draft.snapshot.targetId, project);
        const target = targetById(this.bridge, targetId);
        transactionLock!.assertActive();
        this.bridge.runProjectTransactionMutation(() => this.bridge.currentVm?.setEditingTarget?.(targetId));
        transactionLock!.assertActive();
        const targetSnapshot = readAiTargetSnapshot(this.bridge, targetPlanForProgram.mode, target);
        if (!item.program) throw new Error('项目程序草稿缺失：' + index);
        const program = item.program;
        const enabledExtensions = new Set<BuiltinAiExtension>((draft.manifest?.required_extensions ?? []).filter(isBuiltinAiExtension));
        const preflight = preflightProgram(program, targetSnapshot.isStage ? 'stage' : 'sprite', enabledExtensions, draft.request.scope === 'project' ? draft.manifest : undefined);
        if (!preflight.valid) throw new ProgramIRValidationError(preflight.issues);
        const prepared = prepareProgramForTarget(this.bridge, targetSnapshot, targetPlanForProgram.mode, program, resourceBindings, stateBindings, enabledExtensions);
        compiledPrograms.push({index, targetId, target, snapshot: targetSnapshot, program, prepared});
      }
      for (const queued of compiledPrograms) {
        const {index, targetId, target, snapshot: targetSnapshot, program, prepared} = queued;
        transactionLock!.assertActive();
        throwIfAborted(options.signal);
        journal.push({kind: 'program', targetId, scriptIds: program.scripts.map(script => script.id)});
        const apply = () => programPort.apply({target, compiled: prepared.compiled, removeTopIds: prepared.removeTopIds, pending: prepared.pending, signal: options.signal, onProgress: progress => {
          if (isProjectCommit) {
            options.onProgress?.({current: index, total: programCount, label: targetSnapshot.targetName + ' · ' + progress.current + '/' + progress.total + ' · ' + progress.label});
          } else {
            options.onProgress?.({...progress, current: progress.current + index});
          }
        }, targetName: targetSnapshot.targetName});
        mutationAttempted = true;
        const applied = typeof this.bridge.runProjectTransactionMutation === 'function'
          ? this.bridge.runProjectTransactionMutation(apply)
          : apply();
        transactionLock!.assertActive();
        appliedPrograms.push(applied);
        project?.appliedPrograms.push({target_id: applied.targetId, added_blocks: applied.addedBlocks, added_scripts: applied.addedScripts, removed_scripts: applied.removedScripts});
        if (options.onProgress) options.onProgress({current: index + 1, total: programCount, label: targetSnapshot.targetName + ' 的程序已写入'});
        const logicalId = draft.manifest?.program_targets[index]?.job_id ?? `program_${index}`;
        await recordReceipt('program', logicalId, await originalTargetLocator(this.bridge, targetSnapshot));
      }
      transactionLock!.assertActive();
      this.bridge.runProjectTransactionMutation(() => this.bridge.currentVm?.setEditingTarget?.(draft.snapshot.targetId));
      transactionLock!.assertActive();
      this.bridge.refreshWorkspace();
      transactionLock!.assertActive();
      if (project) project.revision_at_complete = this.bridge.projectRevision;
      const candidate = new Uint8Array(await this.bridge.saveProject());
      await checkpointStore.markVerifying(checkpointRunId, candidate, await fingerprintBytes(candidate));
      const verificationStartedAt = monotonicNow();
      const verification = await verifyCandidateSb3(candidate);
      if (!verification.passed) {
        verificationErrorCode = verification.errorCode;
        throw new Error('候选 SB3 验证失败：' + (verification.error ?? '未知错误'));
      }
      try {
        await markGeneratedAssetsAttached((options.generatedAssets ?? []).map(asset => asset.id), checkpointRunId);
      } catch {
        // Asset attachment is metadata only. Keep the generated result as
        // unattached rather than rolling back an already verified Scratch VM.
      }
      await checkpointStore.markCompleted(checkpointRunId);
      reportTelemetry({
        outcome: 'COMPLETED',
        commitDurationMs: Math.max(0, Math.round(verificationStartedAt - commitStartedAt)),
        verifyDurationMs: Math.max(0, Math.round(monotonicNow() - verificationStartedAt)),
        behaviorTestCount: 0,
        behaviorPassedCount: 0,
        behaviorMustCount: 0,
        behaviorMustPassedCount: 0,
      });
      const result = {
        backup: capturedBackup,
        originalTargetId: draft.snapshot.targetId,
        revisionAtComplete: this.bridge.projectRevision,
        programs: appliedPrograms,
        ...(project ? {project} : {}),
        checkpointRunId
      };
      return result;
    } catch (error) {
      let transactionInvalidated = false;
      try {
        transactionLock?.assertActive();
      } catch {
        transactionInvalidated = true;
      }
      let compensationError: unknown;
      let requiresBackupRestore = mutationAttempted || journal.some(entry => entry.kind === 'program' || entry.kind === 'resource_batch');
      if (journal.length) {
        try {
          requiresBackupRestore = (await compensateJournal(this.bridge, journal)) || requiresBackupRestore;
        } catch (caught) {
          compensationError = caught;
        }
      }
      let restoreError: unknown;
      const vmRebound = transactionVm !== this.bridge.currentVm;
      const canTrackOwnMutations = typeof this.bridge.runProjectTransactionMutation === 'function';
      const concurrentProjectChange = transactionInvalidated && canTrackOwnMutations && !vmRebound && this.bridge.projectRevision !== draft.snapshot.revision;
      if (backup && (requiresBackupRestore || !journal.length || compensationError) && !concurrentProjectChange) {
        try {
          // A stale lock must not prevent restoring the durable backup.
          await this.bridge.loadProject(backup);
          this.bridge.currentVm?.setEditingTarget?.(draft.snapshot.targetId);
          this.bridge.refreshWorkspace();
          if (checkpointStarted) await checkpointStore.markRolledBack(checkpointRunId);
        } catch (caught) {
          restoreError = caught;
        }
      }
      if (checkpointStarted && !compensationError && !restoreError && !concurrentProjectChange) {
        await checkpointStore.markRolledBack(checkpointRunId);
      }
      if (checkpointStarted && (restoreError || compensationError || concurrentProjectChange)) {
        try {
          await checkpointStore.markFailedNeedsRecovery(checkpointRunId);
        } catch (checkpointError) {
          restoreError = restoreError ?? checkpointError;
        }
      }
      const failedNeedsRecovery = Boolean(compensationError || restoreError || concurrentProjectChange);
      reportTelemetry({
        outcome: failedNeedsRecovery ? 'FAILED_NEEDS_RECOVERY' : backup ? 'ROLLED_BACK' : 'REJECTED_BEFORE_WRITE',
        commitDurationMs: Math.max(0, Math.round(monotonicNow() - commitStartedAt)),
        verifyDurationMs: 0,
        behaviorTestCount: 0,
        behaviorPassedCount: 0,
        behaviorMustCount: 0,
        behaviorMustPassedCount: 0,
        ...(failedNeedsRecovery
          ? {errorCode: compensationError || restoreError ? 'COMMIT_RECOVERY_FAILED' as const : 'COMMIT_CONFLICT' as const}
          : {errorCode: verificationErrorCode ?? 'COMMIT_FAILED' as const}),
      });
      if (compensationError || restoreError) {
        const details = [
          transactionInvalidated ? 'AI 项目事务已失效' : '',
          compensationError ? '局部补偿失败：' + (compensationError instanceof Error ? compensationError.message : '未知错误') : '',
          restoreError ? '无法恢复项目备份：' + (restoreError instanceof Error ? restoreError.message : '未知错误') : '',
          error instanceof Error ? '原始错误：' + error.message : '原始错误：未知错误'
        ].filter(Boolean).join('；');
        throw new Error('AI 项目提交失败，' + details);
      }
      if (transactionInvalidated) {
        throw new Error('AI 项目提交失败，项目事务已失效：' + (error instanceof Error ? error.message : '未知错误'));
      }
      throw error;
    } finally {
      transactionLock?.release();
    }
  }

  private existingSpriteNames() {
    return this.bridge.currentVm?.runtime.targets?.filter(target => !target.isStage && target.isOriginal !== false)
      .map(target => String(target.getName?.() ?? target.sprite?.name ?? '').trim())
      .filter(Boolean) ?? [];
  }
}

export async function undoUnifiedGeneration(bridge: ScratchGuiBridge, result: UnifiedCommitResult) {
  if (bridge.projectRevision !== result.revisionAtComplete) throw new Error('生成后项目已经继续修改，无法安全撤销本次生成');
  const release = bridge.beginBlockGeneration();
  try {
    bridge.stop();
    await bridge.loadProject(result.backup);
    bridge.currentVm?.setEditingTarget?.(result.originalTargetId);
    bridge.refreshWorkspace();
    bridge.activateCodeTab();
  } finally {
    release();
  }
}
