import 'fake-indexeddb/auto';
import {beforeEach, describe, expect, it, vi} from 'vitest';
import * as assetStore from '../src/ai/asset-library-store';
import {clearGeneratedAssets, countGeneratedAssets} from '../src/ai/asset-library-store';
import {prepareProjectResources} from '../src/ai-blocks/project-resource-preparer';
import {clearScratchLibraryCache} from '../src/ai/scratch-library';

const source = {kind: 'image_generation' as const, reason: '没有匹配的内置角色', prompt: '一只蓝色小猫', style: 'cartoon' as const, width: 512, height: 512, sheet_layout: '1x1' as const};
const plan = {schema_version: 2 as const, scope: 'project' as const, summary: '创建角色', resource_ops: [{op: 'add_costume' as const, op_id: 'costume-1', target: {kind: 'current_target' as const}, name: '蓝猫', source, tags: ['cat'], select_after: true}], program_targets: [], shared_state: {variables: [], lists: [], broadcasts: []}};

beforeEach(async () => {await clearGeneratedAssets(); clearScratchLibraryCache(); vi.restoreAllMocks();});

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

  it('resolves Scratch library search terms without a manual selection', async () => {
    const item = {name: 'Blue Cat', assetId: 'cat-asset', md5ext: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png', dataFormat: 'png' as const, bitmapResolution: 1 as const, rotationCenterX: 240, rotationCenterY: 180, tags: ['cat']};
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).includes('costumes.json')) return new Response(JSON.stringify([item]), {status: 200});
      return new Response('png-bytes', {status: 200, headers: {'content-type': 'image/png'}});
    });
    vi.stubGlobal('fetch', fetchMock);
    const libraryPlan = {...plan, resource_ops: [{...plan.resource_ops[0], source: {kind: 'scratch_library' as const, asset_type: 'costume' as const, search_terms: ['cat']}}]};

    const result = await prepareProjectResources({plan: libraryPlan});

    expect(result.prepared.get('costume-1')?.kind).toBe('costume');
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(String(fetchMock.mock.calls[0][0])).toContain('costumes.json');
  });

  it('waits for slower workers before cleaning generated assets after a sibling failure', async () => {
    const secondSource = {...source, prompt: '一只红色小猫'};
    const twoResources = {...plan, resource_ops: [
      plan.resource_ops[0],
      {...plan.resource_ops[0], op_id: 'costume-2', name: '红猫', source: secondSource}
    ]};
    const response = {request_id: 'request-1', mime_type: 'image/png', sheet_width: 512, sheet_height: 512, sheet_layout: {columns: 1, rows: 1}, image_base64: 'cG5n', target: 'sprite', requested_canvas: {width: 512, height: 512}};
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).includes('/api/image/generate')) {
        const body = JSON.parse(String(init?.body));
        if (body.prompt === secondSource.prompt) {
          await new Promise(resolve => setTimeout(resolve, 5));
          return new Response(JSON.stringify({detail: {code: 'FAILED', message: 'generation failed'}}), {status: 500});
        }
        await new Promise(resolve => setTimeout(resolve, 30));
        return new Response(JSON.stringify(response), {status: 200});
      }
      throw new Error('unexpected request');
    }));
    const deleteAsset = vi.spyOn(assetStore, 'deleteGeneratedAsset');

    await expect(prepareProjectResources({plan: twoResources, confirmGeneration: () => true, maxConcurrency: 2})).rejects.toThrow('generation failed');

    expect(deleteAsset).toHaveBeenCalledTimes(1);
    expect(await countGeneratedAssets()).toBe(0);
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
