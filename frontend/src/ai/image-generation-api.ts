import type {ImageGenerationRequest, ImageGenerationResponse} from './image-generation-types';

export class ImageGenerationError extends Error {
  constructor(public readonly code: string, message: string) {
    super(message);
    this.name = 'ImageGenerationError';
  }
}

export async function generateImage(
  request: ImageGenerationRequest,
  signal?: AbortSignal
): Promise<ImageGenerationResponse> {
  const response = await fetch('/api/image/generate', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(request),
    signal
  });

  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    const detail = payload?.detail ?? payload?.error ?? {};
    throw new ImageGenerationError(
      detail?.code ?? 'REQUEST_FAILED',
      detail?.message ?? '生图服务暂时不可用'
    );
  }
  if (!payload?.image_base64 || payload?.sheet_layout?.columns !== 1 || payload?.sheet_layout?.rows !== 1) {
    throw new ImageGenerationError('INVALID_RESPONSE', '生图服务返回了无效的图片');
  }
  return payload as ImageGenerationResponse;
}
