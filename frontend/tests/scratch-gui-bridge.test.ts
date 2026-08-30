import {describe, expect, it, vi} from 'vitest';
import {ScratchGuiBridge} from '../src/scratch/scratch-gui-bridge';

function costume(name: string) {return {name, blob: new Blob([new Uint8Array([1, 2, 3])], {type: 'image/png'}), dataFormat: 'png' as const, assetWidth: 100, assetHeight: 100, rotationCenterX: 50, rotationCenterY: 50, bitmapResolution: 2 as const};}

function fixture() {
  let id = 0;
  const sprite = {id: 'sprite-1', isStage: false, currentCostume: 0, getCostumes: () => [{name: '默认', assetId: 'old'}]};
  const stage = {id: 'stage', isStage: true, currentCostume: 1, getCostumes: () => [{name: '白色', assetId: 'white'}, {name: '森林', assetId: 'forest'}], setCostume(index: number) {this.currentCostume = index;}};
  const vm = {editingTarget: sprite, runtime: {targets: [stage, sprite], getTargetForStage: () => stage, getTargetById: (targetId: string) => targetId === sprite.id ? sprite : null, emitProjectChanged: vi.fn()}, addCostume: vi.fn(async (_md5: string, data: any) => {sprite.getCostumes = () => [{name: '默认', assetId: 'old'}, {name: data.name, assetId: data.assetId}];}), addBackdrop: vi.fn(async (_md5: string, data: any) => {stage.getCostumes = () => [{name: '白色', assetId: 'white'}, {name: '森林', assetId: 'forest'}, {name: data.name, assetId: data.assetId}];}), saveProjectSb3: vi.fn(async () => new Uint8Array([1])), loadProject: vi.fn(async () => undefined), setEditingTarget: vi.fn(), deleteSprite: vi.fn(), greenFlag: vi.fn(), stopAll: vi.fn()};
  const bridge = new ScratchGuiBridge();
  bridge.setStorage({AssetType: {ImageBitmap: 'bitmap', ImageVector: 'vector'}, DataFormat: {PNG: 'png', SVG: 'svg'}, createAsset: vi.fn(() => ({assetId: 'asset-' + (++id), assetType: 'bitmap', dataFormat: 'png'}))} as any);
  bridge.setVm(vm as any); bridge.markProjectLoaded();
  return {bridge, vm, sprite, stage};
}

describe('ScratchGuiBridge resource operations', () => {
  it('adds a costume to the requested sprite and can restore selection', async () => {
    const f = fixture(); const result = await f.bridge.addCostumeToTarget('sprite-1', costume('新造型'), false);
    expect(result.assetId).toBe('asset-1'); expect(f.vm.addCostume).toHaveBeenCalledWith('asset-1.png', expect.objectContaining({name: '新造型'}), 'sprite-1'); expect(f.sprite.currentCostume).toBe(0);
  });
  it('adds a backdrop and selects an existing backdrop by name', async () => {
    const f = fixture(); await f.bridge.addBackdropToStage(costume('新背景')); expect(f.vm.addBackdrop).toHaveBeenCalledOnce();
    const selected = f.bridge.selectBackdropByName('森林'); expect(selected).toEqual({assetId: 'forest', name: '森林'}); expect(f.stage.currentCostume).toBe(1);
  });
  it('rejects stage targets for costumes', async () => {
    const f = fixture(); await expect(f.bridge.addCostumeToTarget('stage', costume('错误'))).rejects.toThrow('已有角色');
  });

  it('re-arms readiness and invalidates an old generation release on VM rebind', async () => {
    const f = fixture();
    await expect(f.bridge.ready).resolves.toBe(f.vm);
    const releaseOldGeneration = f.bridge.beginBlockGeneration();
    const secondVm = {...f.vm, editingTarget: f.sprite, runtime: {...f.vm.runtime, targets: [f.stage, f.sprite]}};
    f.bridge.setVm(secondVm as any);
    expect(f.bridge.currentVm).toBe(secondVm);
    await expect(f.bridge.ready).resolves.toBe(secondVm);
    const releaseNewGeneration = f.bridge.beginBlockGeneration();
    releaseOldGeneration();
    expect(() => f.bridge.beginBlockGeneration()).toThrow('已有 AI 积木生成任务');
    releaseNewGeneration();
    expect(() => f.bridge.beginBlockGeneration()).not.toThrow();
  });

  it('re-arms readiness when a disposed bridge is reused', async () => {
    const f = fixture();
    const oldReady = f.bridge.ready;
    oldReady.catch(() => undefined);
    f.bridge.dispose();
    const secondVm = {...f.vm, editingTarget: f.sprite, runtime: {...f.vm.runtime, targets: [f.stage, f.sprite]}};
    f.bridge.setVm(secondVm as any);
    f.bridge.markProjectLoaded();
    await expect(f.bridge.ready).resolves.toBe(secondVm);
  });

  it('invalidates a project transaction when the VM is rebound', () => {
    const f = fixture();
    const transaction = f.bridge.beginProjectTransaction();
    const secondVm = {...f.vm, editingTarget: f.sprite, runtime: {...f.vm.runtime, targets: [f.stage, f.sprite]}};
    f.bridge.setVm(secondVm as any);
    expect(() => transaction.assertActive()).toThrow('项目事务已失效');
    transaction.release();
  });

  it('clears VM listeners and operation state on dispose', () => {
    const f = fixture();
    const targetListener = vi.fn();
    const projectListener = vi.fn();
    f.bridge.subscribeTargets(targetListener);
    f.bridge.subscribeProject(projectListener);
    const release = f.bridge.beginBlockGeneration();
    f.bridge.dispose();
    expect(f.bridge.currentVm).toBeNull();
    expect(f.bridge.isReady).toBe(false);
    expect(f.bridge.importIsFatal).toBe(false);
    expect(() => f.bridge.beginBlockGeneration()).not.toThrow();
    release();
    expect(targetListener).not.toHaveBeenCalled();
    expect(projectListener).not.toHaveBeenCalled();
  });
});
