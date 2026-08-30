from collections.abc import AsyncIterator
from functools import lru_cache
import json
import logging
import time
import uuid

from fastapi import APIRouter, Depends, HTTPException
from fastapi.responses import StreamingResponse

from ..config import get_settings
from ..prompts import build_text_expansion_messages
from ..providers.agnes_text_provider import AgnesTextProvider, AgnesTextProviderError
from ..schemas.text_expansion import TextExpansionRequest


router = APIRouter(prefix='/api/text', tags=['text-expansion'])
logger = logging.getLogger(__name__)


@lru_cache(maxsize=1)
def get_text_provider() -> AgnesTextProvider:
    return AgnesTextProvider(get_settings())


def _sse(event: str, data: dict) -> str:
    return f'event: {event}\ndata: {json.dumps(data, ensure_ascii=False, separators=(",", ":"))}\n\n'


@router.post('/expand')
async def expand_text(
    request: TextExpansionRequest,
    provider: AgnesTextProvider = Depends(get_text_provider),
) -> StreamingResponse:
    request_id = str(uuid.uuid4())
    started_at = time.perf_counter()
    logger.info(
        'text_expansion_started request_id=%s kind=%s text=%s',
        request_id, request.kind, request.text,
    )
    try:
        provider.ensure_configured()
    except (AgnesTextProviderError, RuntimeError) as exc:
        raise HTTPException(
            status_code=503,
            detail={'code': 'AGNES_NOT_CONFIGURED', 'message': '未配置 Agnes 对话服务'},
        ) from exc

    messages = build_text_expansion_messages(request)

    async def generate() -> AsyncIterator[str]:
        received = False
        chunk_count = 0
        try:
            async for text in provider.stream(messages):
                received = True
                chunk_count += 1
                logger.debug('text_expansion_delta: %s', text)
                yield _sse('content_delta', {'text': text})
            if not received:
                raise AgnesTextProviderError('Agnes returned no content')
            logger.info(
                'text_expansion_finished request_id=%s chunk_count=%s duration_ms=%s',
                request_id, chunk_count, round((time.perf_counter() - started_at) * 1000),
            )
            yield _sse('message_complete', {})
        except AgnesTextProviderError as exc:
            logger.error(
                'text_expansion_failed request_id=%s chunk_count=%s duration_ms=%s error_type=%s error=%s',
                request_id, chunk_count, round((time.perf_counter() - started_at) * 1000),
                type(exc).__name__, str(exc), exc_info=True,
            )
            yield _sse(
                'message_error',
                {'code': 'AGNES_STREAM_ERROR', 'message': 'AI 扩写暂时不可用，请稍后重试'},
            )

    return StreamingResponse(
        generate(),
        media_type='text/event-stream',
        headers={'Cache-Control': 'no-cache', 'X-Accel-Buffering': 'no'},
    )
