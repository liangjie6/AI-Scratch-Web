import 'fake-indexeddb/auto';
import {beforeEach, describe, expect, it, vi} from 'vitest';
import {
  clearGeneratedAssets,
  countGeneratedAssets,
  deleteGeneratedAsset,
  listGeneratedAssets,
  saveGeneratedAsset,
  updateGeneratedAsset
} from '../src/ai/asset-library-store';
import type {ImageGenerationRequest, ImageGenerationResponse} from '../src/ai/image-generation-types';

const request: ImageGenerationRequest = {
  prompt: '夜晚的森林小屋',
  style: 'cartoon',
  width: 480,
  height: 360,
  target: 'backdrop',
  sheet_layout: '1x1'
};

function response(id: string): ImageGenerationResponse {
  return {
    request_id: id,
    mime_type: 'image/png',
    sheet_width: 1024,
    sheet_height: 1024,
    sheet_layout: {columns: 1, rows: 1},
    image_base64: 'cG5n',
    target: 'backdrop',
    requested_canvas: {width: 480, height: 360}
  };
}

beforeEach(async () => {
  vi.restoreAllMocks();
  await clearGeneratedAssets();
});

describe('asset library store', () => {
  it('persists generated results and returns newest first', async () => {
    vi.spyOn(Date, 'now').mockReturnValueOnce(100).mockReturnValueOnce(200);
    await saveGeneratedAsset(response('first'), request);
    await saveGeneratedAsset(response('second'), {...request, prompt: '像素城市', style: 'pixel'});

    const assets = await listGeneratedAssets();
    expect(assets.map(asset => asset.prompt)).toEqual(['像素城市', '夜晚的森林小屋']);
    expect(assets[0]).toMatchObject({prompt: '像素城市', style: 'pixel', target: 'backdrop'});
    expect(assets.every(asset => asset.name.startsWith('背景素材 '))).toBe(true);
    expect(new Set(assets.map(asset => asset.id)).size).toBe(2);
    expect(await countGeneratedAssets()).toBe(2);
  });

  it('stores and edits v2 tags and origin metadata', async () => {
    const asset = await saveGeneratedAsset(response('tagged'), request, {tags: ['forest', 'forest', 'night'], origin: 'canvas_derived', groupId: 'group-1', derivedFrom: 'source-1'});
    expect(asset).toMatchObject({schemaVersion: 2, tags: ['forest', 'night'], origin: 'canvas_derived', groupId: 'group-1', derivedFrom: 'source-1'});
    const updated = await updateGeneratedAsset(asset.id, {tags: ['updated'], name: '新名称'});
    expect(updated).toMatchObject({name: '新名称', tags: ['updated']});
  });

  it('keeps duplicate request ids as separate assets and supports deletion', async () => {
    const first = await saveGeneratedAsset(response('same'), request);
    const second = await saveGeneratedAsset(response('same'), {...request, prompt: '更新后的描述'});

    expect(first.id).not.toBe(second.id);
    expect(await countGeneratedAssets()).toBe(2);
    expect((await listGeneratedAssets()).map(asset => asset.prompt)).toContain('更新后的描述');

    await deleteGeneratedAsset(first.id);
    expect(await countGeneratedAssets()).toBe(1);
    expect((await listGeneratedAssets())[0].id).toBe(second.id);
  });
});
