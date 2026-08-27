import type {CostumeExport} from '../scratch/asset-adapter';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';
import type {ResourceOp} from './project-plan';
import type {PreparedResource, ProjectApplyResult, ProjectResourceExecutor} from './project-resource-runner';

export interface ResourceExecutorOptions {bridge: ScratchGuiBridge; currentTargetId: string}
function costumeValue(prepared: PreparedResource) {if (!('value' in prepared) || !prepared.value || typeof prepared.value !== 'object') throw new Error('资源准备结果无效'); return prepared.value as CostumeExport;}
function targetIdFor(bridge: ScratchGuiBridge, target: Exclude<ResourceOp, {op: 'add_sprite' | 'add_builtin_sprite' | 'add_backdrop' | 'select_backdrop'}>['target'], currentTargetId: string) {
  if (!target || target.kind === 'current_target') return currentTargetId;
  if (target.kind === 'existing_sprite') {const found = bridge.currentVm?.runtime.targets.find(item => !item.isStage && (item.getName?.() ?? item.sprite?.name) === target.name); if (!found) throw new Error('找不到已有角色：' + target.name); return found.id;}
  if (target.kind === 'created_sprite') throw new Error('created_sprite 需要在角色创建后由同一 executor 解析');
  throw new Error('舞台不能作为造型目标');
}

export function createProjectResourceExecutor(options: ResourceExecutorOptions): ProjectResourceExecutor {
  return {
    async apply(op, prepared, context) {
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
      if (op.op === 'add_builtin_sprite' || op.op === 'add_sprite') throw new Error('新增角色 executor 尚未接入 SB3 角色导入');
      throw new Error('不支持的资源操作');
    },
    async applyProgram() {throw new Error('项目范围积木 executor 尚未接入');}
  };
}
