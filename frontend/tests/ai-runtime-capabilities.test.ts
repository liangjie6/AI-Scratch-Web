import {afterEach, describe, expect, it, vi} from 'vitest';

afterEach(() => {vi.unstubAllGlobals(); vi.resetModules();});

describe('source pipeline capability negotiation', () => {
  it.each([
    [true, 'scratch-source/1', true],
    [true, undefined, false],
    [true, 'scratch-source/2', false],
    [false, 'scratch-source/1', false],
  ])('requires the feature and exact protocol version (%s, %s)', async (feature, version, enabled) => {
    vi.stubGlobal('sessionStorage', {getItem: () => 'test-scope'});
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ai_blocks_protocol: [3], source_pipeline_version: version, features: {source_pipeline: feature}}))));
    const {getAiRuntimeCapabilities} = await import('../src/ai-blocks/ai-runs-api');
    expect((await getAiRuntimeCapabilities())?.sourcePipeline).toBe(enabled);
  });
});
