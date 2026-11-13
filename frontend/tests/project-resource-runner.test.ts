import {describe, expect, it, vi} from 'vitest';
import {createProjectApplyResult, applyProjectResources, type PreparedResource} from '../src/ai-blocks/project-resource-runner';

function fixture() {
  let revision = 1;
  const target = {id: 'target-1', isStage: false, isOriginal: true, getName: () => '角色'};
  const bridge = {
    get projectRevision() {return revision;},
    currentVm: {editingTarget: target, runtime: {targets: [target], getTargetById: (id: string) => id === target.id ? target : undefined}, setEditingTarget: vi.fn()},
    runProjectTransactionMutation: (operation: () => unknown) => operation(),
    refreshWorkspace: vi.fn(),
    saveProject: vi.fn(async () => new Uint8Array([1, 2, 3])),
    loadProject: vi.fn(async () => {revision += 1;})
  } as any;
  const snapshot = {targetId: 'target-1', targetName: '角色', isStage: false, revision: 1, blockCount: 0, hasScripts: false, scriptRefs: {}, context: {} as any};
  const manifest = {schema_version: 3 as const, scope: 'project' as const, summary: '添加背景', assumptions: [], required_extensions: [], local_state: [], resource_ops: [{op: 'add_backdrop' as const, op_id: 'backdrop-1', source_need_id: 'need-backdrop', name: '森林', source: {kind: 'scratch_library' as const, asset_type: 'backdrop' as const, search_terms: ['森林']}, tags: [], select_after: true}], program_targets: [], shared_state: {variables: [], lists: [], broadcasts: []}};
  const prepared = new Map<string, PreparedResource>([['backdrop-1', {op_id: 'backdrop-1', kind: 'backdrop', value: {}}]]);
  return {bridge, snapshot, manifest, prepared};
}

describe('project resource outer transaction', () => {
  it('applies resources without creating a nested backup', async () => {
    const f = fixture();
    const apply = vi.fn(async () => undefined);
    const result = createProjectApplyResult(f.snapshot, new Uint8Array([1, 2, 3]));
    await applyProjectResources({bridge: f.bridge, manifest: f.manifest, snapshot: f.snapshot, prepared: f.prepared, executor: {apply}}, {result});
    expect(f.bridge.saveProject).not.toHaveBeenCalled();
    expect(f.bridge.refreshWorkspace).toHaveBeenCalledOnce();
    expect(apply).toHaveBeenCalledOnce();
    expect(result.backup).toEqual(new Uint8Array([1, 2, 3]));
  });

  it('propagates resource errors to the outer transaction owner', async () => {
    const f = fixture();
    const error = new Error('安装失败');
    const result = createProjectApplyResult(f.snapshot, new Uint8Array([1, 2, 3]));
    await expect(applyProjectResources({bridge: f.bridge, manifest: f.manifest, snapshot: f.snapshot, prepared: f.prepared, executor: {apply: async () => {throw error;}}}, {result})).rejects.toBe(error);
    expect(f.bridge.loadProject).not.toHaveBeenCalled();
  });
});
