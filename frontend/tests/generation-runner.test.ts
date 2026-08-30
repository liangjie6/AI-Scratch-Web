import {describe, expect, it, vi} from 'vitest';
import * as assetStore from '../src/ai/asset-library-store';
import {UnifiedGenerationRunner, undoUnifiedGeneration, type UnifiedCommitResult} from '../src/ai-blocks/generation-runner';
import type {ProgramIR} from '../src/ai-blocks/program-ir';
import type {AiProjectSnapshot, AiTargetSnapshot} from '../src/ai-blocks/target-context';
import type {UnifiedGenerationDraft, UnifiedGenerationRequest} from '../src/ai-blocks/ai-blocks-api';
import type {ScratchGuiBridge} from '../src/scratch/scratch-gui-bridge';

class MockBlocks implements ScratchBlocksLike {
  _blocks: Record<string, ScratchBlockLike> = {};
  _scripts: string[] = [];
  constructor(private readonly changed: () => void, private readonly failAfter = Number.POSITIVE_INFINITY) {}
  getScripts() { return [...this._scripts]; }
  getBlock(id: string) { return this._blocks[id]; }
  createBlock(block: ScratchBlockLike) {
    if (Object.keys(this._blocks).length >= this.failAfter) throw new Error('模拟积木写入失败');
    this._blocks[block.id] = structuredClone(block);
    if (block.topLevel && !block.shadow) this._scripts.push(block.id);
    this.changed();
  }
  moveBlock(event: {id: string; newParent?: string; newInput?: string}) {
    const block = this._blocks[event.id];
    if (!block || !event.newParent) return;
    this._scripts = this._scripts.filter(id => id !== event.id);
    const parent = this._blocks[event.newParent];
    if (event.newInput) parent.inputs[event.newInput] = {name: event.newInput, block: event.id, shadow: null};
    else parent.next = event.id;
    block.parent = event.newParent;
    block.topLevel = false;
    this.changed();
  }
  deleteBlock(id: string) { delete this._blocks[id]; this._scripts = this._scripts.filter(value => value !== id); this.changed(); }
  deleteAllBlocks() { this.getScripts().forEach(id => this.deleteBlock(id)); }
  toXML() { return '<xml />'; }
}

function oldScript(id = 'old_top'): ScratchBlockLike {
  return {id, opcode: 'event_whenflagclicked', inputs: {}, fields: {}, next: null, parent: null, shadow: false, topLevel: true, x: 20, y: 30};
}
function program(remove_script_ids: string[] = []): ProgramIR {
  return {schema_version: 1, summary: '移动角色', remove_script_ids, variables: [], lists: [], broadcasts: [], scripts: [{id: 'script_move', purpose: '绿旗点击后移动', x: 40, y: 40, blocks: [{opcode: 'event_whenflagclicked'}, {opcode: 'motion_movesteps', inputs: {STEPS: {kind: 'number', value: 10}}}]}]};
}
function request(): UnifiedGenerationRequest {
  return {session_id: 'session-1', request_id: 'request-1', messages: [{role: 'user', content: '移动'}], target: {name: '角色1', is_stage: false, has_scripts: true, mode: 'append', costumes: [], sounds: [], sprite_names: ['角色1'], current_program: '{}'}, thinking: {enabled: false, strength: 'low'}, scope: 'single_sprite', asset_policy: 'builtin_first', proceed_with_defaults: true};
}
function fixture(failAfter = Number.POSITIVE_INFINITY, onChange?: () => void) {
  let revision = 1;
  let armed = false;
  const blocks = new MockBlocks(() => { revision += 1; if (armed) onChange?.(); }, failAfter);
  blocks.createBlock(oldScript());
  armed = true;
  const target: ScratchTargetLike = {id: 'sprite', isStage: false, isOriginal: true, getName: () => '角色1', blocks, comments: {}, variables: {}, getCostumes: () => [], getSounds: () => [], createVariable(id, name, type) { this.variables![id] = {id, name, type, value: type === 'list' ? [] : 0}; }, deleteVariable(id) { delete this.variables![id]; }, createComment(id, blockId, text, x, y, width, height, minimized) { this.comments![id] = {id, blockId, text, x, y, width, height, minimized}; }};
  const stage: ScratchTargetLike = {id: 'stage', isStage: true, isOriginal: true, getName: () => '舞台', blocks: new MockBlocks(() => { revision += 1; }), comments: {}, variables: {}, createVariable(id, name, type) { this.variables![id] = {id, name, type, value: type === 'list' ? [] : 0}; }, deleteVariable(id) { delete this.variables![id]; }};
  const vm = {editingTarget: target, runtime: {targets: [stage, target], getTargetById: (id: string) => [stage, target].find(item => item.id === id) ?? null, getTargetForStage: () => stage}, setEditingTarget: vi.fn((id: string) => { vm.editingTarget = [stage, target].find(item => item.id === id)!; }), stopAll: vi.fn()} as unknown as ScratchVmLike;
  let active = false;
  const bridge = {currentVm: vm, get projectRevision() {return revision;}, saveProject: vi.fn(async () => new Uint8Array([1, 2, 3])), loadProject: vi.fn(async () => undefined), refreshWorkspace: vi.fn(), beginBlockGeneration: vi.fn(() => {if (active) throw new Error('busy'); active = true; return () => {active = false;};}), beginProjectTransaction: vi.fn(() => {if (active) throw new Error('busy'); active = true; let released = false; const assertActive = () => {if (released || !active) throw new Error('transaction inactive');}; return {assertActive, release: () => {if (!released) {released = true; active = false;}}};}), stop: vi.fn(), activateCodeTab: vi.fn()} as unknown as ScratchGuiBridge;
  const snapshot: AiTargetSnapshot = {targetId: 'sprite', targetName: '角色1', isStage: false, revision, blockCount: 1, hasScripts: true, scriptRefs: {}, context: request().target};
  const draft = {request: request(), snapshot, requirements: {ready: true, title: '移动', goal: '移动', behavior_steps: ['移动'], triggers: ['绿旗'], controls: [], success_criteria: ['移动'], missing_questions: []}, plan: {steps: ['添加移动']}, programs: [{targetIndex: 0, metadata: {schema_version: 1, summary: '移动', remove_script_ids: []}, state: {variables: [], lists: [], broadcasts: []}, scripts: program().scripts, program: program()}]} as unknown as UnifiedGenerationDraft & {request: UnifiedGenerationRequest; snapshot: AiTargetSnapshot};
  const invalidateTransaction = () => { active = false; };
  return {bridge, blocks, target, snapshot, draft, invalidateTransaction};
}

