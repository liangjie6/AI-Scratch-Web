import {getBlockSpec} from './block-catalog';
import {deleteGeneratedAsset, type GeneratedAsset} from '../ai/asset-library-store';
import {
  compileProgramIR,
  validateCompiledGraph,
  type CompiledProgram,
  type ProgramResolver,
  type VMBlock
} from './program-compiler';
import {
  ProgramIRValidationError,
  type ProgramIR,
  type ScriptIR,
  type TargetKind
} from './program-ir';
import {validateProgramIR} from './program-compiler';
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
import {assertProjectManifest, validateProjectProgramSharedState, type ProjectManifest, type TargetRef} from './project-plan';
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
}

export interface UnifiedCommitOptions {
  preparedResources?: Map<string, PreparedResource>;
  resourceExecutor?: ProjectResourceExecutor;
  signal?: AbortSignal;
  onProgress?: (progress: AiGenerationProgress) => void;
  generatedAssets?: GeneratedAsset[];
  generateProgram?: (context: {targetIndex: number; targetRef: TargetRef; mode: AiBlocksMode; snapshot: AiTargetSnapshot}) => Promise<ProgramIR>;
}

interface PreparedProgram {
  targetIndex: number;
  targetRef: TargetRef;
  mode: AiBlocksMode;
  program: ProgramIR;
}

interface PendingVariable {
  owner: ScratchTargetLike;
  id: string;
  name: string;
  type: ScratchVariableLike['type'];
  value: ScratchVariableLike['value'];
}

const clone = <T>(value: T): T => structuredClone(value);

function throwIfAborted(signal?: AbortSignal) {
  if (signal?.aborted) throw new DOMException('AI block generation cancelled', 'AbortError');
}

