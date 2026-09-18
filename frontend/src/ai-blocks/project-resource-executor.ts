import {importCostumeSet} from '../scratch/asset-adapter';
import type {CostumeExport, SoundExport, SpriteExport} from '../scratch/asset-adapter';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';
import type {ResourceOp, TargetRef} from './project-plan';
import type {ApplyEntry, PreparedResource, ProjectApplyResult, ProjectResourceExecutor} from './project-resource-runner';

export interface ResourceExecutorOptions {
  bridge: ScratchGuiBridge;
  currentTargetId: string;
}

function costumeValue(prepared: PreparedResource): CostumeExport {
  if (!prepared.value || typeof prepared.value !== 'object') throw new Error('资源准备结果无效');
  return prepared.value as CostumeExport;
}

function spriteValue(prepared: PreparedResource): SpriteExport {
  if (!prepared.value || typeof prepared.value !== 'object' || !('costumes' in prepared.value)) {
    throw new Error('角色资源准备结果无效');
  }
  return prepared.value as SpriteExport;
}

function soundValue(prepared: PreparedResource): SoundExport {
  if (!prepared.value || typeof prepared.value !== 'object' || !('dataFormat' in prepared.value)) throw new Error('声音资源准备结果无效');
  return prepared.value as SoundExport;
}

function targetName(target: ScratchTargetLike): string {
  return String(target.getName?.() ?? target.sprite?.name ?? '').trim();
}

function findSprite(bridge: ScratchGuiBridge, name: string): ScratchTargetLike | undefined {
  const wanted = name.trim().toLocaleLowerCase();
  const matches = bridge.currentVm?.runtime.targets.filter(item => !item.isStage && item.isOriginal !== false && targetName(item).toLocaleLowerCase() === wanted) ?? [];
  if (matches.length > 1) throw new Error('已有角色名称不唯一：' + name);
  return matches[0];
}

function targetIdFor(
  bridge: ScratchGuiBridge,
  target: TargetRef,
  currentTargetId: string,
  result: ProjectApplyResult,
): string {
  if (target.kind === 'current_target') return currentTargetId;
  if (target.kind === 'existing_sprite') {
    const found = findSprite(bridge, target.name);
    if (!found) throw new Error('找不到已有角色：' + target.name);
    return found.id;
  }
  if (target.kind === 'created_sprite') {
    const created = result.addedTargets.find(item => item.op_id === target.op_id);
    if (!created) throw new Error('created_sprite 尚未创建：' + target.op_id);
    return created.target_id;
  }
  throw new Error('舞台不能作为造型目标');
}

export function createProjectResourceExecutor(options: ResourceExecutorOptions): ProjectResourceExecutor {
  return {
    async apply(op, prepared, context) {
      if (op.op === 'delete_sprite') {
        const targetId = targetIdFor(options.bridge, op.target, options.currentTargetId, context.result);
        if (targetId === options.currentTargetId) throw new Error('不能删除当前正在编辑的角色');
        const vm = options.bridge.currentVm;
        if (!vm) throw new Error('Scratch VM 尚未就绪');
        const mutate = options.bridge.runProjectTransactionMutationAsync;
        const restoreCandidate = await (typeof mutate === 'function'
          ? mutate.call(options.bridge, () => vm.deleteSprite?.(targetId))
          : Promise.resolve(vm.deleteSprite?.(targetId)));
        if (vm.runtime.targets.some(target => target.id === targetId)) throw new Error('角色删除未完成：' + targetId);
        const restore = typeof restoreCandidate === 'function' ? restoreCandidate as () => void | Promise<void> : undefined;
        return restore ? {kind: 'deleted_sprite', restore, opId: op.op_id} satisfies ApplyEntry : undefined;
      }
      if (op.op === 'add_sprite' || op.op === 'add_builtin_sprite') {
        const sprite = spriteValue(prepared);
        const imported = await importCostumeSet(options.bridge, sprite);
        const installedTarget = options.bridge.currentVm?.runtime.getTargetById?.(imported.targetId)
          ?? options.bridge.currentVm?.runtime.targets.find(target => target.id === imported.targetId);
        context.result.addedTargets.push({op_id: op.op_id, target_id: imported.targetId, name: installedTarget ? targetName(installedTarget) : sprite.name});
        return {kind: 'sprite', targetId: imported.targetId, opId: op.op_id} satisfies ApplyEntry;
      }
      if (op.op === 'add_costume') {
        const targetId = targetIdFor(options.bridge, op.target, options.currentTargetId, context.result);
        const installed = await options.bridge.addCostumeToTarget(targetId, costumeValue(prepared), op.select_after);
        context.result.addedCostumes.push({op_id: op.op_id, target_id: targetId, asset_id: installed.assetId, name: installed.name});
        return {kind: 'costume', targetId, costumeIndex: installed.costumeIndex, assetId: installed.assetId, opId: op.op_id} satisfies ApplyEntry;
      }
      if (op.op === 'add_backdrop') {
        const stage = options.bridge.currentVm?.runtime.getTargetForStage?.() ?? options.bridge.currentVm?.runtime.targets.find(target => target.isStage);
        if (!stage) throw new Error('Scratch 舞台尚未就绪');
        const installed = await options.bridge.addBackdropToStage(costumeValue(prepared), op.select_after);
        context.result.addedBackdrops.push({op_id: op.op_id, asset_id: installed.assetId, name: installed.name});
        return {kind: 'backdrop', targetId: stage.id, costumeIndex: installed.costumeIndex, assetId: installed.assetId, opId: op.op_id} satisfies ApplyEntry;
      }
      if (op.op === 'add_sound') {
        const targetId = op.target.kind === 'stage'
          ? (options.bridge.currentVm?.runtime.getTargetForStage?.() ?? options.bridge.currentVm?.runtime.targets.find(target => target.isStage))?.id
          : targetIdFor(options.bridge, op.target, options.currentTargetId, context.result);
        if (!targetId) throw new Error('Scratch 舞台尚未就绪');
        const installed = await options.bridge.addSoundToTarget(targetId, soundValue(prepared));
        context.result.addedSounds.push({op_id: op.op_id, target_id: targetId, asset_id: installed.assetId, name: installed.name});
        return {kind: 'sound', targetId, soundIndex: installed.soundIndex, assetId: installed.assetId, opId: op.op_id} satisfies ApplyEntry;
      }
      if (op.op === 'select_backdrop') {
        const stage = options.bridge.currentVm?.runtime.getTargetForStage?.() ?? options.bridge.currentVm?.runtime.targets.find(target => target.isStage);
        const previousCostumeIndex = stage?.currentCostume;
        const selected = options.bridge.selectBackdropByName(op.name);
        context.result.selectedBackdrop = {op_id: op.op_id, asset_id: selected.assetId, name: selected.name};
        return stage && previousCostumeIndex !== undefined
          ? {kind: 'backdrop_selection', targetId: stage.id, previousCostumeIndex, opId: op.op_id} satisfies ApplyEntry
          : undefined;
      }
      throw new Error('不支持的资源操作');
    }
  };
}