describe('unified generation transaction', () => {
  it('commits one prepared program beside existing code', async () => {
    const f = fixture();
    const result = await new UnifiedGenerationRunner(f.bridge).commit(f.draft);
    expect(f.bridge.saveProject).toHaveBeenCalledOnce();
    expect(f.blocks.getScripts()).toHaveLength(2);
    expect(Object.values(f.target.comments ?? {})).toHaveLength(1);
    expect(result.programs[0]).toMatchObject({addedBlocks: 2, addedScripts: 1, removedScripts: 0});
  });

  it('restores the single outer backup when program commit fails', async () => {
    const f = fixture(2);
    await expect(new UnifiedGenerationRunner(f.bridge).commit(f.draft)).rejects.toThrow('模拟积木写入失败');
    expect(f.bridge.saveProject).toHaveBeenCalledOnce();
    expect(f.bridge.loadProject).toHaveBeenCalledOnce();
    expect(f.bridge.loadProject).toHaveBeenCalledWith(new Uint8Array([1, 2, 3]));
  });

  it('still restores the durable backup when the transaction lock is invalidated', async () => {
    const f = fixture(2, () => f.invalidateTransaction());
    await expect(new UnifiedGenerationRunner(f.bridge).commit(f.draft)).rejects.toThrow('模拟积木写入失败');
    expect(f.bridge.loadProject).toHaveBeenCalledOnce();
    expect(f.bridge.loadProject).toHaveBeenCalledWith(new Uint8Array([1, 2, 3]));
  });

  it('reports a lost transaction lock separately from backup restore failure', async () => {
    const f = fixture(2, () => f.invalidateTransaction());
    const error = await new UnifiedGenerationRunner(f.bridge).commit(f.draft).catch(caught => caught as Error);
    expect(f.bridge.loadProject).toHaveBeenCalledOnce();
    expect(error.message).not.toContain('无法恢复项目备份');
    expect(error.message).toContain('项目事务已失效');
    expect(error.message).toContain('模拟积木写入失败');
  });

  it('reports both a lost transaction lock and a failed backup restore', async () => {
    const f = fixture(2, () => f.invalidateTransaction());
    vi.mocked(f.bridge.loadProject).mockRejectedValue(new Error('load failed'));
    const error = await new UnifiedGenerationRunner(f.bridge).commit(f.draft).catch(caught => caught as Error);
    expect(error.message).toContain('项目事务已失效');
    expect(error.message).toContain('无法恢复项目备份');
  });

  it('attributes a genuine backup restore failure to restore', async () => {
    const f = fixture(2);
    vi.mocked(f.bridge.loadProject).mockRejectedValue(new Error('load failed'));
    const error = await new UnifiedGenerationRunner(f.bridge).commit(f.draft).catch(caught => caught as Error);
    expect(error.message).toContain('无法恢复项目备份');
    expect(error.message).not.toContain('项目事务已失效');
  });

  it('cleans generated assets when the outer commit rolls back', async () => {
    const deleteAsset = vi.spyOn(assetStore, 'deleteGeneratedAsset').mockResolvedValue();
    const f = fixture(2);
    await expect(new UnifiedGenerationRunner(f.bridge).commit(f.draft, {generatedAssets: [{id: 'generated-1'} as any]})).rejects.toThrow('模拟积木写入失败');
    expect(deleteAsset).toHaveBeenCalledWith('generated-1');
    deleteAsset.mockRestore();
  });

  it('keeps generated assets when restoring the project backup fails', async () => {
    const deleteAsset = vi.spyOn(assetStore, 'deleteGeneratedAsset').mockResolvedValue();
    const f = fixture(2);
    vi.mocked(f.bridge.loadProject).mockRejectedValue(new Error('load failed'));
    await expect(new UnifiedGenerationRunner(f.bridge).commit(f.draft, {generatedAssets: [{id: 'generated-2'} as any]})).rejects.toThrow('无法恢复项目备份');
    expect(deleteAsset).not.toHaveBeenCalled();
    deleteAsset.mockRestore();
  });

  it('undoes a completed unified commit only at its recorded revision', async () => {
    const f = fixture();
    const result = await new UnifiedGenerationRunner(f.bridge).commit(f.draft);
    await undoUnifiedGeneration(f.bridge, result);
    expect(f.bridge.loadProject).toHaveBeenCalledWith(result.backup);
    expect(f.bridge.activateCodeTab).toHaveBeenCalledOnce();
  });

  it('commits all project target programs under the same outer backup', async () => {
    const f = fixture();
    const projectRequest = {...request(), scope: 'project' as const, project_context: {
      project_id: 'scratch-project', version: f.bridge.projectRevision,
      targets: [], shared_state: {}
    }};
    const projectSnapshot: AiProjectSnapshot = {revision: f.bridge.projectRevision, editingTargetId: 'sprite', targetIds: ['stage', 'sprite']};
    const manifest = {
      schema_version: 1 as const, scope: 'project' as const, summary: '项目移动逻辑', resource_ops: [],
      program_targets: [
        {target: {kind: 'current_target' as const}, mode: 'append' as const, intent: '让当前角色移动'},
        {target: {kind: 'stage' as const}, mode: 'append' as const, intent: '让舞台响应绿旗'}
      ],
      shared_state: {variables: [], lists: [], broadcasts: []}
    };
    const secondProgram: ProgramIR = {...program(), scripts: [{...program().scripts[0], blocks: [{opcode: 'event_whenflagclicked'}]}]};
    const draft = {
      request: projectRequest,
      snapshot: f.snapshot,
      projectSnapshot,
      requirements: {ready: true, title: '项目移动', goal: '项目移动', behavior_steps: ['移动'], triggers: ['绿旗'], controls: [], success_criteria: ['移动'], missing_questions: []},
      plan: {steps: ['添加移动']},
      manifest,
      programs: [
        {targetIndex: 0, metadata: {schema_version: 1, summary: '角色移动', remove_script_ids: []}, state: {variables: [], lists: [], broadcasts: []}, scripts: program().scripts, program: program()},
        {targetIndex: 1, metadata: {schema_version: 1, summary: '舞台移动', remove_script_ids: []}, state: {variables: [], lists: [], broadcasts: []}, scripts: secondProgram.scripts, program: secondProgram}
      ]
    } as unknown as UnifiedGenerationDraft & {request: UnifiedGenerationRequest; snapshot: AiTargetSnapshot; projectSnapshot: AiProjectSnapshot};
    const result = await new UnifiedGenerationRunner(f.bridge).commit(draft);
    expect(f.bridge.saveProject).toHaveBeenCalledOnce();
    expect(result.programs).toHaveLength(2);
    expect(result.project?.appliedPrograms).toHaveLength(2);
    expect(f.bridge.currentVm?.editingTarget?.id).toBe('sprite');
  });
});
