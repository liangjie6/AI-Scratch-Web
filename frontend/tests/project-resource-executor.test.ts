import {describe, expect, it, vi} from 'vitest';
import {createProjectResourceExecutor} from '../src/ai-blocks/project-resource-executor';

const costume = {name: '森林', blob: new Blob([new Uint8Array([1])]), dataFormat: 'png' as const, assetWidth: 100, assetHeight: 100, rotationCenterX: 50, rotationCenterY: 50, bitmapResolution: 2 as const};
const prepared = {op_id: 'costume-1', kind: 'costume' as const, value: costume};
function context() {return {result: {addedTargets: [], addedCostumes: [], addedBackdrops: [], appliedPrograms: [], original_target_id: 'current', revision_at_complete: 1, backup: new Uint8Array()}} as any;}

describe('Project manifest resource executor', () => {
  it('applies costumes to the selected target and records the result', async () => {
    const bridge = {addCostumeToTarget: vi.fn(async () => ({assetId: 'asset-1', name: '森林'})), currentVm: {runtime: {targets: []}}} as any;
    const contextValue = context();
    await createProjectResourceExecutor({bridge, currentTargetId: 'target-1'}).apply({op: 'add_costume', op_id: 'costume-1', target: {kind: 'current_target'}, name: '森林', source: {kind: 'image_generation', reason: 'x', prompt: 'x', style: 'cartoon', width: 512, height: 512, sheet_layout: '1x1'}, tags: [], select_after: true}, prepared, contextValue);
    expect(bridge.addCostumeToTarget).toHaveBeenCalledWith('target-1', costume, true); expect(contextValue.result.addedCostumes[0].asset_id).toBe('asset-1');
  });
  it('selects an existing backdrop without preparing a new asset', async () => {
    const bridge = {selectBackdropByName: vi.fn(() => ({assetId: 'asset-2', name: '森林'}))} as any; const contextValue = context();
    await createProjectResourceExecutor({bridge, currentTargetId: 'target-1'}).apply({op: 'select_backdrop', op_id: 'backdrop-1', name: '森林'}, {op_id: 'backdrop-1', kind: 'backdrop_selection', value: {name: '森林'}}, contextValue);
    expect(bridge.selectBackdropByName).toHaveBeenCalledWith('森林'); expect(contextValue.result.selectedBackdrop.asset_id).toBe('asset-2');
  });
  it('awaits sprite deletion and verifies the target is gone', async () => {
    const current = {id: 'target-1', isStage: false, getName: () => '当前角色'};
    const deleted = {id: 'target-2', isStage: false, getName: () => '旧角色'};
    const targets = [current, deleted];
    const bridge = {currentVm: {runtime: {targets}, deleteSprite: vi.fn(async (id: string) => {
      await Promise.resolve();
      const index = targets.findIndex(target => target.id === id);
      if (index >= 0) targets.splice(index, 1);
    })}} as any;
    await createProjectResourceExecutor({bridge, currentTargetId: 'target-1'}).apply({op: 'delete_sprite', op_id: 'delete-1', target: {kind: 'existing_sprite', name: '旧角色'}, name: '旧角色'}, {} as any, context());
    expect(bridge.currentVm.deleteSprite).toHaveBeenCalledWith('target-2');
    expect(targets.map(target => target.id)).toEqual(['target-1']);
  });

  it('rejects a sprite deletion that silently leaves the target in the VM', async () => {
    const bridge = {currentVm: {runtime: {targets: [{id: 'target-1'}, {id: 'target-2', isStage: false, getName: () => '旧角色'}]}, deleteSprite: vi.fn()}} as any;
    await expect(createProjectResourceExecutor({bridge, currentTargetId: 'target-1'}).apply({op: 'delete_sprite', op_id: 'delete-2', target: {kind: 'existing_sprite', name: '旧角色'}, name: '旧角色'}, {} as any, context())).rejects.toThrow('角色删除未完成');
  });

  it('resolves a created sprite when adding a later costume', async () => {
    const bridge = {addCostumeToTarget: vi.fn(async () => ({assetId: 'asset-3', name: '帽子'})), currentVm: {runtime: {targets: []}}} as any;
    const contextValue = context();
    contextValue.result.addedTargets.push({op_id: 'sprite-1', target_id: 'created-target', name: '角色'});
    await createProjectResourceExecutor({bridge, currentTargetId: 'target-1'}).apply({
      op: 'add_costume', op_id: 'costume-2', target: {kind: 'created_sprite', op_id: 'sprite-1'}, name: '帽子',
      source: {kind: 'image_generation', reason: 'x', prompt: 'x', style: 'cartoon', width: 512, height: 512, sheet_layout: '1x1'}, tags: [], select_after: false
    }, {...prepared, op_id: 'costume-2'}, contextValue);
    expect(bridge.addCostumeToTarget).toHaveBeenCalledWith('created-target', costume, false);
    expect(contextValue.result.addedCostumes[0].target_id).toBe('created-target');
  });
});
