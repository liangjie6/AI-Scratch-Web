import type {ProjectPlan, ResourceOp} from './project-plan';
import {assertProjectPlan} from './project-plan';
import type {AiTargetSnapshot} from './target-context';
import {assertSnapshotIsCurrent} from './target-context';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';

export interface PreparedResource {op_id: string; kind: 'builtin_sprite' | 'costume' | 'backdrop' | 'backdrop_selection'; value: unknown}
export interface ProjectApplyResult {addedTargets: Array<{op_id: string; target_id: string; name: string}>; addedCostumes: Array<{op_id: string; target_id: string; asset_id: string; name: string}>; addedBackdrops: Array<{op_id: string; asset_id: string; name: string}>; selectedBackdrop?: {op_id: string; asset_id: string; name: string}; appliedPrograms: Array<{target_id: string; added_blocks: number; added_scripts: number; removed_scripts: number}>; original_target_id: string; revision_at_complete: number; backup: Uint8Array}
export interface ProjectResourceExecutor {apply(op: ResourceOp, prepared: PreparedResource, context: {bridge: ScratchGuiBridge; result: ProjectApplyResult}): Promise<void>; applyProgram?(program: ProjectPlan['block_programs'][number], context: {bridge: ScratchGuiBridge; result: ProjectApplyResult}): Promise<void>}
export interface ProjectRunOptions {bridge: ScratchGuiBridge; plan: ProjectPlan; snapshot: AiTargetSnapshot; prepared: Map<string, PreparedResource>; executor: ProjectResourceExecutor; signal?: AbortSignal; onProgress?: (current: number, total: number, label: string) => void}

function throwIfAborted(signal?: AbortSignal) {if (signal?.aborted) throw new DOMException('Project resource run cancelled', 'AbortError');}
function emptyResult(snapshot: AiTargetSnapshot, backup: Uint8Array): ProjectApplyResult {return {addedTargets: [], addedCostumes: [], addedBackdrops: [], appliedPrograms: [], original_target_id: snapshot.targetId, revision_at_complete: snapshot.revision, backup};}

export async function applyProjectPlan(options: ProjectRunOptions): Promise<ProjectApplyResult> {
  assertProjectPlan(options.plan);
  assertSnapshotIsCurrent(options.bridge, options.snapshot);
  const backup = new Uint8Array(await options.bridge.saveProject());
  const result = emptyResult(options.snapshot, backup);
  const operations = options.plan.resource_ops;
  try {
    for (let index = 0; index < operations.length; index += 1) {
      throwIfAborted(options.signal);
      assertSnapshotIsCurrent(options.bridge, options.snapshot);
      const operation = operations[index];
      const prepared = options.prepared.get(operation.op_id);
      if (!prepared) throw new Error('资源尚未准备完成：' + operation.op_id);
      await options.executor.apply(operation, prepared, {bridge: options.bridge, result});
      options.onProgress?.(index + 1, operations.length + options.plan.block_programs.length, operation.name);
    }
    for (let index = 0; index < options.plan.block_programs.length; index += 1) {
      throwIfAborted(options.signal);
      assertSnapshotIsCurrent(options.bridge, options.snapshot);
      if (!options.executor.applyProgram) throw new Error('项目积木执行器尚未接入');
      await options.executor.applyProgram(options.plan.block_programs[index], {bridge: options.bridge, result});
      options.onProgress?.(operations.length + index + 1, operations.length + options.plan.block_programs.length, '正在写入积木');
    }
    result.revision_at_complete = options.bridge.projectRevision;
    return result;
  } catch (error) {
    try {await options.bridge.loadProject(backup);} catch (restoreError) {throw new Error('项目提交失败且无法恢复备份：' + (restoreError instanceof Error ? restoreError.message : '未知错误'));}
    throw error;
  }
}

export async function undoProjectPlan(bridge: ScratchGuiBridge, result: ProjectApplyResult) {
  if (bridge.projectRevision !== result.revision_at_complete) throw new Error('项目在撤销前已发生变化，拒绝覆盖用户修改');
  await bridge.loadProject(result.backup);
}
