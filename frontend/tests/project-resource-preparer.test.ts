import 'fake-indexeddb/auto';
import {beforeEach, describe, expect, it, vi} from 'vitest';
import {clearGeneratedAssets, countGeneratedAssets} from '../src/ai/asset-library-store';
import {prepareProjectResources} from '../src/ai-blocks/project-resource-preparer';

const source = {kind: 'image_generation' as const, reason: '没有匹配的内置角色', prompt: '一只蓝色小猫', style: 'cartoon' as const, width: 480, height: 360, sheet_layout: '1x1' as const};
const plan = {schema_version: 1 as const, scope: 'project' as const, summary: '创建角色', resource_ops: [{op: 'add_costume' as const, op_id: 'costume-1', target: {kind: 'current_target' as const}, name: '蓝猫', source, tags: ['cat'], select_after: true}], block_programs: []};

beforeEach(async () => {await clearGeneratedAssets(); vi.restoreAllMocks();});

describe('ProjectPlan preparation', () => {
  it('does not call image generation when the user declines', async () => {
    const fetchMock = vi.fn(); vi.stubGlobal('fetch', fetchMock);
    await expect(prepareProjectResources({plan, confirmGeneration: () => false})).rejects.toMatchObject({name: 'AbortError'});
    expect(fetchMock).not.toHaveBeenCalled();
    expect(await countGeneratedAssets()).toBe(0);
  });

  it('confirms, generates, and stores the original before commit', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({request_id: 'request-1', mime_type: 'image/png', sheet_width: 480, sheet_height: 360, sheet_layout: {columns: 1, rows: 1}, image_base64: 'cG5n', target: 'sprite', requested_canvas: {width: 480, height: 360}}), {status: 200})));
    const result = await prepareProjectResources({plan, confirmGeneration: confirmation => confirmation.prompt === source.prompt});
    expect(result.prepared.get('costume-1')?.kind).toBe('costume');
    expect(result.generatedAssets).toHaveLength(1);
    expect(await countGeneratedAssets()).toBe(1);
  });
});
