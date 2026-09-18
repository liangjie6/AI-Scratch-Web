import type {ScratchGuiBridge} from '../scratch/scratch-gui-bridge';
import {createCheckpointStore, fingerprintBytes, type AiRunCheckpointStore, type LogicalTargetLocator, type RunCheckpoint} from './run-checkpoint-store';

export interface CheckpointRecoveryResult {
  restoredRunIds: string[];
  cleanRunIds: string[];
  requiresUserAction: RunCheckpoint[];
  failedRunIds: string[];
  failedCheckpoints: RunCheckpoint[];
  discardedRunIds: string[];
}

function targetForLocator(bridge: ScratchGuiBridge, locator: LogicalTargetLocator): ScratchTargetLike | undefined {
  const targets = bridge.currentVm?.runtime.targets ?? [];
  if (locator.kind === 'stage') return targets.find(target => target.isStage);
  const sprites = targets.filter(target => !target.isStage && target.isOriginal !== false);
  const atOrdinal = locator.ordinal === undefined ? undefined : sprites[locator.ordinal];
  const nameOf = (target: ScratchTargetLike | undefined) => String(target?.getName?.() ?? target?.sprite?.name ?? '').trim();
  if (atOrdinal && (!locator.name || nameOf(atOrdinal) === locator.name)) return atOrdinal;
  return sprites.find(target => nameOf(target) === locator.name);
}

async function restoreBackup(bridge: ScratchGuiBridge, checkpoint: RunCheckpoint, backup: Uint8Array): Promise<void> {
  const release = bridge.beginBlockGeneration();
  try {
    await bridge.loadProject(backup);
    const target = targetForLocator(bridge, checkpoint.originalTargetLocator);
    if (!target) throw new Error('恢复后无法定位原编辑目标');
    bridge.currentVm?.setEditingTarget?.(target.id);
    bridge.refreshWorkspace();
  } finally {
    release();
  }
}

/**
 * Runs before exposing AI commit controls. It only restores a project when the
 * currently open bytes prove they still belong to the interrupted transaction.
 */
export async function recoverInterruptedAiCommits(
  bridge: ScratchGuiBridge,
  projectStorageKey: string,
  store: AiRunCheckpointStore = createCheckpointStore(),
): Promise<CheckpointRecoveryResult> {
  const result: CheckpointRecoveryResult = {
    restoredRunIds: [],
    cleanRunIds: [],
    requiresUserAction: [],
    failedRunIds: [],
    failedCheckpoints: [],
    discardedRunIds: []
  };
  const checkpoints = await store.unfinished(projectStorageKey);
  for (const checkpoint of checkpoints) {
    const backup = await store.backup(checkpoint.runId);
    if (!backup) {
      await store.markRolledBack(checkpoint.runId);
      result.discardedRunIds.push(checkpoint.runId);
      continue;
    }
    const current = new Uint8Array(await bridge.saveProject());
    const currentFingerprint = await fingerprintBytes(current);
    if (currentFingerprint === checkpoint.baseProjectFingerprint) {
      await store.markRolledBack(checkpoint.runId);
      result.cleanRunIds.push(checkpoint.runId);
      continue;
    }
    const knownOwnedFingerprint = currentFingerprint === checkpoint.lastAppliedProjectFingerprint
      || currentFingerprint === checkpoint.candidateProjectFingerprint;
    if (!knownOwnedFingerprint) {
      await store.markRolledBack(checkpoint.runId);
      result.discardedRunIds.push(checkpoint.runId);
      continue;
    }
    try {
      await restoreBackup(bridge, checkpoint, backup);
      const restored = new Uint8Array(await bridge.saveProject());
      if (await fingerprintBytes(restored) !== checkpoint.baseProjectFingerprint) throw new Error('恢复后的项目校验不匹配');
      await store.markRolledBack(checkpoint.runId);
      result.restoredRunIds.push(checkpoint.runId);
    } catch {
      await store.markRolledBack(checkpoint.runId);
      result.discardedRunIds.push(checkpoint.runId);
    }
  }
  return result;
}

export async function checkpointBackupForExport(runId: string, store: AiRunCheckpointStore = createCheckpointStore()): Promise<Blob | undefined> {
  const backup = await store.backup(runId);
  return backup ? new Blob([backup], {type: 'application/x.scratch.sb3'}) : undefined;
}