async function compensateJournal(bridge: ScratchGuiBridge, journal: readonly ApplyEntry[]): Promise<boolean> {
  const failures: unknown[] = [];
  let requiresBackupRestore = false;
  for (const entry of [...journal].reverse()) {
    try {
      if (entry.kind === 'program' || entry.kind === 'resource_batch') {
        requiresBackupRestore = true;
      } else if (entry.kind === 'asset') {
        await deleteGeneratedAsset(entry.assetId);
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

function ownVariable(target: ScratchTargetLike, name: string, type: ScratchVariableLike['type']) {
  return Object.values(target.variables ?? {}).find(variable => variable.name === name && variable.type === type);
}

function createResolver(
  target: ScratchTargetLike,
  stage: ScratchTargetLike,
  program: ProgramIR,
  pending: PendingVariable[]
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
    broadcast: name => broadcastMap.get(name) ?? resolveExisting(name, 'broadcast_msg')
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

function collectInputUnit(compiled: CompiledProgram, id: string, output: Set<string>) {
  if (output.has(id)) return;
  const block = compiled.blocks[id];
  if (!block) return;
  for (const [name, input] of Object.entries(block.inputs)) {
    if (name === 'SUBSTACK' || name === 'SUBSTACK2') continue;
    collectInputUnit(compiled, input.block, output);
    if (input.shadow && input.shadow !== input.block) collectInputUnit(compiled, input.shadow, output);
  }
  output.add(id);
}

function blockForPrefix(block: VMBlock, created: Set<string>): ScratchBlockLike {
  const next = block.next && created.has(block.next) ? block.next : null;
  const inputs = Object.fromEntries(
    Object.entries(block.inputs)
      .filter(([, input]) => created.has(input.block) && (!input.shadow || created.has(input.shadow)))
      .map(([name, input]) => [name, clone(input)])
  );
  return {...clone(block), next, inputs} as ScratchBlockLike;
}

function connectionFor(compiled: CompiledProgram, id: string) {
  const block = compiled.blocks[id];
  if (!block?.parent) return null;
  const parent = compiled.blocks[block.parent];
  if (!parent) return null;
  if (parent.next === id) return {parent: parent.id};
  const input = Object.entries(parent.inputs).find(([, value]) => value.block === id);
  return input ? {parent: parent.id, input: input[0]} : null;
}

function installVariables(pending: PendingVariable[]) {
  for (const variable of pending) {
    variable.owner.createVariable?.(variable.id, variable.name, variable.type, false);
    const created = variable.owner.variables?.[variable.id];
    if (!created) throw new Error('Scratch 无法创建变量：' + variable.name);
    created.value = clone(variable.value);
  }
}

function removeScripts(target: ScratchTargetLike, ids: string[]) {
  for (const id of ids) target.blocks?.deleteBlock(id);
}

function applyCompiledProgram(
  bridge: ScratchGuiBridge,
  target: ScratchTargetLike,
  compiled: CompiledProgram,
  removeTopIds: string[],
  pending: PendingVariable[],
  signal: AbortSignal | undefined,
  onProgress: ((progress: AiGenerationProgress) => void) | undefined,
  targetName: string
): AiGenerationResult {
  throwIfAborted(signal);
  installVariables(pending);
  removeScripts(target, removeTopIds);
  const blocks = target.blocks!;
  const created = new Set<string>();
  const visibleOrder = compiled.blockOrder.filter(id => !compiled.blocks[id].shadow);
  let visibleCreated = 0;
  for (const visibleId of visibleOrder) {
    throwIfAborted(signal);
    const unit = new Set<string>();
    collectInputUnit(compiled, visibleId, unit);
    for (const id of unit) {
      if (created.has(id)) continue;
      created.add(id);
      blocks.createBlock(blockForPrefix(compiled.blocks[id], created));
      if (!compiled.blocks[id].shadow) visibleCreated += 1;
    }
    const connection = connectionFor(compiled, visibleId);
    if (connection) blocks.moveBlock({id: visibleId, newParent: connection.parent, ...(connection.input ? {newInput: connection.input} : {})});
    onProgress?.({current: visibleCreated, total: visibleOrder.length, label: getBlockSpec(compiled.blocks[visibleId].opcode)?.shape === 'hat' ? '正在添加触发积木' : '正在添加 ' + compiled.blocks[visibleId].opcode});
  }
  for (const script of compiled.scripts) {
    const top = compiled.blocks[script.topLevelId];
    target.createComment?.(script.topLevelId + '_comment', script.topLevelId, script.comment, (top.x ?? 0) + 180, top.y ?? 0, 220, 80, true);
  }
  bridge.refreshWorkspace();
  return {
    targetId: target.id,
    targetName,
    addedBlocks: visibleOrder.length,
    addedScripts: compiled.scripts.length,
    removedScripts: removeTopIds.length,
    revisionAtComplete: bridge.projectRevision
  };
}

function prepareProgramForTarget(
  bridge: ScratchGuiBridge,
  snapshot: AiTargetSnapshot,
  mode: AiBlocksMode,
  program: ProgramIR
): {compiled: CompiledProgram; pending: PendingVariable[]; removeTopIds: string[]} {
  assertSnapshotIsCurrent(bridge, snapshot);
  const target = targetById(bridge, snapshot.targetId);
  const stage = stageFor(bridge);
  const removeTopIds = mode === 'rewrite'
    ? target.blocks!.getScripts().filter(id => isAiEditableScript(target.blocks!, id))
    : mode === 'modify'
      ? program.remove_script_ids.map(ref => {
        const id = snapshot.scriptRefs[ref];
        if (!id) throw new Error('AI 尝试修改不存在的脚本：' + ref);
        return id;
      })
      : [];
  if (mode !== 'modify' && program.remove_script_ids.length) throw new Error('当前生成模式不允许删除已有脚本');
  const pending: PendingVariable[] = [];
  const positioned = positionProgram(program, mode, target.blocks!, removeTopIds);
  const compiled = compileProgramIR(positioned, {
    target: (target.isStage ? 'stage' : 'sprite') as TargetKind,
    resolver: createResolver(target, stage, program, pending),
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
    schema_version: metadata.schema_version as 1,
    summary: typeof metadata.summary === 'string' ? metadata.summary : '',
    remove_script_ids: Array.isArray(metadata.remove_script_ids) ? metadata.remove_script_ids as string[] : [],
    variables: Array.isArray(shared.variables) ? shared.variables as ProgramIR['variables'] : [],
    lists: Array.isArray(shared.lists) ? shared.lists as ProgramIR['lists'] : [],
    broadcasts: Array.isArray(shared.broadcasts) ? shared.broadcasts as string[] : [],
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

export class UnifiedGenerationRunner {
  constructor(private readonly bridge: ScratchGuiBridge) {}

  async prepare(
    request: UnifiedGenerationRequest,
    onEvent?: (event: UnifiedGenerationEvent) => void,
    signal?: AbortSignal
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
      if (event.type === 'phase_complete' && event.phase === 'plan') {
        plan.value = asPlan(event);
        if (event.data.manifest) manifest.value = event.data.manifest as ProjectManifest;
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
      await streamProjectManifest(manifestRequest, handleEvent, signal);
    } else {
      await streamUnifiedGeneration(request, handleEvent, signal);
    }

    assertSnapshotIsCurrent(this.bridge, snapshot);
    if (projectSnapshot) assertProjectSnapshotIsCurrent(this.bridge, projectSnapshot);
    if (!requirements.value || !plan.value) throw new Error('统一生成流缺少需求或计划');
    if (request.scope === 'project') {
      if (!manifest.value) throw new Error('统一生成流缺少项目清单');
      assertProjectManifest(manifest.value, this.existingSpriteNames());
    }
    const ordered = [...programs.values()].sort((left, right) => left.targetIndex - right.targetIndex);
    if (request.scope === 'single_sprite' && ordered.length !== 1) throw new Error('统一生成流缺少当前目标程序');
    if (request.scope === 'project' && ordered.length !== 0 && ordered.length !== manifest.value!.program_targets.length) throw new Error('统一生成流缺少项目目标程序');
    for (const item of ordered) {
      const planForTarget = targetPlan(request, manifest.value, item.targetIndex);
      const targetKind = planForTarget.targetRef.kind === 'stage' || (planForTarget.targetRef.kind === 'current_target' && snapshot.isStage) ? 'stage' : 'sprite';
      const result = validateProgramIR(item.program, targetKind);
      if (!result.valid) throw new ProgramIRValidationError(result.issues);
      if (request.scope === 'project') {
        const sharedResult = validateProjectProgramSharedState(item.program!, manifest.value!.shared_state, targetKind);
        if (!sharedResult.valid) throw new ProgramIRValidationError(sharedResult.issues);
      }
    }
    return {request, snapshot, projectSnapshot, requirements: requirements.value, plan: plan.value, manifest: manifest.value, programs: ordered};
  }

  async commit(
    draft: UnifiedGenerationDraft & {request: UnifiedGenerationRequest; snapshot: AiTargetSnapshot; projectSnapshot?: AiProjectSnapshot},
    options: UnifiedCommitOptions = {}
  ): Promise<UnifiedCommitResult> {
    let backup: Uint8Array | undefined;
    let transactionLock: {assertActive: () => void; release: () => void} | undefined;
    let transactionVm = this.bridge.currentVm;
    const appliedPrograms: AiGenerationResult[] = [];
    const journal: ApplyEntry[] = (options.generatedAssets ?? []).map(asset => ({kind: 'asset', assetId: asset.id}));
    try {
      transactionLock = this.bridge.beginProjectTransaction();
      transactionVm = this.bridge.currentVm;
      throwIfAborted(options.signal);
      assertSnapshotIsCurrent(this.bridge, draft.snapshot);
      if (draft.projectSnapshot) assertProjectSnapshotIsCurrent(this.bridge, draft.projectSnapshot);
      const capturedBackup = new Uint8Array(await this.bridge.saveProject());
      backup = capturedBackup;
      assertSnapshotIsCurrent(this.bridge, draft.snapshot);
      if (draft.projectSnapshot) assertProjectSnapshotIsCurrent(this.bridge, draft.projectSnapshot);
      const project = draft.manifest ? createProjectApplyResult(draft.snapshot, capturedBackup) : undefined;
      const transaction = project ? {result: project, journal, assertActive: () => transactionLock!.assertActive()} : undefined;
      if (draft.manifest && draft.manifest.resource_ops.length) {
        if (!options.preparedResources) throw new Error('项目资源尚未准备完成');
        if (!transaction) throw new Error('项目资源事务尚未初始化');
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
      const programCount = options.generateProgram && draft.manifest
        ? draft.manifest.program_targets.length
        : draft.programs.length;
      const isProjectCommit = Boolean(draft.request.scope === 'project' && draft.manifest);
      for (let index = 0; index < programCount; index += 1) {
        transactionLock!.assertActive();
        throwIfAborted(options.signal);
        let targetIndex = index;
        if (!options.generateProgram) {
          const item = draft.programs[index];
          if (!item) throw new Error('项目程序草稿序号无效：' + index);
          targetIndex = item.targetIndex;
        }
        const targetPlanForProgram = targetPlan(draft.request, draft.manifest, targetIndex);
        const targetId = targetIdForRef(this.bridge, targetPlanForProgram.targetRef, draft.snapshot.targetId, project);
        const target = targetById(this.bridge, targetId);
        transactionLock!.assertActive();
        this.bridge.currentVm?.setEditingTarget?.(targetId);
        transactionLock!.assertActive();
        const targetSnapshot = readAiTargetSnapshot(this.bridge, targetPlanForProgram.mode, target);
        let program: ProgramIR;
        if (options.generateProgram) {
          program = await options.generateProgram({targetIndex, targetRef: targetPlanForProgram.targetRef, mode: targetPlanForProgram.mode, snapshot: targetSnapshot});
        } else {
          const item = draft.programs[index];
          if (!item?.program) throw new Error('项目程序草稿缺失：' + index);
          program = item.program;
        }
        const programValidation = validateProgramIR(program, targetSnapshot.isStage ? 'stage' : 'sprite');
        if (!programValidation.valid) throw new ProgramIRValidationError(programValidation.issues);
        if (draft.request.scope === 'project' && draft.manifest) {
          const sharedValidation = validateProjectProgramSharedState(program, draft.manifest.shared_state, targetSnapshot.isStage ? 'stage' : 'sprite');
          if (!sharedValidation.valid) throw new ProgramIRValidationError(sharedValidation.issues);
        }
        const prepared = prepareProgramForTarget(this.bridge, targetSnapshot, targetPlanForProgram.mode, program);
        journal.push({kind: 'program', targetId, scriptIds: program.scripts.map(script => script.id)});
        const apply = () => applyCompiledProgram(this.bridge, target, prepared.compiled, prepared.removeTopIds, prepared.pending, options.signal, progress => {
          if (isProjectCommit) {
            options.onProgress?.({current: index, total: programCount, label: targetSnapshot.targetName + ' · ' + progress.current + '/' + progress.total + ' · ' + progress.label});
          } else {
            options.onProgress?.({...progress, current: progress.current + index});
          }
        }, targetSnapshot.targetName);
        const applied = typeof this.bridge.runProjectTransactionMutation === 'function'
          ? this.bridge.runProjectTransactionMutation(apply)
          : apply();
        transactionLock!.assertActive();
        appliedPrograms.push(applied);
        project?.appliedPrograms.push({target_id: applied.targetId, added_blocks: applied.addedBlocks, added_scripts: applied.addedScripts, removed_scripts: applied.removedScripts});
        if (options.onProgress) options.onProgress({current: index + 1, total: programCount, label: targetSnapshot.targetName + ' 的程序已写入'});
      }
      transactionLock!.assertActive();
      this.bridge.currentVm?.setEditingTarget?.(draft.snapshot.targetId);
      transactionLock!.assertActive();
      this.bridge.refreshWorkspace();
      transactionLock!.assertActive();
      if (project) project.revision_at_complete = this.bridge.projectRevision;
      const result = {
        backup: capturedBackup,
        originalTargetId: draft.snapshot.targetId,
        revisionAtComplete: this.bridge.projectRevision,
        programs: appliedPrograms,
        ...(project ? {project} : {})
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
      let requiresBackupRestore = journal.some(entry => entry.kind === 'program' || entry.kind === 'resource_batch');
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
        } catch (caught) {
          restoreError = caught;
        }
      }
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
