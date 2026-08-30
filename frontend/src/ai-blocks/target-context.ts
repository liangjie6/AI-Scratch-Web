import type {AiBlocksMode, AiBlocksTargetContext} from './ai-blocks-api';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';
import {getBlockSpec} from './block-catalog';

interface NormalizedBlock {
  opcode: string;
  fields: Record<string, string | number | boolean>;
  inputs: Record<string, NormalizedBlock | null>;
  next: NormalizedBlock | null;
}

export interface AiTargetSnapshot {
  targetId: string;
  targetName: string;
  isStage: boolean;
  revision: number;
  blockCount: number;
  hasScripts: boolean;
  scriptRefs: Record<string, string>;
  context: AiBlocksTargetContext;
  targetRef?: ScratchTargetLike;
  stateSignature?: string;
}

export interface AiProjectSnapshot {
  revision: number;
  editingTargetId: string;
  targetIds: string[];
  targetRefs?: ScratchTargetLike[];
  targetSignatures?: string[];
  targetStateSignatures?: string[];
}

function normalizeBlock(
  blocks: ScratchBlocksLike,
  id: string | null,
  path: Set<string>
): NormalizedBlock | null {
  if (!id) return null;
  if (path.has(id)) throw new Error('当前积木代码包含循环引用，无法交给 AI 分析');
  const block = blocks.getBlock(id);
  if (!block) throw new Error('当前积木代码包含缺失的积木引用：' + id);
  if (typeof block.opcode !== 'string' || !block.opcode) throw new Error('当前积木代码包含无效积木类型');
  const nextPath = new Set(path).add(id);
  const fields = Object.fromEntries(
    Object.entries(block.fields ?? {}).map(([name, field]) => {
      if (!field || !['string', 'number', 'boolean'].includes(typeof field.value)) throw new Error('当前积木代码包含无效字段值');
      return [name, field.value];
    })
  );
  const inputs = Object.fromEntries(
    Object.entries(block.inputs ?? {}).map(([name, input]) => {
      if (!input || (input.block !== null && typeof input.block !== 'string') || (input.shadow !== null && typeof input.shadow !== 'string')) {
        throw new Error('当前积木代码包含无效输入引用');
      }
      return [name, normalizeBlock(blocks, input.block ?? input.shadow, nextPath)];
    })
  );
  return {
    opcode: block.opcode,
    fields,
    inputs,
    next: normalizeBlock(blocks, block.next, nextPath)
  };
}

export function isAiEditableScript(blocks: ScratchBlocksLike, topLevelId: string) {
  const visited = new Set<string>();
  const visit = (id: string | null): boolean => {
    if (!id || visited.has(id)) return true;
    const block = blocks.getBlock(id);
    if (!block) return false;
    visited.add(id);
    if (!block.shadow && !getBlockSpec(block.opcode)) return false;
    if (!visit(block.next)) return false;
    return Object.values(block.inputs ?? {}).every(input =>
      visit(input.block) && (input.shadow === input.block || visit(input.shadow))
    );
  };
  return visit(topLevelId);
}

function targetSignature(target: ScratchTargetLike) {
  return JSON.stringify({
    id: target.id,
    name: String(target.getName?.() ?? target.sprite?.name ?? ''),
    isStage: Boolean(target.isStage),
    isOriginal: target.isOriginal !== false
  });
}

