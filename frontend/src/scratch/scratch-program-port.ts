import {getBlockSpec} from '../ai-blocks/block-catalog';
import type {CompiledProgram, VMBlock} from '../ai-blocks/program-compiler';
import type {ScratchEditorPort} from './scratch-editor-port';
import {toRaw} from 'vue';

export interface PendingScratchVariable {
  owner: ScratchTargetLike;
  id: string;
  name: string;
  type: ScratchVariableLike['type'];
  value: ScratchVariableLike['value'];
}

export interface ScratchProgramProgress {
  current: number;
  total: number;
  label: string;
}

export interface ScratchProgramApplyResult {
  targetId: string;
  targetName: string;
  addedBlocks: number;
  addedScripts: number;
  removedScripts: number;
  revisionAtComplete: number;
}

export interface ScratchProgramPort {
  /**
   * Read access is kept alongside the internal graph writer so feature code
   * does not reach into a VM target's block container directly.
   */
  read(target: ScratchTargetLike): ScratchBlocksLike;
  listBlocks(target: ScratchTargetLike): ScratchBlockLike[];
  countVisibleBlocks(target: ScratchTargetLike): number;
  apply(options: {
    target: ScratchTargetLike;
    compiled: CompiledProgram;
    removeTopIds: string[];
    pending: PendingScratchVariable[];
    signal?: AbortSignal;
    onProgress?: (progress: ScratchProgramProgress) => void;
    targetName: string;
  }): ScratchProgramApplyResult;
}

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

function installVariables(pending: PendingScratchVariable[]) {
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

/**
 * The supported Scratch VM API has no public block-graph writer. Keep the
 * unavoidable direct-block fallback isolated here so package upgrades have one
 * capability and contract boundary.
 */
export function createScratchProgramPort(editor: ScratchEditorPort): ScratchProgramPort {
  const read = (target: ScratchTargetLike): ScratchBlocksLike => {
    const blocks = target.blocks;
    if (!blocks) throw new Error('Scratch 目标缺少积木容器');
    return blocks;
  };
  return {
    read,
    listBlocks(target) {
      // Scratch Blocks does not expose a public complete-graph iterator.
      // Keep this version-sensitive fallback inside the compatibility port.
      return Object.values(read(target)._blocks);
    },
    countVisibleBlocks(target) {
      return this.listBlocks(target).filter(block => !block.shadow).length;
    },
    apply({target, compiled, removeTopIds, pending, signal, onProgress, targetName}) {
      throwIfAborted(signal);
      installVariables(pending);
      removeScripts(target, removeTopIds);
      const blocks = read(target);
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
      editor.refreshWorkspace();
      return {
        targetId: target.id,
        targetName,
        addedBlocks: visibleOrder.length,
        addedScripts: compiled.scripts.length,
        removedScripts: removeTopIds.length,
        revisionAtComplete: editor.projectRevision,
      };
    }
  };
}
