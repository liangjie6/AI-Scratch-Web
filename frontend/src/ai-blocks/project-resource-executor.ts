import {importCostumeSet} from '../scratch/asset-adapter';
import type {CostumeExport, SpriteExport} from '../scratch/asset-adapter';
import {applyAiProgram} from './generation-runner';
import {readAiTargetSnapshot} from './target-context';
import type {AiBlocksMode} from './ai-blocks-api';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';
import type {ResourceOp} from './project-plan';
import type {PreparedResource, ProjectApplyResult, ProjectResourceExecutor} from './project-resource-runner';

export interface ResourceExecutorOptions {bridge: ScratchGuiBridge; currentTargetId: string}
function costumeValue(prepared: PreparedResource) {if (!('value' in prepared) || !prepared.value || typeof prepared.value !== 'object') throw new Error('资源准备结果无效'); return prepared.value as CostumeExport;}
function spriteValue(prepared: PreparedResource) {if (!('value' in prepared) || !prepared.value || typeof prepared.value !== 'object' || !('costumes' in prepared.value)) throw new Error('角色资源准备结果无效'); return prepared.value as SpriteExport;}
function targetIdFor(bridge: ScratchGuiBridge, target: Exclude<ResourceOp, {op: 'add_sprite' | 'add_builtin_sprite' | 'add_backdrop' | 'select_backdrop'}>['target'], currentTargetId: string) {
  if (!target || target.kind === 'current_target') return currentTargetId;
  if (target.kind === 'existing_sprite') {const found = bridge.currentVm?.runtime.targets.find(item => !item.isStage && (item.getName?.() ?? item.sprite?.name) === target.name); if (!found) throw new Error('找不到已有角色：' + target.name); return found.id;}
  if (target.kind === 'created_sprite') throw new Error('created_sprite 需要在角色创建后由同一 executor 解析');
  throw new Error('舞台不能作为造型目标');
}

function blockTargetId(bridge: ScratchGuiBridge, target: {kind: string; name?: string; op_id?: string}, currentTargetId: string, result: ProjectApplyResult) {
  if (target.kind === 'current_target') return currentTargetId;
  if (target.kind === 'stage') {const stage = bridge.currentVm?.runtime.targets.find(item => item.isStage); if (!stage) throw new Error('Scratch 舞台尚未就绪'); return stage.id;}
  if (target.kind === 'existing_sprite') {const found = bridge.currentVm?.runtime.targets.find(item => !item.isStage && (item.getName?.() ?? item.sprite?.name) === target.name); if (!found) throw new Error('找不到已有角色：' + target.name); return found.id;}
  if (target.kind === 'created_sprite') {const created = result.addedTargets.find(item => item.op_id === target.op_id); if (!created) throw new Error('created_sprite 尚未创建：' + target.op_id); return created.target_id;}
  throw new Error('无效的积木 target');
}

export function createProjectResourceExecutor(options: ResourceExecutorOptions): ProjectResourceExecutor {
  return {
    async apply(op, prepared, context) {
      if (op.op === 'add_sprite' || op.op === 'add_builtin_sprite') {
        const sprite = spriteValue(prepared);
        const imported = await importCostumeSet(options.bridge, sprite);
        context.result.addedTargets.push({op_id: op.op_id, target_id: imported.targetId, name: sprite.name});
        return;
      }
      if (op.op === 'add_costume') {
        const targetId = targetIdFor(options.bridge, op.target, options.currentTargetId);
        const installed = await options.bridge.addCostumeToTarget(targetId, costumeValue(prepared), op.select_after ?? true);
        context.result.addedCostumes.push({op_id: op.op_id, target_id: targetId, asset_id: installed.assetId, name: installed.name});
        return;
      }
      if (op.op === 'add_backdrop') {
        const installed = await options.bridge.addBackdropToStage(costumeValue(prepared), op.select_after ?? true);
        context.result.addedBackdrops.push({op_id: op.op_id, asset_id: installed.assetId, name: installed.name});
        return;
      }
      if (op.op === 'select_backdrop') {
        const selected = options.bridge.selectBackdropByName(op.name);
        context.result.selectedBackdrop = {op_id: op.op_id, asset_id: selected.assetId, name: selected.name};
        return;
      }
      throw new Error('不支持的资源操作');
    },
    async applyProgram(programPlan, context) {
      const targetId = blockTargetId(options.bridge, programPlan.target, options.currentTargetId, context.result);
      const vm = options.bridge.currentVm;
      const target = vm?.runtime.targets.find(item => item.id === targetId);
      if (!vm || !target) throw new Error('项目积木 target 不存在');
      vm.setEditingTarget(targetId);
      const snapshot = readAiTargetSnapshot(options.bridge, programPlan.mode as AiBlocksMode);
      const applied = await applyAiProgram({bridge: options.bridge, snapshot, mode: programPlan.mode as AiBlocksMode, program: programPlan.program});
      context.result.appliedPrograms.push({target_id: targetId, added_blocks: applied.addedBlocks, added_scripts: applied.addedScripts, removed_scripts: applied.removedScripts});
    }
  };
}
