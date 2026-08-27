import {getBlockSpec} from './block-catalog';
import {
  compileProgramIR,
  type CompiledProgram,
  type ProgramResolver,
  type VMBlock
} from './program-compiler';
import type {ProgramIR, TargetKind} from './program-ir';
import type {AiBlocksMode} from './ai-blocks-api';
import type {AiTargetSnapshot} from './target-context';
import {assertSnapshotIsCurrent, isAiEditableScript} from './target-context';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';
import {createId} from '../shared/create-id';

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
  backup: Uint8Array;
}

interface ApplyOptions {
  bridge: ScratchGuiBridge;
  snapshot: AiTargetSnapshot;
  mode: AiBlocksMode;
  program: ProgramIR;
  signal?: AbortSignal;
  onProgress?: (progress: AiGenerationProgress) => void;
}

interface PendingVariable {
  owner: ScratchTargetLike;
  id: string;
  name: string;
  type: '' | 'list' | 'broadcast_msg';
  value: ScratchVariableLike['value'];
}

interface RemovedState {
  blocks: Record<string, ScratchBlockLike>;
  comments: ScratchCommentLike[];
}

const clone = <T>(value: T): T => structuredClone(value);
const ownVariable = (target: ScratchTargetLike, name: string, type: ScratchVariableLike['type']) =>
  Object.values(target.variables ?? {}).find(variable => variable.name === name && variable.type === type);

function abortError() {
  return new DOMException('AI block generation cancelled', 'AbortError');
}

function throwIfAborted(signal?: AbortSignal) {
  if (signal?.aborted) throw abortError();
}

function abortableDelay(ms: number, signal?: AbortSignal) {
  if (!signal) return new Promise<void>(resolve => window.setTimeout(resolve, ms));
  if (signal.aborted) return Promise.reject(abortError());
  return new Promise<void>((resolve, reject) => {
    const timer = window.setTimeout(() => {
      signal.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      window.clearTimeout(timer);
      reject(abortError());
    };
    signal.addEventListener('abort', onAbort, {once: true});
  });
}

function randomId(prefix: string) {
  return prefix + '_' + createId().replaceAll('-', '');
}

function positionProgram(
  program: ProgramIR,
  mode: AiBlocksMode,
  blocks: ScratchBlocksLike,
  removeTopIds: string[]
): ProgramIR {
  const positioned = clone(program);
  const topIds = blocks.getScripts();
  const existing = topIds.map(id => blocks.getBlock(id)).filter(Boolean) as ScratchBlockLike[];
  const removed = new Set(removeTopIds);
  const survivors = topIds
    .filter(id => !removed.has(id))
    .map(id => blocks.getBlock(id))
    .filter(Boolean) as ScratchBlockLike[];
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
      return;
    }
    script.x = baseX + Math.floor(index / 4) * 360;
    script.y = 40 + (index % 4) * 220;
  });
  return positioned;
}

function collectRemovedState(target: ScratchTargetLike, topIds: string[]): RemovedState {
  const blocks = target.blocks!;
  const ids = new Set<string>();
  const visit = (id: string | null) => {
    if (!id || ids.has(id)) return;
    const block = blocks.getBlock(id);
    if (!block) return;
    ids.add(id);
    visit(block.next);
    Object.values(block.inputs).forEach(input => {
      visit(input.block);
      if (input.shadow !== input.block) visit(input.shadow);
    });
  };
  topIds.forEach(visit);
  return {
    blocks: Object.fromEntries([...ids].map(id => [id, clone(blocks.getBlock(id)!)])),
    comments: Object.values(target.comments ?? {})
      .filter(comment => Boolean(comment.blockId && ids.has(comment.blockId)))
      .map(comment => clone(comment))
  };
}

