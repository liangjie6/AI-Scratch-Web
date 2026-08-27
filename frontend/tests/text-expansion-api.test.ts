import {afterEach, describe, expect, it, vi} from 'vitest';
import {
  streamTextExpansion,
  TextExpansionError
} from '../src/ai/text-expansion-api';


afterEach(() => vi.restoreAllMocks());


describe('text expansion API', () => {
  it('posts the expansion kind and decodes split UTF-8 SSE chunks', async () => {
    const encoded = new TextEncoder().encode(
      'event: content_delta\r\ndata: {"text":"森林中的小屋"}\r\n\r\n' +
      'event: message_complete\ndata: {}\n\n'
    );
    const firstCarriageReturn = encoded.indexOf(13);
    const chineseStart = encoded.indexOf(new TextEncoder().encode('森')[0]);
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoded.slice(0, firstCarriageReturn + 1));
        controller.enqueue(encoded.slice(firstCarriageReturn + 1, chineseStart + 1));
        controller.enqueue(encoded.slice(chineseStart + 1));
        controller.close();
      }
    });
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(stream, {
      status: 200,
      headers: {'Content-Type': 'text/event-stream; charset=utf-8'}
    }));
    const chunks: string[] = [];

    await streamTextExpansion({kind: 'scene', text: '小屋'}, text => chunks.push(text));

    expect(chunks.join('')).toBe('森林中的小屋');
    expect(fetchMock).toHaveBeenCalledWith('/api/text/expand', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({kind: 'scene', text: '小屋'})
    }));
  });

  it('surfaces sanitized HTTP errors', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      detail: {code: 'AGNES_NOT_CONFIGURED', message: '未配置 Agnes 对话服务'}
    }), {status: 503, headers: {'Content-Type': 'application/json'}}));

    await expect(streamTextExpansion({kind: 'style', text: '木刻'}, () => {})).rejects.toEqual(
      expect.objectContaining({code: 'AGNES_NOT_CONFIGURED'} satisfies Partial<TextExpansionError>)
    );
  });

  it('rejects a stream that closes without completion', async () => {
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('event: content_delta\ndata: {"text":"部分"}\n\n'));
        controller.close();
      }
    });
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(stream, {
      status: 200,
      headers: {'Content-Type': 'text/event-stream'}
    }));

    await expect(streamTextExpansion({kind: 'scene', text: '小屋'}, () => {})).rejects.toEqual(
      expect.objectContaining({code: 'STREAM_INCOMPLETE'} satisfies Partial<TextExpansionError>)
    );
  });
});
