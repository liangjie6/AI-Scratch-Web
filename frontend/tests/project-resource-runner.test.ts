import {describe, expect, it, vi} from 'vitest';
import {applyProjectManifest, undoProjectManifest, type PreparedResource} from '../src/ai-blocks/project-resource-runner';

function fixture() {
  let revision = 1;
  const loads: Uint8Array[] = [];
  const bridge = {
    get projectRevision() {return revision;},
    currentVm: {editingTarget: {id: 'target-1'}, setEditingTarget: vi.fn()},
    refreshWorkspace: vi.fn(),
    saveProject: vi.fn(async () => new Uint8Array([1, 2, 3])),
    loadProject: vi.fn(async (backup: Uint8Array) => {loads.push(backup); revision += 1;})
  } as any;
  const snapshot = {targetId: 'target-1', targetName: '角色', isStage: false, revision: 1, blockCount: 0, hasScripts: false, scriptRefs: {}, context: {} as any};
  const manifest = {schema_version: 1 as const, scope: 'project' as const, summary: '添加背景', resource_ops: [{op: 'add_backdrop' as const, op_id: 'backdrop-1', name: '森林', source: {kind: 'scratch_library' as const, asset_type: 'backdrop' as const, search_terms: ['森林']}, tags: [], select_after: true}], program_targets: [], shared_state: {variables: [], lists: [], broadcasts: []}};
  const prepared = new Map<string, PreparedResource>([['backdrop-1', {op_id: 'backdrop-1', kind: 'backdrop', value: {}}]]);
  return {bridge, snapshot, manifest, prepared, loads, bump: () => {revision += 1;}};
}

describe('project resource runner', () => {
  it('backs up and commits resources in order', async () => {
    const f = fixture();
    const apply = vi.fn(async () => undefined);
    const result = await applyProjectManifest({bridge: f.bridge, manifest: f.manifest, snapshot: f.snapshot, prepared: f.prepared, executor: {apply}});
    expect(f.bridge.saveProject).toHaveBeenCalledOnce();
    expect(f.bridge.refreshWorkspace).toHaveBeenCalledOnce();
    expect(apply).toHaveBeenCalledOnce();
    expect(result.backup).toEqual(new Uint8Array([1, 2, 3]));
  });

  it('restores the backup when an operation fails', async () => {
    const f = fixture();
    const error = new Error('安装失败');
    await expect(applyProjectManifest({bridge: f.bridge, manifest: f.manifest, snapshot: f.snapshot, prepared: f.prepared, executor: {apply: async () => {throw error;}}})).rejects.toBe(error);
    expect(f.loads).toHaveLength(1);
    expect(f.loads[0]).toEqual(new Uint8Array([1, 2, 3]));
  });

  it('rejects undo after a later project edit', async () => {
    const f = fixture();
    const result = await applyProjectManifest({bridge: f.bridge, manifest: f.manifest, snapshot: f.snapshot, prepared: f.prepared, executor: {apply: async () => undefined}});
    f.bump();
    await expect(undoProjectManifest(f.bridge, result)).rejects.toThrow('拒绝覆盖');
  });
});