function removeScripts(target: ScratchTargetLike, topIds: string[], removed: RemovedState) {
  const commentIds = new Set(removed.comments.map(comment => comment.id));
  for (const commentId of commentIds) delete target.comments?.[commentId];
  topIds.forEach(id => target.blocks?.deleteBlock(id));
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
    const existing = ownVariable(owner, name, type)
      ?? (owner !== stage && scope === undefined ? ownVariable(stage, name, type) : undefined);
    if (existing) return {name: existing.name, id: existing.id};
    const id = randomId(type === 'list' ? 'ai_list' : type === 'broadcast_msg' ? 'ai_broadcast' : 'ai_variable');
    pending.push({owner, id, name, type, value});
    return {name, id};
  };

  for (const variable of program.variables) {
    variableMap.set(variable.name, ensure(variable.name, '', variable.scope, variable.value ?? 0));
  }
  for (const list of program.lists) {
    listMap.set(list.name, ensure(list.name, 'list', list.scope, list.value ? [...list.value] : []));
  }
  for (const name of program.broadcasts) {
    broadcastMap.set(name, ensure(name, 'broadcast_msg', 'global', name));
  }

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

function installVariables(pending: PendingVariable[]) {
  for (const variable of pending) {
    variable.owner.createVariable?.(variable.id, variable.name, variable.type, false);
    const created = variable.owner.variables?.[variable.id];
    if (!created) throw new Error('Scratch 无法创建变量：' + variable.name);
    created.value = clone(variable.value);
  }
}

