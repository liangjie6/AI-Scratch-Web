from collections.abc import AsyncIterator
from functools import lru_cache
import asyncio
import json
import logging

from fastapi import APIRouter, Depends, HTTPException, Request
from fastapi.responses import StreamingResponse
from pydantic import ValidationError

from ..config import get_settings
from ..prompts_ai_blocks import (
    build_chat_messages,
    build_plan_messages,
    build_plan_repair_messages,
    build_program_messages,
    build_program_repair_messages,
    build_project_plan_messages,
    build_project_plan_repair_messages,
    build_requirements_messages,
    build_requirements_repair_messages,
)
from ..providers.agnes_text_provider import AgnesTextProvider, AgnesTextProviderError
from ..schemas.ai_blocks import (
    ChatRequest,
    GenerateRequest,
    Plan,
    ProgramIR,
    ProjectPlan,
    RequirementsRequest,
    RequirementsResponse,
    ThinkingSettings,
    extract_single_json,
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


def _sse(event: str, data: dict) -> str:
    body = json.dumps(data, ensure_ascii=False, separators=(',', ':'))
    newline = chr(10)
    return f'event: {event}{newline}data: {body}{newline}{newline}'


def _error_summary(error: Exception, limit: int = 2_000) -> str:
    if isinstance(error, ValidationError):
        return json.dumps(
            error.errors(include_url=False, include_input=False),
            ensure_ascii=False, default=str,
        )[:limit]
    return str(error)[:limit]


def _provider_for_tier(provider: AgnesTextProvider, tier: str) -> AgnesTextProvider:
    return provider.for_tier(tier) if hasattr(provider, 'for_tier') else provider


def _ensure_provider(provider: AgnesTextProvider) -> None:
    try:
        provider.ensure_configured()
    except (AgnesTextProviderError, RuntimeError) as exc:
        raise HTTPException(
            status_code=503,
            detail={'code': 'AGNES_NOT_CONFIGURED', 'message': '未配置 Agnes 对话服务'},
        ) from exc


_PROCEED_PHRASES = (
    '开始编写', '开始生成', '开始写', '直接生成', '直接做', '按默认即可',
    '使用默认值', '按默认', '不用再问', '不要再问', '无需再问', '别再问',
    '开始吧', '开始', '继续', '确认', '可以', '就这样',
)


def _has_functional_content(value: str) -> bool:
    normalized = ''.join(
        character for character in value.strip().lower()
        if not character.isspace() and character not in '，,。.!！？?'
    )
    for phrase in _PROCEED_PHRASES:
        normalized = normalized.replace(phrase, '')
    return len(normalized) >= 2


def _apply_proceed_defaults(
    body: RequirementsRequest,
    result: RequirementsResponse,
) -> RequirementsResponse:
    if result.ready or not body.proceed_with_defaults:
        return result

    functional_request = ''
    for message in reversed(body.messages):
        if message.role != 'user':
            continue
        if _has_functional_content(message.content):
            functional_request = message.content.strip()
            break
    if not functional_request:
        return result

    short_goal = functional_request[:120]
    return RequirementsResponse.model_validate({
        **result.model_dump(mode='json'),
        'ready': True,
        'title': result.title.strip() or '按描述实现功能',
        'goal': result.goal.strip() or functional_request,
        'behavior_steps': result.behavior_steps or [short_goal],
        'triggers': result.triggers or ['点击绿旗'],
        'success_criteria': result.success_criteria or [short_goal],
        'missing_questions': [],
    })


async def _timed_events(
    provider: AgnesTextProvider,
    messages: list[dict[str, str]],
    thinking: ThinkingSettings,
    json_output: bool = False,
):
    started_at = asyncio.get_running_loop().time()
    logger.info(
        'ai_stream_started provider=%s message_count=%s thinking_enabled=%s thinking_strength=%s json_output=%s messages=%s',
        getattr(provider, 'provider', 'unknown'), len(messages), thinking.enabled,
        thinking.strength, json_output, json.dumps(messages, ensure_ascii=False, default=str),
    )
    delta_count = 0
    try:
        async with asyncio.timeout(get_settings().ai_blocks_provider_timeout_seconds):
            stream_kwargs = {
                'thinking_enabled': thinking.enabled,
                'thinking_strength': thinking.strength,
            }
            if hasattr(provider, 'provider'):
                stream_kwargs['json_output'] = json_output
            async for delta in provider.stream_with_reasoning(messages, **stream_kwargs):
                delta_count += 1
                logger.info('ai_stream_delta provider=%s index=%s kind=%s text=%s', getattr(provider, 'provider', 'unknown'), delta_count, delta.kind, delta.text)
                yield delta
        logger.info(
            'ai_stream_finished provider=%s delta_count=%s duration_ms=%s',
            getattr(provider, 'provider', 'unknown'), delta_count,
            round((asyncio.get_running_loop().time() - started_at) * 1000),
        )
    except TimeoutError as exc:
        logger.error(
            'ai_stream_timeout provider=%s delta_count=%s duration_ms=%s',
            getattr(provider, 'provider', 'unknown'), delta_count,
            round((asyncio.get_running_loop().time() - started_at) * 1000), exc_info=True,
        )
        raise AgnesTextProviderError('Agnes response timed out') from exc


async def _collect(
    provider: AgnesTextProvider,
    messages: list[dict[str, str]],
    thinking: ThinkingSettings,
    limit: int = 200_000,
    json_output: bool = False,
) -> tuple[str, str]:
    content_chunks: list[str] = []
    reasoning_chunks: list[str] = []
    size = 0
    async for delta in _timed_events(provider, messages, thinking, json_output):
        size += len(delta.text)
        if size > limit:
            raise AgnesTextProviderError('Agnes response is too large')
        if delta.kind == 'reasoning':
            reasoning_chunks.append(delta.text)
        else:
            content_chunks.append(delta.text)
    if not content_chunks:
        raise AgnesTextProviderError('Agnes returned no content')
    return ''.join(content_chunks), ''.join(reasoning_chunks)


async def _collect_resilient(
    provider: AgnesTextProvider,
    messages: list[dict[str, str]],
    thinking: ThinkingSettings,
    *,
    retries: int = 1,
    json_output: bool = False,
) -> tuple[str, str]:
    """Retry transient provider failures without repeating expensive reasoning."""
    current_thinking = thinking
    for attempt in range(retries + 1):
        try:
            return await _collect(provider, messages, current_thinking, json_output=json_output)
        except AgnesTextProviderError:
            if attempt >= retries:
                raise
            current_thinking = ThinkingSettings(enabled=False, strength='low')
    raise RuntimeError('AI provider collection produced no result')


@router.post('/chat')
async def chat(
    body: ChatRequest,
    request: Request,
    provider: AgnesTextProvider = Depends(get_ai_blocks_provider),
) -> StreamingResponse:
    provider = _provider_for_tier(provider, body.model_tier)
    logger.info(
        'ai_chat_started request_id=%s session_id=%s model_tier=%s message_count=%s thinking=%s body=%s',
        body.request_id, body.session_id, body.model_tier, len(body.messages),
        body.thinking.model_dump(), body.model_dump(mode='json'),
    )
    _ensure_provider(provider)
    messages = build_chat_messages(body)

    async def generate() -> AsyncIterator[str]:
        try:
            async with get_ai_blocks_semaphore():
                received_content = False
                async for delta in _timed_events(provider, messages, body.thinking):
                    if await request.is_disconnected():
                        logger.info('ai_chat_disconnected request_id=%s', body.request_id)
                        return
                    event = 'reasoning_delta' if delta.kind == 'reasoning' else 'assistant_delta'
                    if delta.kind == 'content':
                        received_content = True
                    yield _sse(event, {'request_id': body.request_id, 'text': delta.text})
                if not received_content:
                    raise AgnesTextProviderError('Agnes returned no content')
                if await request.is_disconnected():
                    return
                logger.info('ai_chat_finished request_id=%s received_content=%s', body.request_id, received_content)
                yield _sse('message_complete', {'request_id': body.request_id})
        except (AgnesTextProviderError, RuntimeError) as exc:
            logger.error(
                'ai_chat_failed request_id=%s error_type=%s error=%s',
                body.request_id, type(exc).__name__, str(exc), exc_info=True,
            )
            yield _sse('message_error', {
                'request_id': body.request_id,
                'code': 'AI_BLOCKS_STREAM_ERROR',
                'message': 'AI 对话暂时不可用，请稍后重试',
            })

    return StreamingResponse(generate(), media_type='text/event-stream', headers={
        'Cache-Control': 'no-cache', 'X-Accel-Buffering': 'no',
    })


@router.post('/requirements', response_model=RequirementsResponse)
async def requirements(
    body: RequirementsRequest,
    provider: AgnesTextProvider = Depends(get_ai_blocks_provider),
) -> RequirementsResponse:
    provider = _provider_for_tier(provider, body.model_tier)
    _ensure_provider(provider)
    try:
        async with get_ai_blocks_semaphore():
            failed_attempts: list[tuple[str, str]] = []
            messages = build_requirements_messages(body)
            for attempt in range(3):
                raw, _ = await _collect_resilient(provider, messages, body.thinking, json_output=body.model_tier == 'advanced')
                try:
                    result = RequirementsResponse.model_validate(extract_single_json(raw))
                    return _apply_proceed_defaults(body, result)
                except (ValidationError, ValueError) as validation_error:
                    logger.warning(
                        'AI requirements validation failed request_id=%s attempt=%s error=%s',
                        body.request_id, attempt + 1, _error_summary(validation_error),
                    )
                    if attempt == 2:
                        raise
                    failed_attempts.append((
                        raw, _error_summary(validation_error, 12_000),
                    ))
                    messages = build_requirements_repair_messages(body, failed_attempts)
        raise RuntimeError('AI requirements generation produced no result')
    except (AgnesTextProviderError, ValidationError, ValueError, RuntimeError) as exc:
        raise HTTPException(
            status_code=502,
            detail={'code': 'INVALID_REQUIREMENTS_RESPONSE', 'message': 'AI 返回的需求格式无效，请重试'},
        ) from exc


@router.post('/generate')
async def generate_program(
    body: GenerateRequest,
    request: Request,
    provider: AgnesTextProvider = Depends(get_ai_blocks_provider),
) -> StreamingResponse:
    provider = _provider_for_tier(provider, body.model_tier)
    _ensure_provider(provider)

    async def generate() -> AsyncIterator[str]:
        try:
            async with get_ai_blocks_semaphore():
                if await request.is_disconnected():
                    return
                yield _sse('phase', {
                    'request_id': body.request_id,
                    'phase': 'planning',
                    'message': '正在规划积木步骤',
                })
                plan: Plan | None = None
                plan_failed_attempts: list[tuple[str, str]] = []
                plan_messages = build_plan_messages(body)
                for attempt in range(3):
                    plan_raw, plan_reasoning = await _collect_resilient(
                        provider, plan_messages, body.thinking, json_output=body.model_tier == 'advanced',
                    )
                    if plan_reasoning:
                        yield _sse('reasoning_delta', {
                            'request_id': body.request_id,
                            'phase': 'planning' if attempt == 0 else 'repairing',
                            'text': plan_reasoning,
                        })
                    try:
                        plan = Plan.model_validate(extract_single_json(plan_raw))
                        break
                    except (ValidationError, ValueError) as validation_error:
                        logger.warning(
                            'AI plan validation failed request_id=%s attempt=%s error=%s',
                            body.request_id, attempt + 1, _error_summary(validation_error),
                        )
                        if attempt == 2:
                            raise
                        yield _sse('phase', {
                            'request_id': body.request_id,
                            'phase': 'repairing',
                            'message': '正在校正规划结构',
                        })
                        plan_failed_attempts.append((
                            plan_raw, _error_summary(validation_error, 12_000),
                        ))
                        plan_messages = build_plan_repair_messages(
                            body, plan_failed_attempts,
                        )
                if plan is None:
                    raise RuntimeError('AI plan generation produced no result')
                yield _sse('plan_ready', {'request_id': body.request_id, 'steps': plan.steps})
                if await request.is_disconnected():
                    return
                if body.output_contract == 'project_plan':
                    yield _sse('phase', {
                        'request_id': body.request_id,
                        'phase': 'generating',
                        'message': '正在生成项目资源计划',
                    })
                    project_raw, project_reasoning = await _collect_resilient(
                        provider,
                        build_project_plan_messages(body, plan.steps),
                        body.thinking,
                        json_output=body.model_tier == 'advanced',
                    )
                    if project_reasoning:
                        yield _sse('reasoning_delta', {
                            'request_id': body.request_id,
                            'phase': 'generating',
                            'text': project_reasoning,
                        })
                    project: ProjectPlan | None = None
                    project_failed_attempts: list[tuple[str, str]] = []
                    for attempt in range(3):
                        try:
                            project = ProjectPlan.model_validate(extract_single_json(project_raw))
                            if project.scope != body.scope:
                                raise ValueError('project scope does not match request scope')
                            if body.asset_policy == 'existing_only' and any(
                                operation.source is not None and operation.source.kind == 'image_generation'
                                for operation in project.resource_ops
                            ):
                                raise ValueError('existing_only cannot use image_generation')
                            break
                        except (ValidationError, ValueError) as validation_error:
                            if attempt == 2:
                                raise
                            error_detail = _error_summary(validation_error, 12_000)
                            project_failed_attempts.append((project_raw, error_detail))
                            yield _sse('phase', {
                                'request_id': body.request_id,
                                'phase': 'repairing',
                                'message': '正在校正项目计划',
                            })
                            project_raw, repair_reasoning = await _collect_resilient(
                                provider,
                                build_project_plan_repair_messages(body, plan.steps, project_failed_attempts),
                                body.thinking,
                                json_output=body.model_tier == 'advanced',
                            )
                            if repair_reasoning:
                                yield _sse('reasoning_delta', {
                                    'request_id': body.request_id,
                                    'phase': 'repairing',
                                    'text': repair_reasoning,
                                })
                    if project is None:
                        raise RuntimeError('AI ProjectPlan generation produced no result')
                    if await request.is_disconnected():
                        return
                    yield _sse('project_ready', {
                        'request_id': body.request_id,
                        'project': project.model_dump(mode='json', exclude_none=True),
                    })
                    yield _sse('message_complete', {'request_id': body.request_id})
                    return
                yield _sse('phase', {
                    'request_id': body.request_id,
                    'phase': 'generating',
                    'message': '正在生成积木程序',
                })
                program_raw, program_reasoning = await _collect_resilient(
                    provider, build_program_messages(body, plan.steps), body.thinking, json_output=body.model_tier == 'advanced',
                )
                if program_reasoning:
                    yield _sse('reasoning_delta', {
                        'request_id': body.request_id,
                        'phase': 'generating',
                        'text': program_reasoning,
                    })
                program: ProgramIR | None = None
                failed_attempts: list[tuple[str, str]] = []
                for attempt in range(5):
                    try:
                        program = ProgramIR.model_validate(extract_single_json(program_raw))
                        validate_program_for_mode(
                            program, body.target.mode, body.target.current_program,
                        )
                        validate_program_contracts(
                            program, 'stage' if body.target.is_stage else 'sprite',
                        )
                        break
                    except (ValidationError, ValueError) as validation_error:
                        logger.warning(
                            'AI ProgramIR validation failed request_id=%s attempt=%s error=%s',
                            body.request_id, attempt + 1, _error_summary(validation_error),
                        )
                        if attempt == 4:
                            raise
                        yield _sse('phase', {
                            'request_id': body.request_id,
                            'phase': 'repairing',
                            'message': '正在校正积木结构',
                        })
                        error_detail = _error_summary(validation_error, 12_000)
                        failed_attempts.append((program_raw, error_detail))
                        program_raw, repair_reasoning = await _collect_resilient(
                            provider,
                            build_program_repair_messages(body, plan.steps, failed_attempts),
                            body.thinking, json_output=body.model_tier == 'advanced',
                        )
                        if repair_reasoning:
                            yield _sse('reasoning_delta', {
                                'request_id': body.request_id,
                                'phase': 'repairing',
                                'text': repair_reasoning,
                            })
                if program is None:
                    raise RuntimeError('AI ProgramIR generation produced no result')
                if await request.is_disconnected():
                    return
                yield _sse('program_ready', {
                    'request_id': body.request_id,
                    'program': program.model_dump(mode='json', exclude_none=True),
                })
                yield _sse('message_complete', {'request_id': body.request_id})
        except (AgnesTextProviderError, ValidationError, ValueError, RuntimeError) as error:
            logger.warning(
                'AI block generation failed request_id=%s error_type=%s error=%s',
                body.request_id, type(error).__name__, _error_summary(error),
            )
            if isinstance(error, AgnesTextProviderError):
                code = 'AI_BLOCKS_PROVIDER_ERROR'
                message = 'AI 服务暂时不可用，请稍后重试'
            elif isinstance(error, (ValidationError, ValueError)):
                code = 'INVALID_PROGRAM_RESPONSE'
                message = 'AI 返回的积木结构无效，自动校正未成功，请重试'
            else:
                code = 'AI_BLOCKS_GENERATION_ERROR'
                message = 'AI 积木生成未完成，请重试'
            yield _sse('message_error', {
                'request_id': body.request_id,
                'code': code,
                'message': message,
            })

    return StreamingResponse(generate(), media_type='text/event-stream', headers={
        'Cache-Control': 'no-cache', 'X-Accel-Buffering': 'no',
    })
