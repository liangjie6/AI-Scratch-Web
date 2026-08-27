export type TextExpansionKind = 'scene' | 'style';

export interface TextExpansionRequest {
  kind: TextExpansionKind;
  text: string;
}

export class TextExpansionError extends Error {
  constructor(public readonly code: string, message: string) {
    super(message);
    this.name = 'TextExpansionError';
  }
}

interface SseEvent {
  event: string;
  data: unknown;
}

function parseFieldLine(line: string): [string, string] {
  const separator = line.indexOf(':');
  if (separator < 0) return [line, ''];
  const field = line.slice(0, separator);
  let value = line.slice(separator + 1);
  if (value.startsWith(' ')) value = value.slice(1);
  return [field, value];
}

export function createSseParser(onEvent: (event: SseEvent) => void) {
  let buffer = '';
  let pendingCarriageReturn = false;

  function dispatch(frame: string) {
    if (!frame) return;
    let event = 'message';
    const data: string[] = [];
    for (const line of frame.split('\n')) {
      if (!line || line.startsWith(':')) continue;
      const [field, value] = parseFieldLine(line);
      if (field === 'event') event = value;
      else if (field === 'data') data.push(value);
    }
    if (!data.length) return;
    const raw = data.join('\n');
    let payload: unknown = raw;
    try {
      payload = JSON.parse(raw);
    } catch {
      // Text-only frames are still useful for protocol diagnostics.
    }
    onEvent({event, data: payload});
  }

  return {
    push(text: string) {
      if (pendingCarriageReturn) {
        text = '\r' + text;
        pendingCarriageReturn = false;
      }
      if (text.endsWith('\r')) {
        text = text.slice(0, -1);
        pendingCarriageReturn = true;
      }
      buffer += text.replace(/\r\n?/g, '\n');
      let boundary = buffer.indexOf('\n\n');
      while (boundary !== -1) {
        dispatch(buffer.slice(0, boundary));
        buffer = buffer.slice(boundary + 2);
        boundary = buffer.indexOf('\n\n');
      }
    },
    finish() {
      if (pendingCarriageReturn) buffer += '\n';
      if (buffer.trim()) dispatch(buffer);
      buffer = '';
      pendingCarriageReturn = false;
    }
  };
}

function errorFromPayload(payload: unknown, fallback: string): TextExpansionError {
  const value = payload as {detail?: {code?: string; message?: string}; code?: string; message?: string} | null;
  const detail = value?.detail ?? value;
  return new TextExpansionError(detail?.code ?? 'EXPANSION_FAILED', detail?.message ?? fallback);
}

export async function streamTextExpansion(
  request: TextExpansionRequest,
  onDelta: (text: string) => void,
  signal?: AbortSignal
): Promise<void> {
  const response = await fetch('/api/text/expand', {
    method: 'POST',
    headers: {
      Accept: 'text/event-stream',
      'Content-Type': 'application/json'
    },
    body: JSON.stringify(request),
    signal
  });

  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    throw errorFromPayload(payload, 'AI 扩写请求失败（HTTP ' + response.status + '）');
  }
  if (!(response.headers.get('content-type') ?? '').toLowerCase().includes('text/event-stream')) {
    throw new TextExpansionError('STREAM_PROTOCOL_ERROR', '服务端未返回文本流');
  }
  if (!response.body) {
    throw new TextExpansionError('STREAM_PROTOCOL_ERROR', '浏览器未获得响应流');
  }

  let completed = false;
  const parser = createSseParser(({event, data}) => {
    const payload = data as {text?: unknown; code?: string; message?: string};
    if (event === 'content_delta' && typeof payload?.text === 'string') {
      onDelta(payload.text);
    } else if (event === 'message_complete') {
      completed = true;
    } else if (event === 'message_error') {
      throw new TextExpansionError(
        payload?.code ?? 'AGNES_STREAM_ERROR',
        payload?.message ?? 'AI 扩写暂时不可用'
      );
    }
  });
  const reader = response.body.getReader();
  const decoder = new TextDecoder('utf-8');
  try {
    while (true) {
      const {done, value} = await reader.read();
      if (done) break;
      parser.push(decoder.decode(value, {stream: true}));
    }
    parser.push(decoder.decode());
    parser.finish();
  } finally {
    reader.releaseLock();
  }
  if (!completed) {
    throw new TextExpansionError('STREAM_INCOMPLETE', 'AI 扩写连接意外中断');
  }
}
