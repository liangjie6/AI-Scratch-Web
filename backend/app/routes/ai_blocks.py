from collections.abc import AsyncIterator
from functools import lru_cache
import asyncio
import json
import logging
from typing import Any

from fastapi import APIRouter, Depends, HTTPException
from fastapi.responses import StreamingResponse
from pydantic import ValidationError

from ..config import get_settings
from ..orchestration.orchestrator import GenerationOrchestrator
from ..prompt_builder import PromptBuilder
from ..providers.agnes_text_provider import AgnesTextProvider, AgnesTextProviderError
from ..schemas.ai_blocks import (
    ChatRequest,
    ProgramContractError,
    ProgramIR,
    ProjectManifest,
    ResourceContractError,
    UnifiedGenerationRequest,
    ValidationRequest,
    normalize_project_manifest_payload,
    to_json_pointer,
    validate_program_contracts,
    validate_program_for_mode,
)


router = APIRouter(prefix='/api/ai-blocks', tags=['ai-blocks'])
logger = logging.getLogger(__name__)


@lru_cache(maxsize=1)
def get_ai_blocks_provider() -> AgnesTextProvider:
    return AgnesTextProvider(get_settings())


@lru_cache(maxsize=1)
def get_ai_blocks_semaphore() -> asyncio.Semaphore:
    return asyncio.Semaphore(get_settings().ai_blocks_max_concurrency)


@lru_cache(maxsize=1)
def get_ai_blocks_orchestrator() -> GenerationOrchestrator:
    settings = get_settings()
    return GenerationOrchestrator(PromptBuilder(), max_repair_rounds=settings.ai_blocks_max_repair_rounds)


def _sse(event: str, payload: dict[str, Any]) -> str:
    return f'event: {event}\ndata: {json.dumps(payload, ensure_ascii=False, separators=(",", ":"))}\n\n'


def _select_provider(provider: AgnesTextProvider, tier: str) -> AgnesTextProvider:
    if tier == 'normal':
        return provider
    return provider.for_tier(tier)


def _require_provider(provider: AgnesTextProvider, tier: str) -> AgnesTextProvider:
    try:
        selected = _select_provider(provider, tier)
        selected.ensure_configured()
    except (RuntimeError, ValueError) as exc:
        logger.error('ai_provider_not_configured tier=%s error_type=%s', tier, type(exc).__name__)
        raise HTTPException(status_code=503, detail={'code': 'AI_PROVIDER_NOT_CONFIGURED', 'message': 'AI 服务暂未配置'}) from exc
    return selected


@router.post('/chat')
async def chat(
    body: ChatRequest,
    provider: AgnesTextProvider = Depends(get_ai_blocks_provider),
    semaphore: asyncio.Semaphore = Depends(get_ai_blocks_semaphore),
    orchestrator: GenerationOrchestrator = Depends(get_ai_blocks_orchestrator),
):
    selected = _require_provider(provider, body.model_tier)

    async def generate() -> AsyncIterator[str]:
        terminal = False
        content_chunk_count = 0
        async with semaphore:
            try:
                async with asyncio.timeout(get_settings().ai_blocks_provider_timeout_seconds):
                    messages = orchestrator.prompt_builder.build_chat_messages(body)
                    async for delta in selected.stream_with_reasoning(
                        messages,
                        thinking_enabled=body.thinking.enabled,
                        thinking_strength=body.thinking.strength,
                        json_output=False,
                    ):
                        if delta.kind == 'reasoning' and delta.text:
                            yield _sse('reasoning_delta', {'request_id': body.request_id, 'text': delta.text})
                        elif delta.kind == 'content' and delta.text:
                            content_chunk_count += 1
                            logger.debug('chat_delta: %s', delta.text)
                            yield _sse('assistant_delta', {'request_id': body.request_id, 'text': delta.text})
                terminal = True
                logger.info('ai_chat 完成 request_id=%s 内容块=%d', body.request_id, content_chunk_count)
                yield _sse('message_complete', {'request_id': body.request_id})
            except asyncio.TimeoutError:
                if not terminal:
                    terminal = True
                    yield _sse('message_error', {'request_id': body.request_id, 'code': 'AI_PROVIDER_TIMEOUT', 'message': 'AI 对话超时，请稍后重试'})
            except Exception as exc:
                logger.error('ai_chat_failed request_id=%s error_type=%s', body.request_id, type(exc).__name__)
                if not terminal:
                    terminal = True
                    yield _sse('message_error', {'request_id': body.request_id, 'code': 'AI_CHAT_ERROR', 'message': 'AI 对话暂时不可用，请稍后重试'})

    return StreamingResponse(generate(), media_type='text/event-stream', headers={'Cache-Control': 'no-cache', 'X-Accel-Buffering': 'no'})


