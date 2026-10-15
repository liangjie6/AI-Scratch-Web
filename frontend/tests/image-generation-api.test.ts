import {afterEach, describe, expect, it, vi} from 'vitest';
import {generateImage, ImageGenerationError} from '../src/ai/image-generation-api';

afterEach(() => vi.restoreAllMocks());

describe('image generation API', () => {
  it('posts only the local contract and validates the sheet response', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      request_id: 'test', mime_type: 'image/png', sheet_width: 1024, sheet_height: 1024,
      sheet_layout: {columns: 1, rows: 1}, image_base64: 'cG5n', target: 'backdrop',
      requested_canvas: {width: 480, height: 360}
    }), {status: 200, headers: {'Content-Type': 'application/json'}}));
    const result = await generateImage({
      prompt: '小屋', style: 'cartoon', width: 480, height: 360, target: 'backdrop', sheet_layout: '1x1'
    });
    expect(result.request_id).toBe('test');
    expect(fetchMock).toHaveBeenCalledWith('/api/image/generate', expect.objectContaining({method: 'POST'}));
  });

  it('posts with explicit model parameter when provided', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      request_id: 'test-agnes', mime_type: 'image/png', sheet_width: 1024, sheet_height: 1024,
      sheet_layout: {columns: 1, rows: 1}, image_base64: 'cG5n', target: 'backdrop',
      requested_canvas: {width: 480, height: 360}
    }), {status: 200, headers: {'Content-Type': 'application/json'}}));
    const result = await generateImage({
      prompt: '小屋', style: 'cartoon', width: 480, height: 360, target: 'backdrop', sheet_layout: '1x1',
      model: 'agnes-image-2.5-flash'
    });
    expect(result.request_id).toBe('test-agnes');
    expect(fetchMock).toHaveBeenCalledWith('/api/image/generate', expect.objectContaining({
      body: JSON.stringify({
        prompt: '小屋', style: 'cartoon', width: 480, height: 360, target: 'backdrop', sheet_layout: '1x1',
        model: 'agnes-image-2.5-flash'
      })
    }));
  });

  it('normalizes API errors without exposing provider details', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      detail: {code: 'PROVIDER_ERROR', message: '生图服务暂时不可用'}
    }), {status: 502, headers: {'Content-Type': 'application/json'}}));
    await expect(generateImage({
      prompt: '小屋', style: 'cartoon', width: 480, height: 360, target: 'backdrop', sheet_layout: '1x1'
    })).rejects.toEqual(expect.objectContaining({
      code: 'PROVIDER_ERROR'
    } satisfies Partial<ImageGenerationError>));
  });
});