function stableValue(value: unknown, seen = new Set<object>()): unknown {
  if (value === null || typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') return value;
  if (typeof value !== 'object') return undefined;
  if (seen.has(value)) return '[Circular]';
  seen.add(value);
  if (Array.isArray(value)) return value.map(item => stableValue(item, seen));
  const output: Record<string, unknown> = {};
  for (const key of Object.keys(value).sort()) {
    const item = stableValue((value as Record<string, unknown>)[key], seen);
    if (item !== undefined) output[key] = item;
  }
  seen.delete(value);
  return output;
}

function targetStateSignature(target: ScratchTargetLike) {
  const blocks = target.blocks
    ? Object.values(target.blocks._blocks).map(block => stableValue(block)).sort((left, right) => (JSON.stringify(left) ?? '').localeCompare(JSON.stringify(right) ?? ''))
    : [];
  const costumes = (target.getCostumes?.() ?? []).map(costume => ({
    name: costume.name,
    assetId: costume.assetId,
    dataFormat: costume.dataFormat,
    bitmapResolution: costume.bitmapResolution,
    rotationCenterX: costume.rotationCenterX,
    rotationCenterY: costume.rotationCenterY
  }));
  const sounds = (target.getSounds?.() ?? []).map(sound => ({name: sound.name}));
  return JSON.stringify(stableValue({
    identity: targetSignature(target),
    x: target.x,
    y: target.y,
    size: target.size,
    currentCostume: target.currentCostume,
    blocks,
    comments: target.comments,
    variables: target.variables,
    costumes,
    sounds
  }));
}

function listVariables(target: ScratchTargetLike | undefined) {
  return Object.values(target?.variables ?? {}).map(variable => ({
    name: variable.name,
    type: variable.type,
    value: variable.value
  }));
}

export function readAiTargetSnapshot(
  bridge: ScratchGuiBridge,
  mode: AiBlocksMode,
  targetOverride?: ScratchTargetLike
): AiTargetSnapshot {
  const vm = bridge.currentVm;
  const target = targetOverride ?? vm?.editingTarget;
  const blocks = target?.blocks;
  if (!vm || !target || !blocks) throw new Error('请等待 Scratch 编辑器加载完成');

  const topLevelIds = [...blocks.getScripts()].sort((left, right) => {
    const a = blocks.getBlock(left);
    const b = blocks.getBlock(right);
    return ((a?.x ?? 0) - (b?.x ?? 0)) || ((a?.y ?? 0) - (b?.y ?? 0));
  });
  const scriptRefs: Record<string, string> = {};
  const scripts = topLevelIds.map((topLevelId, index) => {
    const ref = 'script_' + (index + 1);
    const protectedScript = !isAiEditableScript(blocks, topLevelId);
    if (!protectedScript) scriptRefs[ref] = topLevelId;
    const block = blocks.getBlock(topLevelId);
    const comment = block?.comment ? target.comments?.[block.comment]?.text : undefined;
    return {
      id: ref,
      protected: protectedScript,
      x: block?.x ?? 0,
      y: block?.y ?? 0,
      comment,
      code: normalizeBlock(blocks, topLevelId, new Set())
    };
  });

  const stage = vm.runtime.getTargetForStage?.()
    ?? vm.runtime.targets.find(item => item.isStage);
  const currentProgram = {
    scripts,
    local_variables: target.isStage ? [] : listVariables(target),
    global_variables: listVariables(stage)
  };
  const targetName = target.getName?.() ?? target.sprite?.name ?? (target.isStage ? '舞台' : '未命名角色');
  const blockCount = Object.values(blocks._blocks).filter(block => !block.shadow).length;

  return {
    targetId: target.id,
    targetName,
    isStage: Boolean(target.isStage),
    revision: bridge.projectRevision,
    blockCount,
    hasScripts: topLevelIds.length > 0,
    scriptRefs,
    targetRef: target,
    stateSignature: targetStateSignature(target),
    context: {
      name: targetName,
      is_stage: Boolean(target.isStage),
      has_scripts: topLevelIds.length > 0,
      mode,
      costumes: (target.getCostumes?.() ?? [])
        .map(costume => String(costume.name ?? ''))
        .filter(Boolean),
      sounds: (target.getSounds?.() ?? [])
        .map(sound => String(sound.name ?? ''))
        .filter(Boolean),
      sprite_names: vm.runtime.targets
        .filter(item => !item.isStage && item.isOriginal !== false)
        .map(item => item.getName?.() ?? item.sprite?.name ?? '')
        .filter(Boolean),
      current_program: JSON.stringify(currentProgram)
    }
  };
}

export function readAiProjectSnapshot(bridge: ScratchGuiBridge): AiProjectSnapshot {
  const vm = bridge.currentVm;
  if (!vm?.editingTarget) throw new Error('请等待 Scratch 编辑器加载完成');
  const targets = vm.runtime.targets.filter(target => target.isOriginal !== false);
  return {
    revision: bridge.projectRevision,
    editingTargetId: vm.editingTarget.id,
    targetIds: targets.map(target => target.id),
    targetRefs: [...targets],
    targetSignatures: targets.map(targetSignature),
    targetStateSignatures: targets.map(targetStateSignature)
  };
}

export function assertProjectSnapshotIsCurrent(bridge: ScratchGuiBridge, snapshot: AiProjectSnapshot) {
  const vm = bridge.currentVm;
  if (!vm?.editingTarget || vm.editingTarget.id !== snapshot.editingTargetId) {
    throw new Error('确认需求后当前目标已经切换，请重新生成计划');
  }
  if (bridge.projectRevision !== snapshot.revision) {
    throw new Error('确认需求后项目发生了变化，请重新生成计划');
  }
  const targets = vm.runtime.targets.filter(target => target.isOriginal !== false);
  const targetIds = targets.map(target => target.id);
  if (targetIds.length !== snapshot.targetIds.length || targetIds.some((id, index) => id !== snapshot.targetIds[index])) {
    throw new Error('确认需求后项目角色发生了变化，请重新生成计划');
  }
  if (snapshot.targetRefs && targets.some((target, index) => target !== snapshot.targetRefs?.[index])) {
    throw new Error('确认需求后项目角色已被替换，请重新生成计划');
  }
  if (snapshot.targetSignatures && targets.some((target, index) => targetSignature(target) !== snapshot.targetSignatures?.[index])) {
    throw new Error('确认需求后项目角色属性发生了变化，请重新生成计划');
  }
  if (snapshot.targetStateSignatures && targets.some((target, index) => targetStateSignature(target) !== snapshot.targetStateSignatures?.[index])) {
    throw new Error('确认需求后项目目标内容发生了变化，请重新生成计划');
  }
}

export function assertSnapshotIsCurrent(bridge: ScratchGuiBridge, snapshot: AiTargetSnapshot) {
  const vm = bridge.currentVm;
  if (!vm?.editingTarget || vm.editingTarget.id !== snapshot.targetId || (snapshot.targetRef && vm.editingTarget !== snapshot.targetRef)) {
    throw new Error('当前目标已经切换，请重新确认后再生成');
  }
  if (bridge.projectRevision !== snapshot.revision) {
    throw new Error('确认需求后项目代码发生了变化，请重新生成计划');
  }
  if (snapshot.stateSignature && targetStateSignature(vm.editingTarget) !== snapshot.stateSignature) {
    throw new Error('确认需求后当前目标内容发生了变化，请重新生成计划');
  }
}
