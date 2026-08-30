import type {ProjectManifest, ResourceOp} from './project-plan';
import {assertProjectManifest} from './project-plan';
import type {AiTargetSnapshot} from './target-context';
import {assertSnapshotIsCurrent} from './target-context';
import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';

export interface PreparedResource {
  op_id: string;
  kind: 'builtin_sprite' | 'costume' | 'backdrop' | 'backdrop_selection';
  value: unknown;
}

export interface ProjectApplyResult {
  addedTargets: Array<{op_id: string; target_id: string; name: string}>;
  addedCostumes: Array<{op_id: string; target_id: string; asset_id: string; name: string}>;
  addedBackdrops: Array<{op_id: string; asset_id: string; name: string}>;
  selectedBackdrop?: {op_id: string; asset_id: string; name: string};
  appliedPrograms: Array<{target_id: string; added_blocks: number; added_scripts: number; removed_scripts: number}>;
  original_target_id: string;
  revision_at_complete: number;
  backup: Uint8Array;
}

export interface ProjectResourceExecutor {
  apply(op: ResourceOp, prepared: PreparedResource, context: {bridge: ScratchGuiBridge; result: ProjectApplyResult}): Promise<void>;
}

export interface ProjectResourceRunOptions {
  bridge: ScratchGuiBridge;
  manifest: ProjectManifest;
  snapshot: AiTargetSnapshot;
  prepared: Map<string, PreparedResource>;
  executor: ProjectResourceExecutor;
  signal?: AbortSignal;
  onProgress?: (current: number, total: number, label: string) => void;
}

export interface ProjectTransaction {
  result: ProjectApplyResult;
  assertActive?: () => void;
}

function throwIfAborted(signal?: AbortSignal) {
  if (signal?.aborted) throw new DOMException('Project resource run cancelled', 'AbortError');
}

function existingSpriteNames(bridge: ScratchGuiBridge): string[] {
  return bridge.currentVm?.runtime?.targets?.filter(target => !target.isStage && target.isOriginal !== false)
    .map(target => String(target.getName?.() ?? target.sprite?.name ?? '').trim())
    .filter(Boolean) ?? [];
}

export function createProjectApplyResult(snapshot: AiTargetSnapshot, backup: Uint8Array): ProjectApplyResult {
  return {
    addedTargets: [],
    addedCostumes: [],
    addedBackdrops: [],
    appliedPrograms: [],
    original_target_id: snapshot.targetId,
    revision_at_complete: snapshot.revision,
    backup: new Uint8Array(backup)
  };
}

export async function applyProjectResources(
  options: ProjectResourceRunOptions,
  transaction: ProjectTransaction
): Promise<ProjectApplyResult> {
  assertSnapshotIsCurrent(options.bridge, options.snapshot);
  assertProjectManifest(options.manifest, existingSpriteNames(options.bridge));
  const operations = options.manifest.resource_ops;
  for (let index = 0; index < operations.length; index += 1) {
    throwIfAborted(options.signal);
    const operation = operations[index];
    const prepared = options.prepared.get(operation.op_id);
    if (!prepared) throw new Error('资源尚未准备完成：' + operation.op_id);
    transaction.assertActive?.();
    await options.executor.apply(operation, prepared, {bridge: options.bridge, result: transaction.result});
    transaction.assertActive?.();
    options.onProgress?.(index + 1, operations.length, operation.name);
  }
  transaction.assertActive?.();
  options.bridge.currentVm?.setEditingTarget?.(options.snapshot.targetId);
  options.bridge.refreshWorkspace();
  transaction.assertActive?.();
  transaction.result.revision_at_complete = options.bridge.projectRevision;
  return transaction.result;
}
