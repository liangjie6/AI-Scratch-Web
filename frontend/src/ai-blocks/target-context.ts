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
}

function normalizeBlock(
  blocks: ScratchBlocksLike,
  id: string | null,
  path: Set<string>
): NormalizedBlock | null {
  if (!id) return null;
  if (path.has(id)) throw new Error('当前积木代码包含循环引用，无法交给 AI 分析');
  const block = blocks.getBlock(id);
  if (!block) return null;
  const nextPath = new Set(path).add(id);
  const fields = Object.fromEntries(
    Object.entries(block.fields ?? {}).map(([name, field]) => [name, field.value])
  );
  const inputs = Object.fromEntries(
    Object.entries(block.inputs ?? {}).map(([name, input]) => [
      name,
      normalizeBlock(blocks, input.block, nextPath)
    ])
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

function listVariables(target: ScratchTargetLike | undefined) {
  return Object.values(target?.variables ?? {}).map(variable => ({
    name: variable.name,
    type: variable.type,
    value: variable.value
  }));
}

export function readAiTargetSnapshot(
  bridge: ScratchGuiBridge,
  mode: AiBlocksMode
): AiTargetSnapshot {
  const vm = bridge.currentVm;
  const target = vm?.editingTarget;
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

export function assertSnapshotIsCurrent(bridge: ScratchGuiBridge, snapshot: AiTargetSnapshot) {
  const vm = bridge.currentVm;
  if (!vm?.editingTarget || vm.editingTarget.id !== snapshot.targetId) {
    throw new Error('当前目标已经切换，请重新确认后再生成');
  }
  if (bridge.projectRevision !== snapshot.revision) {
    throw new Error('确认需求后项目代码发生了变化，请重新生成计划');
  }
}