@router.post('/generate-unified')
async def generate_unified(
    body: UnifiedGenerationRequest,
    provider: AgnesTextProvider = Depends(get_ai_blocks_provider),
    semaphore: asyncio.Semaphore = Depends(get_ai_blocks_semaphore),
    orchestrator: GenerationOrchestrator = Depends(get_ai_blocks_orchestrator),
):
    selected = _require_provider(provider, body.model_tier)

    async def generate() -> AsyncIterator[str]:
        terminal = False
        async with semaphore:
            try:
                async with asyncio.timeout(get_settings().ai_blocks_provider_timeout_seconds):
                    async for event in orchestrator.generate_unified(body, selected):
                        yield _sse(event.type, event.payload())
                        if event.type in {'complete', 'error'}:
                            terminal = True
                            return
            except asyncio.TimeoutError:
                if not terminal:
                    terminal = True
                    yield _sse('error', {'request_id': body.request_id, 'code': 'AI_PROVIDER_TIMEOUT', 'message': 'AI 生成超时，请稍后重试'})
            except AgnesTextProviderError:
                logger.error('ai_unified_provider_failed request_id=%s', body.request_id)
                if not terminal:
                    terminal = True
                    yield _sse('error', {'request_id': body.request_id, 'code': 'AI_PROVIDER_ERROR', 'message': 'AI 生成暂时不可用，请稍后重试'})
            except Exception as exc:
                logger.error('ai_unified_failed request_id=%s error_type=%s', body.request_id, type(exc).__name__)
                if not terminal:
                    terminal = True
                    yield _sse('error', {'request_id': body.request_id, 'code': 'AI_GENERATION_ERROR', 'message': 'AI 输出未完成，请重新生成'})

    return StreamingResponse(
        generate(),
        media_type='text/event-stream',
        headers={'Cache-Control': 'no-cache', 'X-Accel-Buffering': 'no'},
    )


def _validation_issues(error: Exception) -> list[dict[str, Any]]:
    if isinstance(error, ResourceContractError):
        return [error.issue.as_dict()]
    if isinstance(error, ProgramContractError):
        return [{
            'code': 'PROGRAM_CONTRACT_INVALID',
            'path': to_json_pointer(path),
            'operation': 'program',
            'actual': None,
            'expected': message,
            'repair_scope': 'program',
            'repair_id': None,
            'message': message,
        } for path, message in error.issues]
    if isinstance(error, ValidationError):
        issues = []
        for item in error.errors():
            message = item.get('msg')
            expected = message if isinstance(message, str) else '结构化输出无效'
            issues.append({
                'code': 'SCHEMA_VALIDATION_ERROR',
                'path': to_json_pointer(tuple(item.get('loc', ()))) or '/output',
                'operation': 'validation',
                'actual': None,
                'expected': expected,
                'repair_scope': 'validation',
                'repair_id': None,
                'message': expected,
            })
        return issues or [{
            'code': 'SCHEMA_VALIDATION_ERROR',
            'path': '/output',
            'operation': 'validation',
            'actual': None,
            'expected': '结构化输出无效',
            'repair_scope': 'validation',
            'repair_id': None,
            'message': '结构化输出无效',
        }]
    message = str(error)
    return [{
        'code': 'OUTPUT_VALIDATION_ERROR',
        'path': '/output',
        'operation': 'validation',
        'actual': None,
        'expected': message,
        'repair_scope': 'validation',
        'repair_id': None,
        'message': message,
    }]


@router.post('/validate')
async def validate_output(body: ValidationRequest) -> dict[str, Any]:
    try:
        if body.kind == 'program':
            program = ProgramIR.model_validate(body.payload)
            validate_program_contracts(program, body.target_kind)
            validate_program_for_mode(program, body.mode, body.current_program)
        else:
            normalized_payload, normalizations = normalize_project_manifest_payload(body.payload)
            ProjectManifest.model_validate(normalized_payload)
    except (ValidationError, ValueError) as exc:
        return {'valid': False, 'issues': _validation_issues(exc)}
    return {'valid': True, 'issues': [], 'normalizations': [item.as_dict() for item in normalizations] if body.kind == 'manifest' else []}
