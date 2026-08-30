import 'fake-indexeddb/auto';
import {beforeEach, describe, expect, it, vi} from 'vitest';
import * as assetStore from '../src/ai/asset-library-store';
import {clearGeneratedAssets, countGeneratedAssets} from '../src/ai/asset-library-store';
import {prepareProjectResources} from '../src/ai-blocks/project-resource-preparer';

const source = {kind: 'image_generation' as const, reason: '没有匹配的内置角色', prompt: '一只蓝色小猫', style: 'cartoon' as const, width: 512, height: 512, sheet_layout: '1x1' as const};
const plan = {schema_version: 1 as const, scope: 'project' as const, summary: '创建角色', resource_ops: [{op: 'add_costume' as const, op_id: 'costume-1', target: {kind: 'current_target' as const}, name: '蓝猫', source, tags: ['cat'], select_after: true}], program_targets: [], shared_state: {variables: [], lists: [], broadcasts: []}};

beforeEach(async () => {await clearGeneratedAssets(); vi.restoreAllMocks();});

describe('ProjectManifest preparation', () => {
  it('does not call image generation when the user declines', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    await expect(prepareProjectResources({plan, confirmGeneration: () => false})).rejects.toMatchObject({name: 'AbortError'});
    expect(fetchMock).not.toHaveBeenCalled();
    expect(await countGeneratedAssets()).toBe(0);
  });

  it('rejects image generation under existing-only policy', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    await expect(prepareProjectResources({plan, assetPolicy: 'existing_only', confirmGeneration: () => true})).rejects.toThrow('仅已有素材');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('confirms, generates, and stores the original before commit', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({request_id: 'request-1', mime_type: 'image/png', sheet_width: 512, sheet_height: 512, sheet_layout: {columns: 1, rows: 1}, image_base64: 'cG5n', target: 'sprite', requested_canvas: {width: 512, height: 512}}), {status: 200})));
    const result = await prepareProjectResources({plan, confirmGeneration: confirmation => confirmation.prompt === source.prompt});
    expect(result.prepared.get('costume-1')?.kind).toBe('costume');
    expect(result.generatedAssets).toHaveLength(1);
    expect(await countGeneratedAssets()).toBe(1);
  });

  it('attempts every generated-asset deletion when cleanup partially fails', async () => {
    const secondSource = {...source, prompt: '一只红色小猫'};
    const twoResources = {...plan, resource_ops: [
      plan.resource_ops[0],
      {...plan.resource_ops[0], op_id: 'costume-2', name: '红猫', source: secondSource}
    ]};
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({request_id: 'request-1', mime_type: 'image/png', sheet_width: 512, sheet_height: 512, sheet_layout: {columns: 1, rows: 1}, image_base64: 'cG5n', target: 'sprite', requested_canvas: {width: 512, height: 512}}), {status: 200})));
    const deleteAsset = vi.spyOn(assetStore, 'deleteGeneratedAsset')
      .mockRejectedValueOnce(new Error('first cleanup failed'))
      .mockResolvedValue(undefined);
    await expect(prepareProjectResources({
      plan: twoResources,
      confirmGeneration: () => true,
      onProgress: current => {if (current === 2) throw new Error('stop after both resources');}
    })).rejects.toThrow('无法清理已生成素材');
    expect(deleteAsset).toHaveBeenCalledTimes(2);
    deleteAsset.mockRestore();
  });

  it('removes generated assets when preparation fails after a successful operation', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({request_id: 'request-1', mime_type: 'image/png', sheet_width: 512, sheet_height: 512, sheet_layout: {columns: 1, rows: 1}, image_base64: 'cG5n', target: 'sprite', requested_canvas: {width: 512, height: 512}}), {status: 200})));
    await expect(prepareProjectResources({
      plan,
      confirmGeneration: () => true,
      onProgress: () => { throw new Error('stop after first resource'); }
    })).rejects.toThrow('stop after first resource');
    expect(await countGeneratedAssets()).toBe(0);
  });
});
