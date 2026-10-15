// @vitest-environment jsdom
import {Blob as NodeBlob} from 'node:buffer';
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import type {CostumeExport} from '../src/scratch/asset-adapter';
import {bitmapCostumeEditorScale, normalizeCostumeForEditor, normalizeSpriteForEditor} from '../src/scratch/costume-normalizer';

function costume(width: number, height: number, rotationCenterX = width / 2, rotationCenterY = height / 2): CostumeExport {
  return {name: '角色', blob: new Blob(), dataFormat: 'png', assetWidth: width, assetHeight: height, rotationCenterX, rotationCenterY, bitmapResolution: 2};
}

beforeEach(() => vi.stubGlobal('Blob', NodeBlob));
afterEach(() => vi.unstubAllGlobals());

describe('Scratch bitmap costume canvas limits', () => {
  it.each([
    [400, 2048, 200, 1024],
    [4096, 300, 2048, 150],
    [800, 1000, 50, 900],
    [102, 100, 1000, -500]
  ])('contains all pixels around the pivot for %i × %i', async (width, height, centerX, centerY) => {
    const canvas = {width: 0, height: 0, getContext: () => ({drawImage: vi.fn()}), toBlob: (callback: (blob: Blob) => void) => callback(new Blob())};
    const close = vi.fn();
    vi.stubGlobal('createImageBitmap', vi.fn(async () => ({close})));
    vi.stubGlobal('document', {createElement: () => canvas});
    const input = costume(width, height, centerX, centerY);
    const output = await normalizeCostumeForEditor(input);

    expect(480 - output.rotationCenterX).toBeGreaterThanOrEqual(0);
    expect(480 - output.rotationCenterX + output.assetWidth).toBeLessThanOrEqual(960);
    expect(360 - output.rotationCenterY).toBeGreaterThanOrEqual(0);
    expect(360 - output.rotationCenterY + output.assetHeight).toBeLessThanOrEqual(720);
    expect(output.rotationCenterX / centerX).toBeCloseTo(output.rotationCenterY / centerY);
    expect(close).toHaveBeenCalledOnce();
  });

  it('preserves an existing costume that already fits', async () => {
    const input = costume(960, 720);
    expect(bitmapCostumeEditorScale(input)).toBe(1);
    expect(await normalizeCostumeForEditor(input)).toBe(input);
  });

  it('keeps relative costume sizes and compensates the stage size', async () => {
    vi.stubGlobal('createImageBitmap', vi.fn(async () => ({close() {}})));
    vi.stubGlobal('document', {createElement: () => ({width: 0, height: 0, getContext: () => ({drawImage() {}}), toBlob: (callback: (blob: Blob) => void) => callback(new Blob())})});
    const input = {name: '长角色', initialSize: 25, costumes: [costume(400, 2000), costume(200, 1000)]};
    const output = await normalizeSpriteForEditor(input);
    const first = output.costumes[0]!;
    const second = output.costumes[1]!;
    expect(first.rotationCenterY).toBeCloseTo(second.rotationCenterY * 2);
    expect(first.rotationCenterX).toBeCloseTo(second.rotationCenterX * 2);
    expect(first.rotationCenterY * output.initialSize).toBeCloseTo(input.costumes[0]!.rotationCenterY * input.initialSize);
    expect(input.costumes[0]!.assetHeight).toBe(2000);
  });

  it('keeps a vector aligned with a resized bitmap in the same sprite', async () => {
    vi.stubGlobal('createImageBitmap', vi.fn(async () => ({close() {}})));
    vi.stubGlobal('document', {createElement: () => ({width: 0, height: 0, getContext: () => ({drawImage() {}}), toBlob: (callback: (blob: Blob) => void) => callback(new Blob())})});
    const svg = {...costume(400, 2000), dataFormat: 'svg' as const, bitmapResolution: 1 as const, blob: new Blob(['<svg xmlns="http://www.w3.org/2000/svg" width="400" height="2000"><rect width="400" height="2000" fill="red"/></svg>'])};
    const output = await normalizeSpriteForEditor({name: '混合造型', initialSize: 25, costumes: [costume(400, 2000), svg]});
    const vector = output.costumes[1]!;
    const document = new DOMParser().parseFromString(await vector.blob.text(), 'image/svg+xml');
    expect(document.documentElement.getAttribute('viewBox')).toBe('0 0 400 2000');
    expect(Number(document.documentElement.getAttribute('height'))).toBeCloseTo(output.costumes[0]!.rotationCenterY * 2);
    expect(vector.rotationCenterY).toBe(output.costumes[0]!.rotationCenterY);
    expect(vector.dataFormat).toBe('svg');
    expect(vector.bitmapResolution).toBe(1);
  });

  it.each([NaN, Infinity, -5, 0])('rejects invalid image dimensions before decoding (%s)', async width => {
    const decode = vi.fn();
    vi.stubGlobal('createImageBitmap', decode);
    await expect(normalizeSpriteForEditor({name: '无效角色', initialSize: 100, costumes: [costume(width, 1000, 100, 500)]})).rejects.toThrow('资源规格无效');
    expect(decode).not.toHaveBeenCalled();
  });
});
