import {describe, expect, it, vi} from 'vitest';
import {fitSpriteInitialSize, getGeneratedImageRect, importBackdropCostume} from '../src/scratch/asset-adapter';

describe('generated image coordinates', () => {
  it('uses the complete generated image', () => {
    expect(getGeneratedImageRect(1024, 768)).toEqual({x: 0, y: 0, width: 1024, height: 768});
  });
});

describe('Scratch target imports', () => {
  it('scales oversized bitmap costumes to fit the stage', () => {
    const costume = {name: '整幅背景', blob: new Blob(), dataFormat: 'png' as const, assetWidth: 3072, assetHeight: 2048, rotationCenterX: 1536, rotationCenterY: 1024, bitmapResolution: 2 as const};
    expect(fitSpriteInitialSize([costume])).toBeCloseTo(31.25);
  });

  it('keeps stage-sized costumes at 100 percent', () => {
    const costume = {name: '舞台尺寸', blob: new Blob(), dataFormat: 'png' as const, assetWidth: 960, assetHeight: 720, rotationCenterX: 480, rotationCenterY: 360, bitmapResolution: 2 as const};
    expect(fitSpriteInitialSize([costume])).toBe(100);
  });

  it('commits a standard bitmap backdrop through the VM', async () => {
    const addBackdrop = vi.fn(async () => undefined);
    const asset = {assetId: 'background-asset'};
    const bridge = {currentVm: {addBackdrop}, currentStorage: {AssetType: {ImageBitmap: 'bitmap'}, DataFormat: {PNG: 'png'}, createAsset: vi.fn(() => asset)}} as any;
    const backdrop = {name: '森林', blob: new Blob([new Uint8Array([1, 2, 3])], {type: 'image/png'}), dataFormat: 'png' as const, assetWidth: 960, assetHeight: 720, rotationCenterX: 480, rotationCenterY: 360, bitmapResolution: 2 as const};
    await expect(importBackdropCostume(bridge, backdrop)).resolves.toEqual({assetId: 'background-asset'});
    expect(addBackdrop).toHaveBeenCalledWith('background-asset.png', expect.objectContaining({name: '森林', bitmapResolution: 2, rotationCenterX: 480, rotationCenterY: 360}));
  });
});

