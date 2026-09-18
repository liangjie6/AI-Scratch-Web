import {describe, expect, it, vi} from 'vitest';
import {recoverInterruptedAiCommits} from '../src/ai-blocks/checkpoint-recovery';
import {fingerprintBytes, type AiRunCheckpointStore, type RunCheckpoint} from '../src/ai-blocks/run-checkpoint-store';
import type {ScratchGuiBridge} from '../src/scratch/scratch-gui-bridge';

function mockCheckpoint(runId: string, baseFingerprint: string): RunCheckpoint {
  return {
    runId,
    state: 'COMMITTING',
    editorSessionId: 'session-test',
    projectEpoch: 1,
    projectStorageKey: 'scratch-project',
    baseSnapshotHash: 'hash-1',
    baseProjectFingerprint: baseFingerprint,
    lastAppliedProjectFingerprint: 'last-1',
    candidateProjectFingerprint: 'candidate-1',
    resolvedPlanHash: 'plan-1',
    backupBlobKey: `checkpoint:${runId}:backup`,
    journal: [],
    originalTargetLocator: {kind: 'sprite', name: '角色1'},
    updatedAt: Date.now()
  };
}

describe('recoverInterruptedAiCommits', () => {
  it('当项目与备份不一致时，默认丢弃任务（markRolledBack）并记录到 discardedRunIds，不产生 requiresUserAction', async () => {
    const currentBytes = new Uint8Array([10, 20, 30]);
    const currentFingerprint = await fingerprintBytes(currentBytes);
    const checkpoint = mockCheckpoint('run-conflict', 'different-base-fingerprint');
    // 让 currentFingerprint 与 checkpoint 的 base / last / candidate 均不匹配
    checkpoint.lastAppliedProjectFingerprint = 'other-last';
    checkpoint.candidateProjectFingerprint = 'other-candidate';

    const rolledBackIds: string[] = [];
    const mockStore: Partial<AiRunCheckpointStore> = {
      unfinished: vi.fn(async () => [checkpoint]),
      backup: vi.fn(async () => new Uint8Array([1, 2, 3])),
      markRolledBack: vi.fn(async (runId: string) => {
        rolledBackIds.push(runId);
        return {...checkpoint, state: 'ROLLED_BACK' as const};
      })
    };

    const bridge = {
      saveProject: vi.fn(async () => currentBytes),
      currentVm: {
        runtime: {targets: [{id: 'sprite-1', isStage: false, isOriginal: true, getName: () => '角色1'}]}
      }
    } as unknown as ScratchGuiBridge;

    const result = await recoverInterruptedAiCommits(bridge, 'scratch-project', mockStore as AiRunCheckpointStore);

    expect(result.requiresUserAction).toEqual([]);
    expect(result.failedRunIds).toEqual([]);
    expect(result.discardedRunIds).toEqual(['run-conflict']);
    expect(rolledBackIds).toContain('run-conflict');
    expect(mockStore.markRolledBack).toHaveBeenCalledWith('run-conflict');
  });

  it('当未完成任务缺少备份时，默认丢弃任务', async () => {
    const checkpoint = mockCheckpoint('run-no-backup', 'base-1');
    const rolledBackIds: string[] = [];
    const mockStore: Partial<AiRunCheckpointStore> = {
      unfinished: vi.fn(async () => [checkpoint]),
      backup: vi.fn(async () => undefined),
      markRolledBack: vi.fn(async (runId: string) => {
        rolledBackIds.push(runId);
        return {...checkpoint, state: 'ROLLED_BACK' as const};
      })
    };

    const bridge = {
      saveProject: vi.fn(async () => new Uint8Array([1, 2, 3]))
    } as unknown as ScratchGuiBridge;

    const result = await recoverInterruptedAiCommits(bridge, 'scratch-project', mockStore as AiRunCheckpointStore);

    expect(result.requiresUserAction).toEqual([]);
    expect(result.discardedRunIds).toEqual(['run-no-backup']);
    expect(rolledBackIds).toContain('run-no-backup');
  });

  it('当项目指纹已与 base 相同，正常标记为 rolledBack 并计入 cleanRunIds', async () => {
    const currentBytes = new Uint8Array([1, 2, 3]);
    const currentFingerprint = await fingerprintBytes(currentBytes);
    const checkpoint = mockCheckpoint('run-clean', currentFingerprint);

    const mockStore: Partial<AiRunCheckpointStore> = {
      unfinished: vi.fn(async () => [checkpoint]),
      backup: vi.fn(async () => new Uint8Array([1, 2, 3])),
      markRolledBack: vi.fn(async (_runId: string) => ({...checkpoint, state: 'ROLLED_BACK' as const}))
    };

    const bridge = {
      saveProject: vi.fn(async () => currentBytes)
    } as unknown as ScratchGuiBridge;

    const result = await recoverInterruptedAiCommits(bridge, 'scratch-project', mockStore as AiRunCheckpointStore);

    expect(result.cleanRunIds).toEqual(['run-clean']);
    expect(result.discardedRunIds).toEqual([]);
    expect(result.requiresUserAction).toEqual([]);
  });
});