function connectionFor(compiled: CompiledProgram, id: string) {
  const block = compiled.blocks[id];
  if (!block.parent) return null;
  const parent = compiled.blocks[block.parent];
  if (parent.next === id) return {parent: parent.id};
  const input = Object.entries(parent.inputs).find(([, value]) => value.block === id);
  return input ? {parent: parent.id, input: input[0]} : null;
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

async function injectProgressively(
  bridge: ScratchGuiBridge,
  target: ScratchTargetLike,
  compiled: CompiledProgram,
  signal: AbortSignal | undefined,
  onProgress: ApplyOptions['onProgress']
) {
  const blocks = target.blocks!;
  const created = new Set<string>();
  const visibleOrder = compiled.blockOrder.filter(id => !compiled.blocks[id].shadow);
  let visibleCreated = 0;

  for (const visibleId of visibleOrder) {
    if (created.has(visibleId)) continue;
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
    if (connection) {
      blocks.moveBlock({
        id: visibleId,
        newParent: connection.parent,
        ...(connection.input ? {newInput: connection.input} : {})
      });
    }
    bridge.refreshWorkspace();
    const opcode = compiled.blocks[visibleId].opcode;
    onProgress?.({
      current: visibleCreated,
      total: visibleOrder.length,
      label: getBlockSpec(opcode)?.shape === 'hat' ? '正在添加触发积木' : '正在添加 ' + opcode
    });
    await abortableDelay(110, signal);
  }
  return created;
}

function restoreRemovedState(target: ScratchTargetLike, removed: RemovedState) {
  const blocks = target.blocks!;
  Object.values(removed.blocks).forEach(block => blocks.createBlock(clone(block)));
  removed.comments.forEach(comment => {
    target.createComment?.(
      comment.id,
      comment.blockId,
      comment.text,
      comment.x,
      comment.y,
      comment.width,
      comment.height,
      comment.minimized
    );
  });
}

async function localRollback(
  bridge: ScratchGuiBridge,
  target: ScratchTargetLike,
  compiled: CompiledProgram,
  removed: RemovedState,
  pending: PendingVariable[]
) {
  const blocks = target.blocks!;
  compiled.topLevelIds.forEach(id => blocks.deleteBlock(id));
  compiled.blockOrder.forEach(id => blocks.deleteBlock(id));
  for (const script of compiled.scripts) delete target.comments?.[script.topLevelId + '_comment'];
  for (const variable of pending) variable.owner.deleteVariable?.(variable.id);
  restoreRemovedState(target, removed);
  bridge.refreshWorkspace();
}

export async function applyAiProgram(options: ApplyOptions): Promise<AiGenerationResult> {
  const {bridge, snapshot, mode, signal, onProgress} = options;
  assertSnapshotIsCurrent(bridge, snapshot);
  const vm = bridge.currentVm;
  const target = vm?.runtime.getTargetById?.(snapshot.targetId)
    ?? vm?.runtime.targets.find(item => item.id === snapshot.targetId);
  const blocks = target?.blocks;
  const stage = vm?.runtime.getTargetForStage?.()
    ?? vm?.runtime.targets.find(item => item.isStage);
  if (!vm || !target || !blocks || !stage) throw new Error('当前 Scratch 目标已不存在');

  const removeTopIds = mode === 'rewrite'
    ? [...blocks.getScripts()].filter(id => isAiEditableScript(blocks, id))
    : mode === 'modify'
      ? options.program.remove_script_ids.map(ref => {
        const id = snapshot.scriptRefs[ref];
        if (!id) throw new Error('AI 尝试修改不存在的脚本：' + ref);
        return id;
      })
      : [];
  if (mode !== 'modify' && options.program.remove_script_ids.length) {
    throw new Error('当前生成模式不允许删除已有脚本');
  }

  const pending: PendingVariable[] = [];
  const resolver = createResolver(target, stage, options.program, pending);
  const positioned = positionProgram(options.program, mode, blocks, removeTopIds);
  const idPrefix = randomId('ai');
  const compiled = compileProgramIR(positioned, {
    target: (target.isStage ? 'stage' : 'sprite') as TargetKind,
    resolver,
    idGenerator: (kind, ordinal) => idPrefix + '_' + (kind === 'shadow' ? 's' : 'b') + ordinal
  });
  const removed = collectRemovedState(target, removeTopIds);
  const backup = await bridge.saveProject();
  assertSnapshotIsCurrent(bridge, snapshot);
  const release = bridge.beginBlockGeneration();
  let mutated = false;

  try {
    throwIfAborted(signal);
    mutated = pending.length > 0;
    installVariables(pending);
    if (removeTopIds.length) {
      removeScripts(target, removeTopIds, removed);
      bridge.refreshWorkspace();
      mutated = true;
    }
    mutated = true;
    await injectProgressively(bridge, target, compiled, signal, onProgress);

    if (mode === 'append') {
      for (const script of compiled.scripts) {
        const top = compiled.blocks[script.topLevelId];
        target.createComment?.(
          script.topLevelId + '_comment',
          script.topLevelId,
          script.comment,
          (top.x ?? 0) + 180,
          top.y ?? 0,
          220,
          80,
          true
        );
      }
      bridge.refreshWorkspace();
    }

    return {
      targetId: target.id,
      targetName: snapshot.targetName,
      addedBlocks: compiled.blockOrder.filter(id => !compiled.blocks[id].shadow).length,
      addedScripts: compiled.scripts.length,
      removedScripts: removeTopIds.length,
      revisionAtComplete: bridge.projectRevision,
      backup
    };
  } catch (error) {
    if (mutated) {
      try {
        await localRollback(bridge, target, compiled, removed, pending);
      } catch {
        await bridge.loadProject(backup);
        bridge.currentVm?.setEditingTarget(snapshot.targetId);
      }
    }
    throw error;
  } finally {
    release();
  }
}

export async function undoAiGeneration(bridge: ScratchGuiBridge, result: AiGenerationResult) {
  if (bridge.projectRevision !== result.revisionAtComplete) {
    throw new Error('生成后项目已经继续修改，无法安全撤销本次生成');
  }
  const release = bridge.beginBlockGeneration();
  try {
    bridge.stop();
    await bridge.loadProject(result.backup);
    bridge.currentVm?.setEditingTarget(result.targetId);
    bridge.activateCodeTab();
  } finally {
    release();
  }
}
